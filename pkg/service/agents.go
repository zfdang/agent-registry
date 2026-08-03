package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zfdang/agent-registry/pkg/client/pg"
	"github.com/zfdang/agent-registry/pkg/model"
	"github.com/zfdang/agent-registry/pkg/model/db"
)

// ValidationTTL is how long a pre-registration validation stays usable (03).
const ValidationTTL = 15 * time.Minute

// Resolve implements the ref-resolution rules of 04: agt_ prefix → registry
// id; URL-shaped ref → normalized exact match on url, then card_url, then
// substring fallback; anything else → agent_id exact match. A URL-shaped
// ref that resolves nothing falls back to an agent_id exact match (DIDs
// contain dots).
func (s *Service) Resolve(ctx context.Context, ref string) (*db.Agent, error) {
	if ref == "" {
		return nil, ErrNotFound
	}

	if IsRegistryID(ref) {
		a, err := s.store.GetAgentByID(ctx, ref)
		if err != nil {
			if pg.IsNotFound(err) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		return a, nil
	}

	if IsURLShaped(ref) {
		if a, err := s.resolveByURL(ctx, ref); err == nil {
			return a, nil
		} else if !errors.Is(err, ErrNotFound) {
			return nil, err // ambiguous or transport error
		}
		// fall through to agent_id exact match (DID-with-dots case)
	}

	return s.resolveByAgentID(ctx, ref)
}

func (s *Service) resolveByURL(ctx context.Context, ref string) (*db.Agent, error) {
	normalized, err := NormalizeURL(PrepareURLRef(ref))
	if err != nil {
		return nil, ErrNotFound
	}

	if a, err := s.store.GetAgentByURL(ctx, normalized); err == nil {
		return a, nil
	} else if !pg.IsNotFound(err) {
		return nil, err
	}

	if a, err := s.store.GetAgentByCardURL(ctx, normalized); err == nil {
		return a, nil
	} else if !pg.IsNotFound(err) {
		return nil, err
	}

	matches, err := s.store.SearchAgentsByURLSubstring(ctx, ref)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return &matches[0], nil
	default:
		return nil, &AmbiguousRefError{Candidates: toCandidates(matches)}
	}
}

func (s *Service) resolveByAgentID(ctx context.Context, ref string) (*db.Agent, error) {
	matches, err := s.store.GetAgentsByAgentID(ctx, ref)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, ErrNotFound
	case 1:
		return &matches[0], nil
	default:
		return nil, &AmbiguousRefError{Candidates: toCandidates(matches)}
	}
}

func toCandidates(agents []db.Agent) []model.AgentCandidate {
	out := make([]model.AgentCandidate, 0, len(agents))
	for _, a := range agents {
		out = append(out, model.AgentCandidate{ID: a.ID, URL: a.URL, Name: a.Name})
	}
	return out
}

