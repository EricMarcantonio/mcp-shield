package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// canned replies with the id echoed back, which is what the gateway does.
func echoBackend(t *testing.T) *httptest.Server {
	t.Helper()
	const path = "/mcp/cal"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			t.Errorf("unexpected path %s, want %s", r.URL.Path, path)
		}
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("backend decode: %v", err)
		}
		out, _ := json.Marshal(mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			ID:      req.ID,
			Result:  json.RawMessage(`{"ok":true}`),
		})
		_, _ = w.Write(out)
	}))
}

func runLines(t *testing.T, gateway string, lines ...string) (string, string, error) {
	t.Helper()
	var out, diag pipeWriter
	s := newShim(gateway, "cal")
	s.diag = &diag
	err := s.run(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out)
	return out.String(), diag.String(), err
}

// pipeWriter is the test's stand-in for stdout, and it models the property
// that makes the shim's write lock necessary: on a real pipe a single Write
// is only atomic below PIPE_BUF, so a 64 KiB frame lands in pieces and two
// unsynchronized writers interleave mid-frame. Chunking with a yield in
// between reproduces that deterministically. The per-chunk lock exists only
// so the harness itself is race-free — any interleaving a test observes came
// from the shim.
type pipeWriter struct {
	mu sync.Mutex
	b  []byte
}

const pipeChunkSize = 512

func (w *pipeWriter) Write(p []byte) (int, error) {
	for offset := 0; offset < len(p); offset += pipeChunkSize {
		end := min(offset+pipeChunkSize, len(p))
		w.mu.Lock()
		w.b = append(w.b, p[offset:end]...)
		w.mu.Unlock()
		runtime.Gosched()
	}
	return len(p), nil
}

func (w *pipeWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.b)
}

func responseLines(t *testing.T, out string) []mcp.Response {
	t.Helper()
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil
	}
	lines := strings.Split(trimmed, "\n")
	responses := make([]mcp.Response, 0, len(lines))
	for i, line := range lines {
		var resp mcp.Response
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("line %d is not a JSON-RPC frame (%v): %q", i, err, line)
		}
		responses = append(responses, resp)
	}
	return responses
}

func TestShimForwardsRequestsAndDropsNotificationResponses(t *testing.T) {
	backend := echoBackend(t)
	defer backend.Close()

	out, _, err := runLines(t, backend.URL,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
	)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}

	responses := responseLines(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response line (the notification gets none), got %d: %q", len(responses), out)
	}
	if !strings.Contains(string(responses[0].Result), `"ok":true`) {
		t.Fatalf("unexpected response: %+v", responses[0])
	}
}

func TestShimSkipsBlankLines(t *testing.T) {
	backend := echoBackend(t)
	defer backend.Close()

	out, _, err := runLines(t, backend.URL,
		``,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
		`   `,
	)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if got := len(responseLines(t, out)); got != 1 {
		t.Fatalf("expected 1 response line, got %d: %q", got, out)
	}
}

func TestShimSendsTargetProtocolVersionHeader(t *testing.T) {
	var got string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("MCP-Protocol-Version")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer backend.Close()

	if _, _, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); err != nil {
		t.Fatalf("shim: %v", err)
	}
	if got != mcp.ProtocolVersion {
		t.Fatalf("MCP-Protocol-Version header = %q, want %q", got, mcp.ProtocolVersion)
	}
}

// A tools/call with a big argument blob, or a tools/list reply listing many
// tools, blows past bufio.Scanner's 64 KiB default. Truncating either one in
// a security gateway would silently drop capability data, so both directions
// are asserted byte-for-byte.
func TestShimCarriesOversizedRequestAndResponseIntact(t *testing.T) {
	const payloadSize = 2 << 20 // 2 MiB, well past the 64 KiB scanner default
	payload := strings.Repeat("x", payloadSize)

	var gotRequest int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("backend read: %v", err)
		}
		gotRequest = len(body)
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":{"blob":%q}}`, payload)
	}))
	defer backend.Close()

	line := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"blob":%q}}`, payload)
	out, _, err := runLines(t, backend.URL, line)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if gotRequest < payloadSize {
		t.Fatalf("gateway received %d bytes, want at least %d — the request line was truncated", gotRequest, payloadSize)
	}

	responses := responseLines(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 response line, got %d", len(responses))
	}
	var result struct {
		Blob string `json:"blob"`
	}
	if err := json.Unmarshal(responses[0].Result, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Blob) != payloadSize {
		t.Fatalf("response blob is %d bytes, want %d — the response was truncated", len(result.Blob), payloadSize)
	}
}

// Past the ceiling the shim must fail loudly on the protocol channel rather
// than hand the client a truncated frame.
func TestShimRejectsFrameOverTheSizeCeiling(t *testing.T) {
	backend := echoBackend(t)
	defer backend.Close()

	oversized := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"blob":%q}}`,
		strings.Repeat("x", mcp.MaxFrameSize+1024))

	out, diag, err := runLines(t, backend.URL, oversized)
	if err == nil {
		t.Fatal("expected an error for a frame over the size ceiling")
	}
	if !strings.Contains(err.Error(), "8 MiB") {
		t.Fatalf("error should name the limit, got: %v", err)
	}
	responses := responseLines(t, out)
	if len(responses) != 1 || responses[0].Error == nil {
		t.Fatalf("client should get one JSON-RPC error frame, got %q", out)
	}
	if diag == "" {
		t.Fatal("expected a diagnostic on stderr")
	}
}

