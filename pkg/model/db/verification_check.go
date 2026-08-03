package db

import (
	"encoding/json"
	"time"
)

// Check kinds.
const (
	CheckL1Card     = "l1_card"
	CheckL2ID       = "l2_id"
	CheckL3Protocol = "l3_protocol"
)

// Check statuses.
const (
	CheckStatusPassed  = "passed"
	CheckStatusWarning = "warning"
	CheckStatusFailed  = "failed"
)

// VerificationCheck is at most one live row per (agent, kind), or per
// (validation, kind) before an agent exists. See docs/03.
type VerificationCheck struct {
	ID           string          `gorm:"column:id;primaryKey;type:text"`
	AgentID      *string         `gorm:"column:agent_id;type:text;index"`
	ValidationID *string         `gorm:"column:validation_id;type:text;index"`
	Kind         string          `gorm:"column:kind;type:text;not null"`
	Status       string          `gorm:"column:status;type:text;not null"`
	Detail       json.RawMessage `gorm:"column:detail;type:jsonb"`
	CheckedAt    time.Time       `gorm:"column:checked_at;not null"`
	ExpiresAt    time.Time       `gorm:"column:expires_at;not null"`
}

// TableName pins the GORM table name explicitly.
func (VerificationCheck) TableName() string { return "verification_checks" }
