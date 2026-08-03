// Package probe implements the only outbound HTTP client in the codebase
// allowed to fetch user-supplied URLs. It is SSRF-hardened per
// docs/05-verification.md: https-only, small timeouts, capped response
// bodies, an address guard on the dialer (checked both pre-resolution and
// post-resolution), no proxy, and bounded same-scheme redirects.
package probe

import (
	"fmt"
	"net"
	"strings"
	"syscall"
)

// AddressGuard decides whether a resolved network address may be dialed.
// It is exported so tests can exercise the address-classification logic
// directly with table tests, without any real network I/O.
type AddressGuard struct {
	// Allowlist holds lowercase hostnames permitted to resolve to
	// private-network addresses (self-hosted test deployments only).
	// Matching is suffix-anchored at a label boundary; loopback is never
	// exempted regardless of allowlist membership.
	Allowlist []string
}

// NewAddressGuard builds a guard from the operator-configured allowlist.
func NewAddressGuard(allowlist []string) *AddressGuard {
	return &AddressGuard{Allowlist: allowlist}
}

// hostAllowlisted reports whether host matches an allowlist entry,
// suffix-anchored at a label boundary (e.g. "internal.test" matches
// "svc.internal.test" but not "evil-internal.test").
func (g *AddressGuard) hostAllowlisted(host string) bool {
	host = strings.ToLower(host)
	for _, allowed := range g.Allowlist {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

// CheckHost is the pre-resolution guard: it rejects hostnames that are
// themselves literal refused addresses. Hostnames that require DNS
// resolution are allowed through here and re-checked post-resolution by
// CheckIP (the dialer Control func) — that second check is what actually
// prevents SSRF via DNS.
func (g *AddressGuard) CheckHost(host string) error {
	if ip := net.ParseIP(host); ip != nil {
		return g.CheckIP(host, ip)
	}
	return nil
}

// CheckIP is the address-classification core, exercised directly by unit
// tests. It refuses loopback, link-local (including cloud metadata
// 169.254.0.0/16), RFC1918 private space, CGN (100.64.0.0/10), unspecified,
// and multicast addresses — over IPv4, IPv6, and IPv4-mapped IPv6. An
// allowlisted hostname exempts RFC1918 only; loopback is never exempted.
func (g *AddressGuard) CheckIP(host string, ip net.IP) error {
	allowed := host != "" && g.hostAllowlisted(host)

	if ip.IsLoopback() {
		return fmt.Errorf("refused address: loopback (%s)", ip)
	}
	if ip.IsUnspecified() {
		return fmt.Errorf("refused address: unspecified (%s)", ip)
	}
	if ip.IsMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("refused address: multicast (%s)", ip)
	}
	if ip.IsLinkLocalUnicast() {
		// Includes 169.254.0.0/16 (cloud metadata) and fe80::/10.
		return fmt.Errorf("refused address: link-local (%s)", ip)
	}

	v4 := ip.To4()
	if v4 != nil {
		if isRFC1918(v4) {
			if allowed {
				return nil
			}
			return fmt.Errorf("refused address: private RFC1918 (%s)", ip)
		}
		if isCGNAT(v4) {
			return fmt.Errorf("refused address: CGN 100.64.0.0/10 (%s)", ip)
		}
		return nil
	}

	// IPv6: unique local addresses (fc00::/7) are the v6 analogue of
	// RFC1918 and get the same allowlist treatment.
	if isUniqueLocalV6(ip) {
		if allowed {
			return nil
		}
		return fmt.Errorf("refused address: unique local v6 (%s)", ip)
	}

	return nil
}

func isRFC1918(v4 net.IP) bool {
	switch {
	case v4[0] == 10:
		return true
	case v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31:
		return true
	case v4[0] == 192 && v4[1] == 168:
		return true
	}
	return false
}

func isCGNAT(v4 net.IP) bool {
	return v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127
}

func isUniqueLocalV6(ip net.IP) bool {
	return len(ip) == net.IPv6len && ip[0]&0xfe == 0xfc
}

// Control is a net.Dialer.Control func: it runs the address guard on the
// resolved socket address immediately before connect(2), which is what
// defeats DNS-rebinding between the pre-resolution check and the dial.
func (g *AddressGuard) Control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("dial guard: unparseable address %q", address)
	}
	return g.CheckIP("", ip)
}
