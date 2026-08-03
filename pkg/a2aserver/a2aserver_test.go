package a2aserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/zfdang/agent-registry/pkg/client/pg"
	"github.com/zfdang/agent-registry/pkg/middleware"
	"github.com/zfdang/agent-registry/pkg/model/db"
	"github.com/zfdang/agent-registry/pkg/probe"
	"github.com/zfdang/agent-registry/pkg/service"
)

// fakeStore is a no-op pg.Store — every lookup reports "not found" — good
// enough to exercise A2A dispatch without a real database. The A2A task
// lifecycle itself no longer touches pg.Store at all (see a2aserver.go's
// task store decision), so unlike the pre-SDK tests this fake doesn't need
// to be stateful for tasks.
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

func newTestEngine(t *testing.T, sendLimiter, taskLimiter *middleware.Limiter) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	log := zap.NewNop().Sugar()
	svc := service.New(fakeStore{}, probe.NewClient(), log)
	resolver := middleware.NewIPResolver(nil)

	r := gin.New()
	Mount(r, svc, "https://registry.example", sendLimiter, taskLimiter, resolver)
	return r
}

func doRPC(t *testing.T, engine *gin.Engine, body string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/a2a", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w, req)

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body=%s)", err, w.Body.String())
	}
	return resp
}

func errorCode(t *testing.T, resp map[string]any) int {
	t.Helper()
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected an error response, got %v", resp)
	}
	code, ok := errObj["code"].(float64)
	if !ok {
		t.Fatalf("error response has no numeric code: %v", errObj)
	}
	return int(code)
}

// TestAgentCard_RoundTripsThroughSDKType is the regression test for P1-1:
// the served card must be a valid a2a.AgentCard (round-tripping through
// the SDK's own type, not just "some JSON that happens to look right"),
// carry a JSONRPC supportedInterfaces entry, and give every skill the
// v1.0-required tags.
func TestAgentCard_RoundTripsThroughSDKType(t *testing.T) {
	original := AgentCard("https://registry.example")

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal card: %v", err)
	}

	var roundTripped a2a.AgentCard
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("card does not round-trip through a2a.AgentCard: %v", err)
	}

	if roundTripped.Name != "Agent Registry" {
		t.Errorf("Name = %q, want Agent Registry", roundTripped.Name)
	}
	if len(roundTripped.SupportedInterfaces) == 0 {
		t.Fatal("SupportedInterfaces is empty")
	}
	iface := roundTripped.SupportedInterfaces[0]
	if iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("ProtocolBinding = %v, want JSONRPC", iface.ProtocolBinding)
	}
	if iface.URL != "https://registry.example/a2a" {
		t.Errorf("URL = %q, want https://registry.example/a2a", iface.URL)
	}
	if len(roundTripped.Skills) != 3 {
		t.Fatalf("expected 3 skills, got %d", len(roundTripped.Skills))
	}
	for _, sk := range roundTripped.Skills {
		if len(sk.Tags) == 0 {
			t.Errorf("skill %q missing required tags", sk.ID)
		}
	}
}

// TestSendMessage_SearchAgents confirms the read skill answers
// synchronously with a data-part Message (no Task at all).
func TestSendMessage_SearchAgents(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1000))
	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"kind":"data","data":{"skill":"search-agents","query":"x"}}]}}}`
	resp := doRPC(t, engine, body)

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected a result, got %v", resp)
	}
	// SendMessageResult is a discriminated union (Task | Message); a
	// message-only response wraps the Message under a "message" key.
	message, ok := result["message"].(map[string]any)
	if !ok {
		t.Fatalf("expected result.message, got %v", result)
	}
	parts, ok := message["parts"].([]any)
	if !ok || len(parts) == 0 {
		t.Fatalf("expected the response message to carry a data part, got %v", message)
	}
}

// TestSendMessage_UnknownSkill confirms an unrecognized skill is rejected
// with UnsupportedOperation (-32004), matching the code the a2a.Error
// sentinel maps to.
func TestSendMessage_UnknownSkill(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1000))
	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"kind":"data","data":{"skill":"not-a-real-skill"}}]}}}`
	resp := doRPC(t, engine, body)

	if code := errorCode(t, resp); code != -32004 {
		t.Errorf("error code = %d, want -32004 (UnsupportedOperation)", code)
	}
}

// TestSendMessage_RegisterAgentMissingURL confirms register-agent without
// a url is rejected with InvalidParams (-32602) before any task is created.
func TestSendMessage_RegisterAgentMissingURL(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1000))
	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"kind":"data","data":{"skill":"register-agent"}}]}}}`
	resp := doRPC(t, engine, body)

	if code := errorCode(t, resp); code != -32602 {
		t.Errorf("error code = %d, want -32602 (InvalidParams)", code)
	}
}

// TestGetTask_NotFound confirms an unknown task id fails with TaskNotFound
// (-32001).
func TestGetTask_NotFound(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1000))
	body := `{"jsonrpc":"2.0","id":1,"method":"GetTask","params":{"id":"task_does_not_exist"}}`
	resp := doRPC(t, engine, body)

	if code := errorCode(t, resp); code != -32001 {
		t.Errorf("error code = %d, want -32001 (TaskNotFound)", code)
	}
}

// TestRateLimit_SendMessage confirms SendMessage is billed against
// sendLimiter specifically (P1-3 continuation) — exhausting a
// one-request limiter must surface -32029 on the very next call.
func TestRateLimit_SendMessage(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1), middleware.NewLimiter(1000))
	body := `{"jsonrpc":"2.0","id":1,"method":"SendMessage","params":{"message":{"messageId":"m1","role":"ROLE_USER","parts":[{"kind":"data","data":{"skill":"search-agents"}}]}}}`

	first := doRPC(t, engine, body)
	if _, ok := first["error"]; ok {
		t.Fatalf("first SendMessage unexpectedly errored: %v", first)
	}

	second := doRPC(t, engine, body)
	if code := errorCode(t, second); code != -32029 {
		t.Errorf("second SendMessage error code = %d, want -32029 (rate limited)", code)
	}
}

// TestRateLimit_GetTaskUsesTaskLimiter confirms GetTask is billed against
// taskLimiter, independently of sendLimiter.
func TestRateLimit_GetTaskUsesTaskLimiter(t *testing.T) {
	engine := newTestEngine(t, middleware.NewLimiter(1000), middleware.NewLimiter(1))
	body := `{"jsonrpc":"2.0","id":1,"method":"GetTask","params":{"id":"task_x"}}`

	first := doRPC(t, engine, body)
	if errObj, ok := first["error"].(map[string]any); ok {
		if code, ok := errObj["code"].(float64); ok && int(code) == -32029 {
			t.Fatalf("first GetTask was already rate limited: %v", first)
		}
	}

	second := doRPC(t, engine, body)
	if code := errorCode(t, second); code != -32029 {
		t.Errorf("second GetTask error code = %d, want -32029 (rate limited)", code)
	}
}
