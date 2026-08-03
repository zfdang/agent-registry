# 07 — A2A Interface

The registry is itself an A2A agent: agent runtimes discover it, send it
tasks, poll their status, and cancel them. This is the surface for
**task-shaped** work — registration with its multi-step validation pipeline
runs as an observable, cancelable task, which MCP's request→response model
can't represent.

```
GET  /.well-known/agent-card.json     the registry's own agent card
POST /a2a                             JSON-RPC 2.0: SendMessage, GetTask, CancelTask
```

**Spec baseline: A2A v1.0.1**, implemented with the official Go SDK
(`github.com/a2aproject/a2a-go/v2`). v1.0.1 is a patch over v1.0.0 — no
method, card field, or error code changed; the one wire-relevant difference
is that the HTTP binding now prefers `Content-Type: application/a2a+json`.
(Note: SDK v2.4.0 still sends `application/json` — its `a2aclient` hardcodes
`jsonrpc.ContentJSON`. We inherit that, so don't assume we emit the preferred
type until the SDK does.)

Two things changed from the 0.x line and both are load-bearing:

- **Method names are PascalCase.** `SendMessage` / `GetTask` / `CancelTask`.
  The lineage is `tasks/send` (v0.1) → `message/send` + `tasks/get` (v0.2–0.3)
  → PascalCase (v1.0).
- **The card's top-level `url` is gone**, replaced by a required
  `supportedInterfaces` array.

**We are strict as a server and tolerant as a client.** This surface serves
v1.0 methods only — a 0.x caller gets an honest `MethodNotFound`. Serving the
legacy names would mean pulling in the SDK's `a2acompat/a2av0` package, which
drags the pre-v2 SDK module in as a second dependency, and this registry has
no existing clients that could depend on the old names. The **probe** that
verifies third-party agents (05) does the opposite and falls back to legacy
methods, because we don't control the agents we verify.

Legacy *discovery* is still honored: `/.well-known/agent.json` serves the
same card as `/.well-known/agent-card.json`. Discovery paths cost nothing;
method dialects cost a dependency.

No authentication in v1; per-IP rate limits (`SendMessage` 10/min,
`GetTask`/`CancelTask` 60/min).

## The registry's agent card

Served at `/.well-known/agent-card.json` (and `/.well-known/agent.json` for
legacy clients). The registry passes its own L1 check by construction.

```json
{
  "name": "Agent Registry",
  "description": "Public directory of A2A agents: search, resolve, and register.",
  "version": "1.0.0",
  "supportedInterfaces": [
    { "url": "https://<registry-host>/a2a",
      "protocolBinding": "JSONRPC", "protocolVersion": "1.0" }
  ],
  "capabilities": { "streaming": false, "pushNotifications": false },
  "defaultInputModes": ["application/json"],
  "defaultOutputModes": ["application/json"],
  "skills": [
    { "id": "search-agents",  "name": "Search agents", "tags": ["directory", "search"],
      "description": "Find registered agents by free-text query and filters." },
    { "id": "get-agent",      "name": "Get agent", "tags": ["directory", "resolve"],
      "description": "Resolve one agent by registry id, agent_id, or URL; returns its card and verification state." },
    { "id": "register-agent", "name": "Register agent", "tags": ["directory", "registration"],
      "description": "Validate a live agent endpoint (L1/L2/L3) and add it to the directory." }
  ]
}
```

Endpoint URLs are populated from config (`REGISTRY_PUBLIC_URL`) so the card
is correct in every environment without a rebuild. `tags` is required on each
skill in v1.0. The card is built as an `a2a.AgentCard` and served by the
SDK's `a2asrv.NewStaticAgentCardHandler`, so it cannot drift from the
spec's shape.

## JSON-RPC methods

Single endpoint `POST /a2a`, JSON-RPC 2.0. Methods:

| Method | Purpose |
|---|---|
| `SendMessage` | Create a task (or send a follow-up message to one — not used by v1 skills) |
| `GetTask` | Fetch a task's current status and artifacts |
| `CancelTask` | Request cancellation of a non-terminal task |

### `SendMessage`

```json
{ "jsonrpc": "2.0", "id": 1, "method": "SendMessage",
  "params": {
    "id": "optional-client-task-id",
    "message": {
      "role": "user",
      "parts": [ { "kind": "data",
                   "data": { "skill": "register-agent",
                             "url": "https://agents.acme.example" } } ]
    }
  } }
```

The first data part must carry `skill` naming one of the card's skills; the
remaining keys are that skill's input. Response is the created task snapshot:

```json
{ "jsonrpc": "2.0", "id": 1, "result": {
    "id": "task_…", "status": { "state": "submitted", "timestamp": "…" } } }
```

