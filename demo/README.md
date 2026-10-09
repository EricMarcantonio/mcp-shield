# demo/

Fixtures for the `docker compose` stack, so `docker compose up` gates
something real without any setup first.

- `servers.json` — points the gateway at the bundled fake MCP server
  (`/app/bin/mcp-shield-testserver`, already in the image).
- `testserver-tools.json` — the tool set that server advertises. It is
  re-read on **every** `tools/list` call, so editing it changes what the
  upstream advertises on the next request. That is the knob for watching the
  gate withhold a capability change.

These are demo fixtures, deliberately committed. Real deployments keep their
own config in `config/` (`servers.json` and `notify.json` there are
gitignored, because a notification config holds webhook URLs and HMAC
secrets).

## Walkthrough

```sh
docker compose up -d

# First connect: no approved baseline, so nothing is exposed and a PENDING
# manifest appears.
curl -s -X POST localhost:8080/mcp/calendar \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
curl -s localhost:8081/api/manifests/pending

# Approve it, and both tools start flowing.
curl -s -X POST localhost:8081/api/manifests/1/approve \
  -H 'Content-Type: application/json' -d '{"username":"you","reason":"baseline"}'

# Now add a tool to testserver-tools.json — say execute_command — and connect
# again. The two approved tools keep working; only the new one is withheld,
# and a second PENDING manifest appears naming exactly what changed.
```

## If an edit does not seem to take effect

On Docker Desktop for macOS and Windows, a bind mount is cached inside the
running container, so a host edit can take up to a minute to become visible
to a process that is already running. If a `tools/list` still returns the old
set, either wait, or force a fresh mount:

```sh
docker compose restart gateway
```

This is a Docker Desktop bind-mount property, not gateway behaviour: the
gateway re-fetches the upstream's full capability set on **every** request
and never caches an approval decision.

## Note

`tools/list` responses are gated per tool name, and the gateway refuses a
capability set that advertises the same tool name twice — a fingerprint it
could not enforce. If you hand-edit this file, do not duplicate a `name`, or
requests fail closed with `tool "..." advertised more than once`.
