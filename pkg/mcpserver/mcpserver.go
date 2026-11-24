// Package mcpserver exposes the registry as an MCP tool server (06):
// search_agents, get_agent_card, get_verification, compare_agents,
// register_agent. It is a thin protocol adapter — every tool call maps
// directly onto a pkg/service call, so MCP can never accept an agent the
// web form would reject.
//
// Built on the official github.com/modelcontextprotocol/go-sdk, which
// implements MCP spec revision 2026-07-28 (mark3labs/mcp-go, the previous
// dependency, was pinned to 2025-11-25 with no announced upgrade path, and
// 2026-07-28 removed the session/initialize handshake entirely — a modern
// client talking to a handshake-era server fails hard per the spec's own
// compatibility matrix, so this wasn't optional version hygiene).
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/zfdang/agent-registry/pkg/middleware"
	"github.com/zfdang/agent-registry/pkg/service"
)

// clientIPKey is the context key the request-wrapping handler stashes the
// resolved client IP under, so register_agent's tool handler — which has
// no gin.Context — can rate-limit itself (P1-3). The SDK's streamable HTTP
// handler is documented to pass req.Context() through to allow exactly
// this ("Pass req.Context() here, to allow middleware to add context
// values" — mcp/streamable.go), so a value stashed here before the SDK's
// ServeHTTP runs reaches the tool handler's ctx parameter.
type clientIPKey struct{}

// Mount builds the MCP tool server and mounts it at POST /mcp on engine
// using the stateless Streamable HTTP transport (06). readsLimiter gates
// the route itself (matching REST's 120/min read limit); mutatingLimiter
// is enforced inside register_agent's handler, since it's the one MCP
// tool that triggers outbound probes and writes (P1-3).
//
// The current MCP spec makes Origin validation a MUST for Streamable HTTP
// servers: an invalid Origin must get 403, while non-browser callers
// (no Origin header at all) must keep working. That's exactly the
// contract of the standard library's http.CrossOriginProtection (Go
// 1.25+), which the SDK's own docs point at now that its built-in
// CrossOriginProtection option is deprecated in favor of wrapping the
// handler — so publicURL is trusted explicitly and everything else not
// same-origin is rejected.
func Mount(engine *gin.Engine, svc *service.Service, publicURL string, readsLimiter, mutatingLimiter *middleware.Limiter, resolver *middleware.IPResolver) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "agent-registry", Version: "1.0.0"}, nil)

	registerSearchAgents(srv, svc)
	registerGetAgentCard(srv, svc)
	registerGetVerification(srv, svc)
	registerCompareAgents(srv, svc)
	registerRegisterAgent(srv, svc, mutatingLimiter)

	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)

	origin := http.NewCrossOriginProtection()
	if err := origin.AddTrustedOrigin(publicURL); err != nil {
		// publicURL is operator config (REGISTRY_PUBLIC_URL); a malformed
		// value here is a boot-time misconfiguration, not a per-request
		// failure — same-origin requests still work either way, so fail
		// open on this specific call rather than panic.
		_ = err
	}
	protected := origin.Handler(streamable)

	engine.Any("/mcp", middleware.RateLimit(readsLimiter, resolver), gin.WrapH(injectClientIP(protected, resolver)))
}

// injectClientIP resolves the caller's IP once (respecting the trusted-
// proxy boundary, P2-1) and stashes it in the request context before
// handing off to the SDK, so tool handlers deep inside — which never see
// a *http.Request — can still rate-limit by IP.
func injectClientIP(next http.Handler, resolver *middleware.IPResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), clientIPKey{}, resolver.FromRequest(r))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func toolJSON(v any) *mcp.CallToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return toolErrorResult("internal_error", err.Error())
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
}

func toolErrorResult(code, message string) *mcp.CallToolResult {
	b, _ := json.Marshal(map[string]any{"code": code, "message": message})
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
		IsError: true,
	}
}

// toolError renders a service error as an MCP tool-error result, keeping
// the same stable code slugs REST uses (06).
func toolError(err error) *mcp.CallToolResult {
	var ambiguous *service.AmbiguousRefError
	if errors.As(err, &ambiguous) {
		b, _ := json.Marshal(map[string]any{"code": "ambiguous_ref", "candidates": ambiguous.Candidates})
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, IsError: true}
	}

	code := "internal_error"
	switch {
	case errors.Is(err, service.ErrNotFound):
		code = "not_found"
	case errors.Is(err, service.ErrInvalidURL):
		code = "invalid_url"
	case errors.Is(err, service.ErrValidationNotFound):
		code = "validation_not_found"
	case errors.Is(err, service.ErrValidationExpired):
		code = "validation_expired"
	case errors.Is(err, service.ErrValidationConsumed):
		code = "validation_consumed"
	case errors.Is(err, service.ErrValidationFailed):
		code = "validation_failed"
	}
	return toolErrorResult(code, err.Error())
}

func floatPtr(f float64) *float64 { return &f }
func intPtr(n int) *int           { return &n }

// refSchema is the {ref: string, required} input shape shared by
// get_agent_card and get_verification (06).
func refSchema(description string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"ref": {Type: "string", Description: description},
		},
		Required: []string{"ref"},
	}
}

