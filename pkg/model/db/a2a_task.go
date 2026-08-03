package db

import (
	"encoding/json"
	"time"
)

// A2A task skills.
const (
	SkillSearchAgents  = "search-agents"
	SkillGetAgent      = "get-agent"
	SkillRegisterAgent = "register-agent"
)

// A2A task statuses.
const (
	TaskSubmitted = "submitted"
	TaskWorking   = "working"
	TaskCompleted = "completed"
	TaskFailed    = "failed"
	TaskCanceled  = "canceled"
)

// A2ATask is a task accepted on the registry's own A2A surface. See docs/07.
type A2ATask struct {
	ID        string          `gorm:"column:id;primaryKey;type:text"`
	Skill     string          `gorm:"column:skill;type:text;not null"`
	Status    string          `gorm:"column:status;type:text;not null;index:idx_tasks_status_created,priority:1"`
	Input     json.RawMessage `gorm:"column:input;type:jsonb"`
	Result    json.RawMessage `gorm:"column:result;type:jsonb"`
	ClientIP  string          `gorm:"column:client_ip;type:inet"`
	CreatedAt time.Time       `gorm:"column:created_at;not null;autoCreateTime;index:idx_tasks_status_created,priority:2"`
	UpdatedAt time.Time       `gorm:"column:updated_at;not null;autoUpdateTime"`
}

// TableName pins the GORM table name explicitly.
func (A2ATask) TableName() string { return "a2a_tasks" }
