# 09 — Build & Deployment (k3s)

Two images and a flat set of Kubernetes manifests, targeting a k3s cluster
with the built-in **Traefik** ingress. Everything needed to deploy lives in
this repo under `deploy/`; secrets and the operator's node list are the only
things that don't.

```
deploy/
├── build-and-deploy.sh    # build → import to nodes → apply → rollout
├── docker/
│   ├── api.Dockerfile
│   └── web.Dockerfile
└── k8s/
    ├── namespace.yaml
    ├── postgres.yaml      # Service + StatefulSet
    ├── api.yaml           # Service + Deployment
    ├── web.yaml           # Service + Deployment
    ├── ingress.yaml       # Middleware + Certificate + Ingress
    └── secret.example.yaml
```

## Images

### `registry-api` (`api.Dockerfile`)

Multi-stage: `golang:1.26-bookworm` builder → `gcr.io/distroless/static-debian12:nonroot`
runtime. Only the static binary is copied in (config is env-only). Runs as nonroot;
listens on `:8080`.

### `registry-web` (`web.Dockerfile`)

`node:22-alpine` + pnpm builds the SPA → `nginxinc/nginx-unprivileged:alpine`
serves `dist/` on `:3000`. `VITE_API_URL` stays empty: the SPA calls
same-origin paths and the ingress does the routing, so **one image serves
every environment** — no per-env rebuilds.

nginx config: SPA fallback (`try_files … /index.html`), long-cache for hashed
assets, `no-store` for `index.html`, gzip on.

## Routing (Traefik ingress)

One host, path-routed so the SPA, API, MCP, and A2A share an origin (no CORS
in production):

| Path | Service |
|---|---|
| `/v1`, `/mcp`, `/a2a`, `/.well-known`, `/health` | `registry-api:8080` |
| everything else | `registry-web:3000` |

TLS via cert-manager (Let's Encrypt HTTP-01 through Traefik). The API also
sets `server.public_url` from config so the A2A agent card advertises the
right public endpoint.

## PostgreSQL

A single-replica StatefulSet (`postgres:17-alpine`) with a PVC on k3s's
`local-path` storage class is the default, sized for a directory workload
(the whole dataset is tiny). The API takes a plain `DATABASE_URL`, so
pointing prod at a managed/external Postgres is a Secret change, not a
manifest change. Nightly `pg_dump` CronJob to a PVC (or S3-compatible
target configured via Secret) for backups.

## Configuration & secrets

- Non-secret config: plain env vars on the Deployment (`REGISTRY_PUBLIC_URL`,
  `REGISTRY_LOG_LEVEL`, `REGISTRY_TRUSTED_PROXY_CIDRS`,
  `REGISTRY_PROBE_HOST_ALLOWLIST` — empty in prod).
- Secrets: a `registry-secrets` Secret providing `REGISTRY_DATABASE_URL` (and the
  Postgres password for the StatefulSet). Created out-of-band
  (`kubectl create secret` / SealedSecrets if the cluster has it) — **never
  committed**; `deploy/` contains only a `secret.example.yaml`.

## Health & operations

- **Probes**: API liveness `GET /health` (process up), readiness
  `GET /health?ready=1` (DB ping). Web liveness = nginx `GET /`.
- **Migrations**: GORM AutoMigrate runs at API boot; with >1 replica the
  first pod wins and the operation is idempotent. Rollout strategy
  `RollingUpdate maxUnavailable=0`.
- **Resources** (starting points): api `100m/128Mi → 500m/256Mi`,
  web `50m/32Mi → 200m/64Mi`, postgres `250m/256Mi → 1/1Gi`.
- **Logs**: zap JSON to stdout; whatever log stack the cluster runs picks
  them up.

## CI (GitHub Actions)

No CI is wired up yet. The gates to add first are the ones already run by
hand on every change: `go vet ./...` + `go test ./... -race`, and
`pnpm typecheck` + `pnpm build`.

Deployment for v1 is manual and cluster-local — there is no image registry
in the loop. `deploy/build-and-deploy.sh [api|web|all]` builds arm64 images,
`docker save`s them into every node's containerd over ssh, applies the
manifests for whatever is being deployed, and waits on the rollout:

```sh
./deploy/build-and-deploy.sh all
```

The node list is operator-specific and never committed: supply it via
`REGISTRY_K3S_NODES="node1 node2 …"` or a gitignored `deploy/nodes.local`.
Images are tagged `localhost/agent-registry/{api,web}:<utc-timestamp>` with
`imagePullPolicy: IfNotPresent`. Moving to a real registry + GitOps
(Flux/Argo) is a drop-in later change.

## Local development

```sh
docker compose up -d postgres        # compose file with just postgres:17
make run                             # api on :8080
cd web && pnpm dev                   # vite on :5173, proxying /v1,/mcp,/a2a to :8080
```

The verification pipeline's tests stand up a stub A2A agent with
`httptest.NewTLSServer` and drive L1/L2 against it, so the probe logic is
covered without a database or network access.
