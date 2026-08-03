package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"github.com/zfdang/agent-registry/pkg/client/pg"
	"github.com/zfdang/agent-registry/pkg/middleware"
	"github.com/zfdang/agent-registry/pkg/model/db"
	"github.com/zfdang/agent-registry/pkg/probe"
	"github.com/zfdang/agent-registry/pkg/service"
)

// fakeStore is a no-op pg.Store good enough to exercise routing without a
// real database — every lookup reports "not found".
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

// TestNewDoesNotPanicOnRouteRegistration exercises gin's route registration
// for conflicts. gin's radix-tree router panics immediately at
// registration time if a static route and a wildcard route are siblings
// under the same prefix for the same method (04's routing constraint) — so
// simply building the engine is the regression test; the follow-up
// requests confirm each route resolves to its intended handler.
func TestNewDoesNotPanicOnRouteRegistration(t *testing.T) {
	log := zap.NewNop().Sugar()
	svc := service.New(fakeStore{}, probe.NewClient(), log)
	resolver := middleware.NewIPResolver(nil)

	engine := New(svc, log, resolver, nil, nil)
	if engine == nil {
		t.Fatal("New returned nil engine")
	}

	for _, req := range []struct {
		method, path string
		wantStatus   int
	}{
		{"GET", "/health", 200},
		{"GET", "/v1/agents", 200},
		{"GET", "/v1/agents/agt_123", 404},
		{"GET", "/v1/agents/agt_123/verification", 404},
		{"GET", "/v1/agents/agt_123/cards", 404},
		{"POST", "/v1/agents", 400},          // no body
		{"POST", "/v1/agents/validate", 400}, // no body
		{"POST", "/v1/agents/agt_123/refresh", 404},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(req.method, req.path, nil)
		engine.ServeHTTP(w, r)
		if w.Code != req.wantStatus {
			t.Errorf("%s %s: status = %d, want %d (body: %s)", req.method, req.path, w.Code, req.wantStatus, w.Body.String())
		}
	}
}
