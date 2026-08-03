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

func TestAddressGuard_CheckDial(t *testing.T) {
	g := NewAddressGuard(nil)

	if err := g.CheckDial("", "tcp4", "127.0.0.1:443", nil); err == nil {
		t.Error("CheckDial with loopback address = nil, want error")
	}
	if err := g.CheckDial("", "tcp4", "93.184.216.34:443", nil); err != nil {
		t.Errorf("CheckDial with public address = %v, want nil", err)
	}
	if err := g.CheckDial("", "tcp4", "not-an-address", nil); err == nil {
		t.Error("CheckDial with unparseable address = nil, want error")
	}
}

// TestAddressGuard_CheckDial_AllowlistPlumbing is the regression test for
// the dead-allowlist bug (P2-4): the dial-time hostname must actually reach
// CheckIP so an allowlisted hostname resolving into RFC1918 is permitted,
// a non-allowlisted one refused, and loopback refused even when
// allowlisted — mirroring exactly how probe.Client's custom DialContext
// calls CheckDial.
func TestAddressGuard_CheckDial_AllowlistPlumbing(t *testing.T) {
	g := NewAddressGuard([]string{"internal.test"})

	// Allowlisted hostname resolving to a private address: permitted.
	if err := g.CheckDial("internal.test", "tcp4", "192.168.1.5:443", nil); err != nil {
		t.Errorf("allowlisted host resolving to RFC1918 = %v, want nil", err)
	}
	// Non-allowlisted hostname resolving to the same private address: refused.
	if err := g.CheckDial("other.test", "tcp4", "192.168.1.5:443", nil); err == nil {
		t.Error("non-allowlisted host resolving to RFC1918 = nil, want error")
	}
	// Loopback stays refused even for an allowlisted hostname.
	if err := g.CheckDial("internal.test", "tcp4", "127.0.0.1:443", nil); err == nil {
		t.Error("loopback via allowlisted host = nil, want error")
	}
}
