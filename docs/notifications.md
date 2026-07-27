# Notifications

mcp-shield fails closed. When an upstream server advertises a new or changed
capability, that capability is withheld until a human approves it. If nobody
is watching the dashboard, the only symptom is "my AI client's tool
disappeared" — with no signal to the person who could approve it.

Notifications remove that failure. When the gate records a new PENDING
manifest, mcp-shield POSTs a signed JSON event to the webhook targets you
configure.

## Enabling

Copy the example config and point the gateway at it:

```bash
cp config/notify.example.json config/notify.json
chmod 600 config/notify.json
```

`config/notify.json` is gitignored. It holds webhook URLs and HMAC secrets —
**a Slack or Discord webhook URL is itself a credential**: anyone holding it
can post to your channel. mcp-shield never logs a webhook URL; it logs the
target's configured `name`. Do the same in whatever you build around it.

The path is `NOTIFY_CONFIG_PATH` (default `config/notify.json`). **A missing
file disables notifications** — that is the default, and it is not an error.
A file that exists but is unusable (no targets, an unset environment
variable, an unknown `format`) is a startup error: a target that looks
configured and silently reaches nobody is the exact failure this feature
exists to remove.

### Configuration

```json
{
  "dashboard_url": "http://localhost:8081",
  "events": ["manifest.pending"],
  "max_attempts": 6,
  "webhooks": [
    {
      "name": "ops-slack",
      "url": "$MCP_SHIELD_SLACK_WEBHOOK",
      "secret": "$MCP_SHIELD_WEBHOOK_SECRET",
      "format": "slack"
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `dashboard_url` | Externally reachable base URL of the approval dashboard. Used to build the deep link in each event; omitted from the payload when empty. |
| `events` | Event types to deliver. Default `["manifest.pending"]`. Also available: `manifest.approved`, `manifest.rejected`. |
| `max_attempts` | Delivery attempts before a target is given up on and the event becomes visible as permanently failed. Default 6. |
| `webhooks[].name` | Identifies the target in logs, errors, and the failed-notification view. Required — and the reason errors never need the URL. |
| `webhooks[].url` | Target endpoint. `os.ExpandEnv` is applied, so `$VAR` keeps the credential out of the file. |
| `webhooks[].secret` | HMAC signing key. `os.ExpandEnv` is applied. Empty means requests are sent **unsigned** (see below). |
| `webhooks[].format` | `""` for the raw JSON event, `"slack"` for a Slack/Discord-compatible `{"text": ...}` rendering. |

Slack and Discord both accept generic JSON webhooks, so `"format": "slack"`
covers both. There is no Slack SDK dependency.

## Payload

```json
{
  "schema": 1,
  "event": "manifest.pending",
  "event_id": 41,
  "server": "calendar",
  "manifest_id": 42,
  "hash": "9f2c...",
  "changes": ["Added tool: upload_attachment", "Schema changed: send_email"],
  "dashboard_url": "http://localhost:8081/manifests/42",
  "created_at": "2026-07-25T12:00:00Z"
}
```

`schema` is the payload version. Reject payloads whose schema you do not
recognise rather than guessing at them.

`changes` is rendered by `diff.Summarize` and covers added, removed, and
changed tools, prompts, and resources.

## Delivery semantics

**At-least-once.** The event is written to a `notification_outbox` table in
the *same database transaction* that inserts the PENDING manifest, so the
event exists if and only if the manifest does. A crash between "gate withheld
something" and "human was told" replays on restart. A row is marked delivered
only after a 2xx response, so a crash between a successful POST and that mark
produces a **redelivery**.

**Deduplicate on `event_id`.** It is the outbox row id, stable across
redeliveries of the same event. A receiver that has already acted on an
`event_id` should acknowledge with 2xx and do nothing else.

**Ordering** is per-server, ascending by `event_id`. There is no global
ordering promise across servers.

**Retry** uses exponential backoff, persisted so it survives a restart:

| Attempt | Next try after |
| --- | --- |
| 1 | 1 minute |
| 2 | 5 minutes |
| 3 | 25 minutes |
| 4 | 2 hours |
| 5 | 12 hours |
| 6+ | 24 hours |

After `max_attempts` failures the dispatcher stops retrying — but it does not
forget. The event stays queryable:

```bash
curl -s localhost:8081/api/notifications/failed | jq
```

Each entry carries `attempts` and `last_error`. Check it; a target failing
silently forever is the thing this feature exists to prevent.

**Isolation.** None of this touches a gate decision. Enqueue is one INSERT
inside a transaction that was already open. Delivery happens in a background
goroutine with its own context, each attempt hard-capped at 10 seconds. A
target that is slow, broken, or hostile cannot block, delay, or crash the
gate — that property is covered by tests (`TestGateDecisionIsUnaffectedByA
HungNotificationTarget`, `TestNotifierPanicIsContained`).

## Verifying the signature

Every request carries:

```
X-MCPShield-Timestamp: 1785024000
X-MCPShield-Signature: sha256=<hex HMAC-SHA256>
```

The MAC is computed over `timestamp + "." + rawBody` with your configured
secret — the Stripe/GitHub webhook pattern. Binding the timestamp into the
MAC is what makes the freshness check meaningful: an attacker replaying a
captured body cannot move it forward in time without invalidating the
signature.

**Receivers must reject a timestamp more than 5 minutes from their own clock,
in either direction**, and must compare MACs in constant time. Verify against
the **raw request bytes**, before any JSON parsing or re-serialization.

### Go

```go
func verify(secret, timestamp string, body []byte, sigHeader string) bool {
	hexMAC, ok := strings.CutPrefix(sigHeader, "sha256=")
	if !ok {
		return false
	}
	received, err := hex.DecodeString(hexMAC)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	if !hmac.Equal(received, mac.Sum(nil)) {
		return false
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	skew := time.Since(time.Unix(seconds, 0))
	return skew <= 5*time.Minute && skew >= -5*time.Minute
}
```

This is `notify.VerifySignature` in `internal/notify/webhook.go`, which is
exported and covered by tests so the snippet above is code that runs in CI
rather than prose nobody checked.

### Python

```python
import hashlib, hmac, time

def verify(secret: str, timestamp: str, body: bytes, sig_header: str) -> bool:
    if not sig_header.startswith("sha256="):
        return False
    expected = hmac.new(
        secret.encode(), timestamp.encode() + b"." + body, hashlib.sha256
    ).hexdigest()
    if not hmac.compare_digest(sig_header[len("sha256="):], expected):
        return False
    return abs(time.time() - int(timestamp)) <= 300
```

### Shell

Given a saved body and the two header values:

```bash
printf '%s.%s' "$TIMESTAMP" "$(cat body.json)" \
  | openssl dgst -sha256 -hmac "$MCP_SHIELD_WEBHOOK_SECRET" -hex
```

Compare the output to the hex after `sha256=`. (Use `printf`, not `echo` —
a trailing newline changes the MAC.)

## Unsigned targets

A target with no `secret` is sent **unsigned**: no signature header at all.
That is deliberate — signing with an empty key produces a signature anyone
can compute, which is worse than none because it looks like authentication.
Leave the secret empty only for a receiver on a trusted local socket.
