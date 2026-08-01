# demo/

Fixtures for the `docker compose` stack, so `docker compose up` gates
something real without any setup first.

- `servers.json` — points the gateway at the bundled fake MCP server
  (`/app/bin/mcp-shield-testserver`, already in the image).
- `testserver-tools.json` — the tool set that server advertises. It is
  re-read on **every** `tools/list` call, so editing it here changes what
  the upstream advertises on the next request, with no restart. That is the
  knob for watching the gate withhold a capability change.

These are demo fixtures, deliberately committed. Real deployments keep their
own config in `config/` (`servers.json` and `notify.json` there are
gitignored, because a notification config holds webhook URLs and HMAC
secrets).
