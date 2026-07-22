//go:build integration

// Package integration drives a fully wired gateway (internal/app) against
// the fake MCP test server (cmd/server), stepping it through v1 -> v2 -> v3
// and asserting the approval workflow's fail-closed behavior end to end.
// Run via `go test -tags=integration ./test/...`; requires `make build`
// (or `go build ./cmd/server`) to have produced the testserver binary.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/app"
	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

func testServerBinary(t *testing.T) string {
	t.Helper()
	candidate := filepath.Join("..", "..", "bin", "mcp-shield-testserver")
	if _, err := os.Stat(candidate); err != nil {
		t.Fatalf("test server binary not found at %s — run `make build` first", candidate)
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}
	return abs
}

// startApp starts a gateway instance against dbPath (so successive calls
// with the same path simulate the gateway restarting/reconnecting to a
// server whose capabilities changed between restarts) proxying to
// serverCmd with TEST_SERVER_VERSION=version.
func startApp(t *testing.T, dbPath, serverCmd, version string) *app.App {
	t.Helper()
	cfg := app.Config{
		DatabasePath: dbPath,
		ProxyAddr:    "127.0.0.1:0",
		APIAddr:      "127.0.0.1:0",
		FailMode:     approval.FailModeBlock,
		Servers: []mcp.ServerConfig{
			{Name: "calendar", Command: serverCmd, Env: []string{"TEST_SERVER_VERSION=" + version}},
		},
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("app.Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.Shutdown(ctx)
	})
	return a
}

func rpcCall(t *testing.T, proxyAddr, server, method string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method})
	resp, err := http.Post("http://"+proxyAddr+"/mcp/"+server, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post %s: %v", method, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func apiGet(t *testing.T, apiAddr, path string, out any) {
	t.Helper()
	resp, err := http.Get("http://" + apiAddr + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func apiPost(t *testing.T, apiAddr, path string) int {
	t.Helper()
	resp, err := http.Post("http://"+apiAddr+path, "application/json", bytes.NewReader([]byte(`{"username":"integration-test"}`)))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

type pendingRow struct {
	ID   int64  `json:"id"`
	Risk string `json:"risk"`
}

func isBlocked(resp map[string]any) bool {
	_, blocked := resp["error"]
	return blocked
}

func idStr(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}

func TestFullApprovalLifecycleV1toV3(t *testing.T) {
	bin := testServerBinary(t)
	dbPath := filepath.Join(t.TempDir(), "gateway.db")

	// --- v1: first connect is PENDING, low risk, blocked ---------------
	a1 := startApp(t, dbPath, bin, "v1")

	resp := rpcCall(t, a1.ProxyAddr, "calendar", "tools/list")
	if !isBlocked(resp) {
		t.Fatalf("expected v1 first connect to be blocked, got %+v", resp)
	}

	var pending []pendingRow
	apiGet(t, a1.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending manifest for v1, got %+v", pending)
	}
	if pending[0].Risk != "LOW" {
		t.Fatalf("expected v1 risk LOW, got %s", pending[0].Risk)
	}
	v1ID := pending[0].ID

	if code := apiPost(t, a1.APIAddr, "/api/manifests/"+idStr(v1ID)+"/approve"); code != http.StatusOK {
		t.Fatalf("approve v1: status %d", code)
	}

	resp = rpcCall(t, a1.ProxyAddr, "calendar", "tools/list")
	if isBlocked(resp) {
		t.Fatalf("expected v1 to be allowed after approval, got %+v", resp)
	}
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools for v1, got %+v", tools)
	}

	if err := a1.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown v1 app: %v", err)
	}

	// --- v2: bump to a HIGH-risk addition (upload_attachment), blocked --
	a2 := startApp(t, dbPath, bin, "v2")

	resp = rpcCall(t, a2.ProxyAddr, "calendar", "tools/list")
	if !isBlocked(resp) {
		t.Fatalf("expected v2 to be blocked before approval, got %+v", resp)
	}

	pending = nil
	apiGet(t, a2.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending manifest for v2, got %+v", pending)
	}
	if pending[0].Risk != "HIGH" {
		t.Fatalf("expected v2 risk HIGH (upload_attachment), got %s", pending[0].Risk)
	}
	v2ID := pending[0].ID

	if code := apiPost(t, a2.APIAddr, "/api/manifests/"+idStr(v2ID)+"/approve"); code != http.StatusOK {
		t.Fatalf("approve v2: status %d", code)
	}
	resp = rpcCall(t, a2.ProxyAddr, "calendar", "tools/list")
	if isBlocked(resp) {
		t.Fatalf("expected v2 to be allowed after approval, got %+v", resp)
	}

	if err := a2.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown v2 app: %v", err)
	}

	// --- v3: HIGH risk (delete_calendar, execute_command), rejected -----
	a3 := startApp(t, dbPath, bin, "v3")

	resp = rpcCall(t, a3.ProxyAddr, "calendar", "tools/list")
	if !isBlocked(resp) {
		t.Fatalf("expected v3 to be blocked before decision, got %+v", resp)
	}

	pending = nil
	apiGet(t, a3.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending manifest for v3, got %+v", pending)
	}
	if pending[0].Risk != "HIGH" {
		t.Fatalf("expected v3 risk HIGH, got %s", pending[0].Risk)
	}
	v3ID := pending[0].ID

	if code := apiPost(t, a3.APIAddr, "/api/manifests/"+idStr(v3ID)+"/reject"); code != http.StatusOK {
		t.Fatalf("reject v3: status %d", code)
	}

	resp = rpcCall(t, a3.ProxyAddr, "calendar", "tools/list")
	if !isBlocked(resp) {
		t.Fatalf("expected v3 to remain blocked after rejection, got %+v", resp)
	}

	pending = nil
	apiGet(t, a3.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 0 {
		t.Fatalf("expected no pending manifests after rejection, got %+v", pending)
	}

	// A retry with the same (rejected) v3 hash must not create a second
	// PENDING row — it stays resolved to the REJECTED one.
	resp = rpcCall(t, a3.ProxyAddr, "calendar", "tools/list")
	if !isBlocked(resp) {
		t.Fatalf("expected repeat v3 connect to still be blocked, got %+v", resp)
	}
	pending = nil
	apiGet(t, a3.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 0 {
		t.Fatalf("expected retry not to re-insert a pending row, got %+v", pending)
	}
}
