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
- a declared A2A service endpoint (`supportedInterfaces[].url`, or the
  legacy top-level `url` — see below)

L1 is also the **extraction point** for every card-derived listing field:
`name`, `description`, `skills`, `capabilities`, `provider.organization`
(→ `owner_name`), the card's own `version`, and the declared A2A
`protocol_version`. The submitter types none of these; the card is the sole
source (the declared agent ID is extracted by L2).

**The service endpoint comes from one of two places**, since A2A v1.0 dropped
the card's top-level `url` in favor of a required `supportedInterfaces`
array: prefer the first `supportedInterfaces` entry whose `protocolBinding`
is `JSONRPC`, and fall back to the legacy top-level `url`. A card with
neither declares no endpoint and fails L1. The entry's `protocolVersion` is
retained — L3 uses it to pick its method dialect.

Parsing uses the SDK's `a2a.AgentCard` type (which imports no `net/http`, so
it is safe to use purely as a schema), plus a raw-map pass to recover the
legacy top-level `url` that the v1.0 struct no longer models.

On the winning candidate, record in `detail` and on the validation/agent row:
- `card_url` — where it was found
- `card_hash` — sha256 of the raw body (drift detection on re-verify)
- `card_json` — parsed card (cached for `get_agent_card`)

Failure detail lists every candidate tried with its status/error, so the
submitter can see exactly why discovery failed.

### Same-origin and https rules

- The registered URL must be **https** (rejected at input otherwise).
- The card's declared endpoint must be on **exactly the same
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

`POST` to the card's declared endpoint, `Content-Type: application/json`,
asking for a task that cannot exist:

```json
{ "jsonrpc": "2.0", "id": "probe-<random>", "method": "GetTask",
  "params": { "id": "task_probe_nonexistent" } }
```

The probe is deliberately benign: a task read on a nonexistent id mutates
nothing and costs the agent nothing.

### Dialect selection

A2A renamed its methods twice (`tasks/get` in v0.1–v0.3, `GetTask` in v1.0),
and registered agents span both generations. The registry's own surface is
v1.0-only (07), but the probe must be tolerant — we don't control the agents
we verify.

1. **v1.0 first**, through the official Go SDK client (`a2aclient` built from
   the parsed card, with our guarded HTTP client injected). It sends the
   `A2A-Version` header automatically, and outcomes are read off the SDK's
   sentinel errors (`errors.Is(err, a2a.ErrTaskNotFound)` and friends) rather
   than by inspecting raw codes.
2. **Legacy fallback**: a raw `tasks/get` request through the *same* guarded
   client, with the numeric-code table applied by hand (the SDK keeps those
   constants internal).

At most two attempts. What decides whether the second one happens is
**whether we got an HTTP response at all** — not whether the first error was
exactly `MethodNotFound`:

| Outcome of attempt 1 | Next |
|---|---|
| Positive evidence (`result`, or an assigned A2A sentinel) | **pass**, stop |
| Any HTTP response without positive evidence — `-32601`, `-32600`, `-32603`, a non-200 status, a malformed or non-JSON body | try the legacy dialect |
| No HTTP response — network/DNS/TLS failure, timeout, or our SSRF guard refusing the address | **fail**, stop |
| The card declares no JSONRPC interface the SDK can build a transport from (every pre-1.0 card) | try the legacy dialect |

The discriminator is deliberately "did we reach the endpoint", because the
only thing the second attempt varies is the **method name**. If we reached the
server, its complaint may well be about the name, and plenty of real agents
signal an unknown method as `-32600`, `-32603`, or an HTTP 404 rather than the
correct `-32601`. If we never reached it, a different method name against the
same dead or blocked address cannot change anything, and retrying a guard
refusal would only burn the probe budget.

This matters most for **transitional agents**: one that publishes a v1.0-style
card while its server still implements only the 0.x method names would be
rejected under a strict `-32601`-only rule, despite being a working A2A
endpoint. Pre-1.0 agents proper are covered by the last row — their cards have
no `supportedInterfaces`, so the SDK client can't be built and the legacy
dialect is tried directly.

### Pass criteria

HTTP 200 with a well-formed JSON-RPC 2.0 envelope (`jsonrpc == "2.0"`, `id`
echoes the request, exactly one of `result` / `error`) **and** the response
must be evidence the endpoint implements the method:

| Response | Verdict |
|---|---|
| `result` present | **pass** |
| `error.code` in `-32000…-32099` (A2A-specific, e.g. `-32001 TaskNotFound`, `-32004 UnsupportedOperation`) | **pass** |
| `error.code == -32602` InvalidParams — knows the method, rejected the dummy id | **pass** |
| `error.code == -32601` MethodNotFound on **every** dialect tried | **fail** |
| `-32700` / `-32600` / `-32603`, non-200, non-JSON, malformed envelope, network error | **fail** |

**`MethodNotFound` must not pass.** Accepting it was the original bug: it made
*any* JSON-RPC service — one that has never heard of A2A — verify as an A2A
agent, and it simultaneously hid the fact that our own probe was using a
method no v1.0 agent implements.

`detail` records the endpoint, HTTP status, which method finally answered,
the observed error code, and the card's declared protocol version (null for
legacy cards) — so the dialect decision is auditable after the fact.

## Probe safety (SSRF hardening)

All outbound requests in L1/L3 go through `pkg/probe`, which is the only
HTTP client in the codebase allowed to fetch user-supplied URLs. **The A2A
SDK client is constructed with this same `*http.Client` injected**
(`agentcard.NewResolver(c)`, `a2aclient.WithJSONRPCTransport(c)`) — the SDK's
default resolver and nil-client paths fall back to an unguarded internal
client and are never used. Every guarantee below therefore covers the SDK
path as well as the raw legacy path:

- **Timeout 5 s** per request; response body capped at **256 KiB**.
- **Address guard on the dialer**: the policy check runs on the hostname
  before resolution **and again on the post-resolution socket address**, so
  DNS rebinding between check and connect doesn't help.
- Refused address ranges: loopback, link-local (including cloud metadata
  `169.254.0.0/16`), RFC1918 private space, CGN (`100.64.0.0/10`),
  unspecified, and multicast — over both IPv4 and IPv6 (incl. v4-mapped v6).
- **No proxy** is ever configured — a proxy would tunnel past the dialer guard.
- **Redirects**: at most 3, each re-validated by the guard, each must remain
  **https**, and each must stay on the **same origin** as the original
  request (scheme + host + port).

  Same-origin redirects are a security boundary, not politeness. L1's third
  discovery candidate is the submitted URL itself, so without this rule an
  attacker registers `https://target.example/<open-redirect>?url=https://attacker.example/`,
  the fetch follows the hop, and the **attacker's** card gets recorded under
  `target.example`. 307/308 preserve method and body, so L3's POST would land
  on the attacker too — they would answer the protocol probe on a domain they
  do not control, defeating the whole URL-ownership anchor. As defense in
  depth the pipeline also asserts the response's **final URL origin** matches
  the expected origin for both L1 and L3, so a redirect that slipped through
  still fails the check.
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
