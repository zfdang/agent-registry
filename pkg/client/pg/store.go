// Package pg is the only package that talks to PostgreSQL. It exposes a
// Store interface so pkg/service depends on behavior, not gorm directly.
package pg

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zfdang/agent-registry/pkg/model/db"
)

// ListParams filters/paginates GET /v1/agents (04).
type ListParams struct {
	Query    string // matched via ILIKE across name/owner/description/url/card_text
	Status   string // "" | verified | verified_with_warnings | stale
	Sort     string // "newest" | "name"
	Page     int
	PageSize int
}

// Stats holds the computed landing-page counters (03).
type Stats struct {
	TotalAgents    int64
	VerifiedAgents int64
	NewThisWeek    int64
	DistinctOwners int64
}

// Store is the persistence boundary used by pkg/service.
type Store interface {
	Ping(ctx context.Context) error

	// Agents
	CreateAgent(ctx context.Context, a *db.Agent) error
	UpdateAgent(ctx context.Context, a *db.Agent) error
	GetAgentByID(ctx context.Context, id string) (*db.Agent, error)
	GetAgentByURL(ctx context.Context, normalizedURL string) (*db.Agent, error)
	GetAgentByCardURL(ctx context.Context, normalizedURL string) (*db.Agent, error)
	SearchAgentsByURLSubstring(ctx context.Context, substr string) ([]db.Agent, error)
	GetAgentsByAgentID(ctx context.Context, agentID string) ([]db.Agent, error)
	ListAgents(ctx context.Context, p ListParams) ([]db.Agent, int64, error)
	Stats(ctx context.Context) (Stats, error)

	// Validations
	CreateValidation(ctx context.Context, v *db.Validation) error
	GetValidation(ctx context.Context, id string) (*db.Validation, error)
	// ConsumeValidation atomically marks a validation consumed
	// (UPDATE ... WHERE consumed_at IS NULL) and reports whether this call
	// won the race.
	ConsumeValidation(ctx context.Context, id string, at time.Time) (bool, error)

	// Verification checks
	UpsertCheckForValidation(ctx context.Context, validationID, kind, status string, detail json.RawMessage, checkedAt, expiresAt time.Time) error
	UpsertCheckForAgent(ctx context.Context, agentID, kind, status string, detail json.RawMessage, checkedAt, expiresAt time.Time) error
	GetChecksForValidation(ctx context.Context, validationID string) ([]db.VerificationCheck, error)
	GetChecksForAgent(ctx context.Context, agentID string) ([]db.VerificationCheck, error)
	// LinkValidationChecksToAgent copies the validation-scoped checks onto
	// the agent (upserting per kind) and detaches the validation-scoped
	// rows so they don't linger past the validation's GC.
	LinkValidationChecksToAgent(ctx context.Context, validationID, agentID string) error

	// Card snapshots
	LatestSnapshot(ctx context.Context, agentID string) (*db.AgentCardSnapshot, error)
	CreateSnapshot(ctx context.Context, s *db.AgentCardSnapshot) error
	ListSnapshots(ctx context.Context, agentID string) ([]db.AgentCardSnapshot, error)
	TrimSnapshots(ctx context.Context, agentID string, keep int) error

	// A2A tasks
	CreateTask(ctx context.Context, t *db.A2ATask) error
	GetTask(ctx context.Context, id string) (*db.A2ATask, error)
	UpdateTask(ctx context.Context, t *db.A2ATask) error
	// CASTaskStatus performs an atomic status transition, used by the
	// register-agent worker to claim submitted tasks exactly once.
	CASTaskStatus(ctx context.Context, id, fromStatus, toStatus string) (bool, error)
	ListTasksByStatus(ctx context.Context, status string, limit int) ([]db.A2ATask, error)

	// Garbage collection
	// SweepExpiredValidations deletes validations (and their checks)
	// created before createdBefore.
	SweepExpiredValidations(ctx context.Context, createdBefore time.Time) (int64, error)
	SweepTerminalTasks(ctx context.Context, updatedBefore time.Time) (int64, error)
}
