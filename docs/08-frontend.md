# 08 — Web Frontend

React 19 + TypeScript SPA built with Vite; TanStack Router (code-based
route tree) and TanStack Query; Tailwind CSS v4 with hand-rolled UI primitives. The API
client is a small hand-written typed `fetch` wrapper — the surface is seven
endpoints, which doesn't justify a codegen step.

## Routes

```
/                  Landing: stats + search + paginated agent grid
/agents/$          Agent detail — splat param, canonical form is the
                   scheme-stripped agent URL (/agents/api.acme.example/agent);
                   agt_ ids and agent_ids also resolve (04 "Agent references")
/add               "Add your agent" form
```

Card links on the landing grid use the canonical URL form, so detail links
are human-readable and survive re-registration (the registry id changes
never, but the URL *is* the identity, per 03).

## Layout

A slim top bar on every page: logo/name on the left, an **“Add your agent”**
primary button on the right. Footer links to the GitHub repo and the
MCP/A2A endpoint docs.

## Landing page (`/`)

Top to bottom:

1. **Stats bar** — four stat tiles from `GET /v1/stats`: *Agents*,
   *Verified*, *New this week*, *Owners*. Rendered from cache-first query
   (60 s stale time matches the server cache).
2. **Search box** — a single large input; placeholder makes the scope
   legible: *"Search by name, skill, owner, URL, or card content…"* (a bare
   hostname like `acme.example` partial-matches agent URLs). Input is
   debounced 300 ms into the `q` param of `GET /v1/agents`; the query string is mirrored into the URL
   (`/?q=image&page=2`) so results are shareable and survive refresh.
   Optional filter chips: verification status.
3. **Agent card grid** — responsive grid (1/2/3 columns by breakpoint) of
   agent cards, then a pager.

### Agent card

```
┌───────────────────────────────┐
│ Image Studio        ✓ Verified│   name + verification badge
│ by Acme Labs                  │   owner_name
│ Generates and edits images…   │   description, 2-line clamp
│ [generate-image] [edit-image] │   skill chips (max 3, “+2 more”)
│ registered Jul 12 2026        │
└───────────────────────────────┘
```

A **Verified** badge means both checks passed. **Partially verified** means
the card passed and the protocol check failed. **Stale** marks expired checks
on a fully verified listing. Clicking a card opens its detail page.

### Pagination

Classic pager (`page`/`page_size=18` params — 18 fills the 3-column grid
in whole rows, where the API's default 20 would leave a ragged one) rather
than infinite scroll —
the URL stays addressable and total count is already returned. Show
`total` ("1,240 agents") next to the pager.

## Agent detail (`/agents/$`)

- Header: name, owner, badge, registered/verified dates, the agent's URL
  (outbound link, `rel="noopener nofollow"`).
- **Identity**: `agent_id` with copy button; `card_url`.
- **Skills & capabilities**: skills render as a collapsible list
  (collapsed by default, summary row shows the count; each row is the
  skill's name, id, and description), followed by capability chips.
- **Verification panel**: the two checks from
  `GET /v1/agents/*ref/verification`, each with status icon, human label
  (L1 Agent card / L2 A2A protocol), expiry, and an
  expandable raw `detail` block. A **Refresh** button calls
  `POST /v1/agents/*ref/refresh`, which re-fetches the card and re-runs all
  checks, then refreshes the panel and card sections (button disabled while
  running; 429 surfaces as "try again in a minute"). If the refresh fetched
  a changed card, a toast notes "agent card updated".
- **Agent card (current)**: collapsed JSON viewer over the latest
  `card_json`.
- **Card history**: timeline from `GET /v1/agents/*ref/cards` — one row per
  snapshot (`fetched_at`, `source` chip: registered/updated/refreshed, short
  hash). Expanding a row shows that version's JSON; two adjacent versions can
  be diffed side-by-side (client-side JSON diff). Collapsed by default when
  there's only one snapshot.

## Add your agent (`/add`)

A two-step, single-page flow driven by the validate/register API pair.

### Step 1 — URL + Validate

One field. Everything else comes from the agent card, and the copy says so:

```
Agent URL    [ https://…                          ]   [ Validate ]
             helper: "we'll fetch your agent card from
             /.well-known/agent-card.json and list what it publishes —
             name, owner, skills, and agent ID all come from the card"
```

Client-side rule: must be a valid https URL. **Validate** calls
`POST /v1/agents/validate` and renders the checks as a live checklist
(optimistic "running…" spinners resolve top-to-bottom):

```
✓ L1 · Agent card      found at https://…/.well-known/agent-card.json
✓ L2 · A2A protocol    endpoint answered a JSON-RPC 2.0 envelope
```

Each row expands to the check's `detail` for debugging. Failure states show
the exact candidates tried / probe error so the submitter can fix their
deployment and re-validate.

- `mode: "update"` in the response renders as an informational callout —
  "this URL is already registered; registering will update the existing
  listing" — linking to the existing detail page. Register stays enabled
  (the button relabels to **Update agent**).
- Editing the URL after a validation invalidates the shown result and
  reverts the page to step 1 (the server reads only the stored validation
  row anyway — the UI just makes that legible).

### Step 2 — Preview & Register

Enabled only when `registerable: true`. Shows a preview card (exactly the
landing-grid card, rendered from the response's `extracted` object — what
the card publishes is what gets listed) above a **Register agent** button,
which posts `{validation_id}` to `POST /v1/agents`. If the extracted preview
looks wrong, the fix is to change the agent card at the source and
re-validate — there is nothing to edit here, by design.

- `201` (created) / `200` (updated) → success screen with a link to the
  detail page.
- `410 validation_expired` → toast "validation expired, please re-validate"
  and back to step 1.

## Implementation notes

```
web/
├── src/
│   ├── routes/            root.tsx, index.tsx, agent-detail.tsx, add.tsx
│   ├── router.tsx         route tree assembled in code
│   ├── api/               client.ts, types.ts, query-client.ts
│   ├── components/
│   │   ├── ui/            button, input, card, badge, skeleton, toaster…
│   │   ├── agent-card.tsx, stats-bar.tsx, search-box.tsx, pager.tsx
│   │   └── verification/  check-list.tsx, badge.tsx
│   └── lib/               format-date.ts, agent-id.ts
└── vite.config.ts         dev proxy /v1 + /a2a + /mcp → localhost:8080
```

- **State**: TanStack Query cache + URL search params. No global store.
- **Env**: single `VITE_API_URL` (empty in production — same-origin via
  ingress; the dev proxy covers local).
- **Theming**: light/dark via Tailwind `dark:` variants; honors
  `prefers-color-scheme`.
- **Accessibility**: badges always pair icon + text (never color alone);
  the validation checklist is an `aria-live="polite"` region.
- **Empty/edge states**: zero search results ("no agents match — register
  yours?" linking to `/add`), stats/query error banners with retry.