// Validate runs the full L1/L2/L3 pipeline against rawURL and persists a
// validations row without creating an agent (04 §validate).
func (s *Service) Validate(ctx context.Context, rawURL string) (*model.ValidateResponse, error) {
	normalized, err := ValidateInputURL(rawURL)
	if err != nil {
		return nil, err
	}

	pr, err := s.verifier.Run(ctx, normalized)
	if err != nil {
		return nil, fmt.Errorf("pipeline run: %w", err)
	}

	now := time.Now().UTC()
	v := &db.Validation{
		ID:        db.NewID(db.ValidationIDPrefix),
		URL:       normalized,
		Result:    pr.Result,
		CreatedAt: now,
		ExpiresAt: now.Add(ValidationTTL),
	}

	var extracted *model.ExtractedCard
	if pr.Card != nil {
		name := pr.Card.Name()
		v.Name = &name
		v.OwnerName = pr.Card.OwnerName()
		v.AgentID = pr.DeclaredAgentID
		v.Version = pr.Card.Version()
		v.ProtocolVersion = pr.Card.ProtocolVersionPtr()
		v.CardURL = pr.CardURL
		v.CardHash = pr.CardHash
		v.CardJSON = pr.CardJSON

		extracted = &model.ExtractedCard{
			Name:            pr.Card.Name(),
			OwnerName:       v.OwnerName,
			AgentID:         v.AgentID,
			Description:     pr.Card.Description(),
			Version:         v.Version,
			ProtocolVersion: v.ProtocolVersion,
			Skills:          nonNilSkills(pr.Card.Skills()),
		}
	}

	if err := s.store.CreateValidation(ctx, v); err != nil {
		return nil, fmt.Errorf("create validation: %w", err)
	}
	for _, c := range pr.Checks {
		if err := s.store.UpsertCheckForValidation(ctx, v.ID, c.Kind, c.Status, c.Detail, c.CheckedAt, c.ExpiresAt); err != nil {
			return nil, fmt.Errorf("store check: %w", err)
		}
	}

	mode := "create"
	var existingID *string
	if existing, err := s.store.GetAgentByURL(ctx, normalized); err == nil {
		mode = "update"
		id := existing.ID
		existingID = &id
	} else if !pg.IsNotFound(err) {
		return nil, err
	}

	return &model.ValidateResponse{
		ValidationID: v.ID,
		Result:       v.Result,
		Registerable: Registerable(v.Result),
		Mode:         mode,
		ExistingID:   existingID,
		ExpiresAt:    v.ExpiresAt,
		Extracted:    extracted,
		Checks:       checksFromResults(pr.Checks),
	}, nil
}

func nonNilSkills(s []model.Skill) []model.Skill {
	if s == nil {
		return []model.Skill{}
	}
	return s
}

func checksFromResults(results []CheckResult) []model.Check {
	out := make([]model.Check, 0, len(results))
	for _, r := range results {
		out = append(out, model.Check{
			Kind:      r.Kind,
			Status:    r.Status,
			CheckedAt: r.CheckedAt,
			ExpiresAt: r.ExpiresAt,
			Detail:    r.Detail,
		})
	}
	return out
}

// Register consumes a validation and creates or updates the agent it
// describes (04 §register, 03 §upsert rule). The consume + create/update +
// link-checks + snapshot sequence runs inside a single DB transaction
// (P2-3): without that, a failure partway through (e.g. the process dying
// between consuming the validation and creating the agent) leaves the
// validation burned with no agent row and no way to retry it.
func (s *Service) Register(ctx context.Context, validationID string) (agentDTO model.Agent, mode string, checks []model.Check, created bool, err error) {
	v, err := s.store.GetValidation(ctx, validationID)
	if err != nil {
		if pg.IsNotFound(err) {
			return model.Agent{}, "", nil, false, ErrValidationNotFound
		}
		return model.Agent{}, "", nil, false, err
	}

	now := time.Now().UTC()
	if v.ConsumedAt != nil {
		return model.Agent{}, "", nil, false, ErrValidationConsumed
	}
	if now.After(v.ExpiresAt) {
		return model.Agent{}, "", nil, false, ErrValidationExpired
	}
	if v.Result == db.ValidationFailed {
		return model.Agent{}, "", nil, false, ErrValidationFailed
	}

	var card *CardSchema
	if len(v.CardJSON) > 0 {
		card, _ = ParseCard(v.CardJSON) // already validated at Validate time
	}

	var agent *db.Agent
	var isCreate bool

	txErr := s.store.Transaction(ctx, func(ctx context.Context, tx pg.Store) error {
		ok, err := tx.ConsumeValidation(ctx, validationID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrValidationConsumed
		}

		existing, err := tx.GetAgentByURL(ctx, v.URL)
		isCreate = pg.IsNotFound(err)
		if err != nil && !isCreate {
			return err
		}

		agent = &db.Agent{}
		if !isCreate {
			agent = existing
		} else {
			agent.ID = db.NewID(db.AgentIDPrefix)
			agent.CreatedAt = now
			agent.URL = v.URL
		}

		if v.Name != nil {
			agent.Name = *v.Name
		}
		agent.OwnerName = v.OwnerName
		agent.AgentID = v.AgentID
		agent.Version = v.Version
		agent.ProtocolVersion = v.ProtocolVersion
		agent.CardURL = v.CardURL
		agent.CardHash = v.CardHash
		agent.CardJSON = v.CardJSON
		agent.VerificationStatus = ValidationResultToAgentStatus(v.Result)
		agent.VerifiedAt = &now
		agent.UpdatedAt = now

		if card != nil {
			skillsJSON, _ := json.Marshal(nonNilSkills(card.Skills()))
			agent.Description = card.Description()
			agent.Skills = skillsJSON
			agent.Capabilities = card.CapabilitiesJSON()
			agent.CardText = card.CardText()
		}

		mode = "update"
		if isCreate {
			mode = "create"
			if err := tx.CreateAgent(ctx, agent); err != nil {
				return fmt.Errorf("create agent: %w", err)
			}
		} else {
			if err := tx.UpdateAgent(ctx, agent); err != nil {
				return fmt.Errorf("update agent: %w", err)
			}
		}

		if err := tx.LinkValidationChecksToAgent(ctx, validationID, agent.ID); err != nil {
			return fmt.Errorf("link checks: %w", err)
		}

		if err := s.maybeSnapshotWith(ctx, tx, agent.ID, v.CardURL, v.CardHash, v.CardJSON, sourceForMode(mode)); err != nil {
			return fmt.Errorf("snapshot: %w", err)
		}

		return nil
	})
	if txErr != nil {
		return model.Agent{}, "", nil, false, txErr
	}

	agentChecks, err := s.store.GetChecksForAgent(ctx, agent.ID)
	if err != nil {
		return model.Agent{}, "", nil, false, err
	}

	return s.buildAgentDTO(*agent, agentChecks), mode, checksToDTO(agentChecks), isCreate, nil
}

