# mcp-shield

A Zero Trust gateway for the Model Context Protocol (MCP). It sits between
an AI client and an upstream MCP server, fingerprints the server's
advertised tools/prompts/resources, and blocks any capability change until
a human explicitly approves it.

## Why

An MCP server can change what it offers at any time — a compromised or
updated upstream server could silently add a `delete_*`, `upload_*`, or
`execute_*` tool and start receiving calls from a trusted AI client with no
warning. mcp-shield closes that gap: every connection is fingerprinted into
a canonical manifest, diffed against the last approved version, risk
classified, and gated. New or changed capabilities are withheld until a
human approves them — but withholding is scoped to what actually changed,
not the whole server: tools that are byte-identical to the last approved
version keep working even while a new or modified tool sits pending or
gets rejected. A rejected change never brings down the tools you already
trusted.

## Architecture

```
Client (HTTP JSON-RPC) --> :8080 mcp-shield proxy --> stdio --> upstream MCP server
                                     |
                                     v
                          manifest build + canonicalize + hash
                                     |
                                     v
                              SQLite (servers, manifests, approvals)
                                     |
                                     v
                    :8081 approval API + dashboard (approve/reject)
```

- **Client-facing (`:8080`)**: `POST /mcp/{server}`, one JSON-RPC request
  per HTTP call. Known limitation: this does not work with Claude
  Desktop's classic stdio-spawned-server config, only with clients that
  can point at a remote/HTTP MCP endpoint. A stdio-shim for that case is a
  documented future-extension seam (`internal/mcp.Transport`), not built
  in this MVP.
- **Upstream-facing**: stdio subprocess — the gateway spawns the real MCP
  server and speaks JSON-RPC over its stdin/stdout.
- **Every** intercepted call (`initialize`, `tools/list`, `prompts/list`,
  `resources/list`, `tools/call`) re-fetches the upstream server's current
  capabilities and re-runs the gate before anything is forwarded to the
  client — there's no "already approved this session" shortcut a server
  could exploit by changing behavior mid-session.

## Partial allow, not all-or-nothing

The gate's decision (`approval.CheckResult` / `mcp.GateDecision`) is a
per-item set of safe tool/prompt/resource names, not a single allow/deny
bool:

- A tool identical to the current approved baseline is always in the safe
  set and keeps flowing through `tools/list` and `tools/call`, regardless
  of what else on the server is pending or rejected.
- A new or changed tool is excluded from `tools/list` (it's silently
  omitted, not an error) and `tools/call` on it returns a JSON-RPC error
  naming that specific tool and the manifest state it belongs to.
- A server with **no** approved baseline at all (first-ever connect) has
  an empty safe set — fail closed until at least one manifest is approved.
- `initialize` is never gated (it reveals no capability data, so blocking
  it would just break the handshake for no security benefit).
- Passthrough methods mcp-shield doesn't specifically parse
  (`resources/read`, `prompts/get`, ...) can't be filtered item by item,
  so they fall back to a coarse check: forwarded only once the server has
  *some* approved baseline, blocked entirely otherwise.

## Manifest immutability & fail-closed, enforced structurally

- `database.Store` has exactly one write path for an existing manifest
  row — `UpdateManifestState` — which only ever does
  `UPDATE manifests SET state = ? WHERE id = ?`. There is no method to
  mutate `hash` or `canonical_json` after insert.
- `approval.Workflow` only allows the transitions
  `PENDING→APPROVED`, `PENDING→REJECTED`, `APPROVED→SUPERSEDED`; anything
  else returns `ErrInvalidTransition`/`ErrNotPending` before it reaches SQL.
- Default `FAIL_MODE=block`: anything not in the approved baseline is
  withheld (see "Partial allow" above — this is per-item, not a whole-server
  block). `FAIL_MODE=warn` allows everything through regardless of state,
  for initial rollout observation; it is never the production default.

## Risk classification

Given a diff against the last approved manifest, in precedence order:

1. **HIGH** — any newly added tool's name contains (case-insensitive)
   `delete`, `upload`, `execute`, `shell`, `file`, `write`, `admin`, or
   `credential`. This is a blunt substring match by design — it will also
   flag a benign tool like `filesystem_status` (contains "file"). That's
   intentional: a human still makes the final call.
2. **MEDIUM** — a tool's `input_schema` changed.
3. **LOW** — only a tool's description changed (or nothing risk-relevant).

## Quickstart (Docker)

```sh
cp config/servers.example.json config/servers.json  # edit command/args for your real MCP server
make docker-build
make docker-up
```

Point an MCP-capable HTTP client at `http://localhost:8080/mcp/<name>`
(the `name` from `config/servers.json`). First connection creates a
PENDING manifest; since there's no approved baseline yet, `tools/list`
comes back empty and any `tools/call` is blocked. Review and approve it:

