package a2aserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/a2aproject/a2a-go/v2/a2asrv/taskstore"

	"github.com/zfdang/agent-registry/pkg/middleware"
	"github.com/zfdang/agent-registry/pkg/service"
)

// Mount registers the registry's own A2A surface (07): the agent card at
// both well-known paths, and POST /a2a's JSON-RPC 2.0 methods, built
// entirely on the official A2A SDK (a2asrv). sendLimiter gates
// SendMessage (10/min); taskLimiter gates GetTask/CancelTask/anything else
// (60/min) — see rateLimitedByMethod, which peeks at the JSON-RPC method
// name before the SDK's handler ever sees the request, since a single
// gin route can't otherwise apply different limits per JSON-RPC method.
// resolver determines the trusted boundary for X-Forwarded-For (P2-1).
//
// Task store: the SDK's taskstore.Store interface (Create/Update/Get/List
// with optimistic-concurrency updates and cursor-paginated listing) is
// backed here by taskstore.NewInMemory — a from-scratch Postgres-backed
// implementation of that interface (matching OCC semantics and the
// ListTasks pagination contract) was a substantially larger and more
// error-prone lift than the rest of this adoption, for a task surface
// whose longest task is ~15s. The practical cost is that an in-flight
// register-agent task is lost (not just stuck) if the process crashes
// outright — GetTask on it afterward returns TaskNotFound rather than a
// stuck "working" status, which is an arguably more honest signal to
// callers than the alternative. There is no a2a_tasks table at all
// (docs/03, docs/07): task state is in-memory only, not persisted.
func Mount(engine *gin.Engine, svc *service.Service, publicURL string, sendLimiter, taskLimiter *middleware.Limiter, resolver *middleware.IPResolver) {
	card := AgentCard(publicURL)
	cardHandler := a2asrv.NewStaticAgentCardHandler(card)

	engine.GET(a2asrv.WellKnownAgentCardPath, gin.WrapH(cardHandler))
	engine.GET("/.well-known/agent.json", gin.WrapH(cardHandler)) // legacy discovery path only — harmless and free

	executor := &agentExecutor{svc: svc}
	handler := a2asrv.NewHandler(executor,
		a2asrv.WithCapabilityChecks(&a2a.AgentCapabilities{Streaming: false, PushNotifications: false}),
		a2asrv.WithTaskStore(taskstore.NewInMemory(nil)),
	)
	rpcHandler := a2asrv.NewJSONRPCHandler(handler)

	engine.POST("/a2a", gin.WrapH(rateLimitedByMethod(rpcHandler, sendLimiter, taskLimiter, resolver)))
}

// maxRPCBodyBytes caps how much of a request body rateLimitedByMethod will
// buffer to sniff the JSON-RPC method name before handing off to the SDK.
const maxRPCBodyBytes = 1 << 20 // 1 MiB — every real request here is a small JSON document

// rateLimitedByMethod wraps next (the SDK's JSON-RPC handler) so
// SendMessage is billed against sendLimiter and every other method
// (GetTask, CancelTask, ListTasks, ...) against taskLimiter (07). It
// buffers the body to read the "method" field, then replays the exact
// same bytes to next — the SDK never sees that this happened.
func rateLimitedByMethod(next http.Handler, sendLimiter, taskLimiter *middleware.Limiter, resolver *middleware.IPResolver) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, maxRPCBodyBytes))
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))

		var peek struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &peek) // malformed body: peek stays zero-valued, defaults to the strictest limiter below

		limiter := sendLimiter
		if peek.Method != "" && peek.Method != "SendMessage" {
			limiter = taskLimiter
		}

		if !limiter.Allow(resolver.FromRequest(r)) {
			writeRateLimited(w, peek.ID)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeRateLimited(w http.ResponseWriter, id json.RawMessage) {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	resp := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: id}
	resp.Error.Code = -32029
	resp.Error.Message = "rate limited"

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
