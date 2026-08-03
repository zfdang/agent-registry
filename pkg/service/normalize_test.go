package service

import "testing"

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"lowercases scheme and host", "HTTPS://Agents.Acme.Example", "https://agents.acme.example", false},
		{"drops default https port", "https://agents.acme.example:443", "https://agents.acme.example", false},
		{"keeps non-default port", "https://agents.acme.example:8443", "https://agents.acme.example:8443", false},
		{"trims trailing slash", "https://agents.acme.example/", "https://agents.acme.example", false},
		{"trims trailing slash with path", "https://agents.acme.example/agent/", "https://agents.acme.example/agent", false},
		{"preserves path without trailing slash", "https://agents.acme.example/agent", "https://agents.acme.example/agent", false},
		{"preserves query", "https://agents.acme.example/agent?x=1", "https://agents.acme.example/agent?x=1", false},
		{"http default port dropped", "http://example.com:80/x", "http://example.com/x", false},
		{"missing host errors", "https://", "", true},
		{"unparseable errors", "://bad", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeURL(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeURL(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeURL(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "https://x.example/a", "https://x.example/b", true},
		{"case insensitive host", "https://X.example", "https://x.example", true},
		{"different scheme", "https://x.example", "http://x.example", false},
		{"different host", "https://x.example", "https://y.example", false},
		{"explicit default port matches implicit", "https://x.example:443", "https://x.example", true},
		{"different port", "https://x.example:8443", "https://x.example", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SameOrigin(tt.a, tt.b); got != tt.want {
				t.Errorf("SameOrigin(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
