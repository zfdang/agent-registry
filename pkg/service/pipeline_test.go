package service

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

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

func (allowAllGuard) CheckHost(string) error                                  { return nil }
func (allowAllGuard) CheckDial(string, string, string, syscall.RawConn) error { return nil }

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
// (the card's declared endpoint) with rpcBehavior. rpcBehavior receives
// the decoded request body so it can branch on body["method"] — both the
// SDK's canonical "GetTask" and the hand-rolled legacy "tasks/get"
// fallback land on this same handler.
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

// echoJSONRPCError answers any JSON-RPC request with a TaskNotFound
// (-32001) error, echoing the request id — a well-formed A2A-specific
// error that both the canonical SDK method and the legacy fallback must
// treat as a pass, since it's proof the endpoint speaks the protocol.
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
	if len(result.Checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(result.Checks))
	}
	for _, c := range result.Checks {
		if c.Status != db.CheckStatusPassed {
			t.Errorf("check %s = %s, want passed", c.Kind, c.Status)
		}
	}
	if result.DeclaredAgentID == nil || *result.DeclaredAgentID != "did:example:acme:test-agent" {
		t.Errorf("DeclaredAgentID = %v, want did:example:acme:test-agent", result.DeclaredAgentID)
	}
	if result.Card == nil || result.Card.Name() != "Test Agent" {
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

// TestVerifier_PassesWithoutDeclaredAgentID: most cards in the wild declare
// no agent id — that's informational (extraction comes back nil), never a
// check outcome, so the run passes cleanly.
func TestVerifier_PassesWithoutDeclaredAgentID(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
	}, echoJSONRPCError)

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationPassed {
		t.Errorf("result = %q, want passed", result.Result)
	}
	if result.DeclaredAgentID != nil {
		t.Errorf("DeclaredAgentID = %v, want nil", result.DeclaredAgentID)
	}
	if len(result.Checks) != 2 {
		t.Fatalf("got %d checks, want 2 (l1_card, l2_protocol)", len(result.Checks))
	}
}

func TestVerifier_L2FailsOnMalformedEnvelope(t *testing.T) {
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
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusFailed {
		t.Errorf("l2 = %+v, want failed", l2)
	}
}

// TestVerifier_L2PassesOnJSONRPCResultEnvelope confirms a `result` in the
// response passes L2 regardless of the task actually existing — result
// presence is itself the evidence the endpoint implements the method. The
// stub returns a syntactically valid (if practically unusual) a2a.Task so
// the SDK's typed unmarshal of the result succeeds.
func TestVerifier_L2PassesOnJSONRPCResultEnvelope(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
	}, func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"id":        "task_probe_nonexistent",
				"contextId": "ctx-probe",
				"status":    map[string]any{"state": "TASK_STATE_COMPLETED"},
			},
		})
	})

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationPassed {
		t.Errorf("result = %q, want passed", result.Result)
	}
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusPassed {
		t.Errorf("l2 = %+v, want passed", l2)
	}
}

// TestVerifier_L2FailsWhenMethodNotFoundOnEveryDialect is the regression
// test for P1-2: a generic JSON-RPC service that answers MethodNotFound to
// every dialect tried — both task-read methods and both message-send
// fallbacks — must fail L2, not verify as an A2A agent.
func TestVerifier_L2FailsWhenMethodNotFoundOnEveryDialect(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
	}, func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"error":   map[string]any{"code": -32601, "message": "Method not found"},
		})
	})

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationFailed {
		t.Errorf("result = %q, want failed", result.Result)
	}
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusFailed {
		t.Fatalf("l2 = %+v, want failed", l2)
	}
	var detail map[string]any
	_ = json.Unmarshal(l2.Detail, &detail)
	attempts, _ := detail["attempts"].([]any)
	if len(attempts) != 4 {
		t.Errorf("expected the SDK attempt, the legacy fallback, and both message-send fallbacks to be attempted, got %d attempts: %+v", len(attempts), detail)
	}
}

