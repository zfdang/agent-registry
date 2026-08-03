# 05 — Verification Pipeline (L1 / L2 / L3)

The pipeline proves, at registration time and on any later re-verify, that a
submitted URL is a live A2A agent and (best-effort) that it acknowledges the
claimed agent ID. Three checks, run in order with a short-circuit:

```
L1  agent card discovery ── failed ─→ stop (L2/L3 would probe nothing)
      └─ passed
           ├─ L2  agent ID        (warning-only, never blocks)
           └─ L3  A2A protocol probe
result roll-up
```

| Check | Gating | Passes when |
|---|---|---|
| **L1 `l1_card`** | **yes** | A statically valid agent card is discoverable at a well-known path |
| **L2 `l2_id`** | no — worst case `warning` | The card declares an agent ID |
| **L3 `l3_protocol`** | **yes** | The card's endpoint answers a benign JSON-RPC request with a well-formed 2.0 envelope |

Roll-up:

```
L1 failed or L3 failed        → failed                     (not registerable)
L2 warning, L1+L3 passed      → passed_with_warnings       (registerable)
all passed                    → passed                     (registerable)
```

L2 is advisory by design: many existing agents publish perfectly good cards
that simply don't embed a registry identifier. Blocking on it would empty the
directory; hiding it would overstate what was verified. A visible warning is
the honest middle.

## L1 — Agent card discovery

Try, in order, stopping at the first candidate that returns HTTP 200 with
valid JSON:

1. `<origin>/.well-known/agent-card.json`
2. `<origin>/.well-known/agent.json`   (legacy path)
3. the registered URL itself

Static validity requires, per the A2A card schema:
- non-empty `name`
- non-empty `url` (the A2A service endpoint)

L1 is also the **extraction point** for every card-derived listing field:
`name`, `description`, `skills`, `capabilities`, and `provider.organization`
(→ `owner_name`). The submitter types none of these; the card is the sole
source (the declared agent ID is extracted by L2).

On the winning candidate, record in `detail` and on the validation/agent row:
- `card_url` — where it was found
- `card_hash` — sha256 of the raw body (drift detection on re-verify)
- `card_json` — parsed card (cached for `get_agent_card`)

Failure detail lists every candidate tried with its status/error, so the
submitter can see exactly why discovery failed.

### Same-origin and https rules

- The registered URL must be **https** (rejected at input otherwise).
- The card's declared `url` (the A2A endpoint) must be on **exactly the same
  origin** as the registered URL — scheme, host, and port all included.
  Comparing hosts alone would let an https-served card declare an `http://`
  endpoint on its own host, after which L3 would probe — and the registry
  would implicitly vouch for — a cleartext endpoint. Origin mismatch fails L1
  with `card_endpoint_origin_mismatch`.
- L3 re-applies the same rule after re-reading the card, since the card body
  is untrusted input.

## L2 — Agent ID

Nothing is compared — there is no user-submitted ID. The check **extracts**
the ID the card declares and records whether one exists. Accepted
declaration locations, first match wins:

1. `x-registry.agentId` (extension object, recommended)
2. top-level `id`
3. `provider.id`

| Outcome | Status | Detail |
|---|---|---|
| Declared (non-empty, URI-safe, ≤256 chars) | `passed` | `{declared}` |
| Declared but malformed | `warning` | `{declared, note: "invalid format, ignored"}` — stored `agent_id` stays null |
| Not declared anywhere | `warning` | `{declared: null, note: "card does not declare an agent id"}` |

Never `failed`. The extracted value becomes the listing's `agent_id`
(resolvable per 04); an agent without one is simply not addressable by ID —
the URL always works.

## L3 — A2A protocol probe

`POST` to the card's declared endpoint, `Content-Type: application/json`:

```json
{ "jsonrpc": "2.0", "id": "probe-<random>", "method": "tasks/get",
  "params": { "id": "task_probe_nonexistent" } }
```

The probe is deliberately benign: `tasks/get` on a nonexistent id mutates
nothing and costs the agent nothing. **Pass criteria — the response is a
well-formed JSON-RPC 2.0 envelope**, meaning HTTP 200 with a JSON body where:

- `jsonrpc == "2.0"` and `id` echoes the request id, and
- exactly one of `result` / `error` is present.

A JSON-RPC **error** response (e.g. `-32001 TaskNotFound`, or even
`-32601 MethodNotFound`) **passes** — an error envelope is proof the endpoint
speaks the protocol. What fails: network errors, timeouts, non-2xx, non-JSON,
or a body that isn't a 2.0 envelope. `detail` records the endpoint, HTTP
status, and (truncated) envelope or error.

## Probe safety (SSRF hardening)

All outbound requests in L1/L3 go through `pkg/probe`, which is the only
HTTP client in the codebase allowed to fetch user-supplied URLs:

- **Timeout 5 s** per request; response body capped at **256 KiB**.
- **Address guard on the dialer**: the policy check runs on the hostname
  before resolution **and again on the post-resolution socket address**, so
  DNS rebinding between check and connect doesn't help.
- Refused address ranges: loopback, link-local (including cloud metadata
  `169.254.0.0/16`), RFC1918 private space, CGN (`100.64.0.0/10`),
  unspecified, and multicast — over both IPv4 and IPv6 (incl. v4-mapped v6).
- **No proxy** is ever configured — a proxy would tunnel past the dialer guard.
- **Redirects**: at most 3, each re-validated by the guard, and each must
  remain **https**.
- TLS verification is always on; no insecure-skip option exists in the build.
- Optional operator allowlist (`verification.probe_host_allowlist`) admits
  specific private-network **hostnames** for self-hosted test deployments —
  matching is suffix-anchored at a label boundary, never by IP range, and
  loopback stays refused regardless. Empty by default and in production.

## Card snapshots

Every pipeline run re-fetches the card. When the fetched body's sha256
differs from the agent's latest stored snapshot, a new
`agent_card_snapshots` row is appended (`source` records what triggered it:
`register`, `update`, or `refresh`) and the agent's cached `card_json` /
`card_hash` / `card_url` move to the new version. An unchanged card adds no
row — the run just refreshes check timestamps. This gives every agent a
verifiable card history at zero extra probe cost (see 03 for retention).

## Freshness

- Every check row carries `expires_at = checked_at + 30 days`.
- The agent's `verification_status` is recomputed at read time as well as
  after each run, so expiry flips agents to `stale` without a write.
- `stale` agents remain listed (with a distinct badge) in v1; automated
  periodic re-verification and de-listing of persistently failing agents is a
  v2 item — anyone can trigger `POST /v1/agents/*ref/refresh` meanwhile.

## Concurrency

Pipeline runs for the same URL are deduplicated in-process (singleflight):
double-clicking Validate, or an MCP register racing a web validate, performs
one probe sequence and shares the result.
