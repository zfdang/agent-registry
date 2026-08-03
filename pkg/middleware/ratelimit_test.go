package middleware

import (
	"net"
	"testing"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatalf("ParseCIDR(%q): %v", s, err)
	}
	return n
}

// TestIPResolver_Resolve is the regression test for P2-1: the rate limiter
// previously trusted the left-most X-Forwarded-For entry unconditionally,
// which any direct, untrusted caller can forge to dodge its own bucket.
func TestIPResolver_Resolve(t *testing.T) {
	trusted := []*net.IPNet{mustCIDR(t, "10.0.0.0/8")}
	r := NewIPResolver(trusted)

	tests := []struct {
		name          string
		remoteAddr    string
		xForwardedFor string
		want          string
	}{
		{
			name:          "untrusted direct caller: XFF ignored entirely",
			remoteAddr:    "203.0.113.5:1234",
			xForwardedFor: "1.2.3.4", // forged — remoteAddr isn't a trusted proxy
			want:          "203.0.113.5",
		},
		{
			name:          "untrusted direct caller with no XFF",
			remoteAddr:    "203.0.113.5:1234",
			xForwardedFor: "",
			want:          "203.0.113.5",
		},
		{
			name:          "trusted proxy: right-most untrusted entry wins",
			remoteAddr:    "10.42.0.7:5678",
			xForwardedFor: "198.51.100.9, 10.42.0.1",
			want:          "198.51.100.9",
		},
		{
			name:          "trusted proxy chain: walks past multiple trusted hops",
			remoteAddr:    "10.42.0.7:5678",
			xForwardedFor: "198.51.100.9, 10.0.0.5, 10.42.0.1",
			want:          "198.51.100.9",
		},
		{
			name:          "trusted proxy with no XFF falls back to remote addr",
			remoteAddr:    "10.42.0.7:5678",
			xForwardedFor: "",
			want:          "10.42.0.7",
		},
		{
			name:          "trusted proxy, every hop trusted: falls back to left-most",
			remoteAddr:    "10.42.0.7:5678",
			xForwardedFor: "10.0.0.1, 10.0.0.2",
			want:          "10.0.0.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.Resolve(tt.remoteAddr, tt.xForwardedFor)
			if got != tt.want {
				t.Errorf("Resolve(%q, %q) = %q, want %q", tt.remoteAddr, tt.xForwardedFor, got, tt.want)
			}
		})
	}
}
