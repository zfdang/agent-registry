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
//
// CheckDial takes the pre-resolution dial host (as passed to
// Transport.DialContext, before DNS resolution) separately from the
// resolved socket address, so an allowlist match can see the hostname the
// caller actually asked to dial.
type Guard interface {
	CheckHost(host string) error
	CheckDial(dialHost, network, address string, c syscall.RawConn) error
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

	// A custom DialContext (rather than a bare net.Dialer with a Control
	// func) is required to fix the allowlist: Transport.DialContext
	// receives "addr" as the pre-resolution "host:port" the caller asked
	// to dial, but net.Dialer.Control only ever sees the resolved socket
	// address post-DNS. Capturing the original hostname here and passing
	// it into CheckDial alongside the resolved address is what lets an
	// allowlisted hostname actually match.
	dialContext := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialHost, _, err := net.SplitHostPort(addr)
		if err != nil {
			dialHost = addr
		}
		d := &net.Dialer{
			Timeout: Timeout,
			Control: func(network, address string, c syscall.RawConn) error {
				return guard.CheckDial(dialHost, network, address, c)
			},
		}
		return d.DialContext(ctx, network, addr)
	}

	transport := &http.Transport{
		DialContext:           dialContext,
		Proxy:                 nil, // never proxy — a proxy would tunnel past the dialer guard
		TLSClientConfig:       cfg.rootCAs,
		ResponseHeaderTimeout: Timeout,
		IdleConnTimeout:       Timeout,
	}

	httpClient := &http.Client{
		Transport: &cappedBodyTransport{base: transport, max: MaxBodyBytes},
		Timeout:   Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return fmt.Errorf("probe: too many redirects (max %d)", MaxRedirects)
			}
			if req.URL.Scheme != "https" {
				return ErrNotHTTPS
			}
			// Every hop must stay on the same origin as the *original*
			// request, not just match the previous hop — otherwise a
			// multi-hop chain could still drift origin one step at a
			// time. L1's third discovery candidate is the caller-supplied
			// URL itself, so without this an open redirect on the
			// registered host could hand the card fetch (and L3's POST,
			// since 307/308 preserve method+body) to an attacker-
			// controlled origin. See docs/05 §Redirects.
			//
			// This must normalize the default port the same way
			// service.SameOrigin does (pkg/probe can't import pkg/service —
			// that would be a cycle, since service already imports probe —
			// so the comparison is duplicated here rather than shared).
			// Without that, a redirect from https://example.com/x to the
			// equivalent https://example.com:443/x would be refused as
			// cross-origin even though it isn't.
			if !sameOrigin(via[0].URL, req.URL) {
				return fmt.Errorf("probe: cross-origin redirect refused (%s -> %s)", via[0].URL, req.URL)
			}
			if err := guard.CheckHost(req.URL.Hostname()); err != nil {
				return fmt.Errorf("probe: redirect target refused: %w", err)
			}
			return nil
		},
	}

	return &Client{http: httpClient, guard: guard}
}

// sameOrigin compares two URLs' scheme, host, and *effective* port —
// mirroring pkg/service's SameOrigin so a redirect to an explicit default
// port (e.g. https://example.com:443/x) isn't wrongly refused as
// cross-origin just because the original request omitted it.
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
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

// HTTPClient exposes the underlying guarded *http.Client so it can be
// handed to third-party clients that accept an injectable http.Client
// (e.g. the A2A SDK's a2aclient.WithJSONRPCTransport) — this is how the
// SSRF guard, same-origin redirect policy, and body cap apply to traffic
// this package didn't itself issue. Callers must never substitute a
// different client on that path; doing so bypasses every protection here.
func (c *Client) HTTPClient() *http.Client {
	return c.http
}

// cappedBodyTransport wraps a RoundTripper so every response body is
// capped at max bytes regardless of which caller reads it — Client's own
// Get/PostJSON already cap via io.LimitReader, but code that borrows the
// *http.Client directly via HTTPClient() (e.g. an injected SDK client)
// reads the response body itself and would otherwise bypass that cap.
type cappedBodyTransport struct {
	base http.RoundTripper
	max  int64
}

func (t *cappedBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	resp.Body = &cappedReadCloser{r: io.LimitReader(resp.Body, t.max), c: resp.Body}
	return resp, nil
}

// cappedReadCloser lets the capped reader still close the original body.
type cappedReadCloser struct {
	r io.Reader
	c io.Closer
}

func (c *cappedReadCloser) Read(p []byte) (int, error) { return c.r.Read(p) }
func (c *cappedReadCloser) Close() error               { return c.c.Close() }

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
