package service

import (
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/zfdang/agent-registry/pkg/model"
)

// CardSchema wraps the official a2a.AgentCard type for the fields the
// registry reads structurally (name, description, skills+tags,
// capabilities, supportedInterfaces), plus a parallel raw-map parse for
// fields the SDK's v1.0 type has no place for: the legacy top-level url
// (dropped from the spec in favor of supportedInterfaces, but still
// published by pre-1.0 agents we must keep resolving) and the registry's
// own id-declaration extensions (x-registry.agentId, top-level id,
// provider.id — none of which the SDK type carries, since they aren't
// part of the official schema).
type CardSchema struct {
	Card *a2a.AgentCard
	raw  map[string]json.RawMessage
}

// cardFieldsNotRead are top-level card fields the registry never reads but
// whose schema drifts across A2A revisions in the wild (cards written
// against other spec drafts shape securityRequirements & co. differently
// than the SDK's v1.0 types). A typed-parse mismatch inside any of them
// must not reject an otherwise valid card, so ParseCard retries the typed
// parse with them stripped. Only the typed parse sees the stripped copy —
// the raw parse, and therefore the stored card_json and the card hash,
// always keep the original bytes.
var cardFieldsNotRead = []string{"security", "securityRequirements", "securitySchemes", "signatures"}

// ParseCard unmarshals a raw card body. It does not enforce L1's static
// validity rule (non-empty name/endpoint) — callers check that separately
// so the distinction between "invalid json" (not JSON at all), "card schema
// mismatch" (JSON that contradicts the A2A schema in a field we read), and
// "missing required field" stays visible in check detail.
func ParseCard(body []byte) (*CardSchema, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid json: %w", err)
	}
	var card a2a.AgentCard
	if err := json.Unmarshal(body, &card); err != nil {
		stripped := make(map[string]json.RawMessage, len(raw))
		for k, v := range raw {
			stripped[k] = v
		}
		for _, k := range cardFieldsNotRead {
			delete(stripped, k)
		}
		reduced, mErr := json.Marshal(stripped)
		if mErr != nil {
			return nil, fmt.Errorf("card schema mismatch: %w", err)
		}
		if rErr := json.Unmarshal(reduced, &card); rErr != nil {
			return nil, fmt.Errorf("card schema mismatch: %w", rErr)
		}
	}
	return &CardSchema{Card: &card, raw: raw}, nil
}

// IsStaticallyValid is L1's static validity rule: non-empty name and a
// declared A2A service endpoint (05 §L1).
func (c *CardSchema) IsStaticallyValid() bool {
	return c.Card.Name != "" && c.EndpointURL() != ""
}

// jsonRPCInterface returns the first supportedInterfaces entry whose
// protocolBinding is JSONRPC — that's the only binding this registry
// probes — or nil if the card declares no such interface.
func (c *CardSchema) jsonRPCInterface() *a2a.AgentInterface {
	for _, iface := range c.Card.SupportedInterfaces {
		if iface != nil && iface.ProtocolBinding == a2a.TransportProtocolJSONRPC {
			return iface
		}
	}
	return nil
}

// EndpointURL is the card's declared A2A service endpoint: the first
// JSONRPC entry in supportedInterfaces (v1.0), falling back to the legacy
// top-level url recovered from the raw parse (pre-1.0 cards — a2a.AgentCard
// has no field for it, since v1.0 dropped it). Empty when the card
// declares neither (05 §L1).
func (c *CardSchema) EndpointURL() string {
	if iface := c.jsonRPCInterface(); iface != nil && iface.URL != "" {
		return iface.URL
	}
	return c.legacyURL()
}

func (c *CardSchema) legacyURL() string {
	s, _ := rawNestedString(c.raw, "url")
	return s
}