func sourceForMode(mode string) string {
	if mode == "create" {
		return db.SnapshotSourceRegister
	}
	return db.SnapshotSourceUpdate
}

// maybeSnapshot appends a new agent_card_snapshots row only when cardHash
// differs from the latest stored snapshot (03), then trims to the newest
// 20. It runs against s.store directly — use maybeSnapshotWith to run
// inside a transaction.
func (s *Service) maybeSnapshot(ctx context.Context, agentID, cardURL, cardHash string, cardJSON json.RawMessage, source string) error {
	return s.maybeSnapshotWith(ctx, s.store, agentID, cardURL, cardHash, cardJSON, source)
}

// maybeSnapshotWith is maybeSnapshot parameterized over the Store to use,
// so Register can run it against a transaction (P2-3) while Refresh runs
// it directly against s.store.
func (s *Service) maybeSnapshotWith(ctx context.Context, store pg.Store, agentID, cardURL, cardHash string, cardJSON json.RawMessage, source string) error {
	if cardHash == "" {
		return nil // no card fetched this run (e.g. L1 failed) — nothing to snapshot
	}

	latest, err := store.LatestSnapshot(ctx, agentID)
	if err != nil && !pg.IsNotFound(err) {
		return err
	}
	if err == nil && latest.CardHash == cardHash {
		return nil // unchanged
	}

	snap := &db.AgentCardSnapshot{
		ID:        db.NewID(db.SnapshotIDPrefix),
		AgentID:   agentID,
		CardURL:   cardURL,
		CardHash:  cardHash,
		CardJSON:  cardJSON,
		Source:    source,
		FetchedAt: time.Now().UTC(),
	}
	if err := store.CreateSnapshot(ctx, snap); err != nil {
		return err
	}
	return store.TrimSnapshots(ctx, agentID, 20)
}

