package service

import (
	"strings"
	"testing"
)

// TestParseCard_V1SupportedInterfaces confirms a clean v1.0 card (no
// legacy top-level url) resolves its endpoint and protocol version from
// supportedInterfaces.
func TestParseCard_V1SupportedInterfaces(t *testing.T) {
	body := []byte(`{
		"name": "Test Agent",
		"description": "A test agent",
		"supportedInterfaces": [
			{"url": "https://agents.example/a2a", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}
		],
		"skills": [{"id": "echo", "name": "Echo", "description": "Echoes input", "tags": ["demo"]}]
	}`)

	card, err := ParseCard(body)
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if !card.IsStaticallyValid() {
		t.Fatal("expected a v1.0 card with supportedInterfaces to be statically valid")
	}
	if got := card.EndpointURL(); got != "https://agents.example/a2a" {
		t.Errorf("EndpointURL() = %q, want https://agents.example/a2a", got)
	}
	if got := card.ProtocolVersion(); got != "1.0" {
		t.Errorf("ProtocolVersion() = %q, want 1.0", got)
	}
	if got := card.Name(); got != "Test Agent" {
		t.Errorf("Name() = %q, want Test Agent", got)
	}
	skills := card.Skills()
	if len(skills) != 1 || skills[0].ID != "echo" {
		t.Errorf("Skills() = %+v, want one skill with id echo", skills)
	}
}

// TestParseCard_LegacyTopLevelURL confirms a pre-1.0 card (no
// supportedInterfaces at all) still resolves its endpoint from the
// legacy top-level url — third-party legacy cards are exactly the case
// the registry must keep supporting even though a2a.AgentCard itself has
// no field for it (05 §L1).
func TestParseCard_LegacyTopLevelURL(t *testing.T) {
	body := []byte(`{
		"name": "Legacy Agent",
		"url": "https://legacy.example/agent",
		"provider": {"organization": "Legacy Corp"}
	}`)

	card, err := ParseCard(body)
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if !card.IsStaticallyValid() {
		t.Fatal("expected a legacy card with a top-level url to be statically valid")
	}
	if got := card.EndpointURL(); got != "https://legacy.example/agent" {
		t.Errorf("EndpointURL() = %q, want https://legacy.example/agent", got)
	}
	if got := card.ProtocolVersion(); got != "" {
		t.Errorf("ProtocolVersion() = %q, want empty (no supportedInterfaces)", got)
	}
	owner := card.OwnerName()
	if owner == nil || *owner != "Legacy Corp" {
		t.Errorf("OwnerName() = %v, want Legacy Corp", owner)
	}
}

// TestParseCard_NoEndpointFailsStaticValidity confirms a card with neither
// supportedInterfaces nor a legacy url is not statically valid (05 §L1:
// "A card with neither declares no endpoint and fails L1").
func TestParseCard_NoEndpointFailsStaticValidity(t *testing.T) {
	card, err := ParseCard([]byte(`{"name": "No Endpoint"}`))
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if card.IsStaticallyValid() {
		t.Error("expected a card with no declared endpoint to fail static validity")
	}
}

// TestParseCard_ExtractsVersionAndProtocolVersion confirms a v1.0 card's own
// version and its selected interface's protocolVersion both come back
// non-nil (03: agents.version / agents.protocol_version).
func TestParseCard_ExtractsVersionAndProtocolVersion(t *testing.T) {
	body := []byte(`{
		"name": "Test Agent",
		"version": "2.0.0",
		"supportedInterfaces": [
			{"url": "https://agents.example/a2a", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}
		]
	}`)

	card, err := ParseCard(body)
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	version := card.Version()
	if version == nil || *version != "2.0.0" {
		t.Errorf("Version() = %v, want 2.0.0", version)
	}
	protocolVersion := card.ProtocolVersionPtr()
	if protocolVersion == nil || *protocolVersion != "1.0" {
		t.Errorf("ProtocolVersionPtr() = %v, want 1.0", protocolVersion)
	}
}

