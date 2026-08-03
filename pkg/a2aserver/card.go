// Package a2aserver implements the registry's own A2A surface (07): its
// agent card and the POST /a2a JSON-RPC 2.0 task methods (tasks/send,
// tasks/get, tasks/cancel). Like handler and mcpserver, it is a thin
// protocol adapter over pkg/service.
package a2aserver

import "encoding/json"

// AgentCard builds the registry's own agent card (07), rooted at
// publicURL.
func AgentCard(publicURL string) json.RawMessage {
	card := map[string]any{
		"name":        "Agent Registry",
		"description": "Public directory of A2A agents: search, resolve, and register.",
		"url":         publicURL + "/a2a",
		"version":     "1.0.0",
		"capabilities": map[string]any{
			"streaming":         false,
			"pushNotifications": false,
		},
		"defaultInputModes":  []string{"application/json"},
		"defaultOutputModes": []string{"application/json"},
		"skills": []map[string]any{
			{
				"id":          "search-agents",
				"name":        "Search agents",
				"description": "Find registered agents by free-text query and filters.",
			},
			{
				"id":          "get-agent",
				"name":        "Get agent",
				"description": "Resolve one agent by registry id, agent_id, or URL; returns its card and verification state.",
			},
			{
				"id":          "register-agent",
				"name":        "Register agent",
				"description": "Validate a live agent endpoint (L1/L2/L3) and add it to the directory.",
			},
		},
	}
	b, _ := json.Marshal(card)
	return b
}
