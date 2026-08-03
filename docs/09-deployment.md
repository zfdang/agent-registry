# 09 — Build & Deployment (k3s)

Two images, plain Kubernetes manifests managed with **kustomize** (base +
per-env overlays), targeting a k3s cluster with the built-in **Traefik**
ingress. Everything needed to deploy lives in this repo under `deploy/`;
secrets are the only thing that doesn't.

```
deploy/
├── docker/
│   ├── api.Dockerfile
│   └── web.Dockerfile
└── k8s/
    ├── base/
    │   ├── kustomization.yaml
    │   ├── api-deployment.yaml, api-service.yaml
    │   ├── web-deployment.yaml, web-service.yaml
    │   ├── postgres-statefulset.yaml, postgres-service.yaml
    │   ├── ingress.yaml
    │   └── configmap.yaml
    └── overlays/
        ├── dev/           # 1 replica, small resources, latest tags
        └── prod/          # pinned tags, resource limits, PDB
```

## Images

### `registry-api` (`api.Dockerfile`)

Multi-stage: `golang:1.25-bookworm` builder (BuildKit cache mounts for
`GOMODCACHE`/`GOCACHE`) → `gcr.io/distroless/base-debian12:nonroot` runtime.
Only the static binary and `pkg/config/yaml/` are copied in. Runs as nonroot;
listens on `:8080` (public) and `:9091` (metrics, never exposed via ingress).

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

- Non-secret config: `configmap.yaml` → env vars (`REGISTRY_SERVER_PUBLIC_URL`,
  `REGISTRY_LOG_LEVEL`, rate-limit knobs, probe allowlist — empty in prod).
- Secrets: a `registry-secrets` Secret providing `DATABASE_URL` (and the
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
- **Metrics**: Prometheus scrape annotation on the api pod for `:9091`.
- **Logs**: zap JSON to stdout; whatever log stack the cluster runs picks
  them up.

## CI (GitHub Actions)

```
pr.yml       on PR:   go vet + go test -race  |  pnpm typecheck + lint + build
release.yml  on tag:  buildx both images (amd64+arm64) → push to GHCR
                      (ghcr.io/<org>/registry-api, /registry-web)
```

Deploy step for v1 is intentionally manual and cluster-local:

```sh
kubectl apply -k deploy/k8s/overlays/prod
kubectl set image deploy/registry-api api=ghcr.io/<org>/registry-api:v1.2.3
```

GitOps (Flux/Argo watching this repo's overlays) is a drop-in upgrade later
since the manifests are already kustomize-shaped.

## Local development

```sh
docker compose up -d postgres        # compose file with just postgres:17
make run                             # api on :8080, swagger on /swagger in dev
cd web && pnpm dev                   # vite on :5173, proxying /v1,/mcp,/a2a to :8080
```

A `make e2e` target boots the API against a throwaway Postgres
(testcontainers), registers a stub A2A agent served by the test itself, and
asserts the full validate→register→search path — the same stub agent doubles
as the fixture for verification-pipeline unit tests.
