package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/zfdang/agent-registry/pkg/model/db"
)

// GormStore implements Store on top of gorm.io/gorm + the postgres driver.
type GormStore struct {
	db *gorm.DB
}

// Open connects to Postgres, runs migrations, and returns a ready Store.
func Open(dsn string) (*GormStore, error) {
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger:                                   logger.Default.LogMode(logger.Warn),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	if err := db.Migrate(gdb); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &GormStore{db: gdb}, nil
}

// DB exposes the underlying *gorm.DB for callers (e.g. main.go's readiness
// probe) that need direct access without widening the Store interface.
func (s *GormStore) DB() *gorm.DB { return s.db }

func (s *GormStore) Ping(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// ---- Agents ----

func (s *GormStore) CreateAgent(ctx context.Context, a *db.Agent) error {
	return s.db.WithContext(ctx).Create(a).Error
}

func (s *GormStore) UpdateAgent(ctx context.Context, a *db.Agent) error {
	return s.db.WithContext(ctx).Save(a).Error
}

func (s *GormStore) GetAgentByID(ctx context.Context, id string) (*db.Agent, error) {
	var a db.Agent
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *GormStore) GetAgentByURL(ctx context.Context, normalizedURL string) (*db.Agent, error) {
	var a db.Agent
	if err := s.db.WithContext(ctx).Where("url = ?", normalizedURL).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *GormStore) GetAgentByCardURL(ctx context.Context, normalizedURL string) (*db.Agent, error) {
	var a db.Agent
	if err := s.db.WithContext(ctx).Where("card_url = ?", normalizedURL).First(&a).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *GormStore) SearchAgentsByURLSubstring(ctx context.Context, substr string) ([]db.Agent, error) {
	var agents []db.Agent
	like := "%" + escapeLike(substr) + "%"
	if err := s.db.WithContext(ctx).
		Where("url ILIKE ? ESCAPE '\\' OR card_url ILIKE ? ESCAPE '\\'", like, like).
		Find(&agents).Error; err != nil {
		return nil, err
	}
	return agents, nil
}

func (s *GormStore) GetAgentsByAgentID(ctx context.Context, agentID string) ([]db.Agent, error) {
	var agents []db.Agent
	if err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).Find(&agents).Error; err != nil {
		return nil, err
	}
	return agents, nil
}

