package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Signature headers. The scheme is the Stripe/GitHub webhook pattern:
// sha256=hex(HMAC-SHA256(secret, timestamp + "." + body)). Binding the
// timestamp into the MAC is what makes the skew check meaningful — an
// attacker replaying a captured body cannot move it forward in time
// without invalidating the signature.
const (
	HeaderSignature = "X-MCPShield-Signature"
	HeaderTimestamp = "X-MCPShield-Timestamp"
)

// MaxClockSkew is how far a payload's timestamp may be from the receiver's
// clock, in either direction. Beyond it the payload is a replay (or the
// clocks disagree badly enough that replay protection is not working).
const MaxClockSkew = 5 * time.Minute

// MaxRequestTimeout is the hard ceiling on one delivery attempt. It is a
// cap, not a default: no configuration can raise it, because a target that
// can hold a connection open indefinitely is a target that can starve the
// dispatcher.
const MaxRequestTimeout = 10 * time.Second

// maxResponseBytes bounds how much of a receiver's response is read. The
// body is drained only to let the connection be reused; none of it is
// interpreted, so a receiver cannot make the gateway allocate on demand.
const maxResponseBytes = 4 << 10

// ErrSignatureInvalid is returned by VerifySignature for any payload that
// fails authentication, whatever the reason. Receivers should treat every
// failure identically; distinguishing "bad MAC" from "stale" tells an
// attacker which half to work on.
var ErrSignatureInvalid = errors.New("notify: signature verification failed")

// Webhook POSTs events to one HTTP endpoint. It is safe for concurrent use.
type Webhook struct {
	name    string
	url     string
	secret  string
	format  string
	timeout time.Duration
	client  *http.Client
}

// NewWebhook builds a target from its configuration. The timeout is fixed
// at MaxRequestTimeout; the field exists so tests can shorten it, never so
// configuration can lengthen it.
func NewWebhook(cfg WebhookConfig) *Webhook {
	return &Webhook{
		name:    cfg.Name,
		url:     cfg.URL,
		secret:  cfg.Secret,
		format:  cfg.Format,
		timeout: MaxRequestTimeout,
		client:  &http.Client{},
	}
}

// NewWebhooks builds one target per configured webhook.
func NewWebhooks(cfgs []WebhookConfig) []Notifier {
	targets := make([]Notifier, 0, len(cfgs))
	for _, cfg := range cfgs {
		targets = append(targets, NewWebhook(cfg))
	}
	return targets
}

func (w *Webhook) Name() string { return w.name }

// Notify delivers one event. Every error it returns names the target and
// omits the URL: a Slack or Discord webhook URL grants anyone holding it the
// ability to post, and error strings end up in logs and issue trackers.
func (w *Webhook) Notify(ctx context.Context, ev Event) error {
	body, err := w.render(ev)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, w.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: webhook %q: build request: %w", w.name, redactURL(err, w.url))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "mcp-shield")
	w.sign(req, body, time.Now())

	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: webhook %q: post failed: %w", w.name, redactURL(err, w.url))
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("notify: webhook %q: received HTTP %d", w.name, resp.StatusCode)
	}
	return nil
}

// render turns the event into the bytes this target expects.
func (w *Webhook) render(ev Event) ([]byte, error) {
	if w.format == FormatSlack {
		return slackBody(ev)
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("notify: webhook %q: marshal event: %w", w.name, err)
	}
	return body, nil
}

// sign attaches the timestamp and signature headers. A target with no
// configured secret is sent unsigned rather than signed with an empty key:
// a signature anyone can compute is worse than none, because it looks like
// authentication.
func (w *Webhook) sign(req *http.Request, body []byte, now time.Time) {
	if w.secret == "" {
		return
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	req.Header.Set(HeaderTimestamp, timestamp)
	req.Header.Set(HeaderSignature, "sha256="+hex.EncodeToString(signature(w.secret, timestamp, body)))
}

// signature computes HMAC-SHA256 over timestamp + "." + body.
func signature(secret, timestamp string, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return mac.Sum(nil)
}

// VerifySignature authenticates a received payload: constant-time MAC
// comparison plus a MaxClockSkew freshness window. It lives here, exported
// and tested, so the verification snippet in docs/notifications.md is code
// that runs in CI rather than prose nobody checked.
//
// timestamp and signature are the raw header values; now is the receiver's
// clock (time.Now() in production, fixed in tests).
func VerifySignature(secret, timestamp string, body []byte, signatureHeader string, now time.Time) error {
	hexMAC, ok := strings.CutPrefix(signatureHeader, "sha256=")
	if !ok {
		return ErrSignatureInvalid
	}
	received, err := hex.DecodeString(hexMAC)
	if err != nil {
		return ErrSignatureInvalid
	}
	if !hmac.Equal(received, signature(secret, timestamp, body)) {
		return ErrSignatureInvalid
	}

	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrSignatureInvalid
	}
	if skew := now.Sub(time.Unix(seconds, 0)); skew > MaxClockSkew || skew < -MaxClockSkew {
		return ErrSignatureInvalid
	}
	return nil
}

// redactURL strips the target URL out of an error produced by net/http,
// whose messages embed the full URL by construction.
func redactURL(err error, url string) error {
	if url == "" {
		return err
	}
	redacted := strings.ReplaceAll(err.Error(), url, "<redacted-url>")
	return errors.New(redacted)
}

// slackBody renders an event as Slack's (and Discord's) simple text
// webhook payload. This is a formatting adapter, not an integration: no
// Slack SDK, no API token, no dependency.
func slackBody(ev Event) ([]byte, error) {
	var text strings.Builder
	fmt.Fprintf(&text, "*mcp-shield*: %s for server `%s`\n", ev.Event, ev.Server)
	fmt.Fprintf(&text, "Manifest %d (`%s`)\n", ev.ManifestID, shortHash(ev.Hash))
	if len(ev.Changes) == 0 {
		text.WriteString("No capability changes recorded.\n")
	}
	for _, line := range ev.Changes {
		text.WriteString("• " + line + "\n")
	}
	if ev.DashboardURL != "" {
		text.WriteString("Review: " + ev.DashboardURL)
	}

	body, err := json.Marshal(map[string]string{"text": text.String()})
	if err != nil {
		return nil, fmt.Errorf("notify: marshal slack body: %w", err)
	}
	return body, nil
}

func shortHash(hash string) string {
	const displayed = 12
	if len(hash) <= displayed {
		return hash
	}
	return hash[:displayed]
}
