# mcp-shield admin

A React console for the mcp-shield approval API. It answers four questions:

| View | Question |
|---|---|
| **Pending** (`/`) | What is waiting on me? One expandable card per manifest the gate is holding: what changed, what it leaves alone, and the two buttons that settle it. Approve and reject live here, behind a dialog that requires a reason. |
| **Servers** (`/servers`) | What is approved right now? Per server: the approved manifest hash, how many capabilities it admits, and the last decision recorded. A server with no baseline is shown as the fail-closed state it is, not as an error. |
| **History** (`/history`) | What was decided? One row per decision — and a decision covers a whole manifest, never a single tool. |
| **Notifications** (`/notifications`) | Was anyone actually told? Whether delivery targets exist, and the events the dispatcher gave up on. |

**The approved manifest hash is the approved schema version.** Manifests are
identified throughout by their short hash (`5332e4b6`), never by a sequence
number — the hash is what the gateway logs and what the diff is taken against.

## Develop

Needs a running gateway. From the repository root:

```sh
make build
cp config/servers.example.json config/servers.json          # edit paths
cp config/testserver-tools.example.json config/testserver-tools.json
./bin/mcp-shield                                            # API on :8081
```

Then:

```sh
cd web/admin
npm install
npm run dev        # http://localhost:5173
```

The dev server proxies `/api` and `/healthz` to the gateway, so the browser
stays same-origin and **the gateway needs no CORS configuration** — including
for approve and reject. Point it somewhere else with `DEV_GATEWAY_URL` (see
`.env.example`).

That holds because the proxy forwards `Host` unchanged (`changeOrigin` is off,
see the note in `vite.config.ts`). The gateway refuses a state-changing request
whose `Origin` does not name the host it was addressed to — the check that
stops a form POST on any page on the internet from approving a manifest — so
rewriting `Host` would 403 every approval in dev. Production nginx forwards
`Host` the same way.

To produce something to look at, drive the gateway the way a client would —
`docs/manual-testing.md` in the repository root walks through creating a first
manifest, approving it, then editing the test server's tools to produce a
schema change, a description change, and a new tool.

```sh
npm run build      # tsc -b && vite build -> dist/
npm run typecheck
npm run lint
npm run preview    # serve dist/ locally
```

## Configure

The API base URL is settable at **build time** and overridable at **run time**;
nothing hardcodes `localhost:8081`.

| Setting | Where | Effect |
|---|---|---|
| `window.__MCP_SHIELD__.apiBaseUrl` | `public/config.js`, rewritten at container start from `API_BASE_URL` | Highest precedence. Repoints a built image without rebuilding. |
| `VITE_API_BASE_URL` | build-time env | Baked into the bundle. |
| — | default | Empty: call the same origin the console is served from. |

`OPERATOR_NAME` / `VITE_OPERATOR_NAME` fixes the name decisions are recorded
under and removes the in-browser field.

## Docker

```sh
docker build -t mcp-shield-admin ./web/admin
docker run -p 8082:80 -e API_PROXY_PASS=http://host.docker.internal:8081 mcp-shield-admin
```

| Env var | Purpose |
|---|---|
| `API_PROXY_PASS` | nginx proxies `/api` and `/healthz` to this upstream. Preferred — keeps the browser on one origin, so the gateway needs no CORS headers. |
| `API_BASE_URL` | Absolute URL of the approval API, when the browser must reach it cross-origin. Requires CORS on the gateway. |
| `OPERATOR_NAME` | Fixes the recorded operator name. |

### Compose

This service block belongs in the repository-root `docker-compose.yml`
alongside `gateway`. It is not added here — that file is owned elsewhere.

```yaml
  admin:
    build: ./web/admin
    ports:
      - "8082:80"
    environment:
      # Same-origin: nginx proxies /api and /healthz to the gateway, so the
      # browser never makes a cross-origin request and the gateway needs no
      # CORS configuration. Prefer this over API_BASE_URL.
      API_PROXY_PASS: http://gateway:8081
      # Optional: fixes the name decisions are recorded under and removes the
      # in-browser field.
      # OPERATOR_NAME: ops
    depends_on:
      gateway:
        condition: service_healthy
```

Then `http://localhost:8082`.

## Authentication

There is none, deliberately. The approval API is unauthenticated and meant to
be bound to localhost or fronted by an identity provider; access control
belongs there, not here.

