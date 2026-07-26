# Changelog

All notable changes to mcp-shield are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows
[SemVer](https://semver.org) (0.x: minor bumps may break).

## [Unreleased]

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
