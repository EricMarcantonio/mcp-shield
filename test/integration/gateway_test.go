//go:build integration

// Package integration drives a fully wired gateway (internal/app) against
// the fake MCP test server (cmd/mcp-shield-testserver), stepping it through
// v1 -> v2 -> v3 and asserting the approval workflow's partial-allow
// behavior end to end: an unapproved capability change withholds only the
// new/changed tools, not the whole server.
// Run via `go test -tags=integration ./test/...`; requires `make build`
// (or `go build ./cmd/mcp-shield-testserver`) to have produced the
// testserver binary.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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

func rpcCallTool(t *testing.T, proxyAddr, server, toolName string) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": toolName},
	})
	resp, err := http.Post("http://"+proxyAddr+"/mcp/"+server, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post tools/call: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func toolNames(t *testing.T, resp map[string]any) []string {
	t.Helper()
	if errObj, ok := resp["error"]; ok {
		t.Fatalf("expected a result, got error: %+v", errObj)
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %+v", resp)
	}
	rawTools, _ := result["tools"].([]any)
	names := make([]string, 0, len(rawTools))
	for _, rt := range rawTools {
		tool, _ := rt.(map[string]any)
		names = append(names, tool["name"].(string))
	}
	sort.Strings(names)
	return names
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
	ID int64 `json:"id"`
}

func idStr(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}

func assertStringSlicesEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func TestFullApprovalLifecycleV1toV3(t *testing.T) {
	bin := testServerBinary(t)
	dbPath := filepath.Join(t.TempDir(), "gateway.db")

	// --- v1: first connect is PENDING. No approved baseline exists yet,
	// so nothing is "unchanged" -- tools/list comes back empty (not an
	// error), and the specific tool is blocked on call.
	a1 := startApp(t, dbPath, bin, "v1")

	assertStringSlicesEqual(t, toolNames(t, rpcCall(t, a1.ProxyAddr, "calendar", "tools/list")), []string{})

	callResp := rpcCallTool(t, a1.ProxyAddr, "calendar", "calendar_read")
	if _, blocked := callResp["error"]; !blocked {
		t.Fatalf("expected calendar_read call to be blocked pre-approval, got %+v", callResp)
	}

	var pending []pendingRow
	apiGet(t, a1.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending manifest for v1, got %+v", pending)
	}
	v1ID := pending[0].ID

	if code := apiPost(t, a1.APIAddr, "/api/manifests/"+idStr(v1ID)+"/approve"); code != http.StatusOK {
		t.Fatalf("approve v1: status %d", code)
	}

	assertStringSlicesEqual(t, toolNames(t, rpcCall(t, a1.ProxyAddr, "calendar", "tools/list")),
		[]string{"calendar_create", "calendar_read"})

	callResp = rpcCallTool(t, a1.ProxyAddr, "calendar", "calendar_read")
	if _, blocked := callResp["error"]; blocked {
		t.Fatalf("expected calendar_read call to succeed once approved, got %+v", callResp)
	}

	if err := a1.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown v1 app: %v", err)
	}

	// --- v2: adds upload_attachment. The two already-approved tools must
	// keep working while upload_attachment is withheld specifically.
	a2 := startApp(t, dbPath, bin, "v2")

	assertStringSlicesEqual(t, toolNames(t, rpcCall(t, a2.ProxyAddr, "calendar", "tools/list")),
		[]string{"calendar_create", "calendar_read"})

	if resp := rpcCallTool(t, a2.ProxyAddr, "calendar", "calendar_read"); resp["error"] != nil {
		t.Fatalf("expected unchanged tool calendar_read to still work under a pending change, got %+v", resp)
	}
	if resp := rpcCallTool(t, a2.ProxyAddr, "calendar", "upload_attachment"); resp["error"] == nil {
		t.Fatalf("expected the new pending tool upload_attachment to be blocked, got %+v", resp)
	}

	pending = nil
	apiGet(t, a2.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending manifest for v2, got %+v", pending)
	}
	v2ID := pending[0].ID

	if code := apiPost(t, a2.APIAddr, "/api/manifests/"+idStr(v2ID)+"/approve"); code != http.StatusOK {
		t.Fatalf("approve v2: status %d", code)
	}
	assertStringSlicesEqual(t, toolNames(t, rpcCall(t, a2.ProxyAddr, "calendar", "tools/list")),
		[]string{"calendar_create", "calendar_read", "upload_attachment"})

	if err := a2.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown v2 app: %v", err)
	}

	// --- v3: adds delete_calendar + execute_command, rejected. The three
	// already-approved tools must keep working; the two new ones stay
	// blocked even after the decision.
	a3 := startApp(t, dbPath, bin, "v3")

	assertStringSlicesEqual(t, toolNames(t, rpcCall(t, a3.ProxyAddr, "calendar", "tools/list")),
		[]string{"calendar_create", "calendar_read", "upload_attachment"})

	pending = nil
	apiGet(t, a3.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending manifest for v3, got %+v", pending)
	}
	v3ID := pending[0].ID

	if code := apiPost(t, a3.APIAddr, "/api/manifests/"+idStr(v3ID)+"/reject"); code != http.StatusOK {
		t.Fatalf("reject v3: status %d", code)
	}

	// The point of this whole feature: after rejection, the unchanged
	// tools are still usable.
	assertStringSlicesEqual(t, toolNames(t, rpcCall(t, a3.ProxyAddr, "calendar", "tools/list")),
		[]string{"calendar_create", "calendar_read", "upload_attachment"})
	if resp := rpcCallTool(t, a3.ProxyAddr, "calendar", "calendar_read"); resp["error"] != nil {
		t.Fatalf("expected calendar_read to still work after v3 rejection, got %+v", resp)
	}
	if resp := rpcCallTool(t, a3.ProxyAddr, "calendar", "delete_calendar"); resp["error"] == nil {
		t.Fatalf("expected delete_calendar to stay blocked after rejection, got %+v", resp)
	}
	if resp := rpcCallTool(t, a3.ProxyAddr, "calendar", "execute_command"); resp["error"] == nil {
		t.Fatalf("expected execute_command to stay blocked after rejection, got %+v", resp)
	}

	pending = nil
	apiGet(t, a3.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 0 {
		t.Fatalf("expected no pending manifests after rejection, got %+v", pending)
	}

	// A retry with the same (rejected) v3 hash must not create a second
	// PENDING row — it stays resolved to the REJECTED one, and the same
	// partial-allow set applies.
	assertStringSlicesEqual(t, toolNames(t, rpcCall(t, a3.ProxyAddr, "calendar", "tools/list")),
		[]string{"calendar_create", "calendar_read", "upload_attachment"})
	pending = nil
	apiGet(t, a3.APIAddr, "/api/manifests/pending", &pending)
	if len(pending) != 0 {
		t.Fatalf("expected retry not to re-insert a pending row, got %+v", pending)
	}
}