// TestVerifier_L2PassesOnUnsupportedOperation confirms an A2A-specific
// error code (-32004 UnsupportedOperation) passes L2 — it's evidence the
// endpoint understood the method and simply doesn't support this
// operation, which is different from never having heard of the method.
func TestVerifier_L2PassesOnUnsupportedOperation(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
	}, func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"error":   map[string]any{"code": -32004, "message": "Unsupported operation"},
		})
	})

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusPassed {
		t.Fatalf("l2 = %+v, want passed", l2)
	}
}

// TestVerifier_L2LegacyFallbackSucceeds exercises the two-attempt fallback:
// the SDK's canonical "GetTask" answers MethodNotFound (a pre-1.0 agent
// that only understands the legacy method name), and the hand-rolled
// "tasks/get" retry succeeds — the check must record which method finally
// answered.
func TestVerifier_L2LegacyFallbackSucceeds(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)

	mux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"name": "Test Agent",
			"supportedInterfaces": []map[string]any{
				{"url": ts.URL, "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
			},
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		if body["method"] == canonicalMethod {
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": body["id"],
				"error": map[string]any{"code": -32601, "message": "Method not found"},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": body["id"],
			"error": map[string]any{"code": -32001, "message": "TaskNotFound"},
		})
	})

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusPassed {
		t.Fatalf("l2 = %+v, want passed after legacy fallback", l2)
	}
	var detail map[string]any
	_ = json.Unmarshal(l2.Detail, &detail)
	if detail["method"] != legacyMethod {
		t.Errorf("detail method = %v, want %q (the dialect that finally answered)", detail["method"], legacyMethod)
	}
}

// newV1StubAgent builds a stub agent card with a v1.0 supportedInterfaces
// entry (so a2aclient.NewFromCard succeeds and the SDK actually makes an
// HTTP call), whose endpoint dispatches on the incoming JSON-RPC method
// name via canonicalBehavior (for the SDK's "GetTask") and
// legacyBehavior (for the hand-rolled "tasks/get" fallback).
func newV1StubAgent(t *testing.T, canonicalBehavior, legacyBehavior func(w http.ResponseWriter, body map[string]any)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	t.Cleanup(ts.Close)

	mux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"name": "Test Agent",
			"supportedInterfaces": []map[string]any{
				{"url": ts.URL, "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
			},
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["method"] == canonicalMethod {
			canonicalBehavior(w, body)
			return
		}
		legacyBehavior(w, body)
	})
	return ts
}

// TestVerifier_L2FallsBackOnInternalError is the review's transitional-
// agent regression: a v1.0 card whose server answers the canonical
// GetTask with -32603 InternalError (not a clean -32601) — a common,
// non-compliant way to signal "unknown method" — must still fall through
// to the legacy dialect rather than failing outright, since we did get an
// HTTP response and the failure could be about the method name.
func TestVerifier_L2FallsBackOnInternalError(t *testing.T) {
	ts := newV1StubAgent(t,
		func(w http.ResponseWriter, body map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": body["id"],
				"error": map[string]any{"code": -32603, "message": "Internal error"},
			})
		},
		func(w http.ResponseWriter, body map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": body["id"],
				"error": map[string]any{"code": -32001, "message": "TaskNotFound"},
			})
		},
	)

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusPassed {
		t.Fatalf("l2 = %+v, want passed after falling back from -32603", l2)
	}
	var detail map[string]any
	_ = json.Unmarshal(l2.Detail, &detail)
	if detail["method"] != legacyMethod {
		t.Errorf("detail method = %v, want %q", detail["method"], legacyMethod)
	}
}

