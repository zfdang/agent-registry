package db

import (
	"fmt"

	"gorm.io/gorm"
)

// Migrate runs AutoMigrate for all entities, then applies the hand-written
// indexes AutoMigrate cannot express: the pg_trgm extension and its GIN
// indexes, and the partial/composite unique indexes. See docs/03.
func Migrate(gdb *gorm.DB) error {
	if err := gdb.AutoMigrate(
		&Agent{},
		&VerificationCheck{},
		&Validation{},
		&AgentCardSnapshot{},
	); err != nil {
		return fmt.Errorf("automigrate: %w", err)
	}

	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
		`CREATE INDEX IF NOT EXISTS idx_agents_name_trgm ON agents USING gin (name gin_trgm_ops)`,
		`CREATE INDEX IF NOT EXISTS idx_agents_url_trgm ON agents USING gin (url gin_trgm_ops)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_checks_agent_kind_unique ON verification_checks (agent_id, kind) WHERE agent_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_checks_validation_kind_unique ON verification_checks (validation_id, kind) WHERE validation_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_snapshots_agent_hash_unique ON agent_card_snapshots (agent_id, card_hash)`,
		// 2026-08: verification collapsed from three checks to two —
		// l3_protocol renamed to l2_protocol, the advisory l2_id check
		// removed, and the *_with_warnings vocabulary retired (docs/05).
		// Idempotent data migrations for rows written under the old names.
		`DELETE FROM verification_checks WHERE kind = 'l2_id'`,
		`UPDATE verification_checks SET kind = 'l2_protocol' WHERE kind = 'l3_protocol'`,
		`UPDATE agents SET verification_status = 'verified' WHERE verification_status = 'verified_with_warnings'`,
		`UPDATE validations SET result = 'passed' WHERE result = 'passed_with_warnings'`,
	}

	for _, stmt := range statements {
		if err := gdb.Exec(stmt).Error; err != nil {
			return fmt.Errorf("index migration (%s): %w", stmt, err)
		}
	}

	return nil
}
