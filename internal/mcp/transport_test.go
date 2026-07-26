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
		t.Fatal("Close did not return within 2s")
	}
}

// TestCloseUnblocksSaturatedReadLoop guards finding S6. readLoop sent frames
// with a bare `t.frames <- frame` on a capacity-16 channel, so once nothing
// drained it the goroutine wedged mid-send and Close never released it,
// leaking a goroutine and a pipe per dead upstream. That is exactly the state
// the client leaves behind: after dispatchLoop terminates and fails all
// pending calls, nobody is reading frames any more.
//
// `yes` floods stdout, so the buffer is full long before Close is called.
func TestCloseUnblocksSaturatedReadLoop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on yes")
	}
	before := runtime.NumGoroutine()

	tr := NewStdioTransport("yes", []string{`{"jsonrpc":"2.0","id":1}`}, nil)
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Wait until the frame buffer is saturated and readLoop is parked on a
	// send, rather than sleeping a fixed duration and hoping.
	frames, _ := tr.Recv()
	deadline := time.Now().Add(2 * time.Second)
	for len(frames) < cap(frames) && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if len(frames) < cap(frames) {
		t.Fatalf("frame buffer never filled (%d/%d); test cannot exercise the blocked-send path", len(frames), cap(frames))
	}

	done := make(chan struct{})
	go func() { _ = tr.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a saturated read loop")
	}

	// readLoop must actually exit, not merely stop being waited on. Nothing
	// drains frames here on purpose: draining is precisely what unblocks a
	// wedged send, so a test that drains cannot observe this leak. The
	// goroutine count is the only honest signal.
	settle := time.Now().Add(2 * time.Second)
	for time.Now().Before(settle) {
		if runtime.NumGoroutine() <= before {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("read loop leaked after Close: %d goroutines before Start, %d still running (it is parked on a send to a full frames channel)",
		before, runtime.NumGoroutine())
}
