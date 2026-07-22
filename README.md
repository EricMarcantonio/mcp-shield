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
classified, and gated. Unknown or unapproved manifests are blocked by
default (fail closed).

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

## Manifest immutability & fail-closed, enforced structurally

- `database.Store` has exactly one write path for an existing manifest
  row — `UpdateManifestState` — which only ever does
  `UPDATE manifests SET state = ? WHERE id = ?`. There is no method to
  mutate `hash` or `canonical_json` after insert.
- `approval.Workflow` only allows the transitions
  `PENDING→APPROVED`, `PENDING→REJECTED`, `APPROVED→SUPERSEDED`; anything
  else returns `ErrInvalidTransition`/`ErrNotPending` before it reaches SQL.
- Default `FAIL_MODE=block`: any manifest that isn't in `APPROVED` state
  blocks traffic with a JSON-RPC error. `FAIL_MODE=warn` exists for
  initial rollout observation but is never the default.

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
PENDING manifest and blocks traffic. Review and approve it:

```sh
curl localhost:8081/api/manifests/pending
curl -X POST localhost:8081/api/manifests/1/approve -d '{"username":"you","reason":"reviewed"}'
```

...or use the dashboard at `http://localhost:8081/`.

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
