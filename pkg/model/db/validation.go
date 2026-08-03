package db

import (
	"encoding/json"
	"time"
)

// Validation results.
const (
	ValidationPassed             = "passed"
	ValidationPassedWithWarnings = "passed_with_warnings"
	ValidationFailed             = "failed"
)

// Validation is a short-lived pre-registration validation session created
// by POST /v1/agents/validate and consumed by POST /v1/agents. See docs/03.
type Validation struct {
	ID         string          `gorm:"column:id;primaryKey;type:text"`
	URL        string          `gorm:"column:url;type:text;not null"`
	Name       *string         `gorm:"column:name;type:text"`
	OwnerName  *string         `gorm:"column:owner_name;type:text"`
	AgentID    *string         `gorm:"column:agent_id;type:text"`
	Result     string          `gorm:"column:result;type:text;not null"`
	CardURL    string          `gorm:"column:card_url;type:text"`
	CardHash   string          `gorm:"column:card_hash;type:text"`
	CardJSON   json.RawMessage `gorm:"column:card_json;type:jsonb"`
	ConsumedAt *time.Time      `gorm:"column:consumed_at"`
	CreatedAt  time.Time       `gorm:"column:created_at;not null;autoCreateTime"`
	ExpiresAt  time.Time       `gorm:"column:expires_at;not null"`
}

// TableName pins the GORM table name explicitly.
func (Validation) TableName() string { return "validations" }
