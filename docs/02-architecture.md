# 02 — System Architecture

## Components

```
┌──────────────────────────────────────────────────────────────┐
│                        k3s cluster                           │
│                                                              │
│   ┌───────────────┐        ┌────────────────────────────┐   │
│   │  registry-web │        │       registry-api          │   │
│   │  (React SPA,  │        │        (Go binary)          │   │
│   │  nginx)       │        │                             │   │
│   └───────┬───────┘        │  /v1/*        REST (web)    │   │
│           │                │  /mcp         MCP server    │   │
│   Traefik ingress ────────►│  /a2a/*       A2A JSON-RPC  │   │
│           │                │  /.well-known agent card    │   │
│           │                └───────┬────────────┬────────┘   │
│           │                        │            │            │
│           │                   PostgreSQL   outbound probes   │
│           │                                (verification)    │
└──────────────────────────────────────────────────────────────┘
```

Two deployables plus a database:

1. **registry-api** — a single Go binary serving three protocol surfaces
   (REST, MCP, A2A) over one shared service layer. The verification pipeline
   runs in-process and makes outbound HTTPS probes to candidate agents.
2. **registry-web** — a static React SPA served by nginx, calling
   `registry-api` REST endpoints.
3. **PostgreSQL** — the only stateful component.

There is deliberately **one backend process**: the three interfaces are thin
protocol adapters over the same `registry.Service`, so they can never drift
apart in behavior (an agent registered via MCP goes through exactly the same
validation as one registered via the web form).

## Backend layout (Go)

```
agent-registry/
├── main.go                    # wiring + graceful shutdown
├── Makefile
├── pkg/
│   ├── config/                # env-only config (REGISTRY_* variables)
│   ├── model/                 # request/response DTOs
│   │   └── db/                # GORM entities (single source of schema truth)
│   ├── client/
│   │   └── pg/                # Postgres access behind a Store interface
│   ├── service/               # business logic: agents.go, pipeline.go, stats.go, tasks.go
│   ├── probe/                 # SSRF-hardened outbound HTTP client (used only by verification)
│   ├── handler/               # gin REST handlers
│   ├── mcpserver/             # MCP tool definitions → service calls
│   ├── a2aserver/             # agent card + JSON-RPC task methods → service calls
│   ├── middleware/            # CORS, IP rate limit, request logging, body limit
│   └── server/                # gin engine construction, route registration
├── web/                       # React SPA (see 08-frontend.md)
├── deploy/                    # Dockerfiles + k3s manifests (see 09-deployment.md)
└── docs/                      # these documents
```

### Key backend choices

| Concern | Choice | Notes |
|---|---|---|
| HTTP framework | `gin-gonic/gin` | REST, plus hosts the MCP/A2A endpoints |
| ORM / migrations | `gorm.io/gorm` + `AutoMigrate` at boot | Schema is small; AutoMigrate keeps v1 simple. Revisit (golang-migrate) if the schema grows destructive changes. |
| DB driver | `gorm.io/driver/postgres` (pgx underneath) | |
| Config | env vars only, parsed in `pkg/config` | `REGISTRY_PORT`, `REGISTRY_DATABASE_URL`, `REGISTRY_PUBLIC_URL`, `REGISTRY_PROBE_HOST_ALLOWLIST`, `REGISTRY_TRUSTED_PROXY_CIDRS`, `REGISTRY_LOG_LEVEL`. No config files — a 12-factor deployment needs none, and secrets stay out of the repo by construction |
| Logging | `go.uber.org/zap` (sugared, JSON) | |
| Validation | gin binding tags (`go-playground/validator`) | |
| MCP | `mark3labs/mcp-go`, Streamable HTTP transport | Stateless mode; no per-session state needed |
| A2A | official SDK `github.com/a2aproject/a2a-go/v2` | Spec v1.0.1. Server handlers come back as plain `http.Handler` (mount on gin with `gin.WrapH`); the client transports accept an injected `*http.Client`, so the verification probe keeps its SSRF guard |
| Rate limiting | in-memory token bucket per client IP | Client IP resolved via gin's trusted-proxy handling (`REGISTRY_TRUSTED_PROXY_CIDRS`), so a spoofed `X-Forwarded-For` from outside the cluster can't dodge the limit. Single-replica assumption for v1; move to Redis only if we scale out |
| Metrics | none in v1 | `/health` (with `?ready=1` for a DB ping) is the only operational endpoint |

### Layering rule

`handler` / `mcpserver` / `a2aserver` are **protocol adapters only** — they
parse/serialize and call `service`. All invariants (validation gating,
dedup, pagination clamps) live in `service`, which talks to storage through
the `pg.Store` interface. `probe` is the only package that makes outbound
HTTP requests.

## Frontend (React SPA)

| Concern | Choice |
|---|---|
| Framework | React 19 + TypeScript, Vite |
| Routing | TanStack Router (code-based route tree) |
| Data | TanStack Query; hand-written typed fetch client in `src/api/` |
| UI | Tailwind CSS v4 + hand-rolled primitives, lucide-react icons |
| Serving | Static build behind `nginx-unprivileged`, `/api/*` proxied to registry-api |

The API surface is small enough (seven endpoints) that a hand-written client
in `src/api/client.ts` beats a generated one: no swagger/codegen step in the
build, and the response types live next to the code that consumes them.

## Request flows

### Web registration
```
Browser ── POST /v1/agents/validate ──► handler ─► service.Validate ─► probe (L1,L2) ─► checks persisted (unlinked)
Browser ── POST /v1/agents (validation_id) ──► service.Register ─► agent row + checks linked
```

### MCP registration
```
Assistant ── MCP tool register_agent ──► mcpserver ─► service.Validate + service.Register (single call)
```

### A2A registration (task-shaped)
```
Runtime ── POST /a2a SendMessage {skill: register-agent} ─► task row (submitted)
                └► async worker: working → validation pipeline → completed | failed
Runtime ── POST /a2a GetTask {id} ─► task row snapshot
```

## Cross-cutting

- **All timestamps UTC**, `timestamptz` in Postgres, RFC 3339 on the wire.
- **IDs**: registry-internal ids are prefixed UUIDs (`agt_…`, `task_…`,
  `chk_…`, `val_…`) — self-describing in logs and URLs.
- **Errors**: sentinel errors in `service`, mapped to HTTP statuses in one
  place per adapter (see 04/06/07 for each surface's mapping).
- **Public repo hygiene**: no secrets, internal hostnames, or private
  infrastructure references anywhere in the repo; deployment secrets arrive
  via Kubernetes Secrets → env vars.