What the API *does* require is a `username` on approve and reject — it refuses
to record a decision it cannot attribute, and refuses to invent a name. So this
console collects one and is careful to present it as what it is: **recorded,
not verified**. Nothing in the interface implies the gateway checked who acted.

**The seam for authentication is one function.** Every request in the
application goes through `request()` in `src/api/client.ts`, which asks
`authHeaderProvider` for headers. Attaching a bearer token is:

```ts
setAuthHeaderProvider(() => ({ Authorization: `Bearer ${token}` }));
```

A 401 handler goes in the same function. `src/api/auth.tsx` is the thin context
that supplies the operator name today and would supply it from token claims
later. No component imports `fetch`.

## Where the API cannot answer

The console is written against the full API and degrades where a route is
missing, naming the gap rather than filling it. **An admin console for a
security gateway that displays a plausible-looking fabrication is worse than
one that says it does not know.** Four gaps exist today:

- **`GET /api/manifests/{id}/contents`** — the manifest's capability content.
  The gateway stores every manifest's canonical JSON but serves it on no route,
  so a capability's description and input schema cannot be shown. The
  capability *names* are real: they come from the stored diffs, and an approved
  server's admitted set is reconstructed by replaying the diffs along its
  approval chain and checking every step. Where a description would go, the
  expanded capability row says the route does not exist.
- **`FAIL_MODE`** — no route reports whether the gate is blocking or merely
  observing. The sidebar reads **Not reported**. Rendering `block` because it
  is the default would invent the most consequential fact on the screen: an
  operator running `FAIL_MODE=warn` would see a console telling them they are
  protected. A read-only `fail_mode` field on `/healthz` would light it up.
- **The notification targets** — no route describes them, so their configured
  names and formats are not shown. Their URLs would not be shown even if a
  route existed: a webhook URL is a capability-bearing credential, and the
  gateway already redacts even the host out of its own connection errors.
- **Which target a failed delivery was for** — `GET /api/notifications/failed`
  reports the event, not the target, so the table's first column is the server
  the event was about. The target's configured name appears inside the error
  text the gateway wrote; the console does not parse it back out.

Nothing here is a risk score. Risk classification was removed from this
product deliberately (D8) — a substring match over tool names attaches an
authoritative label to a judgement the tool cannot make. The headline on a
pending card describes the diff and stops there.

## Layout

```
src/
  ds/
    organic.css    the design system — vendored, never edited here
  api/
    client.ts      the only module that talks HTTP — and the auth seam
    types.ts       wire shapes, transcribed from the Go source
    derive.ts      ranking and describing a diff; replaying a capability set
    queries.ts     every read, with its caching policy
    auth.tsx       the operator name
  components/
    primitives.tsx the vocabulary — Tag, StatusTag, EmptyState, Unavailable…
    Shell.tsx      sidebar, nav, recorded-as, gate mode
    DecisionDialog.tsx  approve and reject, behind a required reason
    Failure.tsx    what to show when a read fails
  lib/
    router.tsx     History API, ~90 lines, no routing dependency
    format.ts      short hashes, timestamps, ages
  views/           one file per section
```

## Design

The visual identity is the **Organic** design system, imported from the Claude
Design project that owns it and vendored verbatim at `src/ds/organic.css`.
`web/admin/design/Admin Dashboard.dc.html` is the source of record for the
layout; the React implementation is checked against it rather than against a
description of itself.

- **`src/ds/organic.css` is vendored.** Retune it in the design project and
  re-import; do not edit it here, or the two will drift. The one deliberate
  local change is documented in its header: the Google Fonts `@import` is
  removed.
- **Tailwind does layout only.** `corePlugins.preflight` is off and
  `tailwind.config.ts` carries no palette — colour, type, spacing, radius and
  the reset all come from the design system's variables and classes. A second
  copy of the tokens in Tailwind would be a second source of truth. Spacing
  utilities address the tokens directly (`gap-[var(--space-4)]`).
- **Type:** Caprasimo for headings, Figtree for body, self-hosted through
  `@fontsource` so nothing is fetched from a font CDN — this console is served
  beside a security gateway and should not phone home to render. Monospace is
  the platform's own, and means machine-truth: a tool name, a manifest hash, an
  event type.
- **Colour is never the only carrier.** Every status tag spells out its state;
  the hue is a second reading of something the word already said.
- **Nothing animates.** There is no motion to remove for
  `prefers-reduced-motion`, and the media query stays as a guard.