// TestVerifier_L2FallsBackOnHTTPErrorStatus is the same transitional-agent
// case via a non-200 HTTP status on the canonical attempt (e.g. a
// framework routing unknown JSON-RPC methods to a 404) instead of a
// JSON-RPC error body.
func TestVerifier_L2FallsBackOnHTTPErrorStatus(t *testing.T) {
	ts := newV1StubAgent(t,
		func(w http.ResponseWriter, body map[string]any) {
			w.WriteHeader(http.StatusNotFound)
		},
		func(w http.ResponseWriter, body map[string]any) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": body["id"],
				"error": map[string]any{"code": -32001, "message": "TaskNotFound"},
			})
		},
	)

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusPassed {
		t.Fatalf("l2 = %+v, want passed after falling back from HTTP 404", l2)
	}
	var detail map[string]any
	_ = json.Unmarshal(l2.Detail, &detail)
	if detail["method"] != legacyMethod {
		t.Errorf("detail method = %v, want %q", detail["method"], legacyMethod)
	}
}

// TestVerifier_L2NoRetryOnUnreachableEndpoint is the regression test for
// *not* retrying endpoint-level failures: when the canonical attempt never
// gets an HTTP response at all (here, a closed TCP port — connection
// refused, indistinguishable in kind from a DNS failure, TLS failure, or
// our own SSRF guard refusing the dial), the legacy dialect must not be
// tried either, since a different method name to the same dead address
// can't change the outcome. Exactly one L2 attempt must be recorded.
func TestVerifier_L2NoRetryOnUnreachableEndpoint(t *testing.T) {
	// Bind a listener, learn its address, then close it immediately so the
	// address is guaranteed to refuse connections.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	unreachable := "https://" + addr

	// Exercises probeSDK directly (same pattern as
	// TestProbeSDK_UsesGuardedClient) rather than the full L1+L2 pipeline,
	// since same-origin requires the registered URL and the card's
	// endpoint to match, and this test only cares about L2's retry
	// decision for an endpoint-level failure.
	client := probe.NewClient(probe.WithGuard(allowAllGuard{}))
	v := NewVerifier(client)
	card := &a2a.AgentCard{
		Name:                "Test Agent",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(unreachable, a2a.TransportProtocolJSONRPC)},
	}

	status, attempt, fallThrough := v.probeSDK(context.Background(), card)

	if status != db.CheckStatusFailed {
		t.Errorf("status = %q, want failed", status)
	}
	if fallThrough {
		t.Error("fallThrough = true, want false (an unreachable endpoint must not be retried)")
	}
	if attempt.Method != canonicalMethod {
		t.Errorf("attempt.Method = %q, want %q", attempt.Method, canonicalMethod)
	}
}

// TestVerifier_L1CrossOriginRedirectRefused is P1-4's L1 regression: an
// open redirect on the registered host must not let a different origin's
// card get recorded under it.
func TestVerifier_L1CrossOriginRedirectRefused(t *testing.T) {
	attackerMux := http.NewServeMux()
	attacker := httptest.NewTLSServer(attackerMux)
	defer attacker.Close()
	attackerMux.HandleFunc("/card", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"name":"attacker","url":"` + attacker.URL + `"}`))
	})

	targetMux := http.NewServeMux()
	target := httptest.NewTLSServer(targetMux)
	defer target.Close()
	targetMux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/card", http.StatusFound)
	})

	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())
	pool.AddCert(attacker.Certificate())
	client := probe.NewClient(probe.WithGuard(allowAllGuard{}), probe.WithTLSConfig(&tls.Config{RootCAs: pool}))
	v := NewVerifier(client)

	result, err := v.Run(context.Background(), target.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationFailed {
		t.Errorf("result = %q, want failed (cross-origin redirect must not hand the card to the attacker's origin)", result.Result)
	}
	if len(result.Checks) != 1 || result.Checks[0].Status != db.CheckStatusFailed {
		t.Fatalf("l1 = %+v, want a single failed check", result.Checks)
	}
}

// TestVerifier_L2CrossOriginRedirectRefused is P1-4's L2 regression: the
// protocol probe must not follow a redirect off the card's origin (307/308
// preserve method+body, which would hand the probe to an
// attacker-controlled endpoint). The card here has no supportedInterfaces
// (legacy top-level url only), so the SDK step falls straight through to
// the hand-rolled legacy attempt, which is what actually issues the POST —
// exercising the redirect refusal on that path directly.
func TestVerifier_L2CrossOriginRedirectRefused(t *testing.T) {
	attackerMux := http.NewServeMux()
	attacker := httptest.NewTLSServer(attackerMux)
	defer attacker.Close()
	attackerMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		echoJSONRPCError(w, body)
	})

	originMux := http.NewServeMux()
	origin := httptest.NewTLSServer(originMux)
	defer origin.Close()
	originMux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"name":"Test Agent","url":"` + origin.URL + `"}`))
	})
	originMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, attacker.URL+"/", http.StatusTemporaryRedirect)
	})

	pool := x509.NewCertPool()
	pool.AddCert(origin.Certificate())
	pool.AddCert(attacker.Certificate())
	client := probe.NewClient(probe.WithGuard(allowAllGuard{}), probe.WithTLSConfig(&tls.Config{RootCAs: pool}))
	v := NewVerifier(client)

	result, err := v.Run(context.Background(), origin.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusFailed {
		t.Fatalf("l2 = %+v, want failed (cross-origin redirect must be refused)", l2)
	}
}