```sh
curl localhost:8081/api/manifests/pending
curl -X POST localhost:8081/api/manifests/1/approve -d '{"username":"you","reason":"reviewed"}'
```

...or use the dashboard at `http://localhost:8081/`.

## Manual testing (Docker)

Full setup:

```sh
cp config/servers.example.json config/servers.json
cp config/testserver-tools.example.json config/testserver-tools.json
make docker-build
make docker-up
```

`config/servers.json` points the gateway at the fake test server with
`TOOLS_FILE=/config/testserver-tools.json` — that file is bind-mounted
(`./config:/config:ro`) so edits on your host take effect on the *next*
request, no restart needed (the test server re-reads and re-parses the
file on every `tools/list` call).

Trigger a connect — first-ever connect has no approved baseline, so
`tools/list` comes back with an empty `tools` array (not an error) and
creates a PENDING manifest:

```sh
curl -s -X POST localhost:8080/mcp/calendar -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
curl -s localhost:8081/api/manifests/pending
```

Approve it so a baseline exists — `tools/list` now returns both tools:

```sh
curl -s -X POST localhost:8081/api/manifests/1/approve -d '{"username":"you"}'
curl -s -X POST localhost:8080/mcp/calendar -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Now edit `config/testserver-tools.json` by hand — this is the "modify the
schema and watch the proxy react" step. Try each of these and re-run the
`tools/list` curl above after each edit. Watch two things: a fresh PENDING
manifest appears in `/api/manifests/pending`, **and** `tools/list` still
returns the tools you didn't touch — only the new/changed one drops out:

- **Add a field to an existing tool's `inputSchema`** (e.g. add
  `"notes": {"type": "string"}` under `calendar_create`'s `properties`) →
  risk `MEDIUM`; `calendar_create` itself drops out of `tools/list` until
  approved, `calendar_read` is unaffected.
- **Change a tool's `description` only** → risk `LOW`, same partial effect.
- **Add a new tool object** named e.g. `upload_receipt` or
  `delete_event` → risk `HIGH` (matches the `upload`/`delete` keyword
  list); the two existing tools keep working, only the new one is
  withheld. Confirm with `tools/call`:
  ```sh
  curl -s -X POST localhost:8080/mcp/calendar -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"calendar_read"}}'   # succeeds
  curl -s -X POST localhost:8080/mcp/calendar -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"upload_receipt"}}'  # blocked
  ```
  Reject it (`curl -X POST localhost:8081/api/manifests/{id}/reject -d '{"username":"you"}'`)
  and re-run both calls — `calendar_read` still works, `upload_receipt`
  still blocked. That's the point: a rejected change never takes down
  what you already approved.
- **Remove a tool entirely** from the array → shows up in `removed_tools`
  in the diff (`curl localhost:8081/api/manifests/{id}/diff`); it just
  stops appearing in `tools/list` since the upstream server no longer
  offers it — nothing to approve or reject.

You can also drive this from the dashboard at `http://localhost:8081/`
instead of curl — approve/reject buttons are right there next to the diff.

To reset and start clean: `make docker-down`, delete `data/mcp.db*`,
`make docker-up` again.

## CLI

```sh
mcp-shield servers
mcp-shield manifests
mcp-shield approve <id>
mcp-shield reject <id>
mcp-shield diff <id>
```

Talks to `$MCP_SHIELD_API` (default `http://localhost:8081`).

## Development

```sh
make build          # bin/mcp-shield, bin/mcp-shield-testserver
make test-unit
make test-integration   # requires `make build` first
make test            # both
make lint             # gofmt -l + go vet
```

`cmd/server` is a fake MCP server with three tool sets, used for manual
and automated testing of the approval pipeline:

- `-version v1`: `calendar_read`, `calendar_create` (LOW risk)
- `-version v2`: adds `upload_attachment` (HIGH risk — matches "upload")
- `-version v3`: adds `delete_calendar`, `execute_command` (HIGH risk)

## Explicitly out of scope for this MVP

Kubernetes deployment, a distributed database, advanced sandboxing, eBPF,
and runtime syscall monitoring. Left as seams for later, not built:
- `database.Store` is an interface — a Postgres backend can implement it
  without touching `approval`/`api`/`mcp`.
- `mcp.Transport` is an interface — an HTTP/SSE transport or a
  Claude-Desktop stdio shim can be added without touching `UpstreamClient`.
- `manifest.Hash()` output is a plain hex SHA256 string a future
  Sigstore/cosign signing step could wrap.
- OAuth, network policy enforcement, and runtime sandboxing are not
  addressed; `config/servers.json`'s per-server `env` is the noted future
  hook for secrets-manager-backed credential injection instead of plaintext.
