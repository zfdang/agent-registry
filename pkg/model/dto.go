// Package model holds request/response DTOs shared by the REST, MCP, and
// A2A adapters. These are protocol-agnostic shapes produced by pkg/service;
// see docs/04, docs/06, docs/07.
package model

import (
	"encoding/json"
	"time"
)

// Skill is one entry of an agent card's skills array.
type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// AgentListItem is the shape returned in list/search results and MCP
// search_agents results.
type AgentListItem struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	OwnerName          *string    `json:"owner_name"`
	AgentID            *string    `json:"agent_id"`
	URL                string     `json:"url"`
	Description        string     `json:"description"`
	Version            *string    `json:"version"`
	ProtocolVersion    *string    `json:"protocol_version"`
	Skills             []Skill    `json:"skills"`
	VerificationStatus string     `json:"verification_status"`
	VerifiedAt         *time.Time `json:"verified_at"`
	CreatedAt          time.Time  `json:"created_at"`
}

// Agent is the full record: list item plus capabilities and card fields.
type Agent struct {
	AgentListItem
	Capabilities json.RawMessage `json:"capabilities"`
	CardURL      string          `json:"card_url"`
	CardJSON     json.RawMessage `json:"card_json"`
}

// ListAgentsResponse is GET /v1/agents.
type ListAgentsResponse struct {
	Agents   []AgentListItem `json:"agents"`
	Total    int64           `json:"total"`
	Page     int             `json:"page"`
	PageSize int             `json:"page_size"`
}

// StatsResponse is GET /v1/stats.
type StatsResponse struct {
	TotalAgents    int64 `json:"total_agents"`
	VerifiedAgents int64 `json:"verified_agents"`
	NewThisWeek    int64 `json:"new_this_week"`
	DistinctOwners int64 `json:"distinct_owners"`
}

// Check is one verification check row on the wire.
type Check struct {
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	CheckedAt time.Time       `json:"checked_at"`
	ExpiresAt time.Time       `json:"expires_at"`
	Detail    json.RawMessage `json:"detail"`
}

// VerificationResponse is GET /v1/agents/*ref/verification.
type VerificationResponse struct {
	Status     string     `json:"status"`
	VerifiedAt *time.Time `json:"verified_at"`
	Checks     []Check    `json:"checks"`
}

// CardSnapshot is one entry of GET /v1/agents/*ref/cards.
type CardSnapshot struct {
	ID        string          `json:"id"`
	CardURL   string          `json:"card_url"`
	CardHash  string          `json:"card_hash"`
	Source    string          `json:"source"`
	FetchedAt time.Time       `json:"fetched_at"`
	CardJSON  json.RawMessage `json:"card_json"`
}

// CardsResponse is GET /v1/agents/*ref/cards.
type CardsResponse struct {
	Snapshots []CardSnapshot `json:"snapshots"`
}

// ValidateRequest is POST /v1/agents/validate.
type ValidateRequest struct {
	URL string `json:"url"`
}

// ExtractedCard is the card-derived preview returned by validate.
type ExtractedCard struct {
	Name            string  `json:"name"`
	OwnerName       *string `json:"owner_name"`
	AgentID         *string `json:"agent_id"`
	Description     string  `json:"description"`
	Version         *string `json:"version"`
	ProtocolVersion *string `json:"protocol_version"`
	Skills          []Skill `json:"skills"`
}

// ValidateResponse is POST /v1/agents/validate.
type ValidateResponse struct {
	ValidationID string         `json:"validation_id"`
	Result       string         `json:"result"`
	Registerable bool           `json:"registerable"`
	Mode         string         `json:"mode"`
	ExistingID   *string        `json:"existing_id"`
	ExpiresAt    time.Time      `json:"expires_at"`
	Extracted    *ExtractedCard `json:"extracted"`
	Checks       []Check        `json:"checks"`
}

// RegisterRequest is POST /v1/agents.
type RegisterRequest struct {
	ValidationID string `json:"validation_id"`
}

// AgentCandidate names a match in an ambiguous_ref error.
type AgentCandidate struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

// ErrorBody is the uniform error envelope's inner object.
type ErrorBody struct {
	Code       string           `json:"code"`
	Message    string           `json:"message"`
	Candidates []AgentCandidate `json:"candidates,omitempty"`
	ExistingID *string          `json:"existing_id,omitempty"`
}

// ErrorResponse is the uniform error envelope (04).
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// RefreshResponse is POST /v1/agents/*ref/refresh.
type RefreshResponse struct {
	Agent  Agent   `json:"agent"`
	Checks []Check `json:"checks"`
}

// RegisterAgentResult is the outcome of a single-call register (MCP
// register_agent, A2A register-agent skill).
type RegisterAgentResult struct {
	Registered bool    `json:"registered"`
	Mode       string  `json:"mode,omitempty"`
	Agent      *Agent  `json:"agent,omitempty"`
	Checks     []Check `json:"checks"`
}
