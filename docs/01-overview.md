# 01 — Product Overview

## What it is

Agent Registry is a public, searchable directory of AI agents that speak the
[A2A protocol](https://a2a-protocol.org). Anyone can register an agent by
submitting its metadata and URL; the registry **validates the agent against
its live endpoint** before listing it, so every entry in the directory is an
agent that actually exists and answers the protocol.

The registry is consumed by three kinds of clients:

| Client | Interface | Typical action |
|---|---|---|
| Humans | Web UI (React SPA) | Browse, search, register an agent |
| AI assistants | MCP (Streamable HTTP) | `search_agents`, `get_agent_card`, `compare_agents`, `register_agent` |
| Agent runtimes | A2A (JSON-RPC) | Discover the registry's own agent card, send discovery/registration tasks |

## Why both MCP and A2A?

MCP models a synchronous `request → response` tool call — perfect for an AI
assistant asking "find me an image-generation agent". But agent-to-agent work
is task-shaped: it can take minutes, involve multiple steps, payments,
approvals, and retries. A2A models exactly that lifecycle:

```
task created → working → (input-required / waiting) → completed | failed | canceled
```

So the registry exposes **MCP for lookup-style queries** and **A2A for
task-style interactions** (e.g. a registration that runs the multi-step
validation pipeline as an observable, cancelable task). The registry is itself
an A2A agent: it publishes its own agent card at `/.well-known/agent-card.json`.

```
                        User / AI assistant
                               │
                              MCP
                               │
                     ┌─────────────────┐
        Web UI ──────┤  Agent Registry  ├────── PostgreSQL
                     └─────────────────┘
                               │
                              A2A
                               │
                      Registered agents
                   (validated at register time,
                    re-verifiable at any time)
```

## Core features

### Landing page
- **Stats bar** at the top: total agents, verified agents, registrations this
  week, distinct owners.
- **Search box**: full-text search over name, description, owner, and skills.
- **Agent card grid** with pagination; each card shows name, owner,
  description, skills, and a verification badge.

### Register an agent ("Add your agent")
A single input — the **agent URL** — and a **Validate** button. Everything
else (name, owner, description, skills, agent ID) is **extracted from the
live agent card**, never typed in: the card is the source of truth and the
registry only lists what the endpoint actually publishes. Validation runs
three checks against the URL:

| Level | Check | Gating |
|---|---|---|
| **L1 — Agent card** | An agent card is discoverable at a well-known path and statically valid; metadata is extracted from it | yes — must pass |
| **L2 — Agent ID** | The card declares an agent ID | no — a missing ID is a **warning** |
| **L3 — A2A protocol** | The card's endpoint answers a benign JSON-RPC probe with a well-formed envelope | yes — must pass |

Only after L1 and L3 pass can the agent be submitted to the registry. The full
pipeline, including SSRF hardening of the outbound probes, is specified in
[05-verification.md](05-verification.md).

**The agent URL is the identity anchor.** It is the registry's only
uniqueness constraint; submitting an already-registered URL *updates* that
listing (after passing validation again) instead of conflicting. Every
registration or refresh that fetches a changed agent card appends an
immutable **card snapshot**, so each agent carries a viewable card history;
the detail page's **Refresh** button re-fetches the card and re-runs the
checks on demand.

### Programmatic access
- **MCP server** mounted at `/mcp` — see [06-mcp-interface.md](06-mcp-interface.md).
- **A2A endpoints** — the registry's own card plus `tasks/send`,
  `tasks/status` (`tasks/get`), `tasks/cancel` — see
  [07-a2a-interface.md](07-a2a-interface.md).

## Non-goals (v1)

- **No user accounts / authentication.** Registration is open and validated
  purely against the live endpoint; abuse is contained by IP rate limits and
  by the fact that you can only register an agent whose URL you actually
  control (L1/L3 must pass). Control of the URL is also v1's de facto
  ownership model: since the URL is the unique key and updating a listing
  means re-passing validation against that URL, only whoever operates the
  endpoint can change its entry. Account-based ownership is a v2 concern
  (see below).
- **No reputation / reviews.** The schema leaves room for it, but v1 records
  only verification facts.
- **No task brokering.** The registry helps a buyer *find* an agent; the
  buyer then talks to that agent directly over A2A. The registry never
  proxies third-party tasks.

## v2 candidates

- Ownership proof (challenge published under the agent's `.well-known/` or a
  signed claim) unlocking edit/delete of one's own entries.
- Periodic re-verification with staleness TTLs and de-listing of dead agents.
- Reputation ingestion and display.
- Categories/tags curation and featured listings.
