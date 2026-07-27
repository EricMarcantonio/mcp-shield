# Changelog

All notable changes to mcp-shield are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[SemVer](https://semver.org) (0.x: minor bumps may break).

## [Unreleased]

### Added
- `mcp-shield connect <server> [--gateway URL]` — a stdio shim, and the
  answer to the limitation the README previously called its biggest. Claude
  Desktop's classic config spawns an MCP server as a subprocess and speaks
  newline-delimited JSON-RPC over its stdin/stdout; it cannot point at an
  HTTP endpoint. `connect` is that subprocess: each inbound frame becomes
  one `POST {gateway}/mcp/{server}`, each response one outbound frame. It
  is a subcommand of the existing binary rather than a second artifact, so
  the release pipeline already ships it, and the gate path is untouched.
  The gateway URL also reads from `MCP_SHIELD_PROXY`. See
  [Client compatibility](README.md#client-compatibility) for the Claude
  Desktop config block; decision D3 in
  [the design doc](docs/superpowers/specs/2026-07-25-oss-hardening-design.md#d3--transport-sequencing-amends-the-phase-9-structure-below).

  Behaviour worth knowing: requests are forwarded concurrently, because
  clients pipeline them, and each response is written as one whole frame
  under a lock so concurrent replies cannot interleave. Frames are capped
  at 8 MiB in both directions — a larger one is refused with a JSON-RPC
  error rather than silently truncated, since a truncated `tools/list` in a
  gateway that gates on capabilities is a correctness hole, not a
  performance nuisance. Notifications (no `id`) get no reply under any
  outcome. Every failure — unreachable gateway, non-2xx, a body that is not
  JSON-RPC — comes back as a JSON-RPC error naming the cause, and every
  human-readable diagnostic goes to stderr, because stdout is the protocol
  channel.

  Still one JSON-RPC request per HTTP call, and there are **no
  server-initiated notifications**: the gateway re-fetches and re-gates on
  every call by design, so `listChanged` push semantics are intentionally
  absent rather than missing.

- **Webhook notifications.** The gate fails closed, so a withheld capability
  used to be invisible until someone happened to look at the dashboard.
  mcp-shield now POSTs a versioned, HMAC-signed JSON event to configured
  webhook targets when it records a new pending manifest (and, optionally,
  on approve/reject). Slack and Discord are supported through a
  `"format": "slack"` rendering with no SDK dependency. Configure via
  `config/notify.json` (`NOTIFY_CONFIG_PATH`); see
  [docs/notifications.md](docs/notifications.md).
- `GET /api/notifications/failed` lists events the dispatcher gave up on,
  with attempt counts and the last error. Returns 404 when notifications are
  not configured. Silent notification death is the failure this feature
  exists to remove, so a target that stops working stays visible.
- `notification_outbox` table. The event is written in the same transaction
  as the manifest row, so the two commit together and a crash between "gate
  withheld something" and "operator was told" replays on restart. Delivery
  is at-least-once with persisted backoff (1m, 5m, 25m, 2h, 12h, then
  daily); receivers deduplicate on `event_id`.

Notifications are opt-in and off by default: with no config file, behaviour
is exactly as in 0.1.0. The schema addition is additive — an existing 0.1.0
database is upgraded in place on open, with a test that builds a verbatim
0.1.0 database and asserts every row survives.

Nothing on the notification path can block, delay, or crash a gate decision:
enqueue is one INSERT inside an already-open transaction, and all delivery
happens in a background goroutine whose failures never propagate back.

### Changed
- `internal/mcp` exports `MaxFrameSize` (8 MiB) and `FrameBufferSize`
  (64 KiB), replacing the literals inside `StdioTransport`'s scanner so the
  upstream transport and the shim cannot drift apart on what constitutes an
  acceptable frame.

## [0.1.0] - 2026-07-26

First tagged release. mcp-shield has been usable as a gateway for a while;
this is the point at which it became installable, verifiable, and
releasable by someone who is not its author.

### Removed
- **Breaking:** risk classification (`ClassifyRisk`, the `RiskLow`/
  `RiskMedium`/`RiskHigh` constants, and the substring `riskKeywords` list
  in `internal/diff`) is deleted outright, along with everywhere it
  surfaced: the `risk_level` database column, the `risk`/`risk,omitempty`
  fields on the `/api/manifests/pending` and `/api/manifests/{id}`
  responses, the `RISK` column in `mcp-shield manifests`, and the risk
  badge in both dashboard templates. A substring match that flags
  `filesystem_status` (contains "file") while missing a tool named
  `sync_to_remote` entirely was attaching an authoritative-looking label
  to a judgement the tool cannot make. The diff — added/removed/changed
  against the approved baseline — was already the complete signal; see
  decision D8 in
  [docs/superpowers/specs/2026-07-25-oss-hardening-design.md](docs/superpowers/specs/2026-07-25-oss-hardening-design.md).
  Gate semantics are unchanged: risk never affected whether a capability
  was withheld, only how the pending list displayed it.

### Added
- LICENSE, SECURITY.md, CONTRIBUTING.md, CODE_OF_CONDUCT.md, issue/PR templates.
- `mcp-shield version` subcommand; the binary is now built with
  `-X main.version=...` at release time (`"dev"` for local builds).
- Release automation: `.goreleaser.yaml` + `Dockerfile.goreleaser` build
  cross-compiled `mcp-shield` binaries for linux/darwin/windows ×
  amd64/arm64 (CGO_ENABLED=0 — the only dependency, `modernc.org/sqlite`,
  is pure Go) and a multi-arch Docker image, and
  `.github/workflows/release.yml` publishes both on any `v*` tag push.
  `mcp-shield-testserver` is a test fixture and is intentionally not part
  of the release archives or the published image.
  Verified locally: `goreleaser check`, a full `goreleaser build --snapshot
  --clean` (all 6 binaries built, native binary run in both `version` and
  `serve` mode), and `actionlint` on the new workflow. The multi-arch
  Docker image build/push was **not** exercised locally (verified by
  config inspection only) — the first `v0.1.0` tag is the first real test
  of that half of the pipeline. This repo is currently private, so
  `go install .../cmd/mcp-shield@latest` and `docker pull
  ghcr.io/ericmarcantonio/mcp-shield` do not work yet regardless of
  tagging; see README Install.

### Fixed
- mcp-shield could not start on a clean machine: `DATABASE_PATH` defaults to
  `data/mcp.db` and nothing created `data/`, so a release binary or the
  published container started in a fresh directory exited with
  `unable to open database file (14)`. `database.Open` now creates the
  parent directory (mode `0700` — it holds the approvals audit trail) and
  warns if a pre-existing directory is readable by other users.
- A dead upstream is respawned instead of being reused forever, and
  `dispatchLoop` no longer hangs when the transport's error channel drains
  before its frame channel.
- `manifest.Build` rejects capability sets that advertise the same tool
  name, prompt name, or resource URI twice; such a set previously produced
  an order-dependent manifest hash.
- Diffs and the dashboard now show changed prompts and changed resources,
  which were silently omitted from both.

### Changed
- **Breaking:** the approve/reject endpoints now require a non-empty
  `username` and reject a malformed body with `400`. Previously a garbled
  or absent body returned `200` and wrote the audit row under a fabricated
  `"unknown"` identity. The CLI (`-username`, default `cli`) and the
  dashboard already send one; hand-written `curl` calls that posted `{}`
  must now pass `{"username":"..."}`.
- The dashboard maps decision errors the same way the JSON API does:
  `409` for a manifest that is no longer PENDING and `404` for one that
  does not exist, instead of a blanket `500`.
- `go install github.com/EricMarcantonio/mcp-shield/cmd/mcp-shield@latest` now
  installs a binary named `mcp-shield` (previously `cmd/gateway` produced a
  binary named `gateway`).
- `docker-compose.yml`'s `gateway` service now also declares `image:
  ghcr.io/ericmarcantonio/mcp-shield:latest` alongside its existing
  `build: .`; this has no effect until that tag exists in a public
  registry — local builds remain the working path.

[Unreleased]: https://github.com/EricMarcantonio/mcp-shield/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/EricMarcantonio/mcp-shield/releases/tag/v0.1.0
