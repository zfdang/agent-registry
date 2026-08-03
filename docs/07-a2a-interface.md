# 07 — A2A Interface

The registry is itself an A2A agent: agent runtimes discover it, send it
tasks, poll their status, and cancel them. This is the surface for
**task-shaped** work — registration with its multi-step validation pipeline
runs as an observable, cancelable task, which MCP's request→response model
can't represent.

```
GET  /.well-known/agent-card.json     the registry's own agent card
POST /a2a                             JSON-RPC 2.0: tasks/send, tasks/get, tasks/cancel
```

No authentication in v1; per-IP rate limits (`tasks/send` 10/min,
`tasks/get`/`tasks/cancel` 60/min).

## The registry's agent card

Served at `/.well-known/agent-card.json` (and `/.well-known/agent.json` for
legacy clients). The registry passes its own L1 check by construction.

```json
{
  "name": "Agent Registry",
  "description": "Public directory of A2A agents: search, resolve, and register.",
  "url": "https://<registry-host>/a2a",
  "version": "1.0.0",
  "capabilities": { "streaming": false, "pushNotifications": false },
  "defaultInputModes": ["application/json"],
  "defaultOutputModes": ["application/json"],
  "skills": [
    { "id": "search-agents",  "name": "Search agents",
      "description": "Find registered agents by free-text query and filters." },
    { "id": "get-agent",      "name": "Get agent",
      "description": "Resolve one agent by registry id, agent_id, or URL; returns its card and verification state." },
    { "id": "register-agent", "name": "Register agent",
      "description": "Validate a live agent endpoint (L1/L2/L3) and add it to the directory." }
  ]
}
```

`url` is populated from config (`server.public_url`) so the card is correct
in every environment without a rebuild.

## JSON-RPC methods

Single endpoint `POST /a2a`, JSON-RPC 2.0. Methods:

| Method | Purpose |
|---|---|
| `tasks/send` | Create a task (or send a follow-up message to one — not used by v1 skills) |
| `tasks/get` | Fetch a task's current status and artifacts |
| `tasks/cancel` | Request cancellation of a non-terminal task |

### `tasks/send`

```json
{ "jsonrpc": "2.0", "id": 1, "method": "tasks/send",
  "params": {
    "id": "optional-client-task-id",
    "message": {
      "role": "user",
      "parts": [ { "type": "data",
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
                 └───────► canceled     (via tasks/cancel, only before terminal)
```

Fast skills (`search-agents`, `get-agent`) typically complete before the
`tasks/send` response is written — the caller just sees `completed`
immediately. `register-agent` genuinely runs async: a worker picks the task
up, moves it to `working`, runs the validation pipeline (up to ~15 s of
outbound probes), then lands `completed` or `failed`.

### `tasks/get`

```json
{ "jsonrpc": "2.0", "id": 2, "method": "tasks/get", "params": { "id": "task_…" } }
```

Completed `register-agent` example:

```json
{ "jsonrpc": "2.0", "id": 2, "result": {
    "id": "task_…",
    "status": { "state": "completed", "timestamp": "…" },
    "artifacts": [ { "name": "registration",
      "parts": [ { "type": "data", "data": {
        "registered": true,
        "agent": { "id": "agt_…", "verification_status": "verified" },
        "checks": [ { "kind": "l1_card", "status": "passed" },
                     { "kind": "l2_id", "status": "warning" },
                     { "kind": "l3_protocol", "status": "passed" } ] } } ] } ] } }
```

A validation that doesn't pass is a **`failed` task** whose artifact carries
the per-check diagnosis — the task failed to achieve registration, and the
evidence travels with it.

### `tasks/cancel`

Cancels `submitted`/`working` tasks; the worker checks a context between
pipeline stages, so cancellation lands between probes, not mid-request.
Canceling a terminal task returns error `-32002` (task not cancelable).

## Error codes

| Code | Meaning |
|---|---|
| `-32700 / -32600 / -32601 / -32602` | Standard JSON-RPC parse/request/method/params errors |
| `-32001` | Task not found |
| `-32002` | Task not cancelable (already terminal) |
| `-32010` | Unknown skill |
| `-32011` | Skill input invalid (detail names the field) |
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

## Notes on scope

- **No streaming (`tasks/sendSubscribe`) or push notifications in v1** — the
  longest task is a ~15 s validation; polling `tasks/get` is adequate. The
  card advertises both as `false` so compliant clients won't try.
- The registry **never forwards tasks to registered agents**. Its A2A surface
  covers only its own three skills; discovery clients talk to the agents they
  find directly.
