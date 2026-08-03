package service

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/zfdang/agent-registry/pkg/model"
)

// CardSchema is the subset of the A2A agent card schema the registry reads.
// Unknown fields are ignored; Capabilities is kept as raw JSON since the
// registry only stores and echoes it, never interprets it.
type CardSchema struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	URL          string          `json:"url"`
	Provider     *CardProvider   `json:"provider"`
	Skills       []model.Skill   `json:"skills"`
	Capabilities json.RawMessage `json:"capabilities"`
	ID           string          `json:"id"`
	XRegistry    *CardXRegistry  `json:"x-registry"`
}

// CardProvider is the card's optional provider object.
type CardProvider struct {
	Organization string `json:"organization"`
	ID           string `json:"id"`
}

// CardXRegistry is the card's optional registry extension object — the
// recommended place to declare a registry agent id (05 §L2).
type CardXRegistry struct {
	AgentID string `json:"agentId"`
}

// ParseCard unmarshals a raw card body. It does not enforce L1's static
// validity rule (non-empty name/url) — callers check that separately so
// the distinction between "invalid JSON" and "missing required field"
// stays visible in check detail.
func ParseCard(body []byte) (*CardSchema, error) {
	var c CardSchema
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	return &c, nil
}

// IsStaticallyValid is L1's static validity rule: non-empty name and url.
func (c *CardSchema) IsStaticallyValid() bool {
	return c.Name != "" && c.URL != ""
}

// OwnerName extracts provider.organization, nil when absent (03, 05 §L1).
func (c *CardSchema) OwnerName() *string {
	if c.Provider != nil && c.Provider.Organization != "" {
		org := c.Provider.Organization
		return &org
	}
	return nil
}

// DeclaredAgentID returns the card's declared agent id and whether one was
// found at all, per 05 §L2's precedence: x-registry.agentId, then
// top-level id, then provider.id.
func (c *CardSchema) DeclaredAgentID() (declared string, found bool) {
	if c.XRegistry != nil && c.XRegistry.AgentID != "" {
		return c.XRegistry.AgentID, true
	}
	if c.ID != "" {
		return c.ID, true
	}
	if c.Provider != nil && c.Provider.ID != "" {
		return c.Provider.ID, true
	}
	return "", false
}

// uriSafe matches RFC 3986 URI characters (unreserved + reserved +
// pct-encoded), which is generous enough to admit DIDs, URNs, and plain
// slugs while still rejecting whitespace/control characters.
var uriSafe = regexp.MustCompile(`^[A-Za-z0-9\-._~:/?#\[\]@!$&'()*+,;=%]+$`)

// IsValidAgentIDFormat is 05 §L2's format rule: non-empty, URI-safe,
// at most 256 characters.
func IsValidAgentIDFormat(s string) bool {
	if s == "" || len(s) > 256 {
		return false
	}
	return uriSafe.MatchString(s)
}

// CardText flattens every string worth searching (03 card_text): name,
// description, owner, and every skill's name/description/id.
func (c *CardSchema) CardText() string {
	out := c.Name + " " + c.Description
	if c.Provider != nil {
		out += " " + c.Provider.Organization
	}
	for _, sk := range c.Skills {
		out += " " + sk.ID + " " + sk.Name + " " + sk.Description
	}
	return out
}
