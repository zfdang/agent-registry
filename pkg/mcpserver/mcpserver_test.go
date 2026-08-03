package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zfdang/agent-registry/pkg/client/pg"
	"github.com/zfdang/agent-registry/pkg/middleware"
	"github.com/zfdang/agent-registry/pkg/model/db"
	"github.com/zfdang/agent-registry/pkg/probe"
	"github.com/zfdang/agent-registry/pkg/service"
)

// fakeStore is a no-op pg.Store — every lookup reports "not found" — good
// enough to exercise MCP dispatch without a real database.
type fakeStore struct{}

func (fakeStore) Ping(context.Context) error                   { return nil }
func (fakeStore) CreateAgent(context.Context, *db.Agent) error { return nil }
func (fakeStore) UpdateAgent(context.Context, *db.Agent) error { return nil }
func (fakeStore) GetAgentByID(context.Context, string) (*db.Agent, error) {
	return nil, gorm.ErrRecordNotFound
}
func (fakeStore) GetAgentByURL(context.Context, string) (*db.Agent, error) {
	return nil, gorm.ErrRecordNotFound
}
func (fakeStore) GetAgentByCardURL(context.Context, string) (*db.Agent, error) {
	return nil, gorm.ErrRecordNotFound
}
func (fakeStore) SearchAgentsByURLSubstring(context.Context, string) ([]db.Agent, error) {
	return nil, nil
}
func (fakeStore) GetAgentsByAgentID(context.Context, string) ([]db.Agent, error) { return nil, nil }
func (fakeStore) ListAgents(context.Context, pg.ListParams) ([]db.Agent, int64, error) {
	return nil, 0, nil
}
func (fakeStore) Stats(context.Context) (pg.Stats, error)                { return pg.Stats{}, nil }
func (fakeStore) CreateValidation(context.Context, *db.Validation) error { return nil }
func (fakeStore) GetValidation(context.Context, string) (*db.Validation, error) {
	return nil, gorm.ErrRecordNotFound
}
func (fakeStore) ConsumeValidation(context.Context, string, time.Time) (bool, error) {
	return false, nil
}
func (fakeStore) UpsertCheckForValidation(context.Context, string, string, string, json.RawMessage, time.Time, time.Time) error {
	return nil
}
func (fakeStore) UpsertCheckForAgent(context.Context, string, string, string, json.RawMessage, time.Time, time.Time) error {
	return nil
}
func (fakeStore) GetChecksForValidation(context.Context, string) ([]db.VerificationCheck, error) {
	return nil, nil
}
func (fakeStore) GetChecksForAgent(context.Context, string) ([]db.VerificationCheck, error) {
	return nil, nil
}
func (fakeStore) LinkValidationChecksToAgent(context.Context, string, string) error { return nil }
func (fakeStore) LatestSnapshot(context.Context, string) (*db.AgentCardSnapshot, error) {
	return nil, gorm.ErrRecordNotFound
}
func (fakeStore) CreateSnapshot(context.Context, *db.AgentCardSnapshot) error { return nil }
func (fakeStore) ListSnapshots(context.Context, string) ([]db.AgentCardSnapshot, error) {
	return nil, nil
}
func (fakeStore) TrimSnapshots(context.Context, string, int) error                  { return nil }
func (fakeStore) SweepExpiredValidations(context.Context, time.Time) (int64, error) { return 0, nil }
func (f fakeStore) Transaction(ctx context.Context, fn func(context.Context, pg.Store) error) error {
	return fn(ctx, f)
}

func newTestEngine(t *testing.T, readsLimiter, mutatingLimiter *middleware.Limiter) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := zap.NewNop().Sugar()
	svc := service.New(fakeStore{}, probe.NewClient(), log)
	resolver := middleware.NewIPResolver(nil)

	r := gin.New()
	Mount(r, svc, "https://registry.example", readsLimiter, mutatingLimiter, resolver)
	return r
}

// TestOrigin_InvalidRejected is the regression test for the MCP spec's
// Origin-validation MUST: a browser request whose Origin doesn't match the
// server's own origin (and isn't explicitly trusted) must be rejected
// with 403, before it ever reaches the MCP handler.
func TestOrigin_InvalidRejected(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1000))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Origin", "https://evil.example")
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for an untrusted Origin", w.Code)
	}
}

// TestOrigin_AbsentAllowed confirms non-browser callers (no Origin header
// at all) are not rejected — the whole point of "MUST reject invalid
// Origin" is browser-CSRF protection, not blocking programmatic clients.
func TestOrigin_AbsentAllowed(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1000))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	// No Origin header set.
	engine.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Errorf("status = 403, want anything but 403 when Origin is absent (body: %s)", w.Body.String())
	}
}

