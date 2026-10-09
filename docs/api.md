# mcp-shield HTTP API

The gateway serves two listeners. This document covers the second one.

| Listener | Default | What it is |
|---|---|---|
| Proxy | `:8080` | The MCP proxy itself — `POST /mcp/{server}` |
| API | `:8081` | This API, plus the server-rendered dashboard at `/` |

Every example below was run against a real gateway (`docker compose up`)
and the responses are pasted verbatim.

---

## Conventions

**Timestamps** are RFC 3339 with a `Z` offset, always UTC:
`"2026-08-01T20:41:32.510186212Z"`.

**Manifests are addressed by `id`, described by `hash`.** A manifest's real
identity is its hash — that is the premise of the whole project — but the
hash is not the public address, deliberately:

- A hash is unique only *within one server*. The database's unique index is
  `(server_id, hash)`, and two servers advertising identical capabilities
  legitimately produce the same hash. "The manifest with hash `b04c58…`" is
  not a question with one answer.
- The `id` addresses one row: one snapshot of one server, with one state and
  one decision history. It is what `approve`, `reject`, `/diff` and the
  webhook payloads have referred to since v0.1.0.

So `id` addresses, `hash` describes. The hash is on every manifest response,
and a client holding only a hash can find its row with
`GET /api/manifests?server=<name>&hash=<hash>`. Neither field is hidden and
neither is asked to do the other's job.

**Errors** are always this shape, on every non-2xx:

```json
{"error": "no manifest with id 9999", "code": "not_found"}
```

`error` is prose for a human and may be reworded at any time — never parse
it. `code` is stable and is what a client branches on.