// TestParseCard_LegacyCardHasVersionButNoProtocolVersion confirms a pre-1.0
// card (no supportedInterfaces) can still declare its own version, while
// protocol_version comes back nil — null there is a positive signal that
// the card is in the pre-1.0 format, not missing data (03).
func TestParseCard_LegacyCardHasVersionButNoProtocolVersion(t *testing.T) {
	body := []byte(`{
		"name": "Legacy Agent",
		"url": "https://legacy.example/agent",
		"version": "1.4.2"
	}`)

	card, err := ParseCard(body)
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	version := card.Version()
	if version == nil || *version != "1.4.2" {
		t.Errorf("Version() = %v, want 1.4.2", version)
	}
	if protocolVersion := card.ProtocolVersionPtr(); protocolVersion != nil {
		t.Errorf("ProtocolVersionPtr() = %v, want nil (no supportedInterfaces)", protocolVersion)
	}
}

// TestParseCard_NoVersionFieldsStillPassesL1 confirms a card declaring
// neither its own version nor supportedInterfaces yields both fields nil
// without that absence failing L1 — only a missing name or endpoint fails
// static validity, not missing version metadata (03, 05 §L1).
func TestParseCard_NoVersionFieldsStillPassesL1(t *testing.T) {
	body := []byte(`{"name": "No Version Fields", "url": "https://legacy.example/agent"}`)

	card, err := ParseCard(body)
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if !card.IsStaticallyValid() {
		t.Fatal("expected a card missing only version/protocol metadata to still be statically valid")
	}
	if version := card.Version(); version != nil {
		t.Errorf("Version() = %v, want nil", version)
	}
	if protocolVersion := card.ProtocolVersionPtr(); protocolVersion != nil {
		t.Errorf("ProtocolVersionPtr() = %v, want nil", protocolVersion)
	}
}

// TestParseCard_DeclaredAgentIDPrecedence confirms x-registry.agentId wins
// over top-level id and provider.id (05 §L2) — none of these three fields
// exist on a2a.AgentCard, so they must come from the raw parse.
func TestParseCard_DeclaredAgentIDPrecedence(t *testing.T) {
	body := []byte(`{
		"name": "Test Agent",
		"url": "https://agents.example",
		"id": "top-level-id",
		"provider": {"organization": "Acme", "id": "provider-id"},
		"x-registry": {"agentId": "did:example:acme:agent"}
	}`)
	card, err := ParseCard(body)
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	declared, found := card.DeclaredAgentID()
	if !found || declared != "did:example:acme:agent" {
		t.Errorf("DeclaredAgentID() = (%q, %v), want (did:example:acme:agent, true)", declared, found)
	}
}

// TestParseCard_ToleratesSchemaDriftInUnreadFields: cards in the wild shape
// securityRequirements & co. against other A2A spec drafts, and the SDK's
// typed unmarshal chokes on them. Since the registry never reads those
// fields, ParseCard must strip them and retry rather than reject the card
// (observed live: agent.co-legal.be's card).
func TestParseCard_ToleratesSchemaDriftInUnreadFields(t *testing.T) {
	body := []byte(`{
		"name": "Drifted Agent",
		"supportedInterfaces": [{"url": "https://agent.example/a2a", "protocolBinding": "JSONRPC", "protocolVersion": "1.0"}],
		"securityRequirements": [{"schemes": {"oauth": {"scopes": ["read"]}}}]
	}`)
	card, err := ParseCard(body)
	if err != nil {
		t.Fatalf("ParseCard: %v", err)
	}
	if card.Name() != "Drifted Agent" {
		t.Errorf("Name = %q, want Drifted Agent", card.Name())
	}
	if card.EndpointURL() != "https://agent.example/a2a" {
		t.Errorf("EndpointURL = %q", card.EndpointURL())
	}
}

// TestParseCard_SchemaMismatchInReadFieldStillFails: drift tolerance must
// not extend to fields the registry actually reads — a card whose skills
// contradict the schema is still rejected, with the distinct "card schema
// mismatch" error (not "invalid json").
func TestParseCard_SchemaMismatchInReadFieldStillFails(t *testing.T) {
	body := []byte(`{"name": "Bad Skills", "url": "https://agent.example", "skills": "not-an-array"}`)
	_, err := ParseCard(body)
	if err == nil {
		t.Fatal("ParseCard should reject a schema mismatch in a field the registry reads")
	}
	if !strings.Contains(err.Error(), "card schema mismatch") {
		t.Errorf("err = %q, want card schema mismatch", err)
	}
}