// Refresh re-runs the full pipeline against an already-registered agent's
// URL, re-extracting metadata and recomputing verification_status (04
// §refresh). It never creates.
func (s *Service) Refresh(ctx context.Context, ref string) (model.Agent, []model.Check, error) {
	agent, err := s.Resolve(ctx, ref)
	if err != nil {
		return model.Agent{}, nil, err
	}

	pr, err := s.verifier.Run(ctx, agent.URL)
	if err != nil {
		return model.Agent{}, nil, fmt.Errorf("pipeline run: %w", err)
	}

	now := time.Now().UTC()
	for _, c := range pr.Checks {
		if err := s.store.UpsertCheckForAgent(ctx, agent.ID, c.Kind, c.Status, c.Detail, c.CheckedAt, c.ExpiresAt); err != nil {
			return model.Agent{}, nil, fmt.Errorf("store check: %w", err)
		}
	}

	if pr.Card != nil {
		agent.Name = pr.Card.Name()
		agent.OwnerName = pr.Card.OwnerName()
		agent.AgentID = pr.DeclaredAgentID
		agent.Version = pr.Card.Version()
		agent.ProtocolVersion = pr.Card.ProtocolVersionPtr()
		agent.Description = pr.Card.Description()
		skillsJSON, _ := json.Marshal(nonNilSkills(pr.Card.Skills()))
		agent.Skills = skillsJSON
		agent.Capabilities = pr.Card.CapabilitiesJSON()
		agent.CardText = pr.Card.CardText()
		agent.CardURL = pr.CardURL
		agent.CardHash = pr.CardHash
		agent.CardJSON = pr.CardJSON

		if err := s.maybeSnapshot(ctx, agent.ID, pr.CardURL, pr.CardHash, pr.CardJSON, db.SnapshotSourceRefresh); err != nil {
			return model.Agent{}, nil, fmt.Errorf("snapshot: %w", err)
		}
	}

	agent.VerificationStatus = ValidationResultToAgentStatus(pr.Result)
	// verified_at tracks the *last successful* verification (03: "Last
	// successful pipeline run") — a refresh that fails must not advance it,
	// or a listing that's actually been failing since date X would keep
	// showing today's date as if it had just been confirmed live.
	if pr.Result != db.ValidationFailed {
		agent.VerifiedAt = &now
	}
	agent.UpdatedAt = now

	if err := s.store.UpdateAgent(ctx, agent); err != nil {
		return model.Agent{}, nil, fmt.Errorf("update agent: %w", err)
	}

	agentChecks, err := s.store.GetChecksForAgent(ctx, agent.ID)
	if err != nil {
		return model.Agent{}, nil, err
	}

	return s.buildAgentDTO(*agent, agentChecks), checksToDTO(agentChecks), nil
}

// GetAgent resolves ref and returns the full agent record (04).
func (s *Service) GetAgent(ctx context.Context, ref string) (model.Agent, error) {
	agent, err := s.Resolve(ctx, ref)
	if err != nil {
		return model.Agent{}, err
	}
	checks, err := s.store.GetChecksForAgent(ctx, agent.ID)
	if err != nil {
		return model.Agent{}, err
	}
	return s.buildAgentDTO(*agent, checks), nil
}

