package a2aserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/zfdang/agent-registry/pkg/middleware"
	"github.com/zfdang/agent-registry/pkg/model/db"
	"github.com/zfdang/agent-registry/pkg/service"
)

// Mount registers the registry's own A2A surface: the agent card at both
// well-known paths, and POST /a2a's JSON-RPC 2.0 methods (07). sendLimiter
// gates tasks/send (10/min); taskLimiter gates tasks/get and tasks/cancel
// (60/min).
func Mount(engine *gin.Engine, svc *service.Service, publicURL string, sendLimiter, taskLimiter *middleware.Limiter) {
	card := AgentCard(publicURL)

	serveCard := func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", card)
	}
	engine.GET("/.well-known/agent-card.json", serveCard)
	engine.GET("/.well-known/agent.json", serveCard)

	engine.POST("/a2a", func(c *gin.Context) {
		handleRPC(c, svc, sendLimiter, taskLimiter)
	})
}

type sendParams struct {
	ID      *string `json:"id"`
	Message struct {
		Role  string `json:"role"`
		Parts []struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		} `json:"parts"`
	} `json:"message"`
}

func handleRPC(c *gin.Context, svc *service.Service, sendLimiter, taskLimiter *middleware.Limiter) {
	body, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusOK, rpcErr(nil, codeParseError, "failed to read request body"))
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		c.JSON(http.StatusOK, rpcErr(nil, codeParseError, "invalid JSON"))
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidRequest, "jsonrpc must be \"2.0\" and method is required"))
		return
	}

	ip := middleware.ClientIP(c.Request)

	switch req.Method {
	case "tasks/send":
		if !sendLimiter.Allow(ip) {
			c.JSON(http.StatusOK, rpcErr(req.ID, codeRateLimited, "rate limited"))
			return
		}
		handleTasksSend(c, svc, req, ip)
	case "tasks/get":
		if !taskLimiter.Allow(ip) {
			c.JSON(http.StatusOK, rpcErr(req.ID, codeRateLimited, "rate limited"))
			return
		}
		handleTasksGet(c, svc, req)
	case "tasks/cancel":
		if !taskLimiter.Allow(ip) {
			c.JSON(http.StatusOK, rpcErr(req.ID, codeRateLimited, "rate limited"))
			return
		}
		handleTasksCancel(c, svc, req)
	default:
		c.JSON(http.StatusOK, rpcErr(req.ID, codeMethodNotFound, "unknown method: "+req.Method))
	}
}

func handleTasksSend(c *gin.Context, svc *service.Service, req rpcRequest, clientIP string) {
	var params sendParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidParams, "invalid params"))
		return
	}
	if len(params.Message.Parts) == 0 || params.Message.Parts[0].Type != "data" {
		c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidParams, "message must carry a data part naming a skill"))
		return
	}

	part := params.Message.Parts[0]
	var skillEnvelope struct {
		Skill string `json:"skill"`
	}
	if err := json.Unmarshal(part.Data, &skillEnvelope); err != nil || skillEnvelope.Skill == "" {
		c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidParams, "data part must include a skill"))
		return
	}
	if !service.KnownSkills[skillEnvelope.Skill] {
		c.JSON(http.StatusOK, rpcErr(req.ID, codeUnknownSkill, "unknown skill: "+skillEnvelope.Skill))
		return
	}

	if code, msg, ok := validateSkillInput(skillEnvelope.Skill, part.Data); !ok {
		c.JSON(http.StatusOK, rpcErr(req.ID, code, msg))
		return
	}

	task, err := svc.CreateTask(c.Request.Context(), params.ID, skillEnvelope.Skill, part.Data, clientIP)
	if err != nil {
		c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidParams, err.Error()))
		return
	}

	c.JSON(http.StatusOK, rpcResult(req.ID, taskSnapshot(task, false)))
}

// validateSkillInput enforces 07's per-skill required fields at tasks/send
// time (-32011), rather than letting a malformed request become a failed
// task.
func validateSkillInput(skill string, data json.RawMessage) (code int, message string, ok bool) {
	switch skill {
	case db.SkillGetAgent:
		var in struct {
			Ref string `json:"ref"`
		}
		_ = json.Unmarshal(data, &in)
		if in.Ref == "" {
			return codeInvalidInput, "ref is required", false
		}
	case db.SkillRegisterAgent:
		var in struct {
			URL string `json:"url"`
		}
		_ = json.Unmarshal(data, &in)
		if in.URL == "" {
			return codeInvalidInput, "url is required", false
		}
	}
	return 0, "", true
}

func handleTasksGet(c *gin.Context, svc *service.Service, req rpcRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.ID == "" {
		c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidParams, "id is required"))
		return
	}

	task, err := svc.GetTask(c.Request.Context(), params.ID)
	if err != nil {
		if errors.Is(err, service.ErrTaskNotFound) {
			c.JSON(http.StatusOK, rpcErr(req.ID, codeTaskNotFound, "task not found"))
			return
		}
		c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidParams, err.Error()))
		return
	}

	c.JSON(http.StatusOK, rpcResult(req.ID, taskSnapshot(task, true)))
}

func handleTasksCancel(c *gin.Context, svc *service.Service, req rpcRequest) {
	var params struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil || params.ID == "" {
		c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidParams, "id is required"))
		return
	}

	task, err := svc.CancelTask(c.Request.Context(), params.ID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrTaskNotFound):
			c.JSON(http.StatusOK, rpcErr(req.ID, codeTaskNotFound, "task not found"))
		case errors.Is(err, service.ErrTaskNotCancelable):
			c.JSON(http.StatusOK, rpcErr(req.ID, codeNotCancelable, "task is not cancelable (already terminal)"))
		default:
			c.JSON(http.StatusOK, rpcErr(req.ID, codeInvalidParams, err.Error()))
		}
		return
	}

	c.JSON(http.StatusOK, rpcResult(req.ID, taskSnapshot(task, true)))
}

// taskSnapshot renders a task row as the wire shape from 07: status plus,
// once terminal, an artifacts array carrying the skill's result or
// diagnosis.
func taskSnapshot(t *db.A2ATask, includeArtifacts bool) map[string]any {
	out := map[string]any{
		"id": t.ID,
		"status": map[string]any{
			"state":     t.Status,
			"timestamp": t.UpdatedAt.Format(rfc3339),
		},
	}

	if !includeArtifacts || len(t.Result) == 0 {
		return out
	}
	if t.Status != db.TaskCompleted && t.Status != db.TaskFailed {
		return out
	}

	var data any
	_ = json.Unmarshal(t.Result, &data)
	out["artifacts"] = []map[string]any{
		{
			"name": artifactName(t.Skill),
			"parts": []map[string]any{
				{"type": "data", "data": data},
			},
		},
	}
	return out
}

func artifactName(skill string) string {
	switch skill {
	case db.SkillRegisterAgent:
		return "registration"
	case db.SkillSearchAgents:
		return "search-results"
	case db.SkillGetAgent:
		return "agent"
	default:
		return "result"
	}
}

const rfc3339 = "2006-01-02T15:04:05Z07:00"
