package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sampleEvent() Event {
	return Event{
		Schema:       SchemaVersion,
		Event:        "manifest.pending",
		EventID:      7,
		Server:       "calendar",
		ManifestID:   42,
		Hash:         "abc123",
		Changes:      []string{"Added tool: upload_attachment"},
		DashboardURL: "http://localhost:8081/manifests/42",
		CreatedAt:    time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC),
	}
}

// assertNoSecretIn fails when a capability-bearing string (a webhook URL or
// an HMAC secret) appears in text an operator is likely to read or paste.
func assertNoSecretIn(t *testing.T, text, secret string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Fatalf("secret leaked into %q", text)
	}
}

type capturedRequest struct {
	body      []byte
	signature string
	timestamp string
	userAgent string
}

// receiverRecording stands up a webhook receiver that records one request
// and answers with status.
func receiverRecording(t *testing.T, status int, got *capturedRequest) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.body = body
		got.signature = r.Header.Get(HeaderSignature)
		got.timestamp = r.Header.Get(HeaderTimestamp)
		got.userAgent = r.Header.Get("User-Agent")
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebhookSignsPayload(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusOK, &got)

	wh := NewWebhook(WebhookConfig{Name: "t", URL: srv.URL, Secret: "s3cret"})
	if err := wh.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("notify: %v", err)
	}

	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte(got.timestamp))
	mac.Write([]byte("."))
	mac.Write(got.body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got.signature != want {
		t.Fatalf("signature mismatch: got %s want %s", got.signature, want)
	}

	ts, err := strconv.ParseInt(got.timestamp, 10, 64)
	if err != nil {
		t.Fatalf("timestamp header is not unix seconds: %q", got.timestamp)
	}
	if delta := time.Since(time.Unix(ts, 0)); delta > time.Minute || delta < -time.Minute {
		t.Fatalf("timestamp is not roughly now: %v off", delta)
	}
}

func TestWebhookSendsTheVersionedEventBody(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusOK, &got)

	wh := NewWebhook(WebhookConfig{Name: "t", URL: srv.URL, Secret: "s"})
	if err := wh.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("notify: %v", err)
	}

	var decoded Event
	if err := json.Unmarshal(got.body, &decoded); err != nil {
		t.Fatalf("body is not a decodable Event: %v (%s)", err, got.body)
	}
	if decoded.Schema != SchemaVersion || decoded.EventID != 7 || decoded.Server != "calendar" {
		t.Fatalf("unexpected event body: %+v", decoded)
	}
	if len(decoded.Changes) != 1 || decoded.Changes[0] != "Added tool: upload_attachment" {
		t.Fatalf("change lines did not survive the round trip: %v", decoded.Changes)
	}
	// Risk classification was deleted from this project (D8); a stray
	// "risk" key in the payload would resurrect it as a public contract.
	if strings.Contains(string(got.body), "risk") {
		t.Fatalf("payload must not carry a risk field: %s", got.body)
	}
}

// TestWebhookUnsignedWhenNoSecretConfigured: a target on a trusted local
// socket may legitimately skip HMAC. What must not happen is a signature
// header computed from an empty secret, which looks authenticated and is not.
func TestWebhookOmitsSignatureWhenNoSecretIsConfigured(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusOK, &got)

	wh := NewWebhook(WebhookConfig{Name: "t", URL: srv.URL})
	if err := wh.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if got.signature != "" {
		t.Fatalf("expected no signature header without a secret, got %q", got.signature)
	}
}

func TestWebhookNonSuccessIsAnErrorNamingTheTargetNotItsURL(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusInternalServerError, &got)

	wh := NewWebhook(WebhookConfig{Name: "ops-channel", URL: srv.URL, Secret: "topsecret"})
	err := wh.Notify(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("expected an error for a 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error should report the status: %v", err)
	}
	if !strings.Contains(err.Error(), "ops-channel") {
		t.Fatalf("error should name the target: %v", err)
	}
	assertNoSecretIn(t, err.Error(), srv.URL)
	assertNoSecretIn(t, err.Error(), "topsecret")
}

// TestWebhookTransportErrorNeverLeaksTheURL covers the other error path:
// the request never reached a server at all. net/http's own error text
// embeds the URL, so it must be replaced rather than wrapped.
func TestWebhookTransportErrorNeverLeaksTheURL(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusOK, &got)
	deadURL := srv.URL + "/gone"
	srv.Close()

	wh := NewWebhook(WebhookConfig{Name: "ops-channel", URL: deadURL, Secret: "s"})
	err := wh.Notify(context.Background(), sampleEvent())
	if err == nil {
		t.Fatal("expected an error posting to a closed listener")
	}
	if !strings.Contains(err.Error(), "ops-channel") {
		t.Fatalf("error should name the target: %v", err)
	}
	assertNoSecretIn(t, err.Error(), deadURL)

	// A dial failure names host:port in a separate substring from the full
	// URL, so redacting the URL alone is not enough. The target's name is
	// what identifies it; nothing about where it lives needs to escape.
	host := strings.TrimPrefix(srv.URL, "http://")
	assertNoSecretIn(t, err.Error(), host)
}