// Clients pipeline: they send the next request without waiting for the
// previous response. The barrier below makes that a hard requirement rather
// than a timing guess — no request is answered until all of them are in
// flight at once, so a shim that serializes deadlocks instead of merely
// running slowly. The oversized pad then proves the concurrent responses
// come back as whole frames.
func TestShimHandlesPipelinedRequestsWithoutInterleaving(t *testing.T) {
	const (
		pipelineDepth = 8
		bodyPad       = 64 << 10 // far past PIPE_BUF: unlocked writes would interleave
	)

	var inFlight atomic.Int32
	allArrived := make(chan struct{})
	var closeOnce sync.Once

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcp.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("backend decode: %v", err)
		}
		if inFlight.Add(1) == pipelineDepth {
			closeOnce.Do(func() { close(allArrived) })
		}
		select {
		case <-allArrived:
		case <-time.After(5 * time.Second):
			t.Errorf("only %d of %d requests were ever in flight at once — the shim is serializing them",
				inFlight.Load(), pipelineDepth)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%v,"result":{"pad":%q}}`,
			req.ID, strings.Repeat("p", bodyPad))
	}))
	defer backend.Close()

	lines := make([]string, 0, pipelineDepth)
	for i := 1; i <= pipelineDepth; i++ {
		lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, i))
	}

	out, _, err := runLines(t, backend.URL, lines...)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}

	responses := responseLines(t, out)
	if len(responses) != pipelineDepth {
		t.Fatalf("expected %d response lines, got %d", pipelineDepth, len(responses))
	}
	seen := map[float64]bool{}
	for _, resp := range responses {
		id, ok := resp.ID.(float64)
		if !ok {
			t.Fatalf("response id %v is not a number", resp.ID)
		}
		if seen[id] {
			t.Fatalf("duplicate response for id %v", id)
		}
		seen[id] = true
		var result struct {
			Pad string `json:"pad"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("id %v: result did not survive intact: %v", id, err)
		}
		if len(result.Pad) != bodyPad {
			t.Fatalf("id %v: pad is %d bytes, want %d", id, len(result.Pad), bodyPad)
		}
	}
	for i := 1; i <= pipelineDepth; i++ {
		if !seen[float64(i)] {
			t.Fatalf("no response for id %d", i)
		}
	}
}

func TestShimReportsUnreachableGateway(t *testing.T) {
	backend := echoBackend(t)
	backend.Close() // nothing is listening on that port any more

	out, diag, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if err != nil {
		t.Fatalf("shim should survive an unreachable gateway, got %v", err)
	}
	responses := responseLines(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 error frame, got %d: %q", len(responses), out)
	}
	assertErrorFrame(t, responses[0], "unreachable")
	if diag == "" {
		t.Fatal("expected a diagnostic on stderr")
	}
}

func TestShimReportsNon2xxGatewayStatus(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no such server", http.StatusNotFound)
	}))
	defer backend.Close()

	out, _, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	responses := responseLines(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 error frame, got %d: %q", len(responses), out)
	}
	assertErrorFrame(t, responses[0], "404")
}

func TestShimReportsNonJSONRPCBody(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>proxy error</html>"))
	}))
	defer backend.Close()

	out, _, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	responses := responseLines(t, out)
	if len(responses) != 1 {
		t.Fatalf("expected 1 error frame, got %d: %q", len(responses), out)
	}
	assertErrorFrame(t, responses[0], "JSON-RPC")
}

// A 200 body that is valid JSON but missing the jsonrpc member is not a
// JSON-RPC response; forwarding it would put a non-frame on the wire.
func TestShimReportsJSONBodyThatIsNotAJSONRPCResponse(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer backend.Close()

	out, _, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	assertErrorFrame(t, responseLines(t, out)[0], "JSON-RPC")
}

