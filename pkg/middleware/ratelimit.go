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

// ClientIP extracts the caller's IP, preferring the left-most address in
// X-Forwarded-For since Traefik sits in front of registry-api (02, 04).
func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimit builds gin middleware enforcing l against the caller's IP,
// responding 429 rate_limited on the uniform error envelope (04) when
// exceeded.
func RateLimit(l *Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := ClientIP(c.Request)
		if !l.Allow(ip) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, model.ErrorResponse{
				Error: model.ErrorBody{Code: "rate_limited", Message: "too many requests"},
			})
			return
		}
		c.Next()
	}
}
