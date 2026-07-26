# mcp-shield OSS Hardening — Design Document

Date: 2026-07-25
Status: all decisions D1-D7 resolved; the plan is executable end to end
Companion plan: `docs/superpowers/plans/2026-07-25-oss-hardening.md`

Goal: turn the working MVP into a professional open-source project that 10,000 people
could adopt, trust, and contribute to — without inventing scope the project does not need.

This design was produced non-interactively. Where the brainstorming process would
normally have asked the user a question, the question is recorded below with the
assumption made. Anything genuinely load-bearing is escalated to "Decisions needed
from you" and the corresponding plan phase is gated on it.

---

## Decisions made

| # | Decision | Resolution | Date |
|---|----------|------------|------|
| D1 | License | **Apache-2.0**, as recommended. Landed in Phase 1. | 2026-07-25 |
| D2 | Notification architecture | **A1 — transactional outbox + generic HMAC-signed webhook**, as recommended, with the send path behind a `Notifier` interface so A2 channels can be contributed later. | 2026-07-25 |
| D3 | Transport strategy | **B1 first, then B2 split into two sub-phases with the upstream-facing half first.** See below. | 2026-07-25 |
| D4 | Container registry | **`ghcr.io/ericmarcantonio/mcp-shield`**, as recommended. | 2026-07-25 |
| D5 | Coverage floor | **70%** on `./internal/...`, as recommended. | 2026-07-25 |
| D6 | `cmd/` renames | **Yes**, as recommended. Executing in Phase 6. | 2026-07-25 |
| D7 | Target protocol version | **`2025-11-25` only, for now.** See below. | 2026-07-25 |

### D3 — Transport sequencing (amends the Phase 9 structure below)

B1 (stdio shim) runs first, unchanged. B2 is **split**, and the halves are
reordered relative to this document's original recommendation:

| Sub-phase | Work | Rationale |
|---|---|---|
| 9a | stdio shim (`mcp-shield connect`) | Unblocks Claude Desktop, the most-cited limitation. ~200 lines. |
| 9b | **Upstream-facing** `StreamableHTTPTransport` | mcp-shield can only spawn *local subprocess* upstreams today, so it structurally cannot protect against remote third-party hosted MCP servers — which is exactly where the rug-pull threat this project exists to counter is highest. This is a threat-model expansion, not a compatibility feature. |
| 9c | **Client-facing** Streamable HTTP endpoint | Compatibility work: lets spec-conformant clients connect natively. Includes the mandatory `Origin` validation and localhost-binding defaults. |

9b and 9c each need their own plan document when their turn comes; the 2–4 week
estimate in "Design question B" covers both together, not either alone.

### D7 — Target protocol version

The codebase hardcodes `2024-11-05` in three places (finding S7) and performs no
version negotiation. Verified against modelcontextprotocol.io on 2026-07-25:

