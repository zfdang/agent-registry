package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/zfdang/agent-registry/pkg/model/db"
	"github.com/zfdang/agent-registry/pkg/probe"
)

// CheckExpiry is how long a passing/warning/failed verification check stays
// live before the agent flips to stale on read (03, 05 §Freshness).
const CheckExpiry = 30 * 24 * time.Hour

// CheckResult is one L1/L2/L3 outcome produced by a pipeline run.
type CheckResult struct {
	Kind      string
	Status    string
	Detail    json.RawMessage
	CheckedAt time.Time
	ExpiresAt time.Time
}

// PipelineResult is everything a pipeline run produces: the roll-up result,
// the card-derived extraction (nil when L1 failed), and the per-check
// evidence trail.
type PipelineResult struct {
	Result          string // passed | passed_with_warnings | failed
	Checks          []CheckResult
	CardURL         string
	CardHash        string
	CardJSON        json.RawMessage
	Card            *CardSchema
	DeclaredAgentID *string // nil unless L2 found a well-formed id
}

// Verifier runs the L1/L2/L3 pipeline (05) against candidate agent
// endpoints, deduplicating concurrent runs for the same URL.
type Verifier struct {
	probe *probe.Client
	sf    singleflight.Group
}

// NewVerifier builds a Verifier over the given SSRF-hardened probe client.
func NewVerifier(c *probe.Client) *Verifier {
	return &Verifier{probe: c}
}

// Run executes the pipeline against registeredURL (already validated as an
// absolute https URL by the caller), deduplicating concurrent callers for
// the same URL via singleflight.
func (v *Verifier) Run(ctx context.Context, registeredURL string) (*PipelineResult, error) {
	out, err, _ := v.sf.Do(registeredURL, func() (any, error) {
		return v.run(ctx, registeredURL)
	})
	if err != nil {
		return nil, err
	}
	return out.(*PipelineResult), nil
}

func (v *Verifier) run(ctx context.Context, registeredURL string) (*PipelineResult, error) {
	now := time.Now().UTC()
	checkedAt := now
	expiresAt := now.Add(CheckExpiry)

	l1Detail, cardURL, cardHash, cardBody, card, l1Status := v.runL1(ctx, registeredURL)
	l1Check := CheckResult{Kind: db.CheckL1Card, Status: l1Status, Detail: l1Detail, CheckedAt: checkedAt, ExpiresAt: expiresAt}

	if l1Status == db.CheckStatusFailed {
		return &PipelineResult{
			Result: RollupValidationResult(l1Status, "", ""),
			Checks: []CheckResult{l1Check},
		}, nil
	}

	// Checked between stages so a canceled A2A register-agent task (07)
	// stops before the next outbound probe rather than mid-request.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	l2Status, l2Detail, declaredID := runL2(card)
	l2Check := CheckResult{Kind: db.CheckL2ID, Status: l2Status, Detail: l2Detail, CheckedAt: checkedAt, ExpiresAt: expiresAt}

	l3Status, l3Detail := v.runL3(ctx, registeredURL, card)
	l3Check := CheckResult{Kind: db.CheckL3Protocol, Status: l3Status, Detail: l3Detail, CheckedAt: checkedAt, ExpiresAt: expiresAt}

	result := RollupValidationResult(l1Status, l2Status, l3Status)

	return &PipelineResult{
		Result:          result,
		Checks:          []CheckResult{l1Check, l2Check, l3Check},
		CardURL:         cardURL,
		CardHash:        cardHash,
		CardJSON:        cardBody,
		Card:            card,
		DeclaredAgentID: declaredID,
	}, nil
}

