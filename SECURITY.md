# Security Policy

mcp-shield is a security control: it gates which MCP tools, prompts, and
resources a client can see and call. A bug that lets an unapproved
capability through the gate is a security bug, not an ordinary bug. Please
report it privately.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting:
**https://github.com/EricMarcantonio/mcp-shield/security/advisories/new**

Do not open a public issue for anything you believe is exploitable. This is
a solo-maintained project, so there's no guaranteed response time — you'll
get an acknowledgment as soon as I see the report.

## What counts as a vulnerability here

Anything that breaks one of the guarantees documented in `README.md`,
including:

- **Gate bypass** — any way for a tool, prompt, or resource that is not in
  the approved baseline to be listed to a client or invoked upstream while
  `FAIL_MODE=block`.
- **Canonicalization instability** — two semantically identical capability
  sets producing different hashes, or two different capability sets
  producing the same canonical bytes.
- **Approval state-machine bypass** — any state transition other than
  PENDING→APPROVED, PENDING→REJECTED, APPROVED→SUPERSEDED.
- **Manifest mutation** — any way to alter a manifest row's hash or
  canonical JSON after it's inserted.
- **Audit-trail forgery or loss** — an approval or rejection recorded
  without a corresponding audit row, or an audit row that can be altered.

## Out of scope

- The approval API and dashboard (`:8081`) have no authentication in the
  current version — this is a documented deployment constraint, not a bug.
  Deployments must keep `:8081` off any network a client can't already
  reach. A report that amounts to "the dashboard is reachable" is a
  configuration issue.
- Vulnerabilities in an upstream MCP server that mcp-shield proxies to —
  report those to that project instead.