// A gateway that pretty-prints would otherwise put a multi-line body on
// stdout, which is several corrupt frames as far as the client is concerned.
func TestShimCompactsMultiLineGatewayResponsesIntoOneFrame(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{\n  \"jsonrpc\": \"2.0\",\n  \"id\": 1,\n  \"result\": {\n    \"ok\": true\n  }\n}\n"))
	}))
	defer backend.Close()

	out, _, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if got := len(responseLines(t, out)); got != 1 {
		t.Fatalf("expected exactly 1 frame, got %d: %q", got, out)
	}
}

func TestShimReportsMalformedClientLineWithoutCallingTheGateway(t *testing.T) {
	var calls int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer backend.Close()

	out, diag, err := runLines(t, backend.URL, `{"jsonrpc":"2.0",`)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if calls != 0 {
		t.Fatalf("malformed line should not reach the gateway, got %d calls", calls)
	}
	responses := responseLines(t, out)
	if len(responses) != 1 || responses[0].Error == nil {
		t.Fatalf("expected one JSON-RPC error frame, got %q", out)
	}
	if responses[0].Error.Code != codeParseError {
		t.Fatalf("code = %d, want %d", responses[0].Error.Code, codeParseError)
	}
	if diag == "" {
		t.Fatal("expected a diagnostic on stderr")
	}
}

// A JSON-RPC id of null is not a request id worth answering, and MCP
// notifications carry no id at all — neither may produce output.
func TestShimTreatsNullIDAsANotification(t *testing.T) {
	backend := echoBackend(t)
	defer backend.Close()

	out, _, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","id":null,"method":"notifications/cancelled"}`)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no output for a null-id message, got %q", out)
	}
}

// A notification whose forwarding fails must still produce no stdout frame:
// the client is not waiting for one, and an unsolicited reply corrupts its
// stream. The failure belongs on stderr.
func TestShimKeepsNotificationFailuresOffStdout(t *testing.T) {
	backend := echoBackend(t)
	backend.Close()

	out, diag, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("expected no stdout output, got %q", out)
	}
	if !strings.Contains(diag, "notifications/initialized") {
		t.Fatalf("expected the failure named on stderr, got %q", diag)
	}
}

// Phase 5 found the same class of bug in the upstream stdio transport: a
// goroutine started per frame that outlived the loop which started it, so
// every dead session leaked one. This asserts on the goroutine dump rather
// than assuming.
func TestShimReturnsWhenStdinClosesAndLeaksNoGoroutines(t *testing.T) {
	const requests = 25

	backend := echoBackend(t)
	defer backend.Close()

	lines := make([]string, 0, requests)
	for i := 1; i <= requests; i++ {
		lines = append(lines, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/list"}`, i))
	}

	s := newShim(backend.URL, "cal")
	var diag pipeWriter
	s.diag = &diag

	// One warm-up session first, so the HTTP connection pool's goroutines are
	// already in the baseline and only growth from the real run counts.
	var warmup pipeWriter
	if err := s.run(strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"tools/list"}`+"\n"), &warmup); err != nil {
		t.Fatalf("warmup: %v", err)
	}
	baseline := stableGoroutineCount()

	var out pipeWriter
	if err := s.run(strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("shim: %v", err)
	}
	// run must not return until every forwarded request has been written,
	// which is the other half of not leaking: draining, not abandoning.
	if got := len(responseLines(t, out.String())); got != requests {
		t.Fatalf("expected %d responses (run must drain before returning), got %d", requests, got)
	}

	// Two assertions, because they catch different leaks. The frame count is
	// specific — it names a shim goroutine that outlived run. The total count
	// is broad — it catches anything the shim failed to release on the way
	// out, an unclosed connection pool being the obvious candidate.
	if frames, dump := lingeringShimGoroutines(); frames > 0 {
		t.Fatalf("%d shim stack frame(s) still running after run returned:\n%s", frames, dump)
	}
	if after := settledGoroutineCount(baseline); after > baseline {
		_, dump := liveShimFrames()
		t.Fatalf("goroutine count %d -> %d across %d requests:\n%s", baseline, after, requests, dump)
	}
}

// stableGoroutineCount samples until two consecutive reads agree, which is a
// baseline that does not race the runtime still standing goroutines up.
func stableGoroutineCount() int {
	previous := -1
	for range 200 {
		time.Sleep(5 * time.Millisecond)
		current := runtime.NumGoroutine()
		if current == previous {
			return current
		}
		previous = current
	}
	return previous
}

// settledGoroutineCount polls until the count falls back to target, so the
// assertion does not race the runtime tearing down pooled connections.
func settledGoroutineCount(target int) int {
	count := runtime.NumGoroutine()
	for range 200 {
		if count <= target {
			return count
		}
		time.Sleep(5 * time.Millisecond)
		count = runtime.NumGoroutine()
	}
	return count
}

// liveShimFrames samples the goroutine dump once and counts stack frames
// executing shim code. That is the specific half of the leak question: a
// raw runtime.NumGoroutine() comparison also moves with the HTTP connection
// pool, so on its own it says less about what actually leaked.
func liveShimFrames() (int, string) {
	// Not "main.(*shim)." — under `go test` the symbol carries the full
	// import path rather than "main", and that mismatch would make the
	// leak assertion below quietly vacuous.
	const shimFrame = "(*shim)."
	buf := make([]byte, 1<<20)
	dump := buf[:runtime.Stack(buf, true)]
	return bytes.Count(dump, []byte(shimFrame)), string(dump)
}

// lingeringShimGoroutines is liveShimFrames with a short poll, absorbing the
// gap between a goroutine's last statement and its actual exit.
func lingeringShimGoroutines() (int, string) {
	count, dump := liveShimFrames()
	for range 100 {
		if count == 0 {
			return 0, ""
		}
		time.Sleep(10 * time.Millisecond)
		count, dump = liveShimFrames()
	}
	return count, dump
}

// The leak assertion above is only worth anything if liveShimFrames can
// actually recognise a shim goroutine — a stale frame string would make it
// pass unconditionally, which is exactly what the first draft of it did.
// This samples from inside the backend handler, where the goroutine that
// issued the request is provably parked in handle -> forward.
func TestLeakDetectorRecognisesALiveShimGoroutine(t *testing.T) {
	var framesMidRequest int
	var dumpMidRequest string

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		framesMidRequest, dumpMidRequest = liveShimFrames()
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer backend.Close()

	if _, _, err := runLines(t, backend.URL, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`); err != nil {
		t.Fatalf("shim: %v", err)
	}
	if framesMidRequest == 0 {
		t.Fatalf("no shim frames seen while a request was in flight — the frame string is stale, so the leak test is vacuous. Dump:\n%s", dumpMidRequest)
	}
}