func (s *GormStore) ListAgents(ctx context.Context, p ListParams) ([]db.Agent, int64, error) {
	q := s.db.WithContext(ctx).Model(&db.Agent{})

	if p.Query != "" {
		like := "%" + escapeLike(p.Query) + "%"
		q = q.Where(
			"name ILIKE ? ESCAPE '\\' OR owner_name ILIKE ? ESCAPE '\\' OR description ILIKE ? ESCAPE '\\' OR url ILIKE ? ESCAPE '\\' OR card_text ILIKE ? ESCAPE '\\'",
			like, like, like, like, like,
		)
	}
	// verification_status is denormalized at write time (register/refresh)
	// but a check's expiry can flip the *effective* status to "stale"
	// purely by the clock, with no write (03 §Freshness). The stored
	// column therefore never contains "stale" — filtering on it has to
	// test for live expired checks directly. "failed" takes precedence
	// over staleness in the roll-up (03 §roll-up), so the stale filter
	// excludes agents already stored as failed.
	const staleExists = `EXISTS (SELECT 1 FROM verification_checks vc WHERE vc.agent_id = agents.id AND vc.expires_at < now())`
	switch p.Status {
	case "":
		// no filter
	case db.StatusStale:
		q = q.Where("verification_status != ? AND "+staleExists, db.StatusFailed)
	case db.StatusVerified, db.StatusVerifiedWithWarnings:
		q = q.Where("verification_status = ? AND NOT "+staleExists, p.Status)
	default:
		q = q.Where("verification_status = ?", p.Status)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	switch p.Sort {
	case "name":
		q = q.Order("name ASC")
	default:
		q = q.Order("created_at DESC")
	}

	page, pageSize := p.Page, p.PageSize
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	q = q.Limit(pageSize).Offset((page - 1) * pageSize)

	var agents []db.Agent
	if err := q.Find(&agents).Error; err != nil {
		return nil, 0, err
	}
	return agents, total, nil
}

func (s *GormStore) Stats(ctx context.Context) (Stats, error) {
	var out Stats
	tx := s.db.WithContext(ctx)

	if err := tx.Model(&db.Agent{}).Count(&out.TotalAgents).Error; err != nil {
		return out, err
	}
	if err := tx.Model(&db.Agent{}).
		Where("verification_status IN ?", []string{db.StatusVerified, db.StatusVerifiedWithWarnings}).
		Count(&out.VerifiedAgents).Error; err != nil {
		return out, err
	}
	if err := tx.Model(&db.Agent{}).
		Where("created_at > ?", time.Now().UTC().Add(-7*24*time.Hour)).
		Count(&out.NewThisWeek).Error; err != nil {
		return out, err
	}
	if err := tx.Model(&db.Agent{}).
		Where("owner_name IS NOT NULL").
		Distinct("owner_name").
		Count(&out.DistinctOwners).Error; err != nil {
		return out, err
	}
	return out, nil
}

// ---- Validations ----

func (s *GormStore) CreateValidation(ctx context.Context, v *db.Validation) error {
	return s.db.WithContext(ctx).Create(v).Error
}

func (s *GormStore) GetValidation(ctx context.Context, id string) (*db.Validation, error) {
	var v db.Validation
	if err := s.db.WithContext(ctx).Where("id = ?", id).First(&v).Error; err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *GormStore) ConsumeValidation(ctx context.Context, id string, at time.Time) (bool, error) {
	res := s.db.WithContext(ctx).Model(&db.Validation{}).
		Where("id = ? AND consumed_at IS NULL", id).
		Update("consumed_at", at)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// ---- Verification checks ----

func (s *GormStore) UpsertCheckForValidation(ctx context.Context, validationID, kind, status string, detail json.RawMessage, checkedAt, expiresAt time.Time) error {
	return s.db.WithContext(ctx).Exec(`
		INSERT INTO verification_checks (id, agent_id, validation_id, kind, status, detail, checked_at, expires_at)
		VALUES (?, NULL, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (validation_id, kind) WHERE validation_id IS NOT NULL
		DO UPDATE SET status = EXCLUDED.status, detail = EXCLUDED.detail,
			checked_at = EXCLUDED.checked_at, expires_at = EXCLUDED.expires_at
	`, db.NewID(db.CheckIDPrefix), validationID, kind, status, detail, checkedAt, expiresAt).Error
}

func (s *GormStore) UpsertCheckForAgent(ctx context.Context, agentID, kind, status string, detail json.RawMessage, checkedAt, expiresAt time.Time) error {
	return s.db.WithContext(ctx).Exec(`
		INSERT INTO verification_checks (id, agent_id, validation_id, kind, status, detail, checked_at, expires_at)
		VALUES (?, ?, NULL, ?, ?, ?, ?, ?)
		ON CONFLICT (agent_id, kind) WHERE agent_id IS NOT NULL
		DO UPDATE SET status = EXCLUDED.status, detail = EXCLUDED.detail,
			checked_at = EXCLUDED.checked_at, expires_at = EXCLUDED.expires_at,
			validation_id = NULL
	`, db.NewID(db.CheckIDPrefix), agentID, kind, status, detail, checkedAt, expiresAt).Error
}

func (s *GormStore) GetChecksForValidation(ctx context.Context, validationID string) ([]db.VerificationCheck, error) {
	var checks []db.VerificationCheck
	if err := s.db.WithContext(ctx).Where("validation_id = ?", validationID).Find(&checks).Error; err != nil {
		return nil, err
	}
	return checks, nil
}

func (s *GormStore) GetChecksForAgent(ctx context.Context, agentID string) ([]db.VerificationCheck, error) {
	var checks []db.VerificationCheck
	if err := s.db.WithContext(ctx).Where("agent_id = ?", agentID).Find(&checks).Error; err != nil {
		return nil, err
	}
	return checks, nil
}

func (s *GormStore) LinkValidationChecksToAgent(ctx context.Context, validationID, agentID string) error {
	checks, err := s.GetChecksForValidation(ctx, validationID)
	if err != nil {
		return err
	}
	for _, c := range checks {
		if err := s.UpsertCheckForAgent(ctx, agentID, c.Kind, c.Status, c.Detail, c.CheckedAt, c.ExpiresAt); err != nil {
			return err
		}
	}
	// Detach the validation-scoped rows now that the agent owns them; the
	// validation's own GC sweep will remove them once it expires.
	return s.db.WithContext(ctx).
		Model(&db.VerificationCheck{}).
		Where("validation_id = ?", validationID).
		Update("validation_id", nil).Error
}

// ---- Card snapshots ----

func (s *GormStore) LatestSnapshot(ctx context.Context, agentID string) (*db.AgentCardSnapshot, error) {
	var snap db.AgentCardSnapshot
	err := s.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("fetched_at DESC").
		First(&snap).Error
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

func (s *GormStore) CreateSnapshot(ctx context.Context, snap *db.AgentCardSnapshot) error {
	return s.db.WithContext(ctx).Create(snap).Error
}

func (s *GormStore) ListSnapshots(ctx context.Context, agentID string) ([]db.AgentCardSnapshot, error) {
	var snaps []db.AgentCardSnapshot
	if err := s.db.WithContext(ctx).
		Where("agent_id = ?", agentID).
		Order("fetched_at DESC").
		Find(&snaps).Error; err != nil {
		return nil, err
	}
	return snaps, nil
}

func (s *GormStore) TrimSnapshots(ctx context.Context, agentID string, keep int) error {
	return s.db.WithContext(ctx).Exec(`
		DELETE FROM agent_card_snapshots
		WHERE agent_id = ? AND id NOT IN (
			SELECT id FROM agent_card_snapshots
			WHERE agent_id = ?
			ORDER BY fetched_at DESC
			LIMIT ?
		)
	`, agentID, agentID, keep).Error
}

// ---- Garbage collection ----

// SweepExpiredValidations deletes validation rows (and their still-linked
// checks) created before createdBefore — 24h after creation by default
// (03), independent of the 15-minute usability expiry recorded in
// expires_at.
func (s *GormStore) SweepExpiredValidations(ctx context.Context, createdBefore time.Time) (int64, error) {
	// Delete checks first: they reference validations but the app does not
	// rely on a DB-level cascade for this relationship (see docs/03).
	sub := s.db.WithContext(ctx).Model(&db.Validation{}).
		Select("id").
		Where("created_at < ?", createdBefore)

	if err := s.db.WithContext(ctx).
		Where("validation_id IN (?)", sub).
		Delete(&db.VerificationCheck{}).Error; err != nil {
		return 0, err
	}

	res := s.db.WithContext(ctx).Where("created_at < ?", createdBefore).Delete(&db.Validation{})
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// Transaction runs fn against a Store bound to a single DB transaction; an
// error returned by fn rolls the whole thing back (P2-3). Nested calls
// (Transaction inside a fn already running in a transaction) reuse the
// same underlying *gorm.DB, since gorm.Transaction supports that directly.
func (s *GormStore) Transaction(ctx context.Context, fn func(ctx context.Context, tx Store) error) error {
	return s.db.WithContext(ctx).Transaction(func(txDB *gorm.DB) error {
		return fn(ctx, &GormStore{db: txDB})
	})
}

// escapeLike escapes ILIKE metacharacters so search input can't inject
// wildcards; queries use ESCAPE '\'.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// ErrNotFound is a convenience alias for gorm's not-found sentinel so
// pkg/service doesn't need to import gorm directly.
var ErrNotFound = gorm.ErrRecordNotFound

// IsNotFound reports whether err is a "no rows" error from the store.
func IsNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}
