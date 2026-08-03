package probe

import (
	"net"
	"testing"
)

func TestAddressGuard_CheckIP(t *testing.T) {
	tests := []struct {
		name      string
		ip        string
		allowlist []string
		host      string
		wantErr   bool
	}{
		{"ipv4 loopback refused", "127.0.0.1", nil, "", true},
		{"ipv4 loopback refused even with allowlist", "127.0.0.1", []string{"test.local"}, "test.local", true},
		{"ipv6 loopback refused", "::1", nil, "", true},
		{"ipv4 rfc1918 10.x refused by default", "10.0.0.5", nil, "", true},
		{"ipv4 rfc1918 172.16 refused by default", "172.16.0.5", nil, "", true},
		{"ipv4 rfc1918 192.168 refused by default", "192.168.1.5", nil, "", true},
		{"ipv4 rfc1918 allowed via allowlist", "192.168.1.5", []string{"internal.test"}, "internal.test", false},
		{"ipv4 rfc1918 allowlist requires host match", "192.168.1.5", []string{"internal.test"}, "other.test", true},
		{"ipv4 rfc1918 allowlist suffix boundary", "192.168.1.5", []string{"internal.test"}, "svc.internal.test", false},
		{"ipv4 rfc1918 allowlist suffix boundary rejects lookalike", "192.168.1.5", []string{"internal.test"}, "evil-internal.test", true},
		{"ipv4 link-local metadata refused", "169.254.169.254", nil, "", true},
		{"ipv4 cgn refused", "100.64.0.1", nil, "", true},
		{"ipv4 cgn boundary refused", "100.127.255.255", nil, "", true},
		{"ipv4 unspecified refused", "0.0.0.0", nil, "", true},
		{"ipv4 multicast refused", "224.0.0.1", nil, "", true},
		{"ipv6 link-local refused", "fe80::1", nil, "", true},
		{"ipv6 unique-local refused by default", "fc00::1", nil, "", true},
		{"ipv6 unique-local allowed via allowlist", "fd12:3456::1", []string{"internal.test"}, "internal.test", false},
		{"ipv6 unspecified refused", "::", nil, "", true},
		{"ipv6 multicast refused", "ff02::1", nil, "", true},
		{"ipv4-mapped ipv6 loopback refused", "::ffff:127.0.0.1", nil, "", true},
		{"ipv4-mapped ipv6 rfc1918 refused by default", "::ffff:192.168.1.1", nil, "", true},
		{"public ipv4 allowed", "93.184.216.34", nil, "", false},
		{"public ipv4 allowed without allowlist needed", "8.8.8.8", nil, "", false},
		{"public ipv6 allowed", "2606:4700:4700::1111", nil, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewAddressGuard(tt.allowlist)
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("test setup: unparseable ip %q", tt.ip)
			}
			err := g.CheckIP(tt.host, ip)
			if tt.wantErr && err == nil {
				t.Errorf("CheckIP(%q, %q) = nil, want error", tt.host, tt.ip)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("CheckIP(%q, %q) = %v, want nil", tt.host, tt.ip, err)
			}
		})
	}
}

func TestAddressGuard_CheckHost(t *testing.T) {
	g := NewAddressGuard(nil)

	// A literal loopback/private IP passed as a "hostname" must be refused
	// at the pre-resolution stage too.
	if err := g.CheckHost("127.0.0.1"); err == nil {
		t.Error("CheckHost(127.0.0.1) = nil, want error")
	}
	if err := g.CheckHost("192.168.1.1"); err == nil {
		t.Error("CheckHost(192.168.1.1) = nil, want error")
	}

	// A DNS hostname passes the pre-resolution check — it is the
	// post-resolution Control func that catches DNS rebinding.
	if err := g.CheckHost("example.com"); err != nil {
		t.Errorf("CheckHost(example.com) = %v, want nil", err)
	}
}

func TestAddressGuard_Control(t *testing.T) {
	g := NewAddressGuard(nil)

	if err := g.Control("tcp4", "127.0.0.1:443", nil); err == nil {
		t.Error("Control with loopback address = nil, want error")
	}
	if err := g.Control("tcp4", "93.184.216.34:443", nil); err != nil {
		t.Errorf("Control with public address = %v, want nil", err)
	}
	if err := g.Control("tcp4", "not-an-address", nil); err == nil {
		t.Error("Control with unparseable address = nil, want error")
	}
}