// TestProbeSDK_UsesGuardedClient is the acceptance-criteria regression
// test proving the SDK client path really goes through v.probe's guarded
// *http.Client and not some unguarded default: pointing the SDK client at
// a loopback address (which the real AddressGuard refuses, unlike the
// allowAllGuard the other tests use) must fail with the guard's own
// error, not a plain connection error.
func TestProbeSDK_UsesGuardedClient(t *testing.T) {
	mux := http.NewServeMux()
	ts := httptest.NewTLSServer(mux)
	defer ts.Close()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		echoJSONRPCError(w, body)
	})

	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	// No WithGuard override this time — the real AddressGuard is what
	// must refuse the loopback dial.
	client := probe.NewClient(probe.WithTLSConfig(&tls.Config{RootCAs: pool}))
	v := NewVerifier(client)

	card := &a2a.AgentCard{
		Name:                "Test Agent",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(ts.URL, a2a.TransportProtocolJSONRPC)},
	}

	status, attempt, fallThrough := v.probeSDK(context.Background(), card)
	if status != db.CheckStatusFailed {
		t.Errorf("status = %q, want failed (loopback must be refused)", status)
	}
	if fallThrough {
		t.Error("fallThrough = true, want false (a guard refusal is not a MethodNotFound case)")
	}
	if !strings.Contains(strings.ToLower(attempt.Error), "loopback") {
		t.Errorf("attempt.Error = %q, want it to mention the guard's loopback refusal", attempt.Error)
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

// TestVerifier_L2MessageFallbackSucceeds covers the minimal-agent case
// observed in the wild (docs/05 §Message fallback): a v1.0 agent that
// implements only the message-send surface answers MethodNotFound to both
// task-read dialects, then rejects the deliberately empty SendMessage
// params with InvalidParams — which is positive evidence it implements the
// method, so L2 passes and records SendMessage as the method that finally
// answered.
func TestVerifier_L2MessageFallbackSucceeds(t *testing.T) {
	ts := newStubAgent(t, map[string]any{
		"name": "Test Agent",
	}, func(w http.ResponseWriter, body map[string]any) {
		w.Header().Set("Content-Type", "application/json")
		if body["method"] == canonicalMessageMethod {
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0", "id": body["id"],
				"error": map[string]any{"code": -32602, "message": "Invalid params: no message parts"},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": body["id"],
			"error": map[string]any{"code": -32601, "message": "Method not found"},
		})
	})

	v := newTestVerifier(ts)
	result, err := v.Run(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result != db.ValidationPassed {
		t.Errorf("result = %q, want passed", result.Result)
	}
	l2 := findCheck(result.Checks, db.CheckL2Protocol)
	if l2 == nil || l2.Status != db.CheckStatusPassed {
		t.Fatalf("l2 = %+v, want passed after message fallback", l2)
	}
	var detail map[string]any
	_ = json.Unmarshal(l2.Detail, &detail)
	if detail["method"] != canonicalMessageMethod {
		t.Errorf("detail method = %v, want %q (the dialect that finally answered)", detail["method"], canonicalMessageMethod)
	}
}
