# Contributing to mcp-shield

Thanks for your interest. mcp-shield is a small, security-focused codebase;
contributions are welcome, and changes to gate semantics get extra scrutiny.

## Development setup

Requires Go 1.25+.

    git clone https://github.com/EricMarcantonio/mcp-shield
    cd mcp-shield
    make build          # binaries in bin/
    make test           # unit + integration
    make lint           # golangci-lint (install: see below)

Install tooling:

    go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
    go install golang.org/x/vuln/cmd/govulncheck@latest

Run `make help` to see all targets.

## Ground rules

- **New code needs tests.** Table-driven where there are multiple cases.
- **Changes to gate semantics** (anything in `internal/approval`,
  `internal/diff`'s risk classification, or the blocking behavior in
  `internal/mcp`) need an issue discussing the design *before* a PR.
- **No new runtime dependencies** without prior discussion — the project
  deliberately has one (`modernc.org/sqlite`).
- Run `make lint test` before pushing; CI runs the same targets.
- Security issues go through [SECURITY.md](SECURITY.md), never public issues.

## Pull requests

- Keep PRs focused; one logical change per PR.
- Describe *why*, not just *what*.
- CI must be green.

## Good first contributions

- Additional `Store` backends (the `database.Store` interface is the seam).
- Docs and examples for real-world MCP server configs.

## License

By contributing you agree your contributions are licensed under the
repository's [LICENSE](LICENSE) (inbound = outbound). No CLA.
