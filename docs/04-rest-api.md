# 04 — REST API

The surface the web frontend consumes. All endpoints are public
(no authentication in v1) and **rate limited per client IP**. JSON in/out;
errors use a uniform envelope.

```
GET  /health

GET  /v1/stats
GET  /v1/agents
GET  /v1/agents/*ref
GET  /v1/agents/*ref/verification
GET  /v1/agents/*ref/cards
POST /v1/agents/validate
POST /v1/agents
POST /v1/agents/*ref/refresh
```

## Agent references (`*ref`)

The **agent URL is the canonical key**. Because registered URLs are https-only
and stored normalized, the scheme is dropped in references: agent
`https://api.acme.example/agent` is addressed as
`/v1/agents/api.acme.example/agent` (hence the wildcard `*ref` — it may
contain slashes). Two other forms are accepted:

| Form | Resolution |
|---|---|
| `agt_<uuid>` | Registry id, exact |
| host[/path] (URL-shaped: contains a `.` or `/`) | Normalized match on `url` (with `https://` re-prepended), then `card_url` |
| anything else | `agent_id` exact match |

`agent_id` is not unique (03): when an `agent_id` ref matches multiple
agents, the response is `409 ambiguous_ref` with
`candidates: [{id, url, name}]`; the caller retries with the URL form. An
`agent_id` that is itself URL-shaped (e.g. a DID with dots) is tried as a URL
first, then as `agent_id` — first exact hit wins.

Rate limits (per IP): reads 120/min; `validate`, `refresh` 10/min (they
trigger outbound probes); `POST /v1/agents` 10/min.

## Error envelope

```json
{ "error": { "code": "ambiguous_ref", "message": "multiple agents match this agent_id — use the agent URL", "candidates": [ { "id": "agt_…", "url": "https://…", "name": "…" } ] } }
```

`code` is a stable machine-readable slug; `message` is human-readable and
never echoes internal details. Extra fields (like `existing_id`) are
code-specific.

## Endpoints

### `GET /v1/stats`

```json
{ "total_agents": 1240, "verified_agents": 1103, "new_this_week": 37, "distinct_owners": 412 }
```

### `GET /v1/agents` — search + browse

Query params:

| Param | Rules |
|---|---|
| `q` | Optional. One query, matched everywhere: full-text + substring over name/description/owner/skills, **partial (substring) match on the agent URL**, and full-text over the cached agent-card content (`card_text`). URL example: `q=ip402.xyz` returns every agent whose `url` contains `ip402.xyz` (e.g. `https://ip402.xyz`, `https://api.ip402.xyz/agent`) |
| `page` | ≥1, default 1 |
| `page_size` | 1–100, default 20; out-of-range falls back to default |
| `status` | Optional filter: `verified` \| `stale` |
| `sort` | `newest` (default) \| `name` |

```json
{
  "agents": [
    {
      "id": "agt_…", "name": "Image Studio", "owner_name": "Acme Labs",
      "agent_id": "did:example:acme:image-studio",
      "url": "https://agents.acme.example",
      "description": "Generates and edits images",
      "skills": [{ "id": "generate-image", "name": "Generate image" }],
      "verification_status": "verified",
      "verified_at": "2026-08-01T09:30:00Z",
      "created_at": "2026-07-12T02:11:00Z"
    }
  ],
  "total": 1240, "page": 1, "page_size": 20
}
```

### `GET /v1/agents/*ref`

Full record: everything in the list item plus `capabilities`, `card_url`, and
`card_json` (the latest card snapshot). 404 for an unresolvable ref, 409 for
an ambiguous `agent_id` (see above).

### `GET /v1/agents/*ref/cards` — card history

The agent's card snapshots, newest first (03 — a snapshot is recorded
whenever a fetch returns changed content):

```json
{
  "snapshots": [
    { "id": "snap_…", "card_url": "https://…/.well-known/agent-card.json",
      "card_hash": "sha256:…", "source": "refresh", "fetched_at": "2026-08-01T09:30:00Z",
      "card_json": { … } },
    { "id": "snap_…", "card_hash": "sha256:…", "source": "register",
      "fetched_at": "2026-07-12T02:11:00Z", "card_json": { … } }
  ]
}
```

