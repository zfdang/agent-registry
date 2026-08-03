# 06 — MCP Interface

The registry runs an MCP server for AI assistants (and agent frameworks that
prefer tool-call semantics) at:

```
POST /mcp        (Streamable HTTP transport, stateless)
```

Implemented with `mark3labs/mcp-go`, mounted on the same gin engine as REST.
No authentication in v1; the same per-IP rate limits as REST apply
(reads 120/min, `register_agent` 10/min). Tool results are returned as JSON
text content; errors use MCP tool-error results with the same stable `code`
slugs as the REST API.

MCP is the **lookup surface**: every tool is a single request→response
exchange. Anything task-shaped (long-running, cancelable) belongs on the A2A
surface instead (07).

## Tools

### `search_agents`

Find agents matching a query.

```json
{
  "name": "search_agents",
  "inputSchema": {
    "type": "object",
    "properties": {
      "query":     { "type": "string", "description": "One query matched over name, description, owner, skills, agent URL (partial/substring — a bare hostname works), and cached agent-card content. Empty lists newest agents." },
      "status":    { "type": "string", "enum": ["verified", "verified_with_warnings", "stale"] },
      "limit":     { "type": "integer", "minimum": 1, "maximum": 50, "default": 10 },
      "offset":    { "type": "integer", "minimum": 0, "default": 0 }
    }
  }
}
```

Returns `{agents: [...], total}` with the same list-item shape as
`GET /v1/agents`, trimmed for token economy (no `card_json`).

### `get_agent_card`

```json
{ "inputSchema": { "type": "object",
    "properties": { "ref": { "type": "string",
      "description": "Registry id (agt_…), agent_id, or agent URL" } },
    "required": ["ref"] } }
```

Resolution follows the REST rules (04): **the agent URL is the canonical
key** — `agt_` prefix → registry id; URL-shaped refs (scheme optional) →
normalized match on `url` then `card_url`; anything else → `agent_id`.
`agent_id` is not unique: multiple hits return a tool error
`ambiguous_ref` with `candidates: [{id, url, name}]`. A URL-shaped ref with
no exact match falls back to substring — a unique partial match resolves.
Returns the **latest cached agent card** (`card_json`) plus registry
metadata (`verification_status`, `verified_at`, `card_url`). Serving the
cache keeps this tool fast and prevents assistants from turning the registry
into an SSRF proxy; the card is at most as stale as the last verification.

### `get_verification`

Same `ref` resolution; returns the per-check breakdown, identical to
`GET /v1/agents/*ref/verification`. Lets an assistant explain *why* an agent
carries a warning before recommending it.

### `compare_agents`

```json
{ "inputSchema": { "type": "object",
    "properties": { "refs": { "type": "array", "items": { "type": "string" },
                              "minItems": 2, "maxItems": 5 } },
    "required": ["refs"] } }
```

Returns a side-by-side structure the assistant can reason over:

```json
{
  "agents": [
    { "ref": "agt_…", "name": "…", "owner_name": "…",
      "verification_status": "verified",
      "skills": ["generate-image", "edit-image"],
      "capabilities": { "streaming": true },
      "verified_at": "…", "created_at": "…" }
  ],
  "unresolved": ["did:example:missing"]
}
```

The registry does **not** score or rank — it lays out verifiable facts
(skills, capabilities, verification state, age) and leaves the judgment to
the caller. Unresolvable refs are reported, not errored, so one bad ref
doesn't sink the comparison.

### `register_agent`

```json
{ "inputSchema": { "type": "object",
    "properties": {
      "url": { "type": "string",
        "description": "https base URL of the agent. All listing metadata (name, owner, description, skills, agent ID) is extracted from the agent card discovered at this URL." }
    },
    "required": ["url"] } }
```

Runs the full validation pipeline (05) and, when the result is registerable,
creates **or updates** the agent in one call (upsert keyed on the normalized
URL, per 03) — the two-step validate/submit dance exists for the human UI; a
programmatic caller wants one atomic operation.

Result:

```json
{ "registered": true, "mode": "update",
  "agent": { "id": "agt_…", "verification_status": "verified_with_warnings" },
  "checks": [ … ] }
```

On a failed validation: `{ "registered": false, "checks": [...] }` with the
failing check's detail — a tool *result*, not a tool error, so the assistant
can relay the diagnosis (e.g. "your endpoint didn't answer the JSON-RPC
probe"). Re-registering an existing URL is not an error; `mode` says what
happened.

## Tool → service mapping

| Tool | Service call | Also used by |
|---|---|---|
| `search_agents` | `service.Search` | `GET /v1/agents`, A2A `search-agents` |
| `get_agent_card` | `service.Resolve` + cached card | `GET /v1/agents/*ref` |
| `get_verification` | `service.GetVerification` | `GET /v1/agents/*ref/verification` |
| `compare_agents` | `service.Resolve` ×N | — |
| `register_agent` | `service.Validate` + `service.Register` | `POST /v1/agents/*`, A2A `register-agent` |

One service layer, three adapters — MCP can never accept an agent the web
form would reject.