// assertErrorFrame checks the shape every gateway-failure frame must have:
// addressed to the request that caused it (id 7 throughout these tests),
// carrying the upstream-error code, and naming both mcp-shield and what
// specifically went wrong.
func assertErrorFrame(t *testing.T, resp mcp.Response, wantMessage string) {
	t.Helper()
	const (
		wantID   = float64(7)
		wantCode = mcp.CodeUpstreamError
	)
	if resp.Error == nil {
		t.Fatalf("expected an error frame, got %+v", resp)
	}
	if resp.ID != wantID {
		t.Fatalf("error frame id = %v (%T), want %v", resp.ID, resp.ID, wantID)
	}
	if resp.Error.Code != wantCode {
		t.Fatalf("error code = %d, want %d", resp.Error.Code, wantCode)
	}
	if !strings.Contains(resp.Error.Message, wantMessage) {
		t.Fatalf("error message %q should mention %q", resp.Error.Message, wantMessage)
	}
	if !strings.Contains(resp.Error.Message, "mcp-shield") {
		t.Fatalf("error message %q should name mcp-shield as the source", resp.Error.Message)
	}
}

func TestParseConnectArgsAcceptsServerBeforeOrAfterFlags(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantServer  string
		wantGateway string
	}{
		{"server only", []string{"calendar"}, "calendar", defaultGatewayURL},
		{"server then flag", []string{"calendar", "--gateway", "http://h:9"}, "calendar", "http://h:9"},
		{"flag then server", []string{"--gateway", "http://h:9", "calendar"}, "calendar", "http://h:9"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, gateway, err := parseConnectArgs(tc.args)
			if err != nil {
				t.Fatalf("parseConnectArgs(%q): %v", tc.args, err)
			}
			if server != tc.wantServer || gateway != tc.wantGateway {
				t.Fatalf("got (%q, %q), want (%q, %q)", server, gateway, tc.wantServer, tc.wantGateway)
			}
		})
	}
}

func TestParseConnectArgsRequiresAServerName(t *testing.T) {
	if _, _, err := parseConnectArgs(nil); err == nil {
		t.Fatal("expected an error when no server is named")
	}
}

func TestParseConnectArgsReadsGatewayFromEnvironment(t *testing.T) {
	t.Setenv("MCP_SHIELD_PROXY", "http://from-env:1234")
	_, gateway, err := parseConnectArgs([]string{"calendar"})
	if err != nil {
		t.Fatalf("parseConnectArgs: %v", err)
	}
	if gateway != "http://from-env:1234" {
		t.Fatalf("gateway = %q, want the MCP_SHIELD_PROXY value", gateway)
	}
}
