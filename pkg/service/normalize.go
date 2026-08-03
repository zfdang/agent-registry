package service

import (
	"fmt"
	"net/url"
	"strings"
)

// NormalizeURL lowercases scheme+host, drops the default port for the
// scheme, and trims a trailing slash, so exact-match resolution by URL is
// insensitive to those variations (03).
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid url: missing scheme or host")
	}

	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()

	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}

	hostport := host
	if port != "" {
		hostport = host + ":" + port
	}

	path := strings.TrimSuffix(u.Path, "/")

	out := scheme + "://" + hostport + path
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out, nil
}

// SameOrigin reports whether two URLs share scheme, host, and port exactly
// (05 §Same-origin rule) — hostname comparison is case-insensitive.
func SameOrigin(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.EqualFold(ua.Hostname(), ub.Hostname()) &&
		effectivePort(ua) == effectivePort(ub)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}
