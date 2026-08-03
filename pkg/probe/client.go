package probe

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

const (
	// Timeout is the per-request deadline for all probe traffic.
	Timeout = 5 * time.Second
	// MaxBodyBytes caps how much of a response body is ever read.
	MaxBodyBytes = 256 * 1024
	// MaxRedirects is the maximum number of redirects a probe will follow.
	MaxRedirects = 3
)

// ErrNotHTTPS is returned when a URL (initial or a redirect target) is not
// an https URL.
var ErrNotHTTPS = errors.New("probe: url is not https")

// Guard is the interface the dialer uses to police outbound connections.
// It is injectable so tests can supply a permissive implementation (e.g.
// to exercise an httptest.NewTLSServer on 127.0.0.1) without weakening the
// production guard. Production code always uses *AddressGuard.
type Guard interface {
	CheckHost(host string) error
	Control(network, address string, c syscall.RawConn) error
}

// Client is the SSRF-hardened HTTP client used by the verification
// pipeline. It is the only component in the codebase permitted to fetch
// user-supplied URLs.
type Client struct {
	http  *http.Client
	guard Guard
}

// Option configures a Client.
type Option func(*clientConfig)

type clientConfig struct {
	allowlist []string
	guard     Guard
	rootCAs   *tls.Config
}

// WithAllowlist sets the operator-configured hostname allowlist (05).
func WithAllowlist(hosts []string) Option {
	return func(c *clientConfig) { c.allowlist = hosts }
}

// WithGuard overrides the address guard entirely. Intended for tests only —
// production wiring should use WithAllowlist.
func WithGuard(g Guard) Option {
	return func(c *clientConfig) { c.guard = g }
}

// WithTLSConfig overrides the TLS client config (e.g. to trust an
// httptest.NewTLSServer's certificate in tests). Production wiring should
// never need this — the default config verifies against the system roots.
func WithTLSConfig(tc *tls.Config) Option {
	return func(c *clientConfig) { c.rootCAs = tc }
}

// NewClient builds a probe Client with the given options applied.
func NewClient(opts ...Option) *Client {
	cfg := &clientConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	guard := cfg.guard
	if guard == nil {
		guard = NewAddressGuard(cfg.allowlist)
	}

	dialer := &net.Dialer{
		Timeout: Timeout,
		Control: guard.Control,
	}

	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		Proxy:                 nil, // never proxy — a proxy would tunnel past the dialer guard
		TLSClientConfig:       cfg.rootCAs,
		ResponseHeaderTimeout: Timeout,
		IdleConnTimeout:       Timeout,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("probe: too many redirects (max %d)", MaxRedirects)
			}
			if req.URL.Scheme != "https" {
				return ErrNotHTTPS
			}
			if err := guard.CheckHost(req.URL.Hostname()); err != nil {
				return fmt.Errorf("probe: redirect target refused: %w", err)
			}
			return nil
		},
	}

	return &Client{http: httpClient, guard: guard}
}

// Result is a capped, already-read HTTP response body plus status.
type Result struct {
	StatusCode int
	Body       []byte
	FinalURL   string
}

// Get performs a guarded GET request. The response body is capped at
// MaxBodyBytes.
func (c *Client) Get(ctx context.Context, rawURL string) (*Result, error) {
	return c.do(ctx, http.MethodGet, rawURL, "", nil)
}

// PostJSON performs a guarded POST with a JSON body and
// Content-Type: application/json.
func (c *Client) PostJSON(ctx context.Context, rawURL string, body []byte) (*Result, error) {
	return c.do(ctx, http.MethodPost, rawURL, "application/json", body)
}

func (c *Client) do(ctx context.Context, method, rawURL, contentType string, body []byte) (*Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("probe: invalid url: %w", err)
	}
	if !strings.EqualFold(u.Scheme, "https") {
		return nil, ErrNotHTTPS
	}
	if err := c.guard.CheckHost(u.Hostname()); err != nil {
		return nil, fmt.Errorf("probe: refused: %w", err)
	}

	var bodyReader io.Reader
	if body != nil {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("probe: build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "agent-registry-verifier/1.0")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("probe: request failed: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, MaxBodyBytes)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("probe: read body: %w", err)
	}

	final := rawURL
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}

	return &Result{StatusCode: resp.StatusCode, Body: data, FinalURL: final}, nil
}
