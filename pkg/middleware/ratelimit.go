// Package middleware holds gin middleware shared by the REST, MCP, and A2A
// surfaces: CORS, per-IP rate limiting, and request logging (02).
package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/zfdang/agent-registry/pkg/model"
)

// tokenBucket is a simple in-memory token bucket: it refills continuously
// at ratePerSec and holds at most burst tokens.
type tokenBucket struct {
	tokens   float64
	lastSeen time.Time
}

// Limiter is a per-key (client IP) token-bucket rate limiter. Single-replica
// assumption for v1 (02) — an in-memory map is sufficient.
type Limiter struct {
	mu         sync.Mutex
	buckets    map[string]*tokenBucket
	ratePerSec float64
	burst      float64
}

// NewLimiter builds a limiter allowing ratePerMinute requests per minute
// per key, with a burst equal to that same limit.
func NewLimiter(ratePerMinute int) *Limiter {
	l := &Limiter{
		buckets:    make(map[string]*tokenBucket),
		ratePerSec: float64(ratePerMinute) / 60.0,
		burst:      float64(ratePerMinute),
	}
	go l.gcLoop()
	return l
}

// Allow reports whether a request for key may proceed, consuming a token
// if so.
func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: l.burst - 1, lastSeen: now}
		l.buckets[key] = b
		return true
	}

	elapsed := now.Sub(b.lastSeen).Seconds()
	b.tokens += elapsed * l.ratePerSec
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastSeen = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// gcLoop periodically drops buckets that haven't been touched in a while,
// so long-running processes don't accumulate one entry per IP forever.
func (l *Limiter) gcLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-30 * time.Minute)
		l.mu.Lock()
		for k, b := range l.buckets {
			if b.lastSeen.Before(cutoff) {
				delete(l.buckets, k)
			}
		}
		l.mu.Unlock()
	}
}

// IPResolver resolves the real client IP from a request, trusting
// X-Forwarded-For only when it arrived through a configured trusted-proxy
// CIDR (Traefik sits in front of registry-api — 02, 04). Without a trust
// boundary, any direct, untrusted caller can forge X-Forwarded-For and
// pick whatever bucket its rate limit is billed to; walking the header
// blindly (the previous implementation) makes the whole limiter
// trivially bypassable.
type IPResolver struct {
	trusted []*net.IPNet
}

// NewIPResolver builds a resolver that trusts X-Forwarded-For only from
// the given CIDRs (typically the in-cluster network Traefik runs on).
func NewIPResolver(trusted []*net.IPNet) *IPResolver {
	return &IPResolver{trusted: trusted}
}

func (r *IPResolver) isTrusted(ip net.IP) bool {
	for _, n := range r.trusted {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// Resolve implements the standard trusted-proxy walk: if remoteAddr isn't
// itself a trusted proxy, X-Forwarded-For is ignored entirely (an
// untrusted direct connection can put anything in that header). Otherwise
// it walks X-Forwarded-For from the right, skipping entries that are
// themselves trusted proxies, and returns the first untrusted entry — the
// nearest hop to the real client that isn't infrastructure we control.
func (r *IPResolver) Resolve(remoteAddr, xForwardedFor string) string {
	remoteHost := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		remoteHost = h
	}

	remoteIP := net.ParseIP(remoteHost)
	if remoteIP == nil || !r.isTrusted(remoteIP) {
		return remoteHost
	}

	if xForwardedFor == "" {
		return remoteHost
	}

	parts := strings.Split(xForwardedFor, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		cip := net.ParseIP(candidate)
		if cip == nil {
			continue
		}
		if !r.isTrusted(cip) {
			return candidate
		}
	}
	// Every hop in the chain was itself a trusted proxy (unusual) — fall
	// back to the left-most entry, the closest thing to an origin we have.
	return strings.TrimSpace(parts[0])
}

// FromRequest resolves the client IP for an *http.Request.
func (r *IPResolver) FromRequest(req *http.Request) string {
	return r.Resolve(req.RemoteAddr, req.Header.Get("X-Forwarded-For"))
}

// RateLimit builds gin middleware enforcing l against the caller's IP
// (resolved via resolver), responding 429 rate_limited on the uniform
// error envelope (04) when exceeded.
func RateLimit(l *Limiter, resolver *IPResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := resolver.FromRequest(c.Request)
		if !l.Allow(ip) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, model.ErrorResponse{
				Error: model.ErrorBody{Code: "rate_limited", Message: "too many requests"},
			})
			return
		}
		c.Next()
	}
}
