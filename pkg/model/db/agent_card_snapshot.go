package db

import (
	"encoding/json"
	"time"
)

// Snapshot sources.
const (
	SnapshotSourceRegister = "register"
	SnapshotSourceUpdate   = "update"
	SnapshotSourceRefresh  = "refresh"
)

// AgentCardSnapshot is an append-only history entry of an agent's card body.
// A row is added only when the fetched card_hash differs from the latest
// stored snapshot. See docs/03.
type AgentCardSnapshot struct {
	ID        string          `gorm:"column:id;primaryKey;type:text"`
	AgentID   string          `gorm:"column:agent_id;type:text;not null;index:idx_snapshots_agent_fetched,priority:1"`
	CardURL   string          `gorm:"column:card_url;type:text"`
	CardHash  string          `gorm:"column:card_hash;type:text"`
	CardJSON  json.RawMessage `gorm:"column:card_json;type:jsonb"`
	Source    string          `gorm:"column:source;type:text;not null"`
	FetchedAt time.Time       `gorm:"column:fetched_at;not null;index:idx_snapshots_agent_fetched,priority:2,sort:desc"`
}

// TableName pins the GORM table name explicitly.
func (AgentCardSnapshot) TableName() string { return "agent_card_snapshots" }