// runL1 tries the three candidate card locations in order, per 05 §L1.
func (v *Verifier) runL1(ctx context.Context, registeredURL string) (detail json.RawMessage, cardURL, cardHash string, cardBody json.RawMessage, card *CardSchema, status string) {
	origin := originOf(registeredURL)
	candidates := []string{
		origin + "/.well-known/agent-card.json",
		origin + "/.well-known/agent.json",
		registeredURL,
	}

	type attempt struct {
		URL    string `json:"url"`
		Status int    `json:"status,omitempty"`
		Error  string `json:"error,omitempty"`
	}
	var attempts []attempt

	for _, candidate := range candidates {
		res, err := v.probe.Get(ctx, candidate)
		if err != nil {
			attempts = append(attempts, attempt{URL: candidate, Error: err.Error()})
			continue
		}
		if res.StatusCode != 200 {
			attempts = append(attempts, attempt{URL: candidate, Status: res.StatusCode})
			continue
		}
		parsed, err := ParseCard(res.Body)
		if err != nil {
			attempts = append(attempts, attempt{URL: candidate, Status: res.StatusCode, Error: "invalid json"})
			continue
		}
		if !parsed.IsStaticallyValid() {
			attempts = append(attempts, attempt{URL: candidate, Status: res.StatusCode, Error: "missing required name or url"})
			continue
		}

		// Same-origin rule: the card's declared endpoint must be exactly
		// on the registered URL's origin (05 §Same-origin and https rules).
		if !SameOrigin(registeredURL, parsed.URL) {
			d, _ := json.Marshal(map[string]any{
				"code":         "card_endpoint_origin_mismatch",
				"card_url":     candidate,
				"declared_url": parsed.URL,
				"registered":   registeredURL,
			})
			return d, "", "", nil, nil, db.CheckStatusFailed
		}

		sum := sha256.Sum256(res.Body)
		d, _ := json.Marshal(map[string]any{"card_url": candidate})
		return d, candidate, hex.EncodeToString(sum[:]), json.RawMessage(res.Body), parsed, db.CheckStatusPassed
	}

	d, _ := json.Marshal(map[string]any{"candidates": attempts})
	return d, "", "", nil, nil, db.CheckStatusFailed
}

// runL2 extracts the declared agent id (05 §L2). Never fails.
func runL2(card *CardSchema) (status string, detail json.RawMessage, declared *string) {
	raw, found := card.DeclaredAgentID()
	if !found {
		d, _ := json.Marshal(map[string]any{"declared": nil, "note": "card does not declare an agent id"})
		return db.CheckStatusWarning, d, nil
	}
	if !IsValidAgentIDFormat(raw) {
		d, _ := json.Marshal(map[string]any{"declared": raw, "note": "invalid format, ignored"})
		return db.CheckStatusWarning, d, nil
	}
	d, _ := json.Marshal(map[string]any{"declared": raw})
	return db.CheckStatusPassed, d, &raw
}

// probeRequest / probeResponse model the benign JSON-RPC envelope L3 sends
// and the shape it checks for in the reply (05 §L3).
type probeRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

// runL3 POSTs the benign tasks/get probe to the card's declared endpoint.
func (v *Verifier) runL3(ctx context.Context, registeredURL string, card *CardSchema) (status string, detail json.RawMessage) {
	if !SameOrigin(registeredURL, card.URL) {
		d, _ := json.Marshal(map[string]any{"code": "card_endpoint_origin_mismatch", "endpoint": card.URL})
		return db.CheckStatusFailed, d
	}

	reqID := "probe-" + uuid.NewString()
	body, _ := json.Marshal(probeRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  "tasks/get",
		Params:  map[string]any{"id": "task_probe_nonexistent"},
	})

	res, err := v.probe.PostJSON(ctx, card.URL, body)
	if err != nil {
		d, _ := json.Marshal(map[string]any{"endpoint": card.URL, "error": err.Error()})
		return db.CheckStatusFailed, d
	}
	if res.StatusCode != 200 {
		d, _ := json.Marshal(map[string]any{"endpoint": card.URL, "http_status": res.StatusCode})
		return db.CheckStatusFailed, d
	}

	ok, envelope := isValidJSONRPCEnvelope(res.Body, reqID)
	if !ok {
		d, _ := json.Marshal(map[string]any{"endpoint": card.URL, "http_status": res.StatusCode, "error": "not a well-formed JSON-RPC 2.0 envelope"})
		return db.CheckStatusFailed, d
	}

	d, _ := json.Marshal(map[string]any{"endpoint": card.URL, "jsonrpc": "2.0", "response": envelope})
	return db.CheckStatusPassed, d
}

// isValidJSONRPCEnvelope implements 05 §L3's pass criteria: jsonrpc=="2.0",
// id echoes the request, and exactly one of result/error is present — an
// error envelope passes, since it proves the endpoint speaks the protocol.
func isValidJSONRPCEnvelope(body []byte, wantID string) (bool, map[string]json.RawMessage) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return false, nil
	}

	rpcRaw, ok := m["jsonrpc"]
	if !ok {
		return false, nil
	}
	var rpc string
	if err := json.Unmarshal(rpcRaw, &rpc); err != nil || rpc != "2.0" {
		return false, nil
	}

	idRaw, ok := m["id"]
	if !ok {
		return false, nil
	}
	var id string
	if err := json.Unmarshal(idRaw, &id); err != nil || id != wantID {
		return false, nil
	}

	_, hasResult := m["result"]
	_, hasError := m["error"]
	if hasResult == hasError { // both present or both absent
		return false, nil
	}

	return true, m
}

// originOf returns scheme://host[:port] for a URL.
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Scheme + "://" + u.Host
}