// TestOrigin_SameOriginAllowed confirms an Origin matching the request's
// own Host is treated as same-origin, not rejected.
func TestOrigin_SameOriginAllowed(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1000))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Host = "registry.example"
	req.Header.Set("Origin", "https://registry.example")
	engine.ServeHTTP(w, req)

	if w.Code == http.StatusForbidden {
		t.Errorf("status = 403, want anything but 403 for a same-origin request (body: %s)", w.Body.String())
	}
}

// TestMount_SmokeTest is a live end-to-end check using the SDK's own
// client against our gin-mounted handler — the acceptance criterion asks
// to verify path mounting under gin.WrapH at /mcp is actually exercised,
// not assumed, and this is what "tools/list and search_agents still work
// through the gin mount" means in practice: a real client speaking the
// real (session-less, 2026-07-28) wire protocol, not a hand-crafted
// request that might not match the current handshake-free model.
func TestMount_SmokeTest(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1000))
	httpServer := httptest.NewServer(engine)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("Connect (mounting under gin.WrapH at /mcp): %v", err)
	}
	defer session.Close()

	var names []string
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}
		names = append(names, tool.Name)
	}
	want := []string{"search_agents", "get_agent_card", "get_verification", "compare_agents", "register_agent"}
	for _, w := range want {
		found := false
		for _, n := range names {
			if n == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("tools/list is missing %q; got %v", w, names)
		}
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_agents",
		Arguments: map[string]any{"query": "x"},
	})
	if err != nil {
		t.Fatalf("CallTool(search_agents): %v", err)
	}
	if res.IsError {
		t.Fatalf("search_agents returned an error result: %+v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("search_agents returned no content")
	}
}

// schemaJSON round-trips a tool's InputSchema through JSON, the same way
// it would be marshaled over the wire, so assertions read the same shape
// a real client would see.
func schemaJSON(t *testing.T, s *jsonschema.Schema) map[string]any {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal schema: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	return m
}

// TestSchema_SearchAgents asserts search_agents' generated schema still
// matches docs/06: query/status/limit/offset, status enum, limit 1-50
// default 10, offset min 0 default 0, nothing required.
func TestSchema_SearchAgents(t *testing.T) {
	m := schemaJSON(t, searchAgentsSchema())

	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("no properties in schema: %v", m)
	}
	status := props["status"].(map[string]any)
	enum, _ := status["enum"].([]any)
	wantEnum := map[string]bool{"verified": true, "verified_with_warnings": true, "stale": true}
	if len(enum) != len(wantEnum) {
		t.Errorf("status enum = %v, want %v", enum, wantEnum)
	}
	for _, e := range enum {
		if !wantEnum[e.(string)] {
			t.Errorf("unexpected status enum value %v", e)
		}
	}

	limit := props["limit"].(map[string]any)
	if limit["minimum"] != float64(1) || limit["maximum"] != float64(50) || limit["default"] != float64(10) {
		t.Errorf("limit constraints = %v, want minimum=1 maximum=50 default=10", limit)
	}

	offset := props["offset"].(map[string]any)
	if offset["minimum"] != float64(0) || offset["default"] != float64(0) {
		t.Errorf("offset constraints = %v, want minimum=0 default=0", offset)
	}

	if _, hasRequired := m["required"]; hasRequired {
		t.Errorf("search_agents should have no required fields, got required=%v", m["required"])
	}
}

// TestSchema_RefTools asserts get_agent_card/get_verification's shared
// schema requires "ref" (06).
func TestSchema_RefTools(t *testing.T) {
	m := schemaJSON(t, refSchema("test"))
	required, _ := m["required"].([]any)
	if len(required) != 1 || required[0] != "ref" {
		t.Errorf("required = %v, want [ref]", required)
	}
	props, _ := m["properties"].(map[string]any)
	if _, ok := props["ref"]; !ok {
		t.Errorf("properties missing ref: %v", props)
	}
}

// TestSchema_CompareAgents asserts compare_agents requires refs, an array
// of 2-5 items (06).
func TestSchema_CompareAgents(t *testing.T) {
	m := schemaJSON(t, compareAgentsSchema())

	required, _ := m["required"].([]any)
	if len(required) != 1 || required[0] != "refs" {
		t.Errorf("required = %v, want [refs]", required)
	}
	props, _ := m["properties"].(map[string]any)
	refs, _ := props["refs"].(map[string]any)
	if refs["type"] != "array" || refs["minItems"] != float64(2) || refs["maxItems"] != float64(5) {
		t.Errorf("refs constraints = %v, want type=array minItems=2 maxItems=5", refs)
	}
}

// TestSchema_RegisterAgent asserts register_agent requires url (06).
func TestSchema_RegisterAgent(t *testing.T) {
	m := schemaJSON(t, registerAgentSchema())

	required, _ := m["required"].([]any)
	if len(required) != 1 || required[0] != "url" {
		t.Errorf("required = %v, want [url]", required)
	}
}
