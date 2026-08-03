package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"syscall"
	"testing"

	"github.com/zfdang/agent-registry/pkg/model/db"
	"github.com/zfdang/agent-registry/pkg/probe"
)

// allowAllGuard is a probe.Guard that refuses nothing — the pipeline test
// talks to an httptest.NewTLSServer on 127.0.0.1, which the production
// AddressGuard would (correctly) refuse as loopback. Using the guard's
// injectability (rather than weakening AddressGuard itself) keeps SSRF
// protection unchanged in production while still letting the pipeline run
// against a local stub in tests.
type allowAllGuard struct{}

func (allowAllGuard) CheckHost(string) error                        { return nil }
func (allowAllGuard) Control(string, string, syscall.RawConn) error { return nil }

// newTestVerifier builds a Verifier whose probe client trusts ts's TLS
// certificate and bypasses the address guard, per the test hooks
// pkg/probe exposes for exactly this purpose.
func newTestVerifier(ts *httptest.Server) *Verifier {
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())

	client := probe.NewClient(
		probe.WithGuard(allowAllGuard{}),
		probe.WithTLSConfig(&tls.Config{RootCAs: pool}),
	)
	return NewVerifier(client)
}

// newStubAgent serves an agent card at /.well-known/agent-card.json whose
// own url points back at the server, and answers JSON-RPC POSTs at "/"
// (the card's declared endpoint) with rpcBehavior.
func newStubAgent(t *testing.T, cardFields map[string]any, rpcBehavior func(w http.ResponseWriter, reqBody map[string]any)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)

	if cardFields != nil {
		if _, ok := cardFields["url"]; !ok {
			cardFields["url"] = ts.URL
		}
		body, err := json.Marshal(cardFields)
		if err != nil {
			t.Fatalf("marshal card: %v", err)
		}
		mux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
		})
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || rpcBehavior == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rpcBehavior(w, body)
	})

	return ts
}

func echoJSONRPCError(w http.ResponseWriter, body map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      body["id"],
		"error":   map[string]any{"code": -32001, "message": "TaskNotFound"},
	})
}

func TestVerifier_HappyPath(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name":         "Test Agent",
		"description":  "A test agent",
		"provider":     map[string]any{"organization": "Acme Labs"},
		"skills":       []map[string]any{{"id": "echo", "name": "Echo", "description": "Echoes input"}},
		"capabilities": map[string]any{"streaming": false},
		"x-registry":   map[string]any{"agentId": "did:example:acme:test-agent"},
	}, echoJSONRPCError)

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Result != db.ValidationPassed {
		t.Errorf("result = %q, want %q", result.Result, db.ValidationPassed)
	}
	if len(result.Checks) != 3 {
		t.Fatalf("got %d checks, want 3", len(result.Checks))
	}
	for _, c := range result.Checks {
		if c.Status != db.CheckStatusPassed {
			t.Errorf("check %s = %s, want passed", c.Kind, c.Status)
		}
	}
	if result.DeclaredAgentID == nil || *result.DeclaredAgentID != "did:example:acme:test-agent" {
		t.Errorf("DeclaredAgentID = %v, want did:example:acme:test-agent", result.DeclaredAgentID)
	}
	if result.Card == nil || result.Card.Name != "Test Agent" {
		t.Errorf("Card not extracted correctly: %+v", result.Card)
	}
	if result.CardHash == "" {
		t.Error("expected a non-empty card hash")
	}
}

func TestVerifier_L1FailsWhenCardMissing(t *testing.T) {
	ts := newStubAgent(t, nil, nil) // no card handler registered at all -> 404 everywhere
	v := newTestVerifier(ts)

	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationFailed {
		t.Errorf("result = %q, want failed", result.Result)
	}
	if len(result.Checks) != 1 || result.Checks[0].Kind != db.CheckL1Card {
		t.Fatalf("expected pipeline to short-circuit after L1, got checks: %+v", result.Checks)
	}
	if result.Checks[0].Status != db.CheckStatusFailed {
		t.Errorf("l1 status = %s, want failed", result.Checks[0].Status)
	}
}

func TestVerifier_L1FailsWhenNameMissing(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)
	mux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"description": "missing a name and url"}`))
	})

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationFailed {
		t.Errorf("result = %q, want failed", result.Result)
	}
}

func TestVerifier_OriginMismatchFailsL1(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
		"url":  "https://evil.example/a2a", // different origin than ts.URL
	}, echoJSONRPCError)

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationFailed {
		t.Errorf("result = %q, want failed", result.Result)
	}
	if len(result.Checks) != 1 {
		t.Fatalf("expected short-circuit on origin mismatch, got %+v", result.Checks)
	}
	var detail map[string]any
	_ = json.Unmarshal(result.Checks[0].Detail, &detail)
	if detail["code"] != "card_endpoint_origin_mismatch" {
		t.Errorf("detail code = %v, want card_endpoint_origin_mismatch", detail["code"])
	}
}

func TestVerifier_L2WarningWhenNoAgentIDDeclared(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
	}, echoJSONRPCError)

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationPassedWithWarnings {
		t.Errorf("result = %q, want passed_with_warnings", result.Result)
	}
	if result.DeclaredAgentID != nil {
		t.Errorf("DeclaredAgentID = %v, want nil", result.DeclaredAgentID)
	}
	l2 := findCheck(result.Checks, db.CheckL2ID)
	if l2 == nil || l2.Status != db.CheckStatusWarning {
		t.Errorf("l2 = %+v, want warning", l2)
	}
}

func TestVerifier_L3FailsOnMalformedEnvelope(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
	}, func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"not": "a jsonrpc envelope"}`))
	})

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationFailed {
		t.Errorf("result = %q, want failed", result.Result)
	}
	l3 := findCheck(result.Checks, db.CheckL3Protocol)
	if l3 == nil || l3.Status != db.CheckStatusFailed {
		t.Errorf("l3 = %+v, want failed", l3)
	}
}

func TestVerifier_L3PassesOnJSONRPCResultEnvelope(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
	}, func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result":  map[string]any{"status": "not_found"},
		})
	})

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationPassedWithWarnings {
		t.Errorf("result = %q, want passed_with_warnings (l2 has no declared id)", result.Result)
	}
	l3 := findCheck(result.Checks, db.CheckL3Protocol)
	if l3 == nil || l3.Status != db.CheckStatusPassed {
		t.Errorf("l3 = %+v, want passed", l3)
	}
}

func findCheck(checks []CheckResult, kind string) *CheckResult {
	for i := range checks {
		if checks[i].Kind == kind {
			return &checks[i]
		}
	}
	return nil
}
