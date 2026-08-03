// Package a2aserver implements the registry's own A2A surface (07) on top
// of the official A2A SDK (github.com/a2aproject/a2a-go/v2): its agent
// card and the POST /a2a JSON-RPC 2.0 methods (SendMessage, GetTask,
// CancelTask). Like handler and mcpserver, it is a thin protocol adapter
// over pkg/service — the AgentExecutor in executor.go is the only place
// that translates between A2A wire types and service calls.
package a2aserver

import "github.com/a2aproject/a2a-go/v2/a2a"

// AgentCard builds the registry's own agent card (07), rooted at
// publicURL. This is a clean v1.0.0 card: the legacy top-level url field
// and legacy method aliases are intentionally not served — the registry
// has no existing A2A clients that could depend on the pre-1.0 surface,
// so there's no compatibility cost to being spec-current here (contrast
// with L3's probe client, which stays tolerant of legacy agents it didn't
// choose to talk to).
func AgentCard(publicURL string) *a2a.AgentCard {
	endpoint := publicURL + "/a2a"
	return &a2a.AgentCard{
		Name:        "Agent Registry",
		Description: "Public directory of A2A agents: search, resolve, and register.",
		Version:     "1.0.0",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(endpoint, a2a.TransportProtocolJSONRPC),
		},
		Capabilities: a2a.AgentCapabilities{
			Streaming:         false,
			PushNotifications: false,
		},
		DefaultInputModes:  []string{"application/json"},
		DefaultOutputModes: []string{"application/json"},
		Skills: []a2a.AgentSkill{
			{
				ID:          "search-agents",
				Name:        "Search agents",
				Tags:        []string{"directory", "search"},
				Description: "Find registered agents by free-text query and filters.",
			},
			{
				ID:          "get-agent",
				Name:        "Get agent",
				Tags:        []string{"directory", "resolve"},
				Description: "Resolve one agent by registry id, agent_id, or URL; returns its card and verification state.",
			},
			{
				ID:          "register-agent",
				Name:        "Register agent",
				Tags:        []string{"directory", "registration"},
				Description: "Validate a live agent endpoint (L1/L2/L3) and add it to the directory.",
			},
		},
	}
}
