// Package config loads registry configuration from environment variables
// only — no config files, no viper. Every setting has a REGISTRY_ prefix.
package config

import (
	"fmt"
	"net"
	"os"
	"strings"
)

// Config holds all runtime configuration for the registry-api process.
type Config struct {
	// Port is the HTTP listen port for the gin engine (REST + MCP + A2A).
	Port string
	// DatabaseURL is the Postgres connection string. Required.
	DatabaseURL string
	// PublicURL is the externally reachable base URL of this deployment,
	// used to populate the registry's own A2A agent card.
	PublicURL string
	// ProbeHostAllowlist is a set of hostnames (suffix-anchored at a label
	// boundary) allowed to resolve to private-network addresses during
	// verification probes — for self-hosted test deployments only.
	ProbeHostAllowlist []string
	// LogLevel controls zap's logging verbosity (debug|info|warn|error).
	LogLevel string
	// TrustedProxyCIDRs is the set of networks whose X-Forwarded-For
	// header the rate limiter trusts (Traefik sits in front of
	// registry-api in-cluster). Requests from outside these CIDRs have
	// their X-Forwarded-For ignored entirely, since an untrusted direct
	// caller can put anything in that header.
	TrustedProxyCIDRs []*net.IPNet
}

const (
	defaultPort      = "8080"
	defaultPublicURL = "https://registry.ip402.xyz"
	defaultLogLevel  = "info"
	// defaultTrustedProxyCIDRs covers RFC1918 private address space, which
	// already includes k3s's default flannel pod CIDR (10.42.0.0/16) —
	// Traefik runs in-cluster, so its connections to registry-api always
	// originate from one of these ranges.
	defaultTrustedProxyCIDRs = "10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"
)

// Load reads configuration from the environment. It returns an error when a
// required setting (REGISTRY_DATABASE_URL) is missing so the caller can fail
// fast at boot.
func Load() (*Config, error) {
	cfg := &Config{
		Port:        getEnv("REGISTRY_PORT", defaultPort),
		DatabaseURL: os.Getenv("REGISTRY_DATABASE_URL"),
		PublicURL:   getEnv("REGISTRY_PUBLIC_URL", defaultPublicURL),
		LogLevel:    getEnv("REGISTRY_LOG_LEVEL", defaultLogLevel),
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("REGISTRY_DATABASE_URL is required but not set")
	}

	if raw := os.Getenv("REGISTRY_PROBE_HOST_ALLOWLIST"); raw != "" {
		for _, h := range strings.Split(raw, ",") {
			h = strings.TrimSpace(strings.ToLower(h))
			if h != "" {
				cfg.ProbeHostAllowlist = append(cfg.ProbeHostAllowlist, h)
			}
		}
	}

	trustedRaw := getEnv("REGISTRY_TRUSTED_PROXY_CIDRS", defaultTrustedProxyCIDRs)
	for _, s := range strings.Split(trustedRaw, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("REGISTRY_TRUSTED_PROXY_CIDRS: invalid CIDR %q: %w", s, err)
		}
		cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, ipnet)
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
