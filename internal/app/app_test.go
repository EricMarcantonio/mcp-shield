package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// TestGateAdapterCreatesServerOnFirstSight covers gateAdapter.CheckAndRecord
// (internal/app/app.go:138-163), the bridge between mcp.Gate and
// approval.Workflow that had zero test coverage: it resolves-or-creates the
// database Server row for a server name the gateway hasn't seen before, then
// delegates to the workflow. The first call for a brand new server name must
// create exactly one Server row (not one per call) and — since there is no
// approved baseline yet — report empty safe sets, the fail-closed starting
// state.
func TestGateAdapterCreatesServerOnFirstSight(t *testing.T) {
	store, err := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	workflow := approval.New(store, approval.FailModeBlock)
	gate := &gateAdapter{store: store, workflow: workflow}

	ctx := context.Background()
	first, err := gate.CheckAndRecord(ctx, "newsrv", nil, nil, nil)
	if err != nil {
		t.Fatalf("first CheckAndRecord: %v", err)
	}
	if len(first.SafeTools) != 0 || len(first.SafePrompts) != 0 || len(first.SafeResources) != 0 {
		t.Fatalf("expected empty safe sets for a server with no approved baseline, got %+v", first)
	}

	second, err := gate.CheckAndRecord(ctx, "newsrv", nil, nil, nil)
	if err != nil {
		t.Fatalf("second CheckAndRecord: %v", err)
	}
	if len(second.SafeTools) != 0 || len(second.SafePrompts) != 0 || len(second.SafeResources) != 0 {
		t.Fatalf("expected empty safe sets on the second call too, got %+v", second)
	}

	srv, err := store.GetServerByName(ctx, "newsrv")
	if err != nil {
		t.Fatalf("get server by name: %v", err)
	}
	if srv == nil {
		t.Fatal("expected a server row to have been created")
	}

	servers, err := store.ListServers(ctx)
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected exactly one server row after two calls for the same name, got %d", len(servers))
	}
}

// --- notifications ----------------------------------------------------------

// hungReceiver stands up a webhook target that accepts the connection and
// then never answers, and returns a channel closed once it has one request
// in its hands.
func hungReceiver(t *testing.T) (url string, arrived <-chan struct{}) {
	t.Helper()
	blocked := make(chan struct{})
	got := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(got) })
		<-blocked
	}))
	t.Cleanup(func() { close(blocked); srv.Close() })
	return srv.URL, got
}

func writeNotifyConfig(t *testing.T, webhookURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notify.json")
	body := `{"webhooks":[{"name":"hung-target","url":"` + webhookURL + `"}],"dashboard_url":"http://localhost:8081"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write notify config: %v", err)
	}
	return path
}

func newTestApp(t *testing.T, notifyConfigPath string) *App {
	t.Helper()
	a, err := New(Config{
		DatabasePath:     filepath.Join(t.TempDir(), "app.db"),
		ProxyAddr:        "127.0.0.1:0",
		APIAddr:          "127.0.0.1:0",
		FailMode:         approval.FailModeBlock,
		NotifyConfigPath: notifyConfigPath,
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return a
}

// TestGateDecisionIsUnaffectedByAHungNotificationTarget is the property the
// whole design exists to protect. A webhook target that accepts the
// connection and then never responds holds the dispatcher for its full 10
// second timeout. During that time the gate must decide, and an approver
// must be able to approve, at full speed — because neither path ever waits
// on a notifier. Enqueue is an INSERT; delivery happens elsewhere.
func TestGateDecisionIsUnaffectedByAHungNotificationTarget(t *testing.T) {
	ctx := context.Background()
	webhookURL, arrived := hungReceiver(t)
	a := newTestApp(t, writeNotifyConfig(t, webhookURL))
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	gate := &gateAdapter{store: a.store, workflow: a.workflow}

	// First decision enqueues the event the hung target will choke on.
	first, err := gate.CheckAndRecord(ctx, "calendar", []mcp.Tool{{Name: "calendar_read"}}, nil, nil)
	if err != nil {
		t.Fatalf("first CheckAndRecord: %v", err)
	}

	// Wait until the dispatcher is genuinely stuck inside the target.
	select {
	case <-arrived:
	case <-time.After(15 * time.Second):
		t.Fatal("the dispatcher never attempted delivery; this test would prove nothing")
	}

	// Every gate operation below runs while that delivery is hung.
	const budget = time.Second

	start := time.Now()
	if _, err := gate.CheckAndRecord(ctx, "calendar", []mcp.Tool{{Name: "calendar_read"}, {Name: "calendar_write"}}, nil, nil); err != nil {
		t.Fatalf("CheckAndRecord while a notifier is hung: %v", err)
	}
	if elapsed := time.Since(start); elapsed > budget {
		t.Fatalf("a hung notifier delayed a gate decision by %v", elapsed)
	}

	start = time.Now()
	if err := a.workflow.Approve(ctx, first.ManifestID, "eric", "fine"); err != nil {
		t.Fatalf("Approve while a notifier is hung: %v", err)
	}
	if elapsed := time.Since(start); elapsed > budget {
		t.Fatalf("a hung notifier delayed an approval by %v", elapsed)
	}

	start = time.Now()
	third, err := gate.CheckAndRecord(ctx, "calendar", []mcp.Tool{{Name: "calendar_read"}}, nil, nil)
	if err != nil {
		t.Fatalf("CheckAndRecord after approval: %v", err)
	}
	if elapsed := time.Since(start); elapsed > budget {
		t.Fatalf("a hung notifier delayed a gate decision by %v", elapsed)
	}
	// And the decision is still correct, not merely fast.
	if !third.SafeTools["calendar_read"] {
		t.Fatalf("the approved baseline must still be allowed through, got %+v", third.SafeTools)
	}
}

// TestNotificationsStayOffWithoutAConfigFile: the default posture is
// unchanged from v0.1.0. No config file, no outbox rows, no dispatcher.
func TestNotificationsStayOffWithoutAConfigFile(t *testing.T) {
	ctx := context.Background()
	a := newTestApp(t, filepath.Join(t.TempDir(), "absent.json"))
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	gate := &gateAdapter{store: a.store, workflow: a.workflow}
	if _, err := gate.CheckAndRecord(ctx, "calendar", []mcp.Tool{{Name: "calendar_read"}}, nil, nil); err != nil {
		t.Fatalf("CheckAndRecord: %v", err)
	}

	rows, err := a.store.DueNotifications(ctx, time.Now().UTC().Add(time.Hour), 100, 100)
	if err != nil {
		t.Fatalf("due notifications: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no outbox rows without a notify config, got %+v", rows)
	}

	resp := getAPI(t, a, "/api/notifications/failed")
	if resp != http.StatusNotFound {
		t.Fatalf("expected the failed-notifications route to 404 when disabled, got %d", resp)
	}
}

// TestFailedNotificationsEndpointIsServedWhenConfigured covers the wiring
// between the notify config and the API surface an operator checks.
func TestFailedNotificationsEndpointIsServedWhenConfigured(t *testing.T) {
	ctx := context.Background()
	webhookURL, _ := hungReceiver(t)
	a := newTestApp(t, writeNotifyConfig(t, webhookURL))
	if err := a.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	if code := getAPI(t, a, "/api/notifications/failed"); code != http.StatusOK {
		t.Fatalf("expected 200 from the failed-notifications route, got %d", code)
	}
}

func getAPI(t *testing.T, a *App, path string) int {
	t.Helper()
	resp, err := http.Get("http://" + a.APIAddr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}
