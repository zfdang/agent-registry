package db

import (
	"encoding/json"
	"time"
)

// VerificationStatus values for Agent.VerificationStatus.
const (
	StatusVerified = "verified"
	StatusFailed   = "failed"
	StatusStale    = "stale"
)

// Agent is one listed A2A agent. The agent URL is the identity anchor —
// see docs/03-data-model.md. All display metadata is card-derived.
type Agent struct {
	ID                 string          `gorm:"column:id;primaryKey;type:text"`
	Name               string          `gorm:"column:name;type:text;not null"`
	OwnerName          *string         `gorm:"column:owner_name;type:text"`
	AgentID            *string         `gorm:"column:agent_id;type:text;index:idx_agents_agent_id"`
	URL                string          `gorm:"column:url;type:text;not null;uniqueIndex:idx_agents_url_unique"`
	Description        string          `gorm:"column:description;type:text"`
	Version            *string         `gorm:"column:version;type:text"`
	ProtocolVersion    *string         `gorm:"column:protocol_version;type:text"`
	Skills             json.RawMessage `gorm:"column:skills;type:jsonb;default:'[]'"`
	Capabilities       json.RawMessage `gorm:"column:capabilities;type:jsonb;default:'{}'"`
	CardURL            string          `gorm:"column:card_url;type:text"`
	CardHash           string          `gorm:"column:card_hash;type:text"`
	CardJSON           json.RawMessage `gorm:"column:card_json;type:jsonb"`
	CardText           string          `gorm:"column:card_text;type:text"`
	VerificationStatus string          `gorm:"column:verification_status;type:text;not null"`
	VerifiedAt         *time.Time      `gorm:"column:verified_at"`
	CreatedAt          time.Time       `gorm:"column:created_at;not null;autoCreateTime"`
	UpdatedAt          time.Time       `gorm:"column:updated_at;not null;autoUpdateTime"`
}

// TableName pins the GORM table name explicitly.
func (Agent) TableName() string { return "agents" }
