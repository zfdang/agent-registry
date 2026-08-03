package service

import "testing"

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
