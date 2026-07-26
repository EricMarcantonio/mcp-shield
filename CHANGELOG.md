# Changelog

All notable changes to mcp-shield are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[SemVer](https://semver.org) (0.x: minor bumps may break).

## [Unreleased]

Nothing yet.

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
