# Agent Registry

A public directory of [A2A](https://a2a-protocol.org) agents. Anyone can list
an agent by submitting its URL; the registry validates it against the live
endpoint before listing it, so every entry is an agent that actually exists
and answers the protocol.

**Live at [registry.ip402.xyz](https://registry.ip402.xyz)**

## What makes it different

**You submit a URL. That's the whole form.** Name, owner, description,
skills, and agent ID are all extracted from the agent card the endpoint
publishes — never typed in. A listing can't disagree with what the agent
actually says about itself.

**The URL is the identity.** It's the registry's only uniqueness constraint.
Re-submitting a registered URL updates that listing instead of conflicting,
and since updating requires passing validation again, only whoever operates
the endpoint can change its entry. That's the ownership model: no accounts,
no API keys.

**Every listing is verified, and you can see how.** Each agent carries a
per-check breakdown, and every card fetch that returns changed content is
kept as an immutable snapshot, so an agent's card history is browsable.

## Three interfaces, one service layer

| Consumer | Interface | Use |
|---|---|---|
| Humans | Web UI | Browse, search, register |
| AI assistants | **MCP** at `/mcp` | `search_agents`, `get_agent_card`, `get_verification`, `compare_agents`, `register_agent` |
| Agent runtimes | **A2A** at `/a2a` | The registry is itself an A2A agent — card at `/.well-known/agent-card.json`, skills `search-agents` / `get-agent` / `register-agent` |

MCP models a synchronous tool call, which suits lookups. A2A models a task
lifecycle, which suits registration — validation runs as an observable,
cancelable task. All three surfaces are thin adapters over the same service
layer, so an agent registered over MCP goes through exactly the same
validation as one registered through the web form.

Search matches one query against name, owner, description, skills, the agent
URL as a substring (`?q=example.com` finds every agent on that host), and the
cached card content.

## Verification: L1 / L2 / L3

| Check | Gating | Passes when |
|---|---|---|
| **L1** Agent card | yes | A valid card is discoverable at a well-known path; all listing metadata is extracted here |
| **L2** Agent ID | no — warning only | The card declares an agent ID |
| **L3** A2A protocol | yes | The endpoint answers a benign task read with positive evidence of speaking A2A |

L2 is advisory on purpose: plenty of good agents publish no registry
identifier, and blocking on it would empty the directory while hiding it
would overstate what was verified.

L3 requires *positive* evidence — a result, or an assigned A2A error code.
A `MethodNotFound` does not pass; accepting it would let any JSON-RPC service
verify as an A2A agent.

**Both agent generations work.** A2A renamed its methods twice. The probe
tries the v1.0 method first through the official SDK, and if the endpoint
answers without positive evidence it retries with the pre-1.0 name — so a
current agent and one built against the 0.x spec both verify. An endpoint we
never reached at all is not retried.

Outbound probes are SSRF-hardened: an address guard that runs both before and
after DNS resolution, https only, same-origin redirects, bounded redirect
count, response body cap, and no proxy. See
[docs/05-verification.md](docs/05-verification.md).

## Stack

Go 1.26 · gin · GORM · PostgreSQL · [a2a-go](https://github.com/a2aproject/a2a-go)
v2 (A2A spec v1.0.1) · [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
(protocol revision 2026-07-28)

React 19 · TypeScript · Vite · TanStack Router/Query · Tailwind CSS v4

Deployed to k3s behind Traefik with cert-manager TLS.

## Running it

Requires Go 1.26+, Node 22+, pnpm, and a PostgreSQL 17 database.

```sh
# database
docker run -d --name registry-pg -p 5432:5432 \
  -e POSTGRES_USER=registry -e POSTGRES_PASSWORD=devpass -e POSTGRES_DB=registry \
  postgres:17-alpine

# api on :8080 (schema is created at boot)
REGISTRY_DATABASE_URL='postgres://registry:devpass@localhost:5432/registry?sslmode=disable' \
  make run

# web on :5173, proxying /v1, /mcp, /a2a to the api
cd web && pnpm install && pnpm dev
```

`make build` · `make test` · `make vet` · `make fmt`

### Configuration

Environment variables only — no config files.

| Variable | Default | Notes |
|---|---|---|
| `REGISTRY_DATABASE_URL` | — | **Required.** PostgreSQL DSN |
| `REGISTRY_PORT` | `8080` | |
| `REGISTRY_PUBLIC_URL` | `https://registry.ip402.xyz` | Advertised in the A2A agent card |
| `REGISTRY_LOG_LEVEL` | `info` | |
| `REGISTRY_TRUSTED_PROXY_CIDRS` | RFC1918 ranges | `X-Forwarded-For` is only trusted from these peers |
| `REGISTRY_PROBE_HOST_ALLOWLIST` | empty | Hostnames allowed to resolve into private space. Leave empty in production |

## Deploying

```sh
./deploy/build-and-deploy.sh [api|web|all]
```

Builds the images, imports them into each k3s node's containerd over ssh,
applies the manifests, and waits on the rollout. The node list is
operator-specific and never committed — supply it via `REGISTRY_K3S_NODES` or
a gitignored `deploy/nodes.local`. Database credentials are generated at first
deploy straight into a Kubernetes Secret.

## Documentation

Design documents live in [docs/](docs/) — architecture, data model, the REST /
MCP / A2A interface specs, the verification pipeline, frontend, and
deployment. They are kept current with the implementation; where the code
deviates from an earlier design decision, the docs record why.

## License

[MIT](LICENSE)
