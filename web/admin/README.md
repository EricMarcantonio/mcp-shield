# mcp-shield admin

A React console for the mcp-shield approval API. It answers four questions:

| View | Question |
|---|---|
| **Pending** (`/`) | What is waiting on me? The queue of manifests the gate is holding, ranked so a changed input schema does not sit at the same visual pitch as a reworded sentence. |
| **Manifest** (`/manifests/:id`) | What exactly changed, and do I admit it? The approved baseline left of the seam, what the upstream is advertising right of it, every difference crossing between. Approve and reject live here. |
| **Servers** (`/servers`) | What is approved right now? Per server: the approved manifest hash, and the capabilities that hash admits. A server with no baseline is shown as the fail-closed state it is, not as an error. |
| **Ledger** (`/ledger`) | What happened? Every manifest the gateway has recorded and what was decided about it. |
| **Delivery** (`/delivery`) | What is broken? Gateway health, and notifications the dispatcher gave up on. |

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
stays same-origin and **the gateway needs no CORS configuration**. Point it
somewhere else with `DEV_GATEWAY_URL` (see `.env.example`).

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

## What this gateway build does not serve

The console is written against the full API and degrades where a route is
missing, naming the route it wants rather than showing an empty result that
would read as "nothing happened". Today three things are unavailable:

- **`GET /api/manifests/{id}/approvals`** — who approved or rejected a
  manifest, when, and why. The gateway stores this and its own server-rendered
  dashboard renders it; no JSON route exposes it. The ledger can show what was
  decided but not who decided it.
- **`GET /api/manifests/{id}/contents`** — the manifest's capability content.
  Without it, descriptions and input schemas cannot be shown. The admitted
  capability *names* on the Servers view are reconstructed by replaying the
  stored diffs along a server's approval chain and checking each step; the view
  says so where it does this.
- **`GET /api/manifests`** — a manifest list. Without it there is no way to ask
  which manifest is a server's approved baseline, so `listAllManifests()` in
  `src/api/client.ts` finds the highest manifest id by doubling and bisecting,
  then reads them. It costs a few requests on a local install and disappears
  the moment a list route exists.

Each is a single `optional()` probe. When a route lands, the corresponding
section fills in with no other change.

## Layout

```
src/
  api/
    client.ts      the only module that talks HTTP — and the auth seam
    types.ts       wire shapes, transcribed from the Go source
    derive.ts      ranking a diff; replaying a capability set
    queries.ts     every read, with its caching policy
    auth.tsx       the operator name
  components/
    Seam.tsx       the signature device: baseline | proposed
    primitives.tsx the vocabulary — Hash, Identity, StateChip, HeldMark…
    Shell.tsx      masthead, nav, health, operator
    Failure.tsx    what to show when a read fails
  lib/
    router.tsx     History API, ~90 lines, no routing dependency
    format.ts      short hashes, timestamps, ages
  views/           one file per screen
```

## Design

The concept is a **ledger, not a console** — what this tool produces is a
permanent record of human judgement about what software was allowed to do, and
it should look like a record you would be willing to be audited against. Light,
precise, archival.

- **Palette** lives in `tailwind.config.ts` as named tokens; no component
  spells a hex value. `held` (amber) marks a withheld capability and is
  deliberately not red — withholding is the gateway working as designed, and
  colouring correct behaviour red teaches an operator to read the product's
  whole purpose as a fault. Red (`fault`) is reserved for a gateway that is not
  answering and a notification that never landed. Held state always carries a
  word and a glyph, never colour alone.
- **Type:** IBM Plex, self-hosted through `@fontsource` (nothing is fetched
  from a font CDN). The rule is **mono means machine-truth, sans means we wrote
  it** — a tool name is mono because the upstream asserted it; a heading is
  sans because we authored it.
- **The seam** is the logo's split shield made operational: one continuous rule
  down the manifest view with the approved baseline on its left and the
  proposed state on its right. Approving closes it — the halves meet and the
  rule settles out of `signal` into `slate`. It is the only orchestrated motion
  in the product, and `prefers-reduced-motion` removes it.