If the caller supplied a task `id` it is used as an **idempotency key**:
resending the same id returns the existing task instead of creating a
duplicate.

### Task lifecycle

```
submitted ──► working ──► completed
                 │  └────► failed
                 └───────► canceled     (via CancelTask, only before terminal)
```

Fast skills (`search-agents`, `get-agent`) typically complete before the
`SendMessage` response is written — the caller just sees `completed`
immediately. `register-agent` genuinely runs async: a worker picks the task
up, moves it to `working`, runs the validation pipeline (up to ~15 s of
outbound probes), then lands `completed` or `failed`.

### `GetTask`

```json
{ "jsonrpc": "2.0", "id": 2, "method": "GetTask", "params": { "id": "task_…" } }
```

Completed `register-agent` example:

```json
{ "jsonrpc": "2.0", "id": 2, "result": {
    "id": "task_…",
    "status": { "state": "completed", "timestamp": "…" },
    "artifacts": [ { "name": "registration",
      "parts": [ { "kind": "data", "data": {
        "registered": true,
        "agent": { "id": "agt_…", "verification_status": "verified" },
        "checks": [ { "kind": "l1_card", "status": "passed" },
                     { "kind": "l2_id", "status": "warning" },
                     { "kind": "l3_protocol", "status": "passed" } ] } } ] } ] } }
```

A validation that doesn't pass is a **`failed` task** whose artifact carries
the per-check diagnosis — the task failed to achieve registration, and the
evidence travels with it.

### `CancelTask`

Cancels `submitted`/`working` tasks; the worker checks a context between
pipeline stages, so cancellation lands between probes, not mid-request.
Canceling a terminal task returns error `-32002` (task not cancelable).

## Error codes

| Code | Meaning |
|---|---|
| `-32700 / -32600 / -32601 / -32602` | Standard JSON-RPC parse/request/method/params errors |
| `-32001` | Task not found |
| `-32002` | Task not cancelable (already terminal) |
| `-32004` | Unknown skill (spec: UnsupportedOperation) |
| `-32602` | Skill input invalid (detail names the field) |
| `-32029` | Rate limited |

## Skills

### `search-agents`
Input: `{ query?, status?, limit? (≤50), offset? }` → artifact
`{ agents: [...], total }`, same shape as the MCP tool. `query` matches
name, owner, description, skills, the agent URL (partial/substring), and
cached agent-card content — same semantics as `GET /v1/agents?q=`.

### `get-agent`
Input: `{ ref }` (registry id / agent_id / URL) → artifact with the agent
record, cached card, and verification breakdown. URL refs match `url` or
`card_url` after normalization; a URL-shaped ref with no exact match falls
back to substring — unique hit resolves, multiple hits → `failed` task with
`{ code: "ambiguous_ref", candidates: [...] }`. Unknown ref → `failed` task
with `{ code: "not_found" }`.

### `register-agent`
Input: `{ url }` — the only input, validated with the same rule as REST (04);
all listing metadata is extracted from the fetched card. Then the same
pipeline and upsert rule (05, 03): an already-registered URL is updated in
place, and the completion artifact's `mode` field says `"create"` or
`"update"`. Only a failed validation fails the task.

## Implementation

The SDK does the protocol; we supply the behavior.

| Piece | SDK API |
|---|---|
| Skill dispatch | our `a2asrv.AgentExecutor` (`Execute` / `Cancel`) reading the `skill` data part |
| JSON-RPC endpoint | `a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(executor))` → a plain `http.Handler`, mounted on gin at `POST /a2a` via `gin.WrapH` (our rate-limit middleware still applies) |
| Agent card | `a2asrv.NewStaticAgentCardHandler(card)` at `a2asrv.WellKnownAgentCardPath` |
| Streaming refusal | `a2asrv.WithCapabilityChecks(...)` auto-rejects streaming RPCs with `ErrUnsupportedOperation` |
| Task state | the SDK's default in-memory `taskstore` — **not persisted**; see 03 for the tradeoff and why a Postgres-backed store was rejected for v1 |

## Notes on scope

- **No streaming (`SendStreamingMessage`) or push notifications in v1** — the
  longest task is a ~15 s validation; polling `GetTask` is adequate. The
  card advertises both as `false` so compliant clients won't try.
- The registry **never forwards tasks to registered agents**. Its A2A surface
  covers only its own three skills; discovery clients talk to the agents they
  find directly.
- **Task state does not survive a process restart.** An outright crash mid-task
  makes a later `GetTask` answer `TaskNotFound`; callers re-send, which is
  safe because registration is idempotent on the agent URL. Rationale in 03.
