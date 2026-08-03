# Agent Registry — Design Documents

Agent Registry is a public directory for AI agents. Humans browse and register
agents through a web UI; agents and AI assistants query and register through
MCP; agent runtimes interact through the A2A protocol.

| Doc | Contents |
|---|---|
| [01-overview.md](01-overview.md) | Product goals, personas, feature summary |
| [02-architecture.md](02-architecture.md) | System architecture and tech stack |
| [03-data-model.md](03-data-model.md) | PostgreSQL schema |
| [04-rest-api.md](04-rest-api.md) | REST API consumed by the web frontend |
| [05-verification.md](05-verification.md) | The L1/L2 validation pipeline |
| [06-mcp-interface.md](06-mcp-interface.md) | MCP tools for AI assistants |
| [07-a2a-interface.md](07-a2a-interface.md) | A2A endpoints for agent runtimes |
| [08-frontend.md](08-frontend.md) | Web UI pages and components |
| [09-deployment.md](09-deployment.md) | Docker images and k3s deployment |

## Reading order

Start with the overview and architecture, then the data model. The three
interface docs (REST, MCP, A2A) all sit on top of the same service layer and
share the verification pipeline described in `05-verification.md`.