### `GET /v1/agents/*ref/verification`

```json
{
  "status": "verified",
  "verified_at": "2026-08-01T09:30:00Z",
  "checks": [
    { "kind": "l1_card",     "status": "passed",  "checked_at": "…", "expires_at": "…",
      "detail": { "card_url": "https://agents.acme.example/.well-known/agent-card.json" } },
    { "kind": "l2_protocol", "status": "passed",  "checked_at": "…", "expires_at": "…",
      "detail": { "endpoint": "https://agents.acme.example/a2a", "method": "GetTask", "protocol_version": "1.0" } }
  ]
}
```

### `POST /v1/agents/validate` — dry-run validation

Runs the full L1/L2 pipeline (05-verification.md) against the submitted
URL **without creating an agent**, persists a `validations` row, and returns
its id.

Request — **the URL is the only input**; all display metadata is extracted
from the fetched card server-side:

```json
{ "url": "https://agents.acme.example" }
```

Field rule: `url` must be a valid absolute **https** URL, no userinfo, no
fragment, ≤2048 chars. Probe-level address restrictions are enforced later
by the SSRF guard, not by parsing.

Response (`200` regardless of check outcome — a failed check is a result,
not an HTTP error):

```json
{
  "validation_id": "val_…",
  "result": "passed",
  "registerable": true,
  "mode": "create",
  "existing_id": null,
  "expires_at": "2026-08-03T10:15:00Z",
  "extracted": {
    "name": "Image Studio",
    "owner_name": "Acme Labs",
    "agent_id": null,
    "description": "Generates and edits images",
    "skills": [{ "id": "generate-image", "name": "Generate image" }]
  },
  "checks": [ …same shape as /verification… ]
}
```

`extracted` is what the listing will contain — the UI renders it as a
preview so the submitter sees exactly what their card publishes (`name` from
the card's `name`, `owner_name` from `provider.organization`, `agent_id`
from the card's declared ID per 05 §L1; the latter two may be null). It is
informational: register reads the values stored on the validation row, not
anything the client sends back. `extracted` is null when L1 failed (there is
no card).

`registerable` is simply `result != "failed"`. A URL that is already
registered is **not a conflict**: `mode` becomes `"update"` and
`existing_id` names the row that registration will overwrite (03's upsert
rule) — the UI shows "this will update the existing listing" with a link.

### `POST /v1/agents` — register (create or update)

```json
{ "validation_id": "val_…" }
```

The server reads the URL and the card-extracted metadata **stored on the
validation row** — the client sends nothing else and can alter nothing
between validate and register. Upsert keyed on the normalized URL: create
when new, otherwise re-sync the existing row's card-derived metadata.

| Condition | Status |
|---|---|
| Created | `201` + full agent record |
| Updated existing (same URL) | `200` + full agent record |
| Validation unknown | `404 validation_not_found` |
| Validation expired | `410 validation_expired` |
| Validation already consumed | `409 validation_consumed` |
| Validation result was `failed` | `422 validation_failed` |

On success the validation is consumed atomically (guarded `UPDATE … WHERE
consumed_at IS NULL`), its checks are (re-)linked to the agent row, and a
card snapshot is recorded (`source: "register"` or `"update"`) when the card
content differs from the latest stored one.

### `POST /v1/agents/*ref/refresh` — refresh

The detail page's **Refresh** button. Re-runs the full pipeline against the
stored URL: re-fetches the card (new snapshot if content changed,
`source: "refresh"`), **re-extracts all card-derived metadata** (name, owner,
description, skills, agent_id), re-runs L1/L2, recomputes
`verification_status`, and returns `{agent, checks}`. Open to anyone —
every field is card-derived, so a refresh can only sync the listing with
what the endpoint's operator already publishes; the rate limit is tight
because of the outbound probes. Refresh is the same sync as a
re-registration — it just can't create.

## Status → error code map

| HTTP | Codes |
|---|---|
| 400 | `invalid_url` |
| 404 | `not_found`, `validation_not_found` |
| 409 | `ambiguous_ref` (carries `candidates`), `validation_consumed` |
| 410 | `validation_expired` |
| 422 | `validation_failed` |
| 429 | `rate_limited` |