// ProtocolVersion is the declared protocolVersion of the JSONRPC
// supportedInterfaces entry, or "" for a legacy card with none — L3 uses
// this to pick its method dialect (05 §L3 Dialect selection).
func (c *CardSchema) ProtocolVersion() string {
	if iface := c.jsonRPCInterface(); iface != nil {
		return string(iface.ProtocolVersion)
	}
	return ""
}

// Version returns the card's own top-level version (the agent's release
// version, not the protocol's), nil when the card omits it (03).
func (c *CardSchema) Version() *string {
	if c.Card.Version == "" {
		return nil
	}
	v := c.Card.Version
	return &v
}

// ProtocolVersionPtr is ProtocolVersion() in the nullable form stored on the
// agent row and exposed on the wire: nil when the card declares no
// supportedInterfaces at all, which is itself the signal that the card is in
// the pre-1.0 format (03).
func (c *CardSchema) ProtocolVersionPtr() *string {
	if pv := c.ProtocolVersion(); pv != "" {
		v := pv
		return &v
	}
	return nil
}

// OwnerName extracts provider.organization, nil when absent (03, 05 §L1).
func (c *CardSchema) OwnerName() *string {
	if c.Card.Provider != nil && c.Card.Provider.Org != "" {
		org := c.Card.Provider.Org
		return &org
	}
	return nil
}

// DeclaredAgentID returns the card's declared agent id and whether one was
// found at all, per 05 §L2's precedence: x-registry.agentId, then
// top-level id, then provider.id. None of these three are part of
// a2a.AgentCard (the registry's own id-declaration convention predates and
// sits outside the official schema), so all three are read from the raw
// parse rather than the typed card.
func (c *CardSchema) DeclaredAgentID() (declared string, found bool) {
	if v, ok := rawNestedString(c.raw, "x-registry", "agentId"); ok {
		return v, true
	}
	if v, ok := rawNestedString(c.raw, "id"); ok {
		return v, true
	}
	if v, ok := rawNestedString(c.raw, "provider", "id"); ok {
		return v, true
	}
	return "", false
}

// rawNestedString navigates a chain of object keys in a raw-parsed JSON
// document and returns the string at the end of it, if present and
// non-empty.
func rawNestedString(raw map[string]json.RawMessage, keys ...string) (string, bool) {
	cur := raw
	for i, k := range keys {
		v, ok := cur[k]
		if !ok {
			return "", false
		}
		if i == len(keys)-1 {
			var s string
			if err := json.Unmarshal(v, &s); err != nil || s == "" {
				return "", false
			}
			return s, true
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(v, &nested); err != nil {
			return "", false
		}
		cur = nested
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

// Name returns the card's name.
func (c *CardSchema) Name() string { return c.Card.Name }

// Description returns the card's description.
func (c *CardSchema) Description() string { return c.Card.Description }

// Skills converts the card's typed skills into the registry's storage
// shape (id/name/description — tags aren't part of a listed agent's
// stored skill shape, see docs/04).
func (c *CardSchema) Skills() []model.Skill {
	out := make([]model.Skill, 0, len(c.Card.Skills))
	for _, sk := range c.Card.Skills {
		out = append(out, model.Skill{ID: sk.ID, Name: sk.Name, Description: sk.Description})
	}
	return out
}

// CapabilitiesJSON re-marshals the card's typed capabilities object for
// storage in agents.capabilities (03).
func (c *CardSchema) CapabilitiesJSON() json.RawMessage {
	b, err := json.Marshal(c.Card.Capabilities)
	if err != nil {
		return json.RawMessage("{}")
	}
	return b
}

// CardText flattens every string worth searching (03 card_text): name,
// description, owner, and every skill's name/description/id/tags.
func (c *CardSchema) CardText() string {
	out := c.Card.Name + " " + c.Card.Description
	if c.Card.Provider != nil {
		out += " " + c.Card.Provider.Org
	}
	for _, sk := range c.Card.Skills {
		out += " " + sk.ID + " " + sk.Name + " " + sk.Description
		for _, tag := range sk.Tags {
			out += " " + tag
		}
	}
	return out
}
