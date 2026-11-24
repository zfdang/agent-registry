package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/zfdang/agent-registry/pkg/model/db"
	"github.com/zfdang/agent-registry/pkg/probe"
)

// CheckExpiry is how long a passing/warning/failed verification check stays
// live before the agent flips to stale on read (03, 05 §Freshness).
const CheckExpiry = 30 * 24 * time.Hour

// CheckResult is one L1/L2 outcome produced by a pipeline run.
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
	Result          string // passed | partial | failed
	Checks          []CheckResult
	CardURL         string
	CardHash        string
	CardJSON        json.RawMessage
	Card            *CardSchema
	DeclaredAgentID *string // nil unless the card declares a well-formed id
}

// Verifier runs the L1/L2 pipeline (05) against candidate agent
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
			Result: RollupValidationResult(l1Status, ""),
			Checks: []CheckResult{l1Check},
		}, nil
	}

	// Checked between stages so a canceled A2A register-agent task (07)
	// stops before the next outbound probe rather than mid-request.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	l2Status, l2Detail := v.runL2(ctx, registeredURL, card)
	l2Check := CheckResult{Kind: db.CheckL2Protocol, Status: l2Status, Detail: l2Detail, CheckedAt: checkedAt, ExpiresAt: expiresAt}

	result := RollupValidationResult(l1Status, l2Status)

	return &PipelineResult{
		Result:          result,
		Checks:          []CheckResult{l1Check, l2Check},
		CardURL:         cardURL,
		CardHash:        cardHash,
		CardJSON:        cardBody,
		Card:            card,
		DeclaredAgentID: extractDeclaredID(card),
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
		// Defense in depth against a same-origin-redirect bypass slipping
		// past the probe client's own CheckRedirect (05 §Redirects): the
		// final URL actually fetched must still be on the candidate's
		// origin.
		if !SameOrigin(candidate, res.FinalURL) {
			attempts = append(attempts, attempt{URL: candidate, Status: res.StatusCode, Error: "cross-origin redirect refused"})
			continue
		}
		if res.StatusCode != 200 {
			attempts = append(attempts, attempt{URL: candidate, Status: res.StatusCode})
			continue
		}
		parsed, err := ParseCard(res.Body)
		if err != nil {
			attempts = append(attempts, attempt{URL: candidate, Status: res.StatusCode, Error: shortErr(err)})
			continue
		}
		if !parsed.IsStaticallyValid() {
			attempts = append(attempts, attempt{URL: candidate, Status: res.StatusCode, Error: "missing required name or endpoint"})
			continue
		}

		// Same-origin rule: the card's declared endpoint must be exactly
		// on the registered URL's origin (05 §Same-origin and https rules).
		endpoint := parsed.EndpointURL()
		if !SameOrigin(registeredURL, endpoint) {
			d, _ := json.Marshal(map[string]any{
				"code":         "card_endpoint_origin_mismatch",
				"card_url":     candidate,
				"declared_url": endpoint,
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

// extractDeclaredID pulls the card's declared agent id (05 §L1 is the
// extraction point for every card-derived field): nil when the card
// declares none, or the declared value fails the format rule. Purely
// informational — most cards in the wild declare no id, which is why this
// is an extraction and not a check.
func extractDeclaredID(card *CardSchema) *string {
	raw, found := card.DeclaredAgentID()
	if !found || !IsValidAgentIDFormat(raw) {
		return nil
	}
	return &raw
}

// probeRequest models the benign JSON-RPC envelope L2 sends.
type probeRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

// methodNotFound is the standard JSON-RPC "unknown method" error code.
// L2 must never let this pass — accepting it is what let any generic
// JSON-RPC service masquerade as an A2A agent (05 §L2).
const methodNotFound = -32601

// canonicalMethod / legacyMethod are L2's two task-read dialects: A2A
// renamed tasks/get (v0.1-v0.3) to GetTask in v1.0 (05 §Dialect selection).
// The message-send surface went through the same rename — message/send
// became SendMessage — and is L2's last-resort fallback for minimal agents
// that implement no task-read method at all (05 §Message fallback).
const (
	canonicalMethod        = "GetTask"
	legacyMethod           = "tasks/get"
	canonicalMessageMethod = "SendMessage"
	legacyMessageMethod    = "message/send"
)

// probeAttempt records one probe attempt for the check detail, so the
// dialect decision is auditable after the fact (05 §L2).
type probeAttempt struct {
	Method     string `json:"method"`
	HTTPStatus int    `json:"http_status,omitempty"`
	ErrorCode  *int   `json:"error_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

// runL2 probes the card's declared endpoint with a benign "get a
// nonexistent task" request. It prefers the official A2A SDK client (which
// sends the canonical v1.0 "GetTask" method, the A2A-Version header, and
// gets us typed sentinel errors for the common cases), falling back to a
// hand-rolled legacy "tasks/get" request through the same guarded HTTP
// client when the SDK path reports MethodNotFound. When both task-read
// dialects reach the server without positive evidence, the message-send
// dialects get the same treatment (05 §Message fallback) — a minimal agent
// that implements only SendMessage is still a real A2A agent, and probing
// it with deliberately empty params is benign: every implementation must
// reject a message with no parts (InvalidParams is the expected pass). All
// attempts go through v.probe's guarded *http.Client, so the SSRF guard,
// same-origin redirect policy, and body cap apply regardless of which
// one runs.
func (v *Verifier) runL2(ctx context.Context, registeredURL string, card *CardSchema) (status string, detail json.RawMessage) {
	endpoint := card.EndpointURL()
	protocolVersion := card.ProtocolVersion()

	if !SameOrigin(registeredURL, endpoint) {
		d, _ := json.Marshal(map[string]any{"code": "card_endpoint_origin_mismatch", "endpoint": endpoint})
		return db.CheckStatusFailed, d
	}

	var attempts []probeAttempt

	sdkStatus, sdkAttempt, fallThrough := v.probeSDK(ctx, card.Card)
	attempts = append(attempts, sdkAttempt)
	if !fallThrough {
		return sdkStatus, buildProbeDetail(endpoint, protocolVersion, attempts)
	}

	// Legacy fallback: the SDK path answered MethodNotFound (or couldn't
	// even build a JSONRPC transport from the card, which is the same
	// signal for a pre-1.0 card with no supportedInterfaces) — try the
	// pre-1.0 task-read method name directly. From here on each fallback
	// follows the same discriminator as probeSDK: no HTTP response at all
	// (httpStatus 0) means the endpoint is dead or blocked and another
	// method name cannot change that; any response without positive
	// evidence may still be a complaint about the method name, which is
	// exactly what the next dialect varies.
	fallbacks := []struct {
		method string
		params map[string]any
	}{
		{legacyMethod, map[string]any{"id": "task_probe_nonexistent"}},
		{canonicalMessageMethod, map[string]any{}},
		{legacyMessageMethod, map[string]any{}},
	}
	for _, fb := range fallbacks {
		hasResult, errCode, httpStatus, finalErr := v.probeOnce(ctx, endpoint, fb.method, fb.params)
		attempts = append(attempts, probeAttempt{Method: fb.method, HTTPStatus: httpStatus, ErrorCode: errCode, Error: finalErr})
		if finalErr != "" && httpStatus == 0 {
			return db.CheckStatusFailed, buildProbeDetail(endpoint, protocolVersion, attempts)
		}
		if finalErr == "" && passesProbe(hasResult, errCode) {
			return db.CheckStatusPassed, buildProbeDetail(endpoint, protocolVersion, attempts)
		}
	}
	return db.CheckStatusFailed, buildProbeDetail(endpoint, protocolVersion, attempts)
}

// sentinelCode mirrors the SDK's internal (unexported) code-to-sentinel
// table, kept here only to annotate check detail with the numeric code a
// matched sentinel corresponds to — the pass/fail decision itself is made
// via errors.Is against the sentinels, not this map.
var sentinelCode = map[error]int{
	a2a.ErrParseError:                   -32700,
	a2a.ErrInvalidRequest:               -32600,
	a2a.ErrMethodNotFound:               -32601,
	a2a.ErrInvalidParams:                -32602,
	a2a.ErrInternalError:                -32603,
	a2a.ErrServerError:                  -32000,
	a2a.ErrTaskNotFound:                 -32001,
	a2a.ErrTaskNotCancelable:            -32002,
	a2a.ErrPushNotificationNotSupported: -32003,
	a2a.ErrUnsupportedOperation:         -32004,
	a2a.ErrUnsupportedContentType:       -32005,
	a2a.ErrInvalidAgentResponse:         -32006,
	a2a.ErrExtendedCardNotConfigured:    -32007,
	a2a.ErrExtensionSupportRequired:     -32008,
	a2a.ErrVersionNotSupported:          -32009,
}

func codeForSentinel(err error) *int {
	for sentinel, code := range sentinelCode {
		if errors.Is(err, sentinel) {
			c := code
			return &c
		}
	}
	return nil
}

// probeSDK attempts L2 via the official A2A SDK client, built over the
// same guarded *http.Client v.probe uses (P1-4's SSRF hardening applies
// here too — see probe.Client.HTTPClient).
//
// fallThrough tells the caller whether to retry with the legacy method
// name, and the rule is about *what kind of failure occurred*, not the
// specific error code (05 §L2 compat): if we reached the endpoint and got
// an HTTP response back — MethodNotFound, InvalidRequest, InternalError,
// a non-200 status, or a malformed/non-JSON-RPC body — the failure may be
// about the *method name*, which is exactly what the legacy dialect
// varies, so it's always worth a second attempt. This matters for
// transitional agents: a card can advertise v1.0 supportedInterfaces (so
// the SDK client builds successfully) while the server behind it still
// only implements the pre-1.0 method names, and can signal "unknown
// method" in more ways than a clean -32601 (some frameworks return
// -32600/-32603, a 404/405 at the HTTP layer, or an HTML error page).
// Conversely, if we never got a response at all — network/DNS/TLS
// failure, timeout, or our own SSRF guard refusing the dial — the problem
// is the endpoint or address, not the method name, and retrying a
// different method string against the same dead or blocked target cannot
// change the outcome; that case fails immediately with no retry.
//
// Go's http.Client.Do is documented to return exactly *url.Error when the
// request never got a response (network/DNS/TLS/timeout errors, and our
// dial-time guard refusal surfaces through this same path); errors.As
// against that type is what distinguishes the two cases below, since the
// SDK doesn't otherwise expose a typed "no response" sentinel — every
// other failure path in the SDK's transport (non-200, malformed body,
// JSON-RPC error) wraps a response that was actually received.
func (v *Verifier) probeSDK(ctx context.Context, card *a2a.AgentCard) (status string, attempt probeAttempt, fallThrough bool) {
	client, err := a2aclient.NewFromCard(ctx, card,
		a2aclient.WithDefaultsDisabled(), // never allow the SDK's unguarded default REST/JSONRPC clients
		a2aclient.WithJSONRPCTransport(v.probe.HTTPClient()),
	)
	if err != nil {
		return db.CheckStatusFailed, probeAttempt{Method: canonicalMethod, Error: "no compatible JSONRPC transport: " + err.Error()}, true
	}

	_, err = client.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID("task_probe_nonexistent")})
	if err == nil {
		return db.CheckStatusPassed, probeAttempt{Method: canonicalMethod}, false
	}

	a := probeAttempt{Method: canonicalMethod, ErrorCode: codeForSentinel(err), Error: err.Error()}

	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		// Never got an HTTP response: endpoint-level failure, not
		// method-level. No retry — a different method name to the same
		// dead or blocked address can't help, and retrying a guard
		// refusal just burns the probe budget.
		return db.CheckStatusFailed, a, false
	}

	switch {
	case errors.Is(err, a2a.ErrTaskNotFound),
		errors.Is(err, a2a.ErrTaskNotCancelable),
		errors.Is(err, a2a.ErrPushNotificationNotSupported),
		errors.Is(err, a2a.ErrUnsupportedOperation),
		errors.Is(err, a2a.ErrUnsupportedContentType),
		errors.Is(err, a2a.ErrInvalidAgentResponse),
		errors.Is(err, a2a.ErrExtendedCardNotConfigured),
		errors.Is(err, a2a.ErrExtensionSupportRequired),
		errors.Is(err, a2a.ErrVersionNotSupported),
		errors.Is(err, a2a.ErrServerError),
		errors.Is(err, a2a.ErrInvalidParams):
		return db.CheckStatusPassed, a, false
	default:
		// We got a response, but not positive evidence: MethodNotFound,
		// InvalidRequest, InternalError (which the SDK's client also uses
		// for any code it doesn't explicitly recognize — see docs/05's
		// note on this ambiguity), a non-200 status, or a malformed/
		// non-JSON body. Always worth the legacy retry.
		return db.CheckStatusFailed, a, true
	}
}

// probeOnce sends one JSON-RPC request using method and params and
// classifies the reply. finalErr is non-empty for any hard failure
// (network, non-200, non-JSON, malformed envelope, cross-origin redirect);
// httpStatus is 0 exactly when no HTTP response was received at all;
// errCode is the envelope's error.code when an error was present.
func (v *Verifier) probeOnce(ctx context.Context, endpoint, method string, params map[string]any) (hasResult bool, errCode *int, httpStatus int, finalErr string) {
	reqID := "probe-" + uuid.NewString()
	body, _ := json.Marshal(probeRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  params,
	})

	res, err := v.probe.PostJSON(ctx, endpoint, body)
	if err != nil {
		return false, nil, 0, err.Error()
	}
	// Defense in depth: reject a same-origin-redirect bypass that slipped
	// past CheckRedirect (05 §Redirects).
	if !SameOrigin(endpoint, res.FinalURL) {
		return false, nil, res.StatusCode, "cross-origin redirect refused"
	}
	if res.StatusCode != 200 {
		return false, nil, res.StatusCode, "non-200 response"
	}

	ok, hasResultVal, code, _ := parseJSONRPCEnvelope(res.Body, reqID)
	if !ok {
		return false, nil, res.StatusCode, "not a well-formed JSON-RPC 2.0 envelope"
	}
	return hasResultVal, code, res.StatusCode, ""
}

// passesProbe implements 05 §L2's pass table for a well-formed envelope:
// positive evidence that the endpoint implements the probed method.
// MethodNotFound simply isn't positive evidence — the dialect-retry loop
// moves on to the next method name and fails once all dialects are
// exhausted.
func passesProbe(hasResult bool, errCode *int) bool {
	if hasResult {
		return true
	}
	if errCode == nil {
		return false
	}
	code := *errCode
	if code == -32602 { // InvalidParams: knows the method, disliked our dummy id
		return true
	}
	if code <= -32000 && code >= -32099 { // A2A-specific range
		return true
	}
	return false
}

func buildProbeDetail(endpoint, protocolVersion string, attempts []probeAttempt) json.RawMessage {
	var version any
	if protocolVersion != "" {
		version = protocolVersion
	}
	last := attempts[len(attempts)-1]
	d, _ := json.Marshal(map[string]any{
		"endpoint":         endpoint,
		"method":           last.Method,
		"error_code":       last.ErrorCode,
		"protocol_version": version,
		"attempts":         attempts,
	})
	return d
}

// parseJSONRPCEnvelope implements 05 §L2's envelope well-formedness check:
// jsonrpc=="2.0", id echoes the request, and exactly one of result/error is
// present. It also extracts hasResult and the error's code (if any), since
// the pass/fail decision depends on which error code came back, not just
// whether the envelope is well-formed.
func parseJSONRPCEnvelope(body []byte, wantID string) (ok, hasResult bool, errCode *int, envelope map[string]json.RawMessage) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return false, false, nil, nil
	}

	rpcRaw, present := m["jsonrpc"]
	if !present {
		return false, false, nil, nil
	}
	var rpc string
	if err := json.Unmarshal(rpcRaw, &rpc); err != nil || rpc != "2.0" {
		return false, false, nil, nil
	}

	idRaw, present := m["id"]
	if !present {
		return false, false, nil, nil
	}
	var id string
	if err := json.Unmarshal(idRaw, &id); err != nil || id != wantID {
		return false, false, nil, nil
	}

	errRaw, hasError := m["error"]
	_, hasResultVal := m["result"]
	if hasResultVal == hasError { // both present or both absent
		return false, false, nil, nil
	}

	var code *int
	if hasError {
		var e struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(errRaw, &e); err == nil {
			code = &e.Code
		}
	}

	return true, hasResultVal, code, m
}

// shortErr renders an error for check detail, truncated so a pathological
// parse error can't bloat the stored detail row.
func shortErr(err error) string {
	s := err.Error()
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// originOf returns scheme://host[:port] for a URL.
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Scheme + "://" + u.Host
}
