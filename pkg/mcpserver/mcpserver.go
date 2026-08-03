// Package mcpserver exposes the registry as an MCP tool server (06):
// search_agents, get_agent_card, get_verification, compare_agents,
// register_agent. It is a thin protocol adapter — every tool call maps
// directly onto a pkg/service call, so MCP can never accept an agent the
// web form would reject.
package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/zfdang/agent-registry/pkg/service"
)

// Mount builds the MCP tool server and mounts it at POST /mcp on engine,
// using the stateless Streamable HTTP transport (06).
func Mount(engine *gin.Engine, svc *service.Service) {
	srv := mcpserver.NewMCPServer("agent-registry", "1.0.0")

	registerSearchAgents(srv, svc)
	registerGetAgentCard(srv, svc)
	registerGetVerification(srv, svc)
	registerCompareAgents(srv, svc)
	registerRegisterAgent(srv, svc)

	streamable := mcpserver.NewStreamableHTTPServer(srv, mcpserver.WithStateLess(true))

	engine.Any("/mcp", gin.WrapH(streamable))
}

func toolJSON(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(b)), nil
}

// toolError renders a service error as an MCP tool-error result, keeping
// the same stable code slugs REST uses (06).
func toolError(err error) (*mcp.CallToolResult, error) {
	var ambiguous *service.AmbiguousRefError
	if errors.As(err, &ambiguous) {
		b, _ := json.Marshal(map[string]any{"code": "ambiguous_ref", "candidates": ambiguous.Candidates})
		return mcp.NewToolResultError(string(b)), nil
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
	b, _ := json.Marshal(map[string]any{"code": code, "message": err.Error()})
	return mcp.NewToolResultError(string(b)), nil
}

func registerSearchAgents(srv *mcpserver.MCPServer, svc *service.Service) {
	tool := mcp.NewTool("search_agents",
		mcp.WithDescription("Find agents matching a query. Empty query lists newest agents."),
		mcp.WithString("query", mcp.Description("One query matched over name, description, owner, skills, agent URL (partial/substring), and cached agent-card content.")),
		mcp.WithString("status", mcp.Enum("verified", "verified_with_warnings", "stale")),
		mcp.WithNumber("limit", mcp.DefaultNumber(10), mcp.Min(1), mcp.Max(50)),
		mcp.WithNumber("offset", mcp.DefaultNumber(0), mcp.Min(0)),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		query := req.GetString("query", "")
		status := req.GetString("status", "")
		limit := int(req.GetFloat("limit", 10))
		offset := int(req.GetFloat("offset", 0))
		if limit < 1 || limit > 50 {
			limit = 10
		}
		page := offset/max(limit, 1) + 1

		resp, err := svc.ListAgents(ctx, query, status, "newest", page, limit)
		if err != nil {
			return toolError(err)
		}
		return toolJSON(map[string]any{"agents": resp.Agents, "total": resp.Total})
	})
}

func registerGetAgentCard(srv *mcpserver.MCPServer, svc *service.Service) {
	tool := mcp.NewTool("get_agent_card",
		mcp.WithDescription("Get the latest cached agent card plus registry metadata for one agent."),
		mcp.WithString("ref", mcp.Required(), mcp.Description("Registry id (agt_...), agent_id, or agent URL")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ref, err := req.RequireString("ref")
		if err != nil {
			return toolError(fmt.Errorf("%w: ref is required", service.ErrInvalidInput))
		}
		agent, err := svc.GetAgent(ctx, ref)
		if err != nil {
			return toolError(err)
		}
		return toolJSON(agent)
	})
}

func registerGetVerification(srv *mcpserver.MCPServer, svc *service.Service) {
	tool := mcp.NewTool("get_verification",
		mcp.WithDescription("Get the per-check verification breakdown for one agent."),
		mcp.WithString("ref", mcp.Required(), mcp.Description("Registry id (agt_...), agent_id, or agent URL")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ref, err := req.RequireString("ref")
		if err != nil {
			return toolError(fmt.Errorf("%w: ref is required", service.ErrInvalidInput))
		}
		resp, err := svc.GetVerification(ctx, ref)
		if err != nil {
			return toolError(err)
		}
		return toolJSON(resp)
	})
}

func registerCompareAgents(srv *mcpserver.MCPServer, svc *service.Service) {
	tool := mcp.NewTool("compare_agents",
		mcp.WithDescription("Lay out verifiable facts for 2-5 agents side by side. Unresolvable refs are reported, not errored."),
		mcp.WithArray("refs", mcp.Required(), mcp.MinItems(2), mcp.MaxItems(5), mcp.Items(map[string]any{"type": "string"})),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		raw, ok := req.GetArguments()["refs"].([]any)
		if !ok || len(raw) < 2 || len(raw) > 5 {
			return toolError(fmt.Errorf("%w: refs must have 2-5 items", service.ErrInvalidInput))
		}
		refs := make([]string, 0, len(raw))
		for _, r := range raw {
			s, _ := r.(string)
			refs = append(refs, s)
		}
		resp, err := svc.CompareAgents(ctx, refs)
		if err != nil {
			return toolError(err)
		}
		return toolJSON(resp)
	})
}

func registerRegisterAgent(srv *mcpserver.MCPServer, svc *service.Service) {
	tool := mcp.NewTool("register_agent",
		mcp.WithDescription("Validate a live agent endpoint (L1/L2/L3) and add it to the directory, or update it if already registered. A failed validation is a normal result, not an error."),
		mcp.WithString("url", mcp.Required(), mcp.Description("https base URL of the agent; all listing metadata is extracted from its agent card")),
	)

	srv.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		url, err := req.RequireString("url")
		if err != nil {
			return toolError(fmt.Errorf("%w: url is required", service.ErrInvalidInput))
		}
		result, err := svc.RegisterAgent(ctx, url)
		if err != nil {
			return toolError(err)
		}
		return toolJSON(result)
	})
}
