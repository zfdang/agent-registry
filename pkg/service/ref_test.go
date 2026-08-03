package service

import "testing"

func TestIsRegistryID(t *testing.T) {
	if !IsRegistryID("agt_abc123") {
		t.Error("expected agt_ prefixed ref to be a registry id")
	}
	if IsRegistryID("agent.example.com") {
		t.Error("did not expect a URL-shaped ref to be a registry id")
	}
}

func TestIsURLShaped(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"agents.acme.example", true},
		{"api.acme.example/agent", true},
		{"did:example:acme.io", true}, // DID with a dot — tried as URL first (04)
		{"did:example:acme", false},
		{"plainagentid", false},
		{"agt_abc123", false},
	}
	for _, tt := range tests {
		if got := IsURLShaped(tt.ref); got != tt.want {
			t.Errorf("IsURLShaped(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestPrepareURLRef(t *testing.T) {
	tests := []struct{ in, want string }{
		{"agents.acme.example", "https://agents.acme.example"},
		{"https://agents.acme.example", "https://agents.acme.example"},
		{"http://agents.acme.example", "http://agents.acme.example"},
	}
	for _, tt := range tests {
		if got := PrepareURLRef(tt.in); got != tt.want {
			t.Errorf("PrepareURLRef(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
