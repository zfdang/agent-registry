# 03 — Data Model (PostgreSQL)

Four tables. GORM entities in `pkg/model/db` are the source of truth;
`AutoMigrate` runs at boot, followed by hand-applied indexes that AutoMigrate
cannot express (the partial/functional ones below).

```
agents ──< verification_checks
       ──< agent_card_snapshots
validations (pre-registration, short-lived)
```

**A2A task state is not persisted** — see the note at the end of this
document.

**Identity anchor: the agent URL.** `url` is the only uniqueness constraint —
whoever controls the endpoint controls the listing (validation proves control
by requiring the live card and protocol probe to pass). Registering a URL
that already exists is an **update**, not a conflict: metadata is re-extracted
from the fresh card and a new snapshot is taken. All display metadata (name,
owner, description, skills, agent_id) is **card-derived** — nothing is typed
in by the submitter, so a listing can never disagree with what its endpoint
publishes. `agent_id` is the card's own claim, not a key — it is not unique,
and duplicates across different URLs are surfaced at resolution time as
candidates.

## `agents`

One row per listed agent.

| Column | Type | Constraints | Notes |
|---|---|---|---|
| `id` | text | PK | `agt_<uuid>` |
| `name` | text | not null | **Extracted from the card's `name`** (L1 requires it non-empty) |
| `owner_name` | text | nullable | **Extracted from the card's `provider.organization`**; null when the card names no provider |
| `agent_id` | text | nullable | **The ID the card declares** (see 05 §L2 for the accepted locations); null when the card declares none. **Not unique** — see identity anchor above |
| `url` | text | not null, **unique** | https base URL where the card was discovered — **the registry's identity anchor** |
| `description` | text | | From the agent card |
| `version` | text | nullable | **The card's own `version`** — the agent's release version, not the protocol's |
| `protocol_version` | text | nullable | **The A2A protocol version declared** by the `supportedInterfaces` entry we selected. Null means the card declares no `supportedInterfaces` at all, which is itself the signal that it uses the pre-1.0 card format |
| `skills` | jsonb | default `[]` | `[{id, name, description}]`, extracted from the card |
| `capabilities` | jsonb | default `{}` | Card `capabilities` object (streaming, push, extensions) |
| `card_url` | text | | Exact URL the winning card was fetched from (latest) |
| `card_hash` | text | | sha256 of the latest card body |
| `card_json` | jsonb | | **Cache of the latest snapshot** in `agent_card_snapshots`, served by `get_agent_card` without re-probing |
| `verification_status` | text | not null | `verified` \| `failed` \| `stale` — denormalized roll-up of the live checks |
| `verified_at` | timestamptz | | Last successful pipeline run |
| `created_at` / `updated_at` | timestamptz | not null | |

| `card_text` | text | generated | Flattened text extracted from `card_json` (all string values: description, skill names/descriptions, provider, tags…), maintained by the service on every card write — feeds search |

Indexes:
- `UNIQUE (url)` — one listing per endpoint (the identity anchor).
- Non-unique index on `agent_id` for resolution lookups.
- Search: a generated `tsvector` column over
  `name || description || owner_name || card_text` plus a GIN index, and
  `gin_trgm_ops` indexes on `name` **and `url`** for substring/typo tolerance
  (URL fragments like `acme.example` don't tokenize well in tsvector; trigram
  covers them). Skills are matched via a jsonb containment index on `skills`.

**Search scope** (one `q`, matched everywhere): name, owner, description,
skills, the **agent URL** (substring, e.g. a bare hostname), and the **cached
agent card content** via `card_text`. Card-driven matches use the snapshot
taken at the last verification — search never probes live endpoints.

**URL normalization**: `url` and `card_url` are stored normalized (lowercase
scheme/host, default port dropped, trailing slash trimmed) so exact-match
resolution by URL is insensitive to those variations.

**Upsert rule:** registering a URL that already exists **updates** the
existing row — all card-derived metadata is re-extracted from the freshly
fetched card, the pipeline re-runs, and a new card snapshot is recorded. The
validate step tells the caller up front that they're updating
(`mode: "update"` + the existing `id`). Since validation requires the live
endpoint to pass L1/L2, only someone who controls the URL can effectively
change its listing — that is v1's ownership model. A **refresh**
(`POST /v1/agents/*ref/refresh`) performs exactly the same sync; register
and refresh differ only in who triggers them and that register can create.

## `agent_card_snapshots`

Append-only history of the agent's card. A row is added whenever a pipeline
run (initial registration, re-registration, or a detail-page **Refresh**)
fetches a card whose `card_hash` differs from the latest stored snapshot —
unchanged cards refresh `agents.verified_at` but add no row.

