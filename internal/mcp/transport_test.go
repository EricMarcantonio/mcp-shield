package mcp

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// cat echoes stdin to stdout, making it a JSON-RPC "server" that answers
// every request with the request itself (same id), which is enough to
// exercise framing over a real subprocess.
func TestStdioTransportRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on cat")
	}
	tr := NewStdioTransport("cat", nil, nil)
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = tr.Close() }()
	if err := tr.Send([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("send: %v", err)
	}
	frames, _ := tr.Recv()
	select {
	case frame := <-frames:
		if string(frame) != `{"jsonrpc":"2.0","id":1,"method":"ping"}` {
			t.Fatalf("unexpected frame: %s", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame within 2s")
	}
}

func TestStdioTransportSendBeforeStartErrors(t *testing.T) {
	tr := NewStdioTransport("cat", nil, nil)
	if err := tr.Send([]byte("x")); err == nil {
		t.Fatal("expected error sending before Start")
	}
}

// TestStdioTransportCloseDoesNotLeakReadLoop documents finding S6: readLoop
// blocks forever writing to t.frames (a capacity-16 channel) once nothing is
// draining it, and Close does not wait for readLoop to exit. A real upstream
// that emits enough unread frames after Close leaks a goroutine and pipe per
// dead connection. This is NOT fixed here (Phase 5 owns it) — this test
// exercises the ordinary Close path and passes today because the child never
// writes enough to fill the buffer; it is not a regression guard for S6
// itself, just a placeholder documenting where that fix belongs.
func TestStdioTransportCloseDoesNotBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on cat")
	}
	tr := NewStdioTransport("cat", nil, nil)
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	done := make(chan struct{})
	go func() {
		_ = tr.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return within 2s (see S6: readLoop goroutine leak)")
	}
}