// TestWebhookHardCapsItsTimeout is the isolation property at the target
// level: a receiver that accepts the connection and then never answers must
// not hold a caller forever.
func TestWebhookHardCapsItsTimeout(t *testing.T) {
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		<-blocked
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })

	wh := NewWebhook(WebhookConfig{Name: "hung", URL: srv.URL})
	wh.timeout = 50 * time.Millisecond

	start := time.Now()
	err := wh.Notify(context.Background(), sampleEvent())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error from a receiver that never responds")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("timeout was not enforced: took %v", elapsed)
	}
}

func TestNewWebhookCapsAnOverlyGenerousTimeout(t *testing.T) {
	wh := NewWebhook(WebhookConfig{Name: "t", URL: "https://example.test"})
	if wh.timeout != MaxRequestTimeout {
		t.Fatalf("expected the hard cap %v, got %v", MaxRequestTimeout, wh.timeout)
	}
}

func TestWebhookNameIsTheConfiguredName(t *testing.T) {
	wh := NewWebhook(WebhookConfig{Name: "ops-channel", URL: "https://example.test"})
	if wh.Name() != "ops-channel" {
		t.Fatalf("expected the configured name, got %q", wh.Name())
	}
}

// --- slack adapter ----------------------------------------------------------

func TestSlackFormatRendersTheEventAsSlackText(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusOK, &got)

	wh := NewWebhook(WebhookConfig{Name: "slack", URL: srv.URL, Format: FormatSlack, Secret: "s"})
	if err := wh.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("notify: %v", err)
	}

	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(got.body, &payload); err != nil {
		t.Fatalf("slack body is not JSON: %v (%s)", err, got.body)
	}
	for _, want := range []string{"calendar", "Added tool: upload_attachment", "http://localhost:8081/manifests/42"} {
		if !strings.Contains(payload.Text, want) {
			t.Fatalf("slack text missing %q: %s", want, payload.Text)
		}
	}
}

// TestSlackBodyIsStillSigned: choosing a rendering must not silently drop
// authenticity. The signature covers whatever bytes are actually sent.
func TestSlackBodyIsStillSigned(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusOK, &got)

	wh := NewWebhook(WebhookConfig{Name: "slack", URL: srv.URL, Format: FormatSlack, Secret: "s3cret"})
	if err := wh.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if err := VerifySignature("s3cret", got.timestamp, got.body, got.signature, time.Now()); err != nil {
		t.Fatalf("the signature must cover the bytes actually sent: %v", err)
	}
}

// --- signature verification -------------------------------------------------

func TestVerifySignatureAcceptsAGoodPayload(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusOK, &got)

	wh := NewWebhook(WebhookConfig{Name: "t", URL: srv.URL, Secret: "s3cret"})
	if err := wh.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("notify: %v", err)
	}
	if err := VerifySignature("s3cret", got.timestamp, got.body, got.signature, time.Now()); err != nil {
		t.Fatalf("a freshly signed payload must verify: %v", err)
	}
}

func TestVerifySignatureRejectsTamperingAndStaleness(t *testing.T) {
	var got capturedRequest
	srv := receiverRecording(t, http.StatusOK, &got)

	wh := NewWebhook(WebhookConfig{Name: "t", URL: srv.URL, Secret: "s3cret"})
	if err := wh.Notify(context.Background(), sampleEvent()); err != nil {
		t.Fatalf("notify: %v", err)
	}
	signedAt, err := strconv.ParseInt(got.timestamp, 10, 64)
	if err != nil {
		t.Fatalf("parse timestamp: %v", err)
	}

	tamperedBody := append([]byte{}, got.body...)
	tamperedBody = []byte(strings.Replace(string(tamperedBody), "calendar", "evilsrv", 1))

	tests := map[string]struct {
		secret    string
		timestamp string
		body      []byte
		signature string
		now       time.Time
	}{
		"tampered body":  {"s3cret", got.timestamp, tamperedBody, got.signature, time.Now()},
		"wrong secret":   {"guess", got.timestamp, got.body, got.signature, time.Now()},
		"replayed later": {"s3cret", got.timestamp, got.body, got.signature, time.Unix(signedAt, 0).Add(MaxClockSkew + time.Second)},
		"future stamp":   {"s3cret", got.timestamp, got.body, got.signature, time.Unix(signedAt, 0).Add(-MaxClockSkew - time.Second)},
		"absent header":  {"s3cret", got.timestamp, got.body, "", time.Now()},
		"junk signature": {"s3cret", got.timestamp, got.body, "sha256=zzzz", time.Now()},
		"bad timestamp":  {"s3cret", "not-a-number", got.body, got.signature, time.Now()},
		// A timestamp swapped for another valid-looking one invalidates the
		// MAC, which is what makes the timestamp binding meaningful.
		"swapped timestamp": {"s3cret", strconv.FormatInt(signedAt-1, 10), got.body, got.signature, time.Unix(signedAt, 0)},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if err := VerifySignature(tc.secret, tc.timestamp, tc.body, tc.signature, tc.now); err == nil {
				t.Fatal("expected verification to fail")
			}
		})
	}
}