- **`2025-11-25` is the current stable revision.**
- The **`2026-07-28` release candidate is published**, with the final
  specification shipping 2026-07-28. It removes the
  `initialize`/`notifications/initialized` handshake in favour of a stateless
  core, carrying protocol version, client identity, and client capabilities in
  `_meta` on every request. Beta SDKs (Python, TypeScript, Go, C#) exist.

**Decision: move to `2025-11-25` only. Do not implement multi-version
negotiation yet, and do not build to the RC.**

Rationale: deployed MCP clients speak `2025-11-25` and will for some time. The
version work gets done once now against a stable spec, and once later against a
*finalized* `2026-07-28` with a stable Go SDK — rather than twice against a
moving RC.

Consequences for implementation:

- Phase 5 replaces the three hardcoded `2024-11-05` constants with `2025-11-25`.
- The `initialize` handshake still exists in `2025-11-25`, so the current
  "initialize is never gated" design remains valid and unchanged.
- **Do not bake session-forever assumptions into the transport seam.** The RC's
  direction is stateless, and mcp-shield's existing design — re-fetch and
  re-gate on every call, with no already-approved-this-session shortcut — is
  already aligned with it. Preserve that property.
- Revisit after `2026-07-28` finalizes and the client ecosystem moves. At that
  point the question becomes multi-version negotiation, which the spec requires
  (clients and servers MAY support multiple versions but MUST agree on one).

---

## D2 and D3 — options that were considered

Retained as the record of what was weighed. Both are now decided (see above);
the full analysis lives in "Design question A" and "Design question B" below.

| # | Decision | Options | Recommendation at the time | Gated |
|---|----------|---------|----------------|--------|
| D2 | Notification architecture | A1 outbox + generic HMAC webhook / A2 multi-channel native notifiers / A3 log-and-let-users-bridge | **A1** (with the `Notifier` interface shaped so A2 can be added later). Full analysis in "Design question A" below. | Phase 8 |
| D3 | Transport strategy | B1 stdio shim binary / B2 spec-conformant Streamable HTTP / B3 status quo documented | **B1 now, then B2.** Full analysis in "Design question B" below. | Phase 9 |

---

## Questions I would have asked (with assumed answers)

1. **Who is the target adopter?** Assumed: individual developers and small teams
   running local MCP servers first; platform/security teams second. This drives:
   simple install paths (go install, docker pull, release binaries), no Kubernetes
   manifests, no enterprise SSO in scope.
2. **Solo maintainer or organization?** Assumed solo (single git author). So: no
   GOVERNANCE.md, no CODEOWNERS, no CLA bot. CONTRIBUTING.md stays short and honest.
3. **Is a 0.x version acceptable?** Assumed yes. SemVer with `v0.x` tags, an explicit
   "APIs may change before 1.0" statement, and consequently **no `pkg/` public API**
   (see Structure section).
4. **Are external SaaS dependencies acceptable in CI?** Assumed: minimize. The
   coverage gate is a deterministic threshold check inside CI, not a Codecov
   requirement. Codecov can be added later purely for the badge/graphs.
5. **Which AI clients matter for the transport work?** Assumed: Claude Desktop
   (stdio config), Claude Code, and any spec-conformant Streamable-HTTP client.
6. **Is authentication on the approval API (:8081) in scope?** Assumed no for this
   plan — but it must be *documented* as a deployment constraint (bind to localhost /
   private network only). SECURITY.md and README both say this explicitly. An
   `--api-token` option is listed as a good-first-issue candidate, not planned work.
7. **Notification targets for v1?** Assumed: generic webhook that is
   Slack-incoming-webhook-compatible via a payload adapter. Email and desktop
   notifications excluded (reasons in Design question A).
8. **May the risk-keyword list change?** Assumed no — README documents the blunt
   substring match as intentional. Nothing in this plan alters gate semantics.

---

## Current state (verified 2026-07-25)

- Baseline is green: `go test ./...`, `go test -tags=integration ./test/...`,
  `go vet`, `gofmt -l` all pass on `main` (87d99cd).
- Coverage by package (`go test -cover`): approval 78.4%, manifest 72.0%,
  diff 63.7%, database 58.0%, api 37.3%, **mcp 0%**, **app 0%**, cmd/* 0%.
- ~4,300 lines of Go across 25 files. No import cycles (graphify confirms).
- Single dependency: `modernc.org/sqlite` (pure Go — this is what makes
  CGO_ENABLED=0 cross-compilation trivially possible; the release design relies on it).
- Tooling gap: no golangci-lint, no govulncheck, no CI, no LICENSE, no
  community files, no release process.

---

## 1. Go project structure

**Assessment: the layout is already close to right. Do not churn it.**

`cmd/` + `internal/` is the standard shape. The internal package boundaries
(`database`, `manifest`, `diff`, `approval`, `mcp`, `api`, `app`) are well-drawn:
one responsibility each, no cycles, and the two documented seams
(`database.Store`, `mcp.Transport`) are real interfaces. The `Gate` interface in
`internal/mcp/server.go:43-45` deliberately avoids the manifest→mcp cycle and says so.

Changes that earn their place:

1. **Rename `cmd/gateway` → `cmd/mcp-shield` and `cmd/server` → `cmd/mcp-shield-testserver`** (D6).
   Go names installed binaries after the last path element. Today `go install
   .../cmd/gateway@latest` yields a binary called `gateway` and `.../cmd/server`
   yields `server` — both wrong and the second actively confusing. This also makes
   `README` install instructions honest.
2. **Split `internal/mcp/server.go`** (250 lines). It currently holds six concerns:
   `ServerConfig`, `GateDecision`+`Gate`, `serverSession`, the 94-line `ServeHTTP`,
   filter helpers, and JSON-RPC response writers. Split into `gate.go`
   (GateDecision, Gate), `session.go` (serverSession + upstream factory),
   `downstream.go` (handler + method dispatch), keeping the package name. This is
   motivated by testability (Phase 4 needs to fake the upstream) as much as by size.
3. **Deduplicate `internal/database/sqlite.go`** (419 lines, ~100 of which are
   mechanical forwarding between `SQLiteStore` and `txStore`). One `queries{e execer}`
   type implementing the interface once removes the duplication without touching
   the `Store` interface or any caller. Detailed in Clean code section.
4. **No `pkg/` directory.** A public API is a stability promise. Pre-1.0, with the
   most plausible external consumers being (a) alternative Store backends and
   (b) notifiers — both of which can be contributed in-tree — exporting packages
   would create maintenance surface with no current consumer. Revisit at 1.0 if
   someone actually asks to embed the gateway as a library. YAGNI.
5. **Keep** `test/integration` with its build tag, `web/`, `config/` with
   `.example.json` convention, and `internal/app` as the wiring/composition root.

## 2. Testing

**Gaps, in priority order:**

| Package | Coverage | Gap |
|---|---|---|
| `internal/mcp` | 0% | The security-enforcing HTTP handler, the JSON-RPC client's concurrency, and the stdio transport have **no unit tests at all**. Only the happy path is exercised, indirectly, by the one integration test. This is the highest-risk gap: `DownstreamHandler.ServeHTTP` is the enforcement point. |
| `internal/app` | 0% | Covered only via integration test; acceptable — it is wiring. A single unit test for `gateAdapter` (server auto-creation) is worth having; full app coverage is not. |
| `internal/api` | 37.3% | Dashboard handlers and views are untested; decision-endpoint edge cases (malformed body, conflict mapping) untested. |
| `internal/database` | 58.0% | Error paths and `getApprovedManifest` variants untested. Raised naturally by the ErrNotFound refactor's tests. |
| `cmd/*` | 0% | CLI untested. Low value per line; test `runCLI` against `httptest.Server`. The test server (`cmd/server`) is itself a test fixture — do not chase coverage there. |

**Fuzz / property tests — the canonicalizer and hash are security-critical.**
A canonical form that is not stable under reordering is a gate bypass: two
byte-different manifests with the same meaning would produce different hashes
(spurious re-approval fatigue → rubber-stamping), or worse, a stable-looking hash
that misses semantic change. Native Go fuzzing, no new dependencies:

- `FuzzCanonicalizeValueStable` (internal/manifest): for arbitrary JSON input —
  (P1) canonicalization is idempotent: `C(C(x)) == C(x)`; (P2) permuting
  primitive-only arrays and re-marshaling does not change the output;
  (P3) output is valid JSON.
- `FuzzManifestHashOrderInvariance`: fuzz-generated tool tuples fed to `Build`
  in two different orders must hash identically (extends the existing
  `TestCanonicalizeDeterministicUnderReorder` beyond hand-picked cases).
- `FuzzFromCanonicalJSONRoundTrip`: `Canonicalize(FromCanonicalJSON(C(m)))` must
  equal `C(m)` — the baseline stored in SQLite must never drift from the live form.
- Malformed-frame unit tests (not fuzz) for `UpstreamClient.dispatchLoop`:
  garbage frames, unknown IDs, error-then-close sequences.

CI runs each fuzz target briefly (`-fuzztime=15s`) as a smoke test; long fuzzing is
a local/`make fuzz` activity. Table-driven convention: new tests use table style
with named cases; existing tests are converted only when a task already touches them
(Boy Scout rule, not a rewrite pass).

**Coverage target (D5): 70% floor on `./internal/...`, enforced in CI** by a
deterministic `make cover` threshold check (awk against `go tool cover -func`) —
no external service required. Why not 80% now: `internal/app` is deliberately
integration-covered and the mcp package includes process-spawning code whose error
paths (pipe failures) are not worth mocking an OS for. 70% is achievable in this
plan (mcp handler tests + api tests move the total from ~45% to ~72-75%); 80%
becomes realistic once the transport work adds heavily-tested code. Race detector
(`go test -race`) runs in CI on every push — `UpstreamClient` is exactly the kind
of code that needs it.

## 3. Linting and static analysis

Replace `gofmt -l` + `go vet` with **golangci-lint v2**. Enabled set and why:

- Standard defaults kept: `errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`.
- `gosec` — the project's entire purpose is security; it must pass its own
  scanner. Known intentional finding: G204 (subprocess with variable arguments) in
  `StdioTransport.Start` — that *is* the product; suppressed with a targeted
  `#nosec G204` + justification comment, not a global exclude.
- `errorlint` — the codebase relies on `errors.Is` sentinel comparisons
  (`ErrNotFound`, `ErrNotPending`); this linter keeps wrapping honest.
- `bodyclose`, `noctx` — HTTP client code in the CLI and (later) the webhook notifier.
- `sqlclosecheck`, `rowserrcheck` — hand-written `database/sql` code.
- `gocritic`, `revive`, `predeclared` — general review-grade checks; `predeclared`
  immediately flags the `new` parameter shadowing in `diff.Compare` (diff.go:65).
- `misspell`, `unconvert`, `unparam`, `copyloopvar`, `nolintlint` — hygiene;
  `nolintlint` forces every suppression to carry a reason.
- `thelper`, `tparallel` — test quality.
- Formatters: `gofumpt` + `goimports` (strict superset of gofmt).
- Deliberately **excluded**: `wrapcheck`, `exhaustive`, `prealloc`, `funlen`,
  `gocognit`, `lll` — high-noise linters whose findings this plan addresses by
  refactoring, not by annotation; and `depguard`/`gomodguard` — one dependency,
  nothing to guard.

**`govulncheck`** runs as its own CI job and `make vuln` target (it is not a
golangci linter). Security-specific additions beyond linting:
- `.github/dependabot.yml` for gomod + github-actions (weekly).
- GitHub Actions hardened: top-level `permissions: contents: read`, actions pinned
  to major versions.
- OpenSSF Scorecard action: **deferred** — valuable badge, but it wants branch
  protection and signed releases configured first; listed as follow-up, not plan scope.

## 4. Makefile

Self-documenting (`make help` parses `##` comments), grouped targets:
`help, build, run, fmt, lint, lint-fix, vuln, test, test-unit, test-race,
test-integration, fuzz, cover, cover-html, docker-build, docker-up, docker-down,
release-snapshot, clean`. Full content is in the plan (Phase 2). `lint` becomes
golangci-lint; `test` gains `-race` via `test-race`; CI calls the same targets
developers use — one source of truth for how the project is built and checked.

## 5. Clean code findings (file:line)

Framework: Clean Code (Martin) / Art of Clean Code (Mayer), review format
Critical / Significant / Minor. Only findings that change behavior-risk or
review-cost are listed; each maps to a plan task.

**Critical**

- C1 `internal/mcp/client.go:40-69` — `dispatchLoop` swallows transport errors
  (`_ = err`, line 58) and duplicates its termination condition (lines 59-62 and
  64-67). A security gateway silently discarding upstream transport errors is a
  diagnosability hole: the operator cannot distinguish "upstream crashed" from
  "upstream idle". Fix: range over frames, then drain the error channel, log the
  terminal error, fail pending calls with it.
- C2 `internal/mcp/server.go:57-79` — `serverSession.ensureStarted` never recovers
  a dead upstream. After the subprocess exits, `client` stays non-nil with
  `closed=true` and every future call returns "upstream client closed" until the
  gateway is restarted. Fail-closed is correct; *fail-closed-forever* is an
  availability bug. Fix: expose `UpstreamClient.Closed()`, and have
  `ensureStarted` discard and respawn a dead client (bounded by a restart backoff).
- C3 `internal/mcp/server.go:102-195` — `ServeHTTP` (94 lines) mixes routing, body
  decoding, session management, a triple upstream fetch, gating, and a five-way
  method dispatch: multiple abstraction levels in one function, and the reason the
  enforcement point has zero unit tests. Fix: extract `fetchAndGate` and
  per-method handlers; inject an upstream factory so tests can fake the server.
- C4 `internal/api/handlers.go:163-169` — the approve/reject endpoint ignores JSON
  decode errors (`_ = json.NewDecoder(...).Decode`) and fabricates the audit
  identity (`username = "unknown"`; `"dashboard"` at dashboard.go:170-173). For a
  tool whose product *is* the audit trail, a malformed body must be a 400, not a
  silently empty decision record. (Requiring a real username is a behavior change
  left to the user; the 400 is not.)

**Significant**

- S1 `internal/database/sqlite.go:115-214` — ~100 lines of duplicated forwarding
  (`SQLiteStore` and `txStore` each hand-forward 12 methods to the same helpers).
  DRY violation in structure; every new Store method must be written three times.
  Fix: single `queries{e execer}` value implementing the data methods once;
  `SQLiteStore`/`txStore` embed it and add only `WithTx`.
- S2 `internal/database/sqlite.go:231-253, 392-398` — `GetServerByName`,
  `GetServerByID`, `GetManifestByHash`, `GetApprovedManifest` return `nil, nil`
  for absence while `GetManifestByID` returns `ErrNotFound`: two conventions in
  one interface ("don't return null"). Every caller grows `if x == nil` checks
  (app.go:143, views.go:38,62, workflow.go:89,110). Fix: standardize on
  `ErrNotFound`; callers use `errors.Is`.
- S3 `internal/diff/diff.go:140-169` — `Summarize` omits `ChangedPrompts` and
  `ChangedResources`; the dashboard `diffView` (api/dashboard.go:38-45) drops them
  too. A prompt-argument change (a real injection vector) currently produces an
  empty "changes" list in the UI and in any future notification. This is the seam
  Design question A reuses — it must be complete first.
- S4 `internal/manifest/canonical.go:125-131` — `sortedMap` is a no-op whose name
  claims it sorts; the sorted-keys invariant actually rests on undocumented
  stdlib behavior (`encoding/json` sorting map keys). A name that lies, guarding a
  security invariant. Fix: delete the function, hoist the invariant into the
  package comment, and pin it with a regression test (keys deliberately inserted
  out of order must marshal sorted).
- S5 `internal/app/app.go:91-92` — `http.Server` without `ReadHeaderTimeout` /
  `ReadTimeout` / `IdleTimeout` on both listeners (Slowloris; gosec G112 will flag).
- S6 `internal/mcp/transport.go:78-98` — `readLoop` blocks forever on
  `t.frames <-` (cap 16) once the consumer goroutine exits, leaking a goroutine
  and pipe per dead upstream; `Close` (117-127) kills the process without waiting
  for `readLoop` to finish.
- S7 protocol-version string `"2024-11-05"` duplicated at client.go:156,
  server.go:153, cmd/server/main.go:100; manifest state literal `"REJECTED"` at
  server.go:198 instead of a shared constant. Single source of truth for both.
- S8 `internal/approval/workflow.go:75-149` — `CheckAndRecord` does canonicalize +
  hash + baseline lookup + fast path + diff + insert-if-new + warn-mode shaping.
  Extract `findOrInsertManifest` so the gate decision logic reads at one level.

**Minor**

- M1 `internal/api/dashboard.go:14-16` — `dashboardPendingRow` wraps
  `PendingManifestView` adding nothing; delete.
- M2 `internal/api/dashboard.go:175-178` — dashboard decision errors always map to
  500; the JSON API maps `ErrNotPending`→409/`ErrNotFound`→404 (handlers.go:196-205).
  Same errors, same mapping.
- M3 `cmd/gateway/cli.go:131-156` — `getJSON`/`getRaw` duplicate request/status
  handling; `fs.Parse` return ignored (line 90).
- M4 `cmd/gateway/main.go:24-37` + `cli.go:23-38` — subcommand names listed in two
  switches; one dispatch table.
- M5 `internal/diff/diff.go:65` — parameter `new` shadows the builtin.
- M6 `internal/api/views.go:36-40, 60-63` — duplicated `"unknown"` server-name
  fallback that masks referential breakage; after S2, propagate the error instead.

Not findings: the interface-heavy Store, the deliberate `#nosec`-worthy subprocess
spawn, and the blunt risk keywords are all documented design choices — the plan
does not "fix" documented intent.

## 6. README

**Keep (it is the project's best asset):** the "Why" threat model, "Partial allow"
semantics, "Manifest immutability enforced structurally", risk classification with
its honest false-positive admission, and the explicit out-of-scope list.

**Restructure for the first five minutes.** Current order buries what-do-I-run
under 100 lines of semantics. New order: one-paragraph pitch → badges → 30-second
quickstart (docker) → architecture diagram (Mermaid, replacing the ASCII art —
GitHub renders it) → install matrix (release binaries / `go install` / docker) →
"How it decides" (condensed gate semantics, linking to a full
`docs/security-model.md`) → configuration reference (env var table — currently
undocumented: `DATABASE_PATH`, `PROXY_ADDR`, `API_ADDR`, `FAIL_MODE`,
`CONFIG_PATH`, `TEMPLATES_DIR`, `MCP_SHIELD_API`) → CLI → known limitations →
contributing/security/license pointers. The 85-line manual-testing walkthrough
moves to `docs/manual-testing.md` (it is contributor documentation, not adopter
documentation).

**Missing and added:** CI/Go Report Card/license/release badges; install paths;
client compatibility statement (what works today: HTTP JSON-RPC clients; what does
not: Claude Desktop stdio — until Phase 9); deployment warning that :8081 has no
auth and must not be exposed; versioning statement (SemVer, 0.x instability);
links to CONTRIBUTING/SECURITY/LICENSE.

## 7. Repository hygiene

- **LICENSE** — Apache-2.0 (D1, rationale above).
- **SECURITY.md** — private disclosure via GitHub Security Advisories, 90-day
  coordinated window, supported-versions table (latest minor only), and — because
  this project is a security control — an explicit list of what counts as a
  vulnerability here: gate bypass, canonicalization instability/collision,
  approval state-machine bypass, manifest mutation after insert, blocked-tool
  invocation succeeding. That list doubles as a fuzzing/audit guide.
- **CONTRIBUTING.md** — dev setup, make targets, test expectations (new code needs
  tests; gate semantics changes need design discussion first), PR checklist. No
  CLA, no DCO (solo maintainer, Apache-2.0 inbound=outbound).
- **CODE_OF_CONDUCT.md** — Contributor Covenant 2.1, contact = maintainer email.
- **Issue templates** (bug/feature as YAML forms, config pointing security reports
  to the advisory flow, not public issues) + **PR template**.
- **CHANGELOG.md** — Keep a Changelog format; release automation appends
  GitHub-generated notes; the file is the human-curated summary.
- **Not doing:** GOVERNANCE.md, FUNDING.yml, CODEOWNERS, roadmap documents —
  boilerplate that signals a fictitious organization around a solo project.

## 8. GitHub Actions

**CI (`ci.yml`)** on push to main + PRs: jobs `lint` (golangci-lint-action),
`test` (unit + `-race` + coverage threshold, ubuntu; macOS in the weekly schedule
only — the code has no OS-specific paths, and macOS runners are 10x the cost for
near-zero signal here), `integration` (`make build` + tagged tests),
`fuzz-smoke` (15s per target), `govulncheck` (also on weekly schedule so new CVEs
surface without a push). All jobs use the same make targets as local dev.

**Release (`release.yml`)** on `v*` tags: **GoReleaser** — chosen over hand-rolled
because CGO_ENABLED=0 (pure-Go SQLite) makes cross-compilation config-only, and
GoReleaser gives archives, checksums, changelog, GitHub Release, Docker images,
and multi-arch manifests from one config file that `make release-snapshot` can
validate locally. Hand-rolled matrix builds would re-implement all of that in YAML
with no advantage. Targets: linux/darwin/windows × amd64/arm64 binaries;
linux amd64+arm64 Docker images pushed to `ghcr.io/ericmarcantonio/mcp-shield`
(D4) with a multi-arch manifest tagged `latest` + `vX.Y.Z`. Consumption: README
documents `docker pull`, and `docker-compose.yml` gains an
`image: ghcr.io/...` reference with local `build:` kept for development. A
`--version` flag is added to the binary (ldflags) so releases are identifiable.

---

## Design question A — Notifications (decision D2)

**Problem.** The gate fails closed: a new/changed tool is silently withheld until a
human approves. If no human watches the dashboard, the observable failure is "my
AI client's tool vanished", with no signal to the approver. The notification path
is therefore availability-critical but must never become integrity-critical: a
broken notifier must not block, delay, or crash the gate.

**Existing seam:** `diff.Summarize` (internal/diff/diff.go:140) was extracted
precisely so notifiers can reuse human-readable diff lines. (It must first be
completed — see finding S3.)

### Option A1 — Transactional outbox + generic HMAC-signed webhook (recommended)

New `internal/notify` package. `approval.Workflow.CheckAndRecord` writes a
notification row into a `notification_outbox` SQLite table *in the same
transaction* that inserts a new PENDING manifest (and optionally on
approve/reject, config-gated). A background dispatcher polls the outbox and
POSTs JSON to configured webhook URLs.

- **Delivery guarantee:** at-least-once. The outbox row is only marked delivered
  after a 2xx; crash between insert and delivery replays on restart. Receivers
  deduplicate on `event_id` (the outbox row id + manifest hash).
- **Retry:** exponential backoff (1m, 5m, 25m, 2h, 12h, then daily; configurable
  cap), persisted in `attempts`/`next_attempt_at` columns. Permanent failures stay
  in the table, visible via a `GET /api/notifications/failed` endpoint — silent
  notification death is the exact failure mode this feature exists to remove.
- **Ordering:** per-server, by outbox id (monotonic). No global ordering promise —
  documented. Event volume is naturally bounded: `CheckAndRecord` inserts a
  manifest row (and thus an event) only the *first* time a hash is seen
  (workflow.go:110-137), so a flapping upstream cannot cause a notification storm
  for the same manifest.
- **Payload:** versioned JSON: `{"schema":1,"event":"manifest.pending","event_id":...,
  "server":...,"manifest_id":...,"hash":...,"risk":...,"changes":[diff.Summarize lines],
  "dashboard_url":...,"created_at":...}`.
- **Secrets:** webhook URL + HMAC secret come from a config file
  (`config/notify.json`, gitignored, 0600) or env; URLs never logged (log the
  target's configured *name*, not the URL — Slack webhook URLs are
  capability-bearing secrets).
- **Outbound authenticity:** `X-MCPShield-Signature: sha256=HMAC(secret, timestamp||"."||body)`
  and `X-MCPShield-Timestamp`; receivers reject skew > 5 minutes → replay
  protection. This is the Stripe/GitHub webhook pattern; verification is a
  ten-line snippet in the docs.
- **Isolation:** dispatcher is a goroutine with its own context; enqueue is an
  INSERT in an existing transaction (microseconds, no network); HTTP timeouts
  hard-capped (10s); a panicking notifier is recovered and logged. There is no
  code path by which notification failure can reject or delay a gate decision.
- **Slack/Discord today:** both accept generic JSON webhooks; a per-target
  `format: "slack"` option renders the same event through a Slack-blocks adapter.
  No Slack SDK.

*Tradeoffs:* one new table + poller (~600 lines with tests); polling latency
(1-5s tick — irrelevant for a human approval loop); email/desktop not covered
directly (users bridge via the webhook; native channels can be added later behind
the same `Notifier` interface).

### Option A2 — Native multi-channel notifiers (Slack API, SMTP, desktop)

A `Notifier` interface with concrete Slack-API, email, and desktop
implementations, fan-out on event.
*Pros:* first-class UX per channel. *Cons:* SMTP config surface (auth, TLS,
templates) and per-OS desktop integration (dbus/osascript/toast) are large,
platform-fragile maintenance loads; desktop notifications assume the approver is
on the same machine as the gateway, which contradicts the docker-first deployment;
and every channel still needs A1's retry/outbox machinery underneath to be
trustworthy. This is where the project goes after the webhook core exists —
not where it starts.

### Option A3 — Emit structured events, let users bridge

Write events as structured logs / an events endpoint; users wire
shoutrrr/Apprise/webhook-relay themselves.
*Pros:* near-zero code, zero delivery liability. *Cons:* "run a second daemon to
find out your gate blocked something" fails the 10,000-adopter test; delivery
guarantees are outsourced to tools the project doesn't control; and the failure
mode (nobody configured the bridge) is identical to today's problem.

**Recommendation: A1**, with the dispatcher's send path behind a
`Notifier` interface (`Notify(ctx, Event) error`) so A2 channels can be
contributed later without touching the outbox. **User must confirm before Phase 8
executes.**

---

## Design question B — Upstream / transport strategy (decision D3)

**Spec facts (verified 2026-07-25 against modelcontextprotocol.io):**
- Current stable spec is **2025-11-25**. It defines exactly two standard
  transports: **stdio** (newline-delimited JSON-RPC over stdin/stdout — what
  mcp-shield already speaks upstream) and **Streamable HTTP** (single MCP
  endpoint supporting POST and GET; responses either `application/json` or an
  SSE stream; optional `MCP-Session-Id`; mandatory `MCP-Protocol-Version` header
  on subsequent requests; servers MUST validate `Origin` against DNS rebinding
  and SHOULD bind localhost when local).
- The old **HTTP+SSE transport (2024-11-05) is deprecated**, replaced by
  Streamable HTTP in 2025-03-26. Building it new in 2026 would be building to a
  deprecated spec — ruled out.
- A **2026-07-28 release candidate** (ships days from now) makes the protocol
  stateless: no initialize handshake, no session header, `Mcp-Method`/`Mcp-Name`
  routing headers, `ttlMs` cache metadata on list results. Do not build to the RC
  yet, but the transport seam should not bake in session-forever assumptions.
- mcp-shield currently hardcodes protocol version `"2024-11-05"` in three places
  (finding S7) and does no version negotiation.

### Option B1 — stdio shim binary (recommended first)

`mcp-shield connect <server> [--gateway URL]` (a subcommand, not a new binary —
one artifact to install): Claude Desktop spawns it via its classic
`command`/`args` config; it reads newline-delimited JSON-RPC on stdin, forwards
each request as `POST {gateway}/mcp/{server}`, writes responses to stdout.
~200 lines reusing the existing framing conventions.
*Pros:* unlocks the single most-cited limitation (README names it) in days;
zero new protocol surface on the gateway; the gate path is untouched.
*Cons:* still one-request-per-HTTP-call; no server-initiated notifications
(acceptable: the gateway re-fetches and re-gates on every call by design, so
`listChanged` push semantics are intentionally absent); the shim must handle
requests concurrently (Claude Desktop pipelines) — goroutine per request with a
stdout write mutex.

### Option B2 — Spec-conformant Streamable HTTP, both sides (recommended second)

Client-facing: make `/mcp/{server}` a real 2025-11-25 MCP endpoint — POST with
`Accept` negotiation (JSON response mode first; SSE streaming mode can 405 GET
initially, which the spec permits), `MCP-Protocol-Version` handling, proper
initialize lifecycle, **mandatory Origin validation and localhost-binding
defaults** (a Zero-Trust gateway that is itself DNS-rebindable would be an
embarrassment — this is a security requirement, not a feature).
Upstream-facing: a `StreamableHTTPTransport` implementing the existing
`mcp.Transport` interface so the gateway can also *proxy to remote* MCP servers
(today it can only spawn local subprocesses — upstream-side HTTP is what makes
mcp-shield useful against third-party hosted servers, which is exactly where the
rug-pull threat is highest).
*Pros:* native support for Claude Code / Desktop custom connectors and every
spec-conformant client; positions the gateway for the remote-server ecosystem.
*Cons:* honest effort is **2-4 weeks**: session management, version negotiation,
Origin/auth hardening, conformance testing against real clients, and a decision
about GET/SSE support. This is a sub-project; it gets its own plan document when
its turn comes (per plan Phase 9b).

### Option B3 — Status quo, documented

Keep bare HTTP JSON-RPC, document client requirements better.
*Pros:* zero work. *Cons:* the README itself calls this the biggest limitation;
Claude Desktop users — the largest MCP population — stay locked out. Rejected as
an end-state; it is simply what exists between phases.

**Cross-cutting concerns (apply to whichever option runs):**
- **Multi-upstream fan-out** (one endpoint aggregating several upstreams with
  prefixed tool names): explicitly **deferred and documented as a non-goal** for
  now. It multiplies approval identity questions (whose manifest is pending?) and
  namespace collision handling for a use case per-server endpoints already serve.
- **Upstream lifecycle:** finding C2 (dead upstream is dead forever) is fixed in
  Phase 5 regardless of transport choice: liveness check + respawn with
  exponential backoff (1s..30s cap) and a circuit-breaker after N consecutive
  failures (fail closed with a clear JSON-RPC error naming the upstream state).
- **Timeouts:** per-upstream-call timeout (default 30s, env-configurable) wrapped
  around `UpstreamClient.Call`; HTTP server timeouts from finding S5.

**Recommendation: B1 in this plan (Phase 9a), B2 as the immediately following
sub-project with its own plan (Phase 9b outline included).** **User must confirm
before Phase 9 executes.**

---

## Explicitly not doing (and why)

- `pkg/` public library API — no consumer, pre-1.0 (Structure §1).
- Kubernetes manifests / Helm chart — target adopter runs docker-compose or a
  binary; the README's own out-of-scope list already says this.
- Codecov/Coveralls as required CI — deterministic in-repo gate instead; badge
  can come later.
- OpenSSF Scorecard, signed releases (cosign), SBOM attestation — real value,
  sequenced after the basics; noted in CONTRIBUTING as follow-ups. `manifest.Hash`
  remains the documented seam for signing.
- API authentication on :8081 — documented deployment constraint + good-first-issue,
  not silent scope creep into auth design.
- Email/desktop notification channels — see Option A2.
- Multi-upstream aggregation — see Design question B cross-cutting concerns.

## Effort summary (honest)

| Phase | Content | Effort |
|---|---|---|
| 1 | Hygiene files, license, templates | 0.5 day |
| 2 | golangci-lint + fixes, govulncheck, Makefile | 0.5-1.5 days (lint-fix surface is the variable) |
| 3 | CI workflows | 0.5 day |
| 4 | Test debt: mcp/api/database tests, fuzz targets, coverage gate | 2-3 days |
| 5 | Clean-code refactors C1-C4, S1-S8, minors | 2-3 days |
| 6 | cmd renames, README overhaul, docs split, diagram | 1 day |
| 7 | GoReleaser, ghcr images, version flag, compose | 1-2 days |
| 8 | Notifications (gated D2) | 3-5 days |
| 9a | stdio shim (gated D3) | 2-4 days |
| 9b | Streamable HTTP both sides (gated D3) | **2-4 weeks — separate plan** |

Core professionalization (1-7): roughly two working weeks. Phases 1-7 are
sequenced so every phase ends green and merges independently.