// refArgs is the typed input for tools taking a single ref.
type refArgs struct {
	Ref string `json:"ref"`
}

// searchAgentsArgs is search_agents' input (06): query/status/limit/offset,
// all optional.
type searchAgentsArgs struct {
	Query  string `json:"query,omitempty"`
	Status string `json:"status,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// searchAgentsSchema builds search_agents' input schema (06):
// query/status/limit/offset, all optional, status enum verified | partial | stale,
// limit 1-50 default 10, offset >=0 default 0.
func searchAgentsSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"query": {
				Type:        "string",
				Description: "One query matched over name, description, owner, skills, agent URL (partial/substring), and cached agent-card content.",
			},
			"status": {
				Type: "string",
				Enum: []any{"verified", "partial", "stale"},
			},
			"limit": {
				Type:    "integer",
				Minimum: floatPtr(1),
				Maximum: floatPtr(50),
				Default: json.RawMessage("10"),
			},
			"offset": {
				Type:    "integer",
				Minimum: floatPtr(0),
				Default: json.RawMessage("0"),
			},
		},
	}
}

func registerSearchAgents(srv *mcp.Server, svc *service.Service) {
	tool := &mcp.Tool{
		Name:        "search_agents",
		Description: "Find agents matching a query. Empty query lists newest agents.",
		InputSchema: searchAgentsSchema(),
	}

	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in searchAgentsArgs) (*mcp.CallToolResult, any, error) {
		limit := in.Limit
		if limit < 1 || limit > 50 {
			limit = 10
		}
		page := in.Offset/max(limit, 1) + 1

		resp, err := svc.ListAgents(ctx, in.Query, in.Status, "newest", page, limit)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(map[string]any{"agents": resp.Agents, "total": resp.Total}), nil, nil
	})
}

func registerGetAgentCard(srv *mcp.Server, svc *service.Service) {
	tool := &mcp.Tool{
		Name:        "get_agent_card",
		Description: "Get the latest cached agent card plus registry metadata for one agent.",
		InputSchema: refSchema("Registry id (agt_...), agent_id, or agent URL"),
	}

	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in refArgs) (*mcp.CallToolResult, any, error) {
		agent, err := svc.GetAgent(ctx, in.Ref)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(agent), nil, nil
	})
}

func registerGetVerification(srv *mcp.Server, svc *service.Service) {
	tool := &mcp.Tool{
		Name:        "get_verification",
		Description: "Get the per-check verification breakdown for one agent.",
		InputSchema: refSchema("Registry id (agt_...), agent_id, or agent URL"),
	}

	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in refArgs) (*mcp.CallToolResult, any, error) {
		resp, err := svc.GetVerification(ctx, in.Ref)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(resp), nil, nil
	})
}

// compareAgentsArgs is compare_agents' input (06): 2-5 refs.
type compareAgentsArgs struct {
	Refs []string `json:"refs"`
}

// compareAgentsSchema builds compare_agents' input schema (06): refs, a
// required array of 2-5 strings.
func compareAgentsSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"refs": {
				Type:     "array",
				Items:    &jsonschema.Schema{Type: "string"},
				MinItems: intPtr(2),
				MaxItems: intPtr(5),
			},
		},
		Required: []string{"refs"},
	}
}

func registerCompareAgents(srv *mcp.Server, svc *service.Service) {
	tool := &mcp.Tool{
		Name:        "compare_agents",
		Description: "Lay out verifiable facts for 2-5 agents side by side. Unresolvable refs are reported, not errored.",
		InputSchema: compareAgentsSchema(),
	}

	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in compareAgentsArgs) (*mcp.CallToolResult, any, error) {
		resp, err := svc.CompareAgents(ctx, in.Refs)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(resp), nil, nil
	})
}

// registerAgentArgs is register_agent's input (06): the agent's URL only.
type registerAgentArgs struct {
	URL string `json:"url"`
}

// registerAgentSchema builds register_agent's input schema (06): url,
// required.
func registerAgentSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"url": {
				Type:        "string",
				Description: "https base URL of the agent; all listing metadata is extracted from its agent card",
			},
		},
		Required: []string{"url"},
	}
}

func registerRegisterAgent(srv *mcp.Server, svc *service.Service, mutatingLimiter *middleware.Limiter) {
	tool := &mcp.Tool{
		Name:        "register_agent",
		Description: "Validate a live agent endpoint (L1 agent card, L2 A2A protocol probe) and add it to the directory, or update it if already registered. A failed validation is a normal result, not an error.",
		InputSchema: registerAgentSchema(),
	}

	mcp.AddTool(srv, tool, func(ctx context.Context, _ *mcp.CallToolRequest, in registerAgentArgs) (*mcp.CallToolResult, any, error) {
		ip, _ := ctx.Value(clientIPKey{}).(string)
		if !mutatingLimiter.Allow(ip) {
			return toolErrorResult("rate_limited", "too many requests"), nil, nil
		}

		result, err := svc.RegisterAgent(ctx, in.URL)
		if err != nil {
			return toolError(err), nil, nil
		}
		return toolJSON(result), nil, nil
	})
}
