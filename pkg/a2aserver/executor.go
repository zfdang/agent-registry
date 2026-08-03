package a2aserver

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/zfdang/agent-registry/pkg/service"
)

// agentExecutor implements a2asrv.AgentExecutor (07): it parses the
// registry's own skill discriminator out of the incoming message's data
// part and routes to the corresponding pkg/service call. The two read
// skills (search-agents, get-agent) answer with a single Message — per
// a2asrv's contract that stops execution — so they never create a Task at
// all. register-agent genuinely needs Task semantics (submitted -> working
// -> completed/failed, cancelable), since the pipeline can take up to
// ~15s of outbound probes.
type agentExecutor struct {
	svc *service.Service
}

var _ a2asrv.AgentExecutor = (*agentExecutor)(nil)

// Execute implements a2asrv.AgentExecutor.
func (e *agentExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if execCtx.Message == nil {
			yield(nil, a2a.NewError(a2a.ErrInvalidRequest, "message is required"))
			return
		}

		skill, input, err := parseSkillInput(execCtx.Message)
		if err != nil {
			yield(nil, a2a.NewError(a2a.ErrInvalidParams, err.Error()))
			return
		}
		if !service.KnownSkills[skill] {
			yield(nil, a2a.NewError(a2a.ErrUnsupportedOperation, "unknown skill: "+skill))
			return
		}

		switch skill {
		case "search-agents":
			e.executeSearchAgents(ctx, execCtx, input, yield)
		case "get-agent":
			e.executeGetAgent(ctx, execCtx, input, yield)
		case "register-agent":
			e.executeRegisterAgent(ctx, execCtx, input, yield)
		}
	}
}

// Cancel implements a2asrv.AgentExecutor. The framework itself detects an
// already-terminal task and short-circuits (see a2asrv's canceler), so by
// the time this runs the task is genuinely in flight; emitting a canceled
// status update is the whole job — the framework's optimistic-concurrency
// task-store update is what actually stops a concurrently running Execute
// (its next store write loses the OCC race and unwinds).
func (e *agentExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

// parseSkillInput extracts the skill name and its input from the first
// data part of msg, mirroring 07's "the first data part must carry skill
// naming one of the card's skills."
func parseSkillInput(msg *a2a.Message) (skill string, input map[string]any, err error) {
	for _, part := range msg.Parts {
		if part == nil {
			continue
		}
		data, ok := part.Content.(a2a.Data)
		if !ok {
			continue
		}
		m, ok := data.Value.(map[string]any)
		if !ok {
			return "", nil, fmt.Errorf("data part must be a JSON object")
		}
		skillName, _ := m["skill"].(string)
		if skillName == "" {
			return "", nil, fmt.Errorf("data part must include a skill")
		}
		return skillName, m, nil
	}
	return "", nil, fmt.Errorf("message must carry a data part naming a skill")
}

func (e *agentExecutor) executeSearchAgents(ctx context.Context, execCtx *a2asrv.ExecutorContext, input map[string]any, yield func(a2a.Event, error) bool) {
	query, _ := input["query"].(string)
	status, _ := input["status"].(string)
	limit := intFromAny(input["limit"], 10)
	offset := intFromAny(input["offset"], 0)
	if limit < 1 || limit > 50 {
		limit = 10
	}
	page := offset/max(limit, 1) + 1

	resp, err := e.svc.ListAgents(ctx, query, status, "newest", page, limit)
	if err != nil {
		yield(nil, a2a.NewError(a2a.ErrInternalError, err.Error()))
		return
	}

	result := map[string]any{"agents": resp.Agents, "total": resp.Total}
	yield(a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewDataPart(result)), nil)
}

func (e *agentExecutor) executeGetAgent(ctx context.Context, execCtx *a2asrv.ExecutorContext, input map[string]any, yield func(a2a.Event, error) bool) {
	ref, _ := input["ref"].(string)
	if ref == "" {
		yield(nil, a2a.NewError(a2a.ErrInvalidParams, "ref is required"))
		return
	}

	agent, err := e.svc.GetAgent(ctx, ref)
	if err != nil {
		yield(resolveErrorMessage(execCtx, ref, err), nil)
		return
	}
	verification, err := e.svc.GetVerification(ctx, ref)
	if err != nil {
		yield(nil, a2a.NewError(a2a.ErrInternalError, err.Error()))
		return
	}

	result := map[string]any{"agent": agent, "verification": verification}
	yield(a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewDataPart(result)), nil)
}

// resolveErrorMessage renders a Resolve failure (not_found / ambiguous_ref)
// as a data-part Message rather than a protocol error, so the caller gets
// the same diagnosable shape the REST/MCP surfaces return (04, 06) instead
// of an opaque JSON-RPC error.
func resolveErrorMessage(execCtx *a2asrv.ExecutorContext, ref string, err error) *a2a.Message {
	var ambiguous *service.AmbiguousRefError
	if errors.As(err, &ambiguous) {
		return a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewDataPart(map[string]any{
			"code":       "ambiguous_ref",
			"candidates": ambiguous.Candidates,
		}))
	}
	return a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewDataPart(map[string]any{
		"code":    "not_found",
		"message": "no agent matches ref " + ref,
	}))
}

func (e *agentExecutor) executeRegisterAgent(ctx context.Context, execCtx *a2asrv.ExecutorContext, input map[string]any, yield func(a2a.Event, error) bool) {
	url, _ := input["url"].(string)
	if url == "" {
		yield(nil, a2a.NewError(a2a.ErrInvalidParams, "url is required"))
		return
	}

	if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
		return
	}
	if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
		return
	}

	// The pipeline itself checks ctx.Err() between L1 and L2/L3 (05, 07:
	// "cancellation lands between probes, not mid-request").
	result, err := e.svc.RegisterAgent(ctx, url)
	if err != nil {
		msg := a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewTextPart(err.Error()))
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateFailed, msg), nil)
		return
	}

	if !yield(a2a.NewArtifactEvent(execCtx, a2a.NewDataPart(result)), nil) {
		return
	}

	finalState := a2a.TaskStateCompleted
	if !result.Registered {
		// A failed validation is a normal outcome, not an internal error —
		// the artifact above already carries the per-check diagnosis (07).
		finalState = a2a.TaskStateFailed
	}
	yield(a2a.NewStatusUpdateEvent(execCtx, finalState, nil), nil)
}

func intFromAny(v any, fallback int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return fallback
	}
}