| `code` | HTTP | Meaning |
|---|---|---|
| `invalid_request` | 400 | A path or query parameter was missing or unparseable. |
| `invalid_json` | 400 | The request body is not the JSON this endpoint expects. Nothing was recorded. |
| `username_required` | 400 | A decision arrived with no `username`. See [attestation](#the-username-field-what-it-means-and-does-not) below. |
| `not_found` | 404 | The addressed manifest or server does not exist. |
| `conflict` | 409 | The manifest is no longer `PENDING`; the decision has already been made. Re-read it before retrying. |
| `not_configured` | 404 | The feature behind this route is switched off in this deployment (today: notifications). |
| `origin_not_allowed` | 403 | The browser's `Origin` is not in `CORS_ALLOWED_ORIGINS`. Retrying will not help. |
| `internal` | 500 | The gateway failed for a reason that is not the caller's fault. Safe to retry. |

**Manifest states** are `PENDING`, `APPROVED`, `REJECTED`, `SUPERSEDED`. A
server has at most one `APPROVED` manifest at a time; approving a new one
moves the old one to `SUPERSEDED` in the same transaction.

---

## Pagination

Every list endpoint added in 0.2.0 is bounded and returns the same envelope:

```json
{
  "items": [ ... ],
  "pagination": {"limit": 50, "offset": 0, "count": 2, "has_more": false}
}
```

- `limit` — default **50**, maximum **200**. An oversized `limit` is clamped
  rather than refused, and `pagination.limit` reports what you actually got.
- `offset` — rows skipped; default 0.
- `count` — items in *this* response.
- `has_more` — whether a further page exists.

`items` is always an array, never `null`, so a client can iterate without
special-casing "nothing yet".

**There is no total count.** Counting every matching row means a second full
scan that is stale the moment it returns, and `has_more` answers the only
question a "next page" button actually asks.

**Ordering is newest first**, by `(created_at DESC, id DESC)`. The `id`
tiebreak matters: rows written in the same instant would otherwise tie, and
an unstable order is how a row shows up on two consecutive pages, or on
neither.

Nonsense parameters are refused rather than ignored, because a client whose
paging parameter was silently dropped sees a plausible first page and never
learns it is missing rows:

```console
$ curl -s 'localhost:8081/api/manifests?limit=lots'
{"error":"limit must be a whole number, got \"lots\"","code":"invalid_request"}

$ curl -s 'localhost:8081/api/manifests?limit=-1'
{"error":"limit must be at least 1, got -1","code":"invalid_request"}

$ curl -s 'localhost:8081/api/manifests?offset=-1'
{"error":"offset must be at least 0, got -1","code":"invalid_request"}
```

Paging through the manifest list two at a time:

```console
$ curl -s 'localhost:8081/api/manifests?limit=1' | jq -c '[.items[].id], .pagination'
[2]
{"limit":1,"offset":0,"count":1,"has_more":true}

$ curl -s 'localhost:8081/api/manifests?limit=1&offset=1' | jq -c '[.items[].id], .pagination'
[1]
{"limit":1,"offset":1,"count":1,"has_more":false}

$ curl -s 'localhost:8081/api/manifests?limit=1&offset=99' | jq -c '[.items[].id], .pagination'
[]
{"limit":1,"offset":99,"count":0,"has_more":false}

$ curl -s 'localhost:8081/api/manifests?limit=100000' | jq -c '.pagination'
{"limit":200,"offset":0,"count":2,"has_more":false}
```

### The two endpoints that are not paginated

`GET /api/servers` and `GET /api/manifests/pending` shipped in v0.1.0 as
bare JSON arrays and are unchanged: no envelope, no `limit`, no bound. Adding
either would have broken every client written against v0.1.x, including this
repository's own CLI.

Use `GET /api/manifests?state=PENDING` instead of `/api/manifests/pending`
in new code. It returns the same manifests, paginated, with `state`
included.

---

## The `username` field: what it means, and does not

`approve` and `reject` require a `username` in the request body and **refuse
the request if it is absent or blank**. That refusal is the feature.

What it is: an attestation. The caller states who made this decision, and
the gateway records that statement verbatim and immutably in the approvals
table.

What it is **not**: an authenticated identity. This API has no sessions, no
tokens, no users, and no login. It does not verify that the caller is who
they say they are, and it never will — the gateway is meant to be bound to
localhost, or fronted by something that does authenticate.

Why refuse rather than default: an absent username used to be stored as
`"unknown"`, which is indistinguishable in the audit trail from a real user
of that name, and asserts something nobody supplied. An unverified claim on
the record is still a claim *someone made*; a default is a claim the gateway
invented. Only one of those is an audit trail.

**This is the integration seam.** An application that does authenticate its
users puts itself in front of the gateway, authenticates the human, and
passes that human's identity down as `username`. The gateway then records a
decision attributable to a real person, and the trust boundary is exactly
where it belongs — in the thing that actually knows who is logged in.

If you expose this API to anything wider than localhost without such a
front, anyone who can reach it can approve capability changes under any name
they like.

---

## CORS

There is no cross-origin access until you configure it.

```sh
CORS_ALLOWED_ORIGINS=http://localhost:5173,https://admin.example.com
```

Comma-separated, exact-match, **default empty**. Each entry must be a bare
origin — `scheme://host[:port]`, no path, no trailing slash. Malformed
entries and `"*"` are refused at **startup**, so a misconfigured gateway
fails to boot rather than coming up quietly permissive:

```console
$ CORS_ALLOWED_ORIGINS='*' docker compose up
gateway-1  | fatal: app: cors: "*" is not an allowed origin: this API is
unauthenticated, so a wildcard would let any page on the internet approve
capability changes on this gateway
```

### Why there is no wildcard, and never will be

`approve` and `reject` are unauthenticated by design (see above). That is
safe only while a request has to come from software the operator ran
deliberately.

`Access-Control-Allow-Origin: *` breaks exactly that. Any page a user opened
in another tab could then drive their browser into approving a capability
change on their own local gateway: the browser can reach localhost, the
gateway asks for no credentials, and the audit trail records whatever
username the attacking page chose to send. Reflecting an arbitrary `Origin`
back is the same hole with extra steps, since the attacker controls that
header.

**Enabling CORS here is a deployment decision with consequences, not a
convenience toggle.** You are naming the exact origins you trust to act on
your behalf, without authentication, against your gateway.

`Access-Control-Allow-Credentials` is never sent. There is no cookie or
session for a browser to attach, and allowing them would make an allowlisted
origin able to act as a signed-in operator the moment anything is put in
front of the gateway.

### Behaviour

With `CORS_ALLOWED_ORIGINS=http://localhost:5173`:

```console
$ curl -i -X OPTIONS localhost:8081/api/manifests/1/approve \
    -H 'Origin: http://localhost:5173' \
    -H 'Access-Control-Request-Method: POST' \
    -H 'Access-Control-Request-Headers: Content-Type'
HTTP/1.1 204 No Content
Access-Control-Allow-Headers: Content-Type
Access-Control-Allow-Methods: GET, POST, OPTIONS
Access-Control-Allow-Origin: http://localhost:5173
Access-Control-Max-Age: 600
Vary: Origin
Vary: Access-Control-Request-Method
```

A preflight from anywhere else is refused outright, rather than left to 405
from the router, so a developer wiring up a UI can see which decision was
made and where to change it:

```console
$ curl -i -X OPTIONS localhost:8081/api/manifests/1/approve \
    -H 'Origin: https://evil.example' -H 'Access-Control-Request-Method: POST'
HTTP/1.1 403 Forbidden
Vary: Origin

{"error":"origin \"https://evil.example\" is not in CORS_ALLOWED_ORIGINS","code":"origin_not_allowed"}
```

A *simple* cross-origin GET from an unlisted origin is still served — CORS
is enforced by the browser, not the server — but carries no
`Access-Control-Allow-Origin`, so the browser withholds the body:

```console
$ curl -sD - -o /dev/null localhost:8081/api/servers -H 'Origin: https://evil.example'
HTTP/1.1 200 OK
Vary: Origin
```

A request with **no** `Origin` header is not a browser request and is
unaffected entirely. `curl`, the `mcp-shield` CLI and any server-side client
behave exactly as they did before CORS existed.

### The part CORS does not cover

A form POST (`Content-Type: application/x-www-form-urlencoded`) is a
*simple* request: the browser never preflights it. The dashboard's
approve/reject forms were therefore submittable from any page on the
internet, with CORS none the wiser. So a state-changing request whose
`Origin` is neither same-origin nor allowlisted is refused outright:

```console
$ curl -s -o /dev/null -w '%{http_code}\n' -X POST localhost:8081/manifests/1/approve \
    -H 'Origin: https://evil.example' \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    -d 'username=drive-by&reason=x'
403
```

This is not authentication and does not pretend to be. It closes a drive-by
write; it does nothing about a caller that sets no `Origin` at all, which
is why the "bind to localhost or front it with something that authenticates"
advice still stands.

---

# Endpoints

## `GET /healthz`

```console
$ curl -s localhost:8081/healthz
{"status":"ok"}
```

Always 200 if the process is serving. The container healthcheck uses
`mcp-shield servers` instead, which additionally proves the database is
readable.

---

## `GET /api/servers`

Every registered server. Servers register lazily, on their first connect
through the proxy.

```console
$ curl -s localhost:8081/api/servers
[
  {
    "ID": 1,
    "Name": "calendar",
    "Endpoint": "",
    "CreatedAt": "2026-08-01T20:41:32.441015421Z"
  }
]
```

> **Known wart.** This endpoint's field names are PascalCase, while every
> other endpoint uses snake_case. It shipped that way in v0.1.0 (the struct
> has no JSON tags) and is left alone here because renaming them is a
> breaking change to a released response. Go's decoder matches field names
> case-insensitively, which is why this repository's own CLI decodes it into
> lowercase-tagged fields without noticing. JavaScript clients are not so
> forgiving: read `server.ID`, not `server.id`. Prefer
> `GET /api/servers/{name}` where you can — it is snake_case like the rest.

Not paginated; see [above](#the-two-endpoints-that-are-not-paginated).

---

## `GET /api/servers/{name}`

One server, plus the baseline the gate is currently enforcing for it.

```console
$ curl -s localhost:8081/api/servers/calendar
{
  "id": 1,
  "name": "calendar",
  "endpoint": "",
  "created_at": "2026-08-01T20:41:32.441015421Z",
  "approved_manifest": {
    "id": 1,
    "server": "calendar",
    "hash": "5332e4b6e6a043d70329171146ef3acad05620324efa00cd2a603f55b1bc0940",
    "state": "APPROVED",
    "changes": [
      "Added tool: calendar_create",
      "Added tool: calendar_read"
    ],
    "created_at": "2026-08-01T20:41:32.510186212Z"
  }
}
```

`approved_manifest` is `null` for a server that has never had one approved —
the fail-closed starting state, in which the gate exposes nothing. The field
is always present, so an absent key means a client bug rather than a server
with no baseline.

```console
$ curl -s localhost:8081/api/servers/nope
{"error":"database: not found: no server named \"nope\" is registered","code":"not_found"}
```

---

## `GET /api/manifests`

Every manifest, newest first, filterable and paginated. This is the general
listing: before it, only "currently pending" and "currently approved" were
reachable, so a server's history could not be shown at all.

| Query parameter | Meaning |
|---|---|
| `server` | Server **name**. Unknown name → 404, not an empty list. |
| `state` | `PENDING`, `APPROVED`, `REJECTED`, `SUPERSEDED`. May repeat (`?state=A&state=B`) or be comma-separated (`?state=A,B`). Case-insensitive. Unknown state → 400. |
| `hash` | Exact manifest hash. Combine with `server`; a hash is only unique per server. |
| `limit`, `offset` | See [Pagination](#pagination). |

```console
$ curl -s localhost:8081/api/manifests
{
  "items": [
    {
      "id": 2,
      "server": "calendar",
      "hash": "b04c586fa121479884d1e120bf7df52cd58847923888ae545d128e73f91af34a",
      "state": "REJECTED",
      "changes": [
        "Added tool: execute_command"
      ],
      "created_at": "2026-08-01T20:42:00.634508378Z"
    },
    {
      "id": 1,
      "server": "calendar",
      "hash": "5332e4b6e6a043d70329171146ef3acad05620324efa00cd2a603f55b1bc0940",
      "state": "APPROVED",
      "changes": [
        "Added tool: calendar_create",
        "Added tool: calendar_read"
      ],
      "created_at": "2026-08-01T20:41:32.510186212Z"
    }
  ],
  "pagination": {"limit": 50, "offset": 0, "count": 2, "has_more": false}
}
```

`changes` is the human-readable summary of this manifest's diff against the
baseline it was measured from. It is empty for a server's first-ever
manifest, which had no baseline to diff against.

Filtering:

```console
$ curl -s 'localhost:8081/api/manifests?state=REJECTED' | jq -c '[.items[]|{id,state}]'
[{"id":2,"state":"REJECTED"}]

$ curl -s 'localhost:8081/api/manifests?state=APPROVED,REJECTED' | jq -c '[.items[]|{id,state}]'
[{"id":2,"state":"REJECTED"},{"id":1,"state":"APPROVED"}]

$ curl -s 'localhost:8081/api/manifests?server=calendar&hash=b04c586fa121479884d1e120bf7df52cd58847923888ae545d128e73f91af34a' | jq -c '[.items[].id]'
[2]
```

A misspelled state is refused rather than returning an empty list, because
an empty list for `APROVED` is indistinguishable from "nothing is approved":

```console
$ curl -s 'localhost:8081/api/manifests?state=APROVED'
{"error":"unknown manifest state \"APROVED\": expected one of PENDING, APPROVED, REJECTED, SUPERSEDED","code":"invalid_request"}

$ curl -s 'localhost:8081/api/manifests?server=nope'
{"error":"database: not found: no server named \"nope\" is registered","code":"not_found"}
```

---

## `GET /api/manifests/pending`

Manifests awaiting a decision, oldest first, as a bare array. Released in
v0.1.0 and unchanged.

```console
$ curl -s localhost:8081/api/manifests/pending
[]
```

With something pending:

```json
[
  {
    "id": 2,
    "server": "calendar",
    "hash": "b04c586fa121479884d1e120bf7df52cd58847923888ae545d128e73f91af34a",
    "changes": ["Added tool: execute_command"],
    "created_at": "2026-08-01T20:42:00.634508378Z"
  }
]
```

Note there is no `state` field — every row is `PENDING` by definition. New
code should prefer `GET /api/manifests?state=PENDING`.

---

## `GET /api/manifests/{id}`

```console
$ curl -s localhost:8081/api/manifests/1
{
  "id": 1,
  "server": "calendar",
  "hash": "5332e4b6e6a043d70329171146ef3acad05620324efa00cd2a603f55b1bc0940",
  "state": "APPROVED",
  "created_at": "2026-08-01T20:41:32.510186212Z"
}

$ curl -s localhost:8081/api/manifests/9999
{"error":"database: not found: no manifest with id 9999","code":"not_found"}

$ curl -s localhost:8081/api/manifests/not-a-number
{"error":"manifest id \"not-a-number\" is not a number","code":"invalid_request"}
```

---

## `GET /api/manifests/{id}/diff`

The stored diff of this manifest against the baseline it was measured
against — the thing an approver is actually deciding about. Returns the raw
diff object, or `null` for a manifest that had no prior baseline.

```console
$ curl -s localhost:8081/api/manifests/2/diff
{
  "added_tools": ["execute_command"],
  "removed_tools": [],
  "changed_tools": [],
  "added_prompts": [],
  "removed_prompts": [],
  "changed_prompts": [],
  "added_resources": [],
  "removed_resources": [],
  "changed_resources": []
}
```

This endpoint is **not** wrapped in the pagination envelope: it is one
object, not a list, and it shipped in v0.1.0.

---

## `GET /api/manifests/{id}/decisions`

The decision history of one manifest. In practice one entry — a manifest
leaves `PENDING` exactly once — but it is a list because the underlying
table is append-only and nothing is ever rewritten.

```console
$ curl -s localhost:8081/api/manifests/1/decisions
{
  "items": [
    {
      "id": 1,
      "manifest_id": 1,
      "manifest_hash": "5332e4b6e6a043d70329171146ef3acad05620324efa00cd2a603f55b1bc0940",
      "server": "calendar",
      "decision": "APPROVED",
      "username": "eric",
      "reason": "initial baseline",
      "decided_at": "2026-08-01T20:41:47.136601261Z"
    }
  ],
  "pagination": {"limit": 50, "offset": 0, "count": 1, "has_more": false}
}
```

A manifest with no decisions yet returns `items: []` and 200. An unknown
manifest returns 404 — "nobody has decided yet" and "that manifest does not
exist" are different answers.

---

## `GET /api/decisions`

The audit trail across every server: who approved or rejected what, when,
and why. Newest first.

| Query parameter | Meaning |
|---|---|
| `server` | Server name. Unknown name → 404. |
| `limit`, `offset` | See [Pagination](#pagination). |

```console
$ curl -s 'localhost:8081/api/decisions?limit=1'
{
  "items": [
    {
      "id": 2,
      "manifest_id": 2,
      "manifest_hash": "b04c586fa121479884d1e120bf7df52cd58847923888ae545d128e73f91af34a",
      "server": "calendar",
      "decision": "REJECTED",
      "username": "sam",
      "reason": "execute_command is not acceptable",
      "decided_at": "2026-08-01T20:42:10.396079758Z"
    }
  ],
  "pagination": {"limit": 1, "offset": 0, "count": 1, "has_more": true}
}
```

`decision` is `APPROVED` or `REJECTED`. `reason` is free text and may be
empty — it is optional on the way in. `username` is a caller-supplied
attestation; see [above](#the-username-field-what-it-means-and-does-not).

---

## `POST /api/manifests/{id}/approve`

Marks a `PENDING` manifest `APPROVED`, moves the server's previous
`APPROVED` manifest to `SUPERSEDED`, and writes the audit record — all in
one transaction.

Request:

```json
{"username": "eric", "reason": "initial baseline"}
```

`username` is **required**. `reason` is optional.

```console
$ curl -s -X POST localhost:8081/api/manifests/1/approve \
    -H 'Content-Type: application/json' \
    -d '{"username":"eric","reason":"initial baseline"}'
{"id":1,"ok":true}
```

Failure modes:

```console
$ curl -s -X POST localhost:8081/api/manifests/1/approve -d '{}'
{"error":"username is required: an approval decision must be attributable to whoever made it","code":"username_required"}

$ curl -s -X POST localhost:8081/api/manifests/1/approve -d '{not json'
{"error":"invalid JSON body: invalid character 'n' looking for beginning of object key string","code":"invalid_json"}

$ curl -s -X POST localhost:8081/api/manifests/1/approve -d '{"username":"eric"}'
{"error":"approval: manifest is not in PENDING state: manifest 1 is APPROVED","code":"conflict"}
```

Every refusal above leaves **no** audit row. A decision the gateway cannot
attribute is not recorded under an invented identity.

---

## `POST /api/manifests/{id}/reject`

Marks a `PENDING` manifest `REJECTED` and writes the audit record. Traffic
for that manifest hash stays blocked; the row is never deleted or reused, so
the same capability set reappearing later does not silently get a fresh
decision.

Same request body, same failure modes.

```console
$ curl -s -X POST localhost:8081/api/manifests/2/reject \
    -H 'Content-Type: application/json' \
    -d '{"username":"sam","reason":"execute_command is not acceptable"}'
{"id":2,"ok":true}
```

---

## `GET /api/notifications/failed`

Notification events the dispatcher gave up on. **404s unless notifications
are configured** — an operator with no targets is told the surface does not
exist, rather than shown an empty list that reads as "everything was
delivered".

```console
$ curl -s localhost:8081/api/notifications/failed
{"error":"notifications are not configured on this gateway","code":"not_configured"}
```

With notifications configured, a bare array:

```json
[
  {
    "event_id": 3,
    "event": "manifest.pending",
    "server": "calendar",
    "manifest_id": 2,
    "attempts": 6,
    "last_error": "target \"ops-webhook\": 500 Internal Server Error",
    "created_at": "2026-08-01T20:21:02.301Z"
  }
]
```

`last_error` names the target by its configured name and never by its URL —
a webhook URL is a capability-bearing credential and this is a place
operators copy output from. See [notifications.md](notifications.md).

---

# Operational notes

## Inspecting the database

The compose stack keeps the database on a **named volume**, not a host bind
mount, and that is deliberate.

SQLite in WAL mode coordinates through a shared-memory file (`-shm`) and
POSIX locks. Neither works across a Docker Desktop bind mount on macOS or
Windows: the host and the container run two different SQLite builds against
one file with no working lock between them. Running `sqlite3 data/mcp.db` on
the host while the container has it open — the obvious thing to do, and the
only reason to bind-mount it — can zero the file header. This is observed,
not theoretical: it destroyed the database during development of this
change. That file is the approvals audit trail.

To read it safely, mount the volume into a throwaway container on the same
(Linux) filesystem:

```console
$ docker run --rm -v mcp-shield_gateway-data:/data:ro keinos/sqlite3 \
    sqlite3 /data/mcp.db \
    "select a.id, a.decision, a.username, a.reason, m.state
       from approvals a join manifests m on m.id = a.manifest_id;"
1|APPROVED|eric|initial baseline|APPROVED
2|REJECTED|sam|execute_command is not acceptable|REJECTED
```

A gateway run directly on the host (not in Docker) has no such problem —
one filesystem, one set of locks.

## Configuration

| Variable | Default | Notes |
|---|---|---|
| `API_ADDR` | `:8081` | This API's listen address. |
| `PROXY_ADDR` | `:8080` | The MCP proxy's listen address. |
| `DATABASE_PATH` | `data/mcp.db` | SQLite file. Parent directory is created `0700`. |
| `CORS_ALLOWED_ORIGINS` | *(empty)* | Comma-separated exact origins. Empty means no cross-origin access. `*` refuses to start. |
| `FAIL_MODE` | `block` | `warn` allows unapproved traffic through, flagged. Never a production default. |
| `CONFIG_PATH` | `config/servers.json` | Upstream MCP servers. |
| `NOTIFY_CONFIG_PATH` | `config/notify.json` | Missing file disables notifications. |
| `TEMPLATES_DIR` | `web/dashboard/templates` | Dashboard templates. |
| `UPSTREAM_TIMEOUT` | — | Bounds one proxied request's upstream work. |

## Compatibility

Everything in this document that is marked "released in v0.1.0" keeps its
shape. Added in 0.2.0, all additive:

- `code` on every error body, beside the unchanged `error` string.
- `GET /api/servers/{name}`, `GET /api/manifests`,
  `GET /api/manifests/{id}/decisions`, `GET /api/decisions`.
- `CORS_ALLOWED_ORIGINS`.

Nothing that existed changed shape. The one thing a client may notice is
that a cross-origin form POST with a disallowed `Origin` is now refused —
which was the point.
