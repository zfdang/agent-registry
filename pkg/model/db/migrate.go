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
	}

	for _, stmt := range statements {
		if err := gdb.Exec(stmt).Error; err != nil {
			return fmt.Errorf("index migration (%s): %w", stmt, err)
		}
	}

	return nil
}
