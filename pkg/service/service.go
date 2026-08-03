// Package service holds the business logic shared by the REST, MCP, and
// A2A adapters — the single registry.Service every protocol surface calls
// into (docs/02). All invariants (validation gating, dedup, pagination
// clamps, ref resolution) live here.
package service

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"go.uber.org/zap"

	"github.com/zfdang/agent-registry/pkg/client/pg"
	"github.com/zfdang/agent-registry/pkg/model"
	"github.com/zfdang/agent-registry/pkg/model/db"
	"github.com/zfdang/agent-registry/pkg/probe"
)

// Service is the shared business-logic layer.
type Service struct {
	store    pg.Store
	verifier *Verifier
	log      *zap.SugaredLogger

	statsCache *statsCache
}

// New builds a Service over a Store and a probe client.
func New(store pg.Store, probeClient *probe.Client, log *zap.SugaredLogger) *Service {
	return &Service{
		store:      store,
		verifier:   NewVerifier(probeClient),
		log:        log,
		statsCache: newStatsCache(60 * time.Second),
	}
}

// ValidateInputURL enforces 04's field rule for the submitted URL: absolute
// https, no userinfo, no fragment, at most 2048 characters. It does not
// enforce address restrictions — those are the probe's SSRF guard's job.
func ValidateInputURL(raw string) (string, error) {
	if len(raw) > 2048 {
		return "", fmt.Errorf("%w: too long", ErrInvalidURL)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("%w: must be https", ErrInvalidURL)
	}
	if u.Host == "" {
		return "", fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: userinfo not allowed", ErrInvalidURL)
	}
	if u.Fragment != "" {
		return "", fmt.Errorf("%w: fragment not allowed", ErrInvalidURL)
	}
	return NormalizeURL(raw)
}

// buildAgentDTO converts a stored agent row into the wire DTO, recomputing
// its effective (possibly stale) status from live checks.
func (s *Service) buildAgentDTO(a db.Agent, checks []db.VerificationCheck) model.Agent {
	item := model.AgentListItem{
		ID:                 a.ID,
		Name:               a.Name,
		OwnerName:          a.OwnerName,
		AgentID:            a.AgentID,
		URL:                a.URL,
		Description:        a.Description,
		Version:            a.Version,
		ProtocolVersion:    a.ProtocolVersion,
		Skills:             decodeSkills(a.Skills),
		VerificationStatus: EffectiveStatus(checks, a.VerificationStatus),
		VerifiedAt:         a.VerifiedAt,
		CreatedAt:          a.CreatedAt,
	}
	caps := a.Capabilities
	if len(caps) == 0 {
		caps = json.RawMessage("{}")
	}
	return model.Agent{
		AgentListItem: item,
		Capabilities:  caps,
		CardURL:       a.CardURL,
		CardJSON:      a.CardJSON,
	}
}

func decodeSkills(raw json.RawMessage) []model.Skill {
	if len(raw) == 0 {
		return []model.Skill{}
	}
	var skills []model.Skill
	if err := json.Unmarshal(raw, &skills); err != nil {
		return []model.Skill{}
	}
	if skills == nil {
		skills = []model.Skill{}
	}
	return skills
}

func checkToDTO(c db.VerificationCheck) model.Check {
	return model.Check{
		Kind:      c.Kind,
		Status:    c.Status,
		CheckedAt: c.CheckedAt,
		ExpiresAt: c.ExpiresAt,
		Detail:    c.Detail,
	}
}

func checksToDTO(checks []db.VerificationCheck) []model.Check {
	out := make([]model.Check, 0, len(checks))
	for _, c := range checks {
		out = append(out, checkToDTO(c))
	}
	return out
}