| Column | Type | Notes |
|---|---|---|
| `id` | text | PK, `snap_<uuid>` |
| `agent_id` | text | FK → `agents.id`, cascade delete |
| `card_url` | text | Where this version was fetched from |
| `card_hash` | text | sha256 of the raw body |
| `card_json` | jsonb | The full card as fetched |
| `source` | text | `register` \| `update` \| `refresh` — what triggered the fetch |
| `fetched_at` | timestamptz | |

`UNIQUE (agent_id, card_hash)` — flapping between two versions still records
each version once; `fetched_at` ordering gives the timeline. Index on
`(agent_id, fetched_at DESC)` for the history view. `agents.card_json` /
`card_hash` / `card_url` always mirror the newest row. Retention: keep the
latest 20 snapshots per agent (older ones swept by the daily ticker).

## `verification_checks`

At most **one live row per (agent, kind)** — each pipeline run upserts.
History is not kept in v1; `card_hash` on the agent covers drift detection.

| Column | Type | Notes |
|---|---|---|
| `id` | text | PK, `chk_<uuid>` |
| `agent_id` | text | FK → `agents.id`, cascade delete; **nullable** — checks created during pre-registration validation have no agent yet and are linked on register |
| `validation_id` | text | FK → `validations.id`, nullable — set for pre-registration runs |
| `kind` | text | `l1_card` \| `l2_protocol` |
| `status` | text | `passed` \| `failed` |
| `detail` | jsonb | Machine-readable evidence: candidate URLs tried, HTTP statuses, matched/declared ids, probe response envelope, error strings |
| `checked_at` | timestamptz | |
| `expires_at` | timestamptz | `checked_at + 30 days`; expired checks render the agent `stale` |

`UNIQUE (agent_id, kind)` partial on `agent_id IS NOT NULL`;
`UNIQUE (validation_id, kind)` partial on `validation_id IS NOT NULL`.

## `validations`

A short-lived pre-registration validation session: the web form's
**Validate** button creates one; **Submit** consumes it. This is what lets the
UI show check results before an agent row exists, without trusting the client
to echo results back.

| Column | Type | Notes |
|---|---|---|
| `id` | text | PK, `val_<uuid>` |
| `url` | text | The submitted URL (the only user input) |
| `name`, `owner_name`, `agent_id` | text | **Extracted server-side from the fetched card** during this validation — register copies these, never client-resent values |
| `result` | text | `passed` \| `failed` |
| `card_url`, `card_hash`, `card_json` | as in `agents` | Carried onto the agent row at register |
| `consumed_at` | timestamptz | Set when registration uses it; a consumed validation cannot be reused |
| `created_at` | timestamptz | |
| `expires_at` | timestamptz | `created_at + 15 min` — register with an expired validation → 410, re-validate |

Rows (and their linked checks) are garbage-collected after 24 h by a boot-time
sweep plus a daily ticker.

## A2A tasks are in-memory, deliberately

The A2A surface (07) runs on the official SDK, whose task store we let default
to `taskstore.NewInMemory`. There is no `a2a_tasks` table.

The alternative was implementing the SDK's `taskstore.Store` over Postgres.
Its four methods look small, but `Update` has to honor optimistic-concurrency
semantics and `List` has to reproduce the SDK's cursor-pagination and
filtering contract exactly. Getting that subtly wrong is a worse failure than
the one it prevents, and the longest task this registry runs is a ~15 second
validation.

The cost, stated plainly: if the process dies mid-task (an outright crash —
graceful shutdown already cancels cleanly), a subsequent `GetTask` answers
`TaskNotFound` rather than reporting a task stuck in `working`. Callers
re-send; registration is idempotent on the agent URL, so a repeat costs
nothing. Persisting task state is a v2 item, and the honest `TaskNotFound` is
a better interim signal than a durable row that lies about a goroutine which
no longer exists.

## Stats

Landing-page stats are **computed, not stored**:

```sql
total_agents      = COUNT(*) FROM agents
verified_agents   = COUNT(*) WHERE verification_status = 'verified'
new_this_week     = COUNT(*) WHERE created_at > now() - interval '7 days'
distinct_owners   = COUNT(DISTINCT owner_name) WHERE owner_name IS NOT NULL
```

Served from a 60-second in-process cache — good enough at any realistic scale
for a directory, and it keeps the schema free of counter-maintenance bugs.

## Verification status roll-up

Recomputed after every pipeline run **and at read time** (expiry can flip it
without any write):

```
l1 failed OR l2 failed            → failed
any live check expired            → stale
l1, l2 both passed                → verified
```
