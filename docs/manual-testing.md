# Manual testing (Docker)

This walkthrough exercises the approval pipeline end to end; it is aimed at
contributors verifying gate behavior by hand, not at operators (for that,
see the [Quickstart](../README.md#quickstart) in the README).

Full setup:

```sh
cp config/servers.example.json config/servers.json
cp config/testserver-tools.example.json config/testserver-tools.json
make docker-build
make docker-up
```

`config/servers.json` points the gateway at the fake test server with
`TOOLS_FILE=/config/testserver-tools.json` — that file is bind-mounted
(`./config:/config:ro`) so edits on your host take effect on the *next*
request, no restart needed (the test server re-reads and re-parses the
file on every `tools/list` call).

Trigger a connect — first-ever connect has no approved baseline, so
`tools/list` comes back with an empty `tools` array (not an error) and
creates a PENDING manifest:

```sh
curl -s -X POST localhost:8080/mcp/calendar -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
curl -s localhost:8081/api/manifests/pending
```

Approve it so a baseline exists — `tools/list` now returns both tools:

```sh
curl -s -X POST localhost:8081/api/manifests/1/approve -d '{"username":"you"}'
curl -s -X POST localhost:8080/mcp/calendar -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

Now edit `config/testserver-tools.json` by hand — this is the "modify the
schema and watch the proxy react" step. Try each of these and re-run the
`tools/list` curl above after each edit. Watch two things: a fresh PENDING
manifest appears in `/api/manifests/pending`, **and** `tools/list` still
returns the tools you didn't touch — only the new/changed one drops out:

- **Add a field to an existing tool's `inputSchema`** (e.g. add
  `"notes": {"type": "string"}` under `calendar_create`'s `properties`) →
  `calendar_create` itself drops out of `tools/list` until approved,
  `calendar_read` is unaffected.
- **Change a tool's `description` only** → same partial effect.
- **Add a new tool object** named e.g. `upload_receipt` or
  `delete_event` → the two existing tools keep working, only the new one
  is withheld. Confirm with `tools/call`:
  ```sh
  curl -s -X POST localhost:8080/mcp/calendar -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"calendar_read"}}'   # succeeds
  curl -s -X POST localhost:8080/mcp/calendar -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"upload_receipt"}}'  # blocked
  ```
  Reject it (`curl -X POST localhost:8081/api/manifests/{id}/reject -d '{"username":"you"}'`)
  and re-run both calls — `calendar_read` still works, `upload_receipt`
  still blocked. That's the point: a rejected change never takes down
  what you already approved.
- **Remove a tool entirely** from the array → shows up in `removed_tools`
  in the diff (`curl localhost:8081/api/manifests/{id}/diff`); it just
  stops appearing in `tools/list` since the upstream server no longer
  offers it — nothing to approve or reject.

You can also drive this from the dashboard at `http://localhost:8081/`
instead of curl — approve/reject buttons are right there next to the diff.

To reset and start clean: `make docker-down`, delete `data/mcp.db*`,
`make docker-up` again.
