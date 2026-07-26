# Security model

This is the full detail behind the summaries in the README's "How it works"
and "Risk classification" sections: the exact gate semantics, the
structural guarantees around manifest immutability and fail-closed
behavior, and the precedence rules for risk classification.

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

Nothing in the roadmap changes this keyword list or the fact that it's a
blunt substring match; see the design doc's assumptions if you're
wondering whether that's an oversight (it isn't).
