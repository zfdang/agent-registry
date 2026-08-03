// Package server builds the gin engine and registers every route: REST
// (04), MCP (06), and A2A (07) all share one engine and one middleware
// stack (02).
//
// Routing note (04): gin's radix-tree router panics if a static route and a
// wildcard route are registered as siblings under the same path prefix for
// the same HTTP method — e.g. POST "/v1/agents/validate" cannot coexist
// with POST "/v1/agents/*ref". So for POST we register only the exact
// "/v1/agents" route (bare register) and the wildcard "/v1/agents/*ref"
// (which dispatches /validate, .../refresh, and the empty ref internally —
// see handler.PostRefDispatch). GET uses the same wildcard-dispatch
// pattern for /verification and /cards.
package server

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/zfdang/agent-registry/pkg/handler"
	"github.com/zfdang/agent-registry/pkg/middleware"
	"github.com/zfdang/agent-registry/pkg/service"
)

// Limiters groups the per-route-class rate limiters (04, 06, 07).
type Limiters struct {
	Reads    *middleware.Limiter // 120/min: GET endpoints
	Mutating *middleware.Limiter // 10/min: validate, refresh, register, a2a tasks/send
	TaskRead *middleware.Limiter // 60/min: a2a tasks/get, tasks/cancel
}

// NewLimiters builds the standard rate limiter set.
func NewLimiters() *Limiters {
	return &Limiters{
		Reads:    middleware.NewLimiter(120),
		Mutating: middleware.NewLimiter(10),
		TaskRead: middleware.NewLimiter(60),
	}
}

// MCPMountFunc mounts the MCP streamable-HTTP handler onto the engine.
// Declared as a func type so pkg/server doesn't import pkg/mcpserver
// directly (kept as a thin injection point for main.go to wire up).
type MCPMountFunc func(engine *gin.Engine)

// A2AMountFunc mounts the A2A agent-card + JSON-RPC routes.
type A2AMountFunc func(engine *gin.Engine, limiters *Limiters)

// New builds the fully-wired gin engine.
func New(svc *service.Service, log *zap.SugaredLogger, mountMCP MCPMountFunc, mountA2A A2AMountFunc) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.RequestLogger(log))
	r.Use(middleware.CORS())
	r.Use(middleware.BodyLimit())

	limiters := NewLimiters()
	h := handler.New(svc, log)

	r.GET("/health", h.Health)

	v1 := r.Group("/v1")
	{
		v1.GET("/stats", middleware.RateLimit(limiters.Reads), h.Stats)
		v1.GET("/agents", middleware.RateLimit(limiters.Reads), h.ListAgents)
		v1.GET("/agents/*ref", middleware.RateLimit(limiters.Reads), h.GetRefDispatch)

		v1.POST("/agents", middleware.RateLimit(limiters.Mutating), h.RegisterBare)
		v1.POST("/agents/*ref", middleware.RateLimit(limiters.Mutating), h.PostRefDispatch)
	}

	if mountMCP != nil {
		mountMCP(r)
	}
	if mountA2A != nil {
		mountA2A(r, limiters)
	}

	return r
}
