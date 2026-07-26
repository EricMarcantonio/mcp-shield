#!/usr/bin/env bash
# coverage-gate.sh enforces a minimum combined statement-coverage floor
# across ./internal/... (decision D5, docs/superpowers/specs/2026-07-25-oss-hardening-design.md).
#
# It is a standalone script rather than a Makefile target or CI step so it
# can be wired into either without touching files another workstream owns
# concurrently:
#   - Makefile: add a target that just runs this script, e.g.
#       cover:
#           ./scripts/coverage-gate.sh
#   - CI: call `./scripts/coverage-gate.sh` (or `make cover`, once that
#     target exists) as its own step, after `go test ... -race`.
#
# Usage:
#   scripts/coverage-gate.sh              # 70% floor, ./internal/...
#   COVERAGE_FLOOR=80 scripts/coverage-gate.sh
#   scripts/coverage-gate.sh ./internal/... ./cmd/...
set -euo pipefail

floor="${COVERAGE_FLOOR:-70}"
packages=("$@")
if [ "${#packages[@]}" -eq 0 ]; then
	packages=("./internal/...")
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

profile="$(mktemp -t mcp-shield-coverage.XXXXXX)"
trap 'rm -f "$profile"' EXIT

# go test -coverprofile prints one "ok  pkg  coverage: NN.N% of statements"
# line per package on its own — that's the per-package view. Coverage is
# still emitted for a package with a failing (or deliberately skipped) test,
# so this gate does not need to special-case any of the known-bug
# documentation tests left in the suite (see
# docs/superpowers/plans/2026-07-25-oss-hardening.md, Phase 4). A genuinely
# broken test suite still fails the build here, because `go test` exits
# non-zero and `set -e` stops the script before the floor is even checked.
go test -coverprofile="$profile" -covermode=atomic "${packages[@]}"

# total_line looks like: "total:  (statements)   75.1%"
total_pct="$(go tool cover -func="$profile" | tail -1 | awk '{ print $NF }' | tr -d '%')"

echo
echo "==> total coverage: ${total_pct}% (floor: ${floor}%)"

if ! awk -v total="$total_pct" -v floor="$floor" 'BEGIN { exit !(total >= floor) }'; then
	echo "FAIL: coverage ${total_pct}% is below the ${floor}% floor" >&2
	exit 1
fi

echo "PASS: coverage ${total_pct}% meets the ${floor}% floor"