// ListAgents implements GET /v1/agents' search + browse + pagination (04).
func (s *Service) ListAgents(ctx context.Context, q, status, sort string, page, pageSize int) (model.ListAgentsResponse, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	agents, total, err := s.store.ListAgents(ctx, pg.ListParams{
		Query: q, Status: status, Sort: sort, Page: page, PageSize: pageSize,
	})
	if err != nil {
		return model.ListAgentsResponse{}, err
	}

	items := make([]model.AgentListItem, 0, len(agents))
	for _, a := range agents {
		checks, err := s.store.GetChecksForAgent(ctx, a.ID)
		if err != nil {
			return model.ListAgentsResponse{}, err
		}
		dto := s.buildAgentDTO(a, checks)
		items = append(items, dto.AgentListItem)
	}

	return model.ListAgentsResponse{
		Agents:   items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// GetVerification returns the per-check breakdown for ref (04).
func (s *Service) GetVerification(ctx context.Context, ref string) (model.VerificationResponse, error) {
	agent, err := s.Resolve(ctx, ref)
	if err != nil {
		return model.VerificationResponse{}, err
	}
	checks, err := s.store.GetChecksForAgent(ctx, agent.ID)
	if err != nil {
		return model.VerificationResponse{}, err
	}
	return model.VerificationResponse{
		Status:     EffectiveStatus(checks, agent.VerificationStatus),
		VerifiedAt: agent.VerifiedAt,
		Checks:     checksToDTO(checks),
	}, nil
}

// GetCards returns the agent's card snapshot history, newest first (04).
func (s *Service) GetCards(ctx context.Context, ref string) (model.CardsResponse, error) {
	agent, err := s.Resolve(ctx, ref)
	if err != nil {
		return model.CardsResponse{}, err
	}
	snaps, err := s.store.ListSnapshots(ctx, agent.ID)
	if err != nil {
		return model.CardsResponse{}, err
	}
	out := make([]model.CardSnapshot, 0, len(snaps))
	for _, sn := range snaps {
		out = append(out, model.CardSnapshot{
			ID:        sn.ID,
			CardURL:   sn.CardURL,
			CardHash:  sn.CardHash,
			Source:    sn.Source,
			FetchedAt: sn.FetchedAt,
			CardJSON:  sn.CardJSON,
		})
	}
	return model.CardsResponse{Snapshots: out}, nil
}

// CompareAgentResult is one entry of CompareAgents' side-by-side output.
type CompareAgentResult struct {
	Ref                string          `json:"ref"`
	Name               string          `json:"name"`
	OwnerName          *string         `json:"owner_name"`
	VerificationStatus string          `json:"verification_status"`
	Version            *string         `json:"version"`
	ProtocolVersion    *string         `json:"protocol_version"`
	Skills             []string        `json:"skills"`
	Capabilities       json.RawMessage `json:"capabilities"`
	VerifiedAt         *time.Time      `json:"verified_at"`
	CreatedAt          time.Time       `json:"created_at"`
}

// CompareAgentsResponse is the MCP compare_agents tool result (06).
type CompareAgentsResponse struct {
	Agents     []CompareAgentResult `json:"agents"`
	Unresolved []string             `json:"unresolved"`
}

// CompareAgents resolves each ref and lays out verifiable facts
// side-by-side; unresolvable refs are reported, not errored (06).
func (s *Service) CompareAgents(ctx context.Context, refs []string) (CompareAgentsResponse, error) {
	var out CompareAgentsResponse
	for _, ref := range refs {
		agent, err := s.Resolve(ctx, ref)
		if err != nil {
			out.Unresolved = append(out.Unresolved, ref)
			continue
		}
		checks, err := s.store.GetChecksForAgent(ctx, agent.ID)
		if err != nil {
			return CompareAgentsResponse{}, err
		}
		skillNames := []string{}
		for _, sk := range decodeSkills(agent.Skills) {
			skillNames = append(skillNames, sk.ID)
		}
		caps := agent.Capabilities
		if len(caps) == 0 {
			caps = json.RawMessage("{}")
		}
		out.Agents = append(out.Agents, CompareAgentResult{
			Ref:                ref,
			Name:               agent.Name,
			OwnerName:          agent.OwnerName,
			VerificationStatus: EffectiveStatus(checks, agent.VerificationStatus),
			Version:            agent.Version,
			ProtocolVersion:    agent.ProtocolVersion,
			Skills:             skillNames,
			Capabilities:       caps,
			VerifiedAt:         agent.VerifiedAt,
			CreatedAt:          agent.CreatedAt,
		})
	}
	if out.Unresolved == nil {
		out.Unresolved = []string{}
	}
	return out, nil
}

// RegisterAgent runs Validate + Register in one call, for programmatic
// callers (MCP register_agent, A2A register-agent skill) — 06, 07. A
// failed validation is a normal result, not an error.
func (s *Service) RegisterAgent(ctx context.Context, rawURL string) (model.RegisterAgentResult, error) {
	val, err := s.Validate(ctx, rawURL)
	if err != nil {
		return model.RegisterAgentResult{}, err
	}
	if !val.Registerable {
		return model.RegisterAgentResult{Registered: false, Checks: val.Checks}, nil
	}

	agent, mode, checks, _, err := s.Register(ctx, val.ValidationID)
	if err != nil {
		return model.RegisterAgentResult{}, err
	}

	return model.RegisterAgentResult{
		Registered: true,
		Mode:       mode,
		Agent:      &agent,
		Checks:     checks,
	}, nil
}
