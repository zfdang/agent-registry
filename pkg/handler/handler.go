package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/zfdang/agent-registry/pkg/model"
	"github.com/zfdang/agent-registry/pkg/service"
)

// Handler wires REST endpoints to the shared service.
type Handler struct {
	svc *service.Service
	log *zap.SugaredLogger
}

// New builds a Handler.
func New(svc *service.Service, log *zap.SugaredLogger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Health handles GET /health. With ?ready=1 it also pings the database.
func (h *Handler) Health(c *gin.Context) {
	if c.Query("ready") == "1" {
		if err := h.svc.Ping(c.Request.Context()); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "unavailable"})
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Stats handles GET /v1/stats.
func (h *Handler) Stats(c *gin.Context) {
	stats, err := h.svc.Stats(c.Request.Context())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, stats)
}

// ListAgents handles GET /v1/agents (04).
func (h *Handler) ListAgents(c *gin.Context) {
	q := c.Query("q")
	status := c.Query("status")
	sort := c.DefaultQuery("sort", "newest")

	page := 1
	if v, err := strconv.Atoi(c.Query("page")); err == nil && v >= 1 {
		page = v
	}
	pageSize := 20
	if v, err := strconv.Atoi(c.Query("page_size")); err == nil && v >= 1 && v <= 100 {
		pageSize = v
	}

	resp, err := h.svc.ListAgents(c.Request.Context(), q, status, sort, page, pageSize)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetRefDispatch handles GET /v1/agents/*ref, dispatching on the reserved
// suffixes /verification and /cards (04's gin routing constraint — see the
// package doc comment on routes.go).
func (h *Handler) GetRefDispatch(c *gin.Context) {
	ref := strings.TrimPrefix(c.Param("ref"), "/")

	switch {
	case strings.HasSuffix(ref, "/verification"):
		h.getVerification(c, strings.TrimSuffix(ref, "/verification"))
	case strings.HasSuffix(ref, "/cards"):
		h.getCards(c, strings.TrimSuffix(ref, "/cards"))
	default:
		h.getAgent(c, ref)
	}
}

func (h *Handler) getAgent(c *gin.Context, ref string) {
	agent, err := h.svc.GetAgent(c.Request.Context(), ref)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, agent)
}

func (h *Handler) getVerification(c *gin.Context, ref string) {
	resp, err := h.svc.GetVerification(c.Request.Context(), ref)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) getCards(c *gin.Context, ref string) {
	resp, err := h.svc.GetCards(c.Request.Context(), ref)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// PostRefDispatch handles POST /v1/agents/*ref, dispatching /validate and
// the /refresh suffix (see routes.go for why this can't coexist with a
// static /v1/agents/validate route).
func (h *Handler) PostRefDispatch(c *gin.Context) {
	ref := c.Param("ref")

	switch {
	case ref == "/validate":
		h.validate(c)
	case strings.HasSuffix(ref, "/refresh"):
		h.refresh(c, strings.TrimPrefix(strings.TrimSuffix(ref, "/refresh"), "/"))
	case ref == "" || ref == "/":
		h.register(c)
	default:
		c.JSON(http.StatusNotFound, errBody("not_found", "unknown route"))
	}
}

// RegisterBare handles the exact POST /v1/agents route (04 §register).
func (h *Handler) RegisterBare(c *gin.Context) {
	h.register(c)
}

func (h *Handler) validate(c *gin.Context) {
	var req model.ValidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("invalid_url", "request body must be {\"url\": \"...\"}"))
		return
	}
	resp, err := h.svc.Validate(c.Request.Context(), req.URL)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) register(c *gin.Context) {
	var req model.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, errBody("invalid_url", "request body must be {\"validation_id\": \"...\"}"))
		return
	}
	agent, _, _, created, err := h.svc.Register(c.Request.Context(), req.ValidationID)
	if err != nil {
		writeError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	c.JSON(status, agent)
}

func (h *Handler) refresh(c *gin.Context, ref string) {
	agent, checks, err := h.svc.Refresh(c.Request.Context(), ref)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, model.RefreshResponse{Agent: agent, Checks: checks})
}
