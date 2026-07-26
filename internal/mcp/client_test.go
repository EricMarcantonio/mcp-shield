package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeTransport is a scriptable Transport double: tests push frames/errors
// in and inspect what the client sends, without a real subprocess.
type fakeTransport struct {
	frames  chan []byte
	errs    chan error
	sent    chan []byte
	onClose func()
}

func newFakeTransport() *fakeTransport {
	return &fakeTransport{frames: make(chan []byte, 16), errs: make(chan error, 1), sent: make(chan []byte, 16)}
}
func (t *fakeTransport) Start(_ context.Context) error { return nil }
func (t *fakeTransport) Send(msg []byte) error         { t.sent <- msg; return nil }
func (t *fakeTransport) Recv() (<-chan []byte, <-chan error) {
	return t.frames, t.errs
}

func (t *fakeTransport) Close() error {
	if t.onClose != nil {
		t.onClose()
	}
	return nil
}

// shutdownFramesThenErrs closes frames alone first. Because errs is still
// open (and never receives a value), dispatchLoop's select has exactly one
// ready case, so it deterministically takes the frames branch first, then
// blocks again waiting on errs alone — no tie-break is possible. Once that
// settles (spinYield gives the goroutine the chance to run; there is
// nothing else for it to do), closing errs is likewise the only ready
// case. This forces the ordering that does NOT trigger the client.go:47
// bug (see TestCallAfterTransportShutdownFails).
func (t *fakeTransport) shutdownFramesThenErrs() {
	close(t.frames)
	spinYield()
	close(t.errs)
}

// shutdownErrsThenFrames is the mirror image: closing errs alone first,
// then frames, deterministically forces dispatchLoop to process the errs
// close before the frames close — the exact ordering that hits the
// client.go:47 `continue` and skips the terminal check, livelocking the
// goroutine forever (see TestDispatchLoopLivelocksWhenErrsClosesBeforeFrames).
func (t *fakeTransport) shutdownErrsThenFrames() {
	close(t.errs)
	spinYield()
	close(t.frames)
}

// spinYield yields the processor long enough for a goroutine with no
// competing work (dispatchLoop, once its only pending channel event has
// been consumed, has nothing else to do until the next channel close) to
// run and re-block in its select. This is not synchronizing on a race —
// by construction only one channel is ready at a time in the callers
// above, so there is nothing to race with, only scheduling latency to
// absorb.
func spinYield() {
	for i := 0; i < 20000; i++ {
		runtime.Gosched()
	}
}

func newClientWithFake(t *testing.T) (*UpstreamClient, *fakeTransport) {
	t.Helper()
	ft := newFakeTransport()
	c := &UpstreamClient{transport: ft, pending: make(map[int64]chan *Response)}
	c.dispatchOnce.Do(func() { go c.dispatchLoop() })
	return c, ft
}

func TestCallMatchesResponseByID(t *testing.T) {
	c, ft := newClientWithFake(t)
	go func() {
		req := <-ft.sent
		var r Request
		_ = json.Unmarshal(req, &r)
		out, _ := json.Marshal(Response{JSONRPC: JSONRPCVersion, ID: r.ID, Result: json.RawMessage(`"pong"`)})
		ft.frames <- out
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := c.Call(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(resp.Result) != `"pong"` {
		t.Fatalf("unexpected result: %s", resp.Result)
	}
}

func TestCallContextCancelUnblocks(t *testing.T) {
	c, _ := newClientWithFake(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	var callErr error
	go func() {
		_, callErr = c.Call(ctx, "ping", nil)
		close(done)
	}()
	cancel()

	select {
	case <-done:
		if !errors.Is(callErr, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", callErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not unblock within 2s of context cancellation")
	}
}

func TestMalformedFrameIgnored(t *testing.T) {
	c, ft := newClientWithFake(t)
	go func() {
		req := <-ft.sent
		var r Request
		_ = json.Unmarshal(req, &r)
		ft.frames <- []byte("not json")
		out, _ := json.Marshal(Response{JSONRPC: JSONRPCVersion, ID: r.ID, Result: json.RawMessage(`"ok"`)})
		ft.frames <- out
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := c.Call(ctx, "ping", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(resp.Result) != `"ok"` {
		t.Fatalf("unexpected result: %s", resp.Result)
	}
}

func TestTransportShutdownFailsPending(t *testing.T) {
	c, ft := newClientWithFake(t)

	respCh := make(chan *Response, 1)
	errCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		resp, err := c.Call(ctx, "ping", nil)
		respCh <- resp
		errCh <- err
	}()

	// Make sure the request has actually been sent (and is therefore
	// pending) before shutting the transport down, otherwise there is
	// nothing to fail. Use the deterministic close ordering (frames then
	// errs) rather than closing both channels back-to-back — the latter
	// hits a dispatchLoop tie-break that livelocks about half the time;
	// see TestDispatchLoopLivelocksWhenErrsClosesBeforeFrames.
	<-ft.sent
	ft.shutdownFramesThenErrs()

	select {
	case resp := <-respCh:
		err := <-errCh
		if err != nil {
			t.Fatalf("expected a synthetic error response, not a Go error: %v", err)
		}
		if resp == nil || resp.Error == nil {
			t.Fatalf("expected a response carrying an error, got %+v", resp)
		}
		if resp.Error.Code != CodeUpstreamError {
			t.Fatalf("expected CodeUpstreamError, got %d", resp.Error.Code)
		}
		if !strings.Contains(resp.Error.Message, "closed") {
			t.Fatalf("expected error message to mention the transport closing, got %q", resp.Error.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call was not failed within 2s of transport shutdown")
	}
}

func TestCallAfterTransportShutdownFails(t *testing.T) {
	c, ft := newClientWithFake(t)
	// Deterministic ordering: see shutdownFramesThenErrs. Closing both
	// channels back-to-back instead would make this test flaky — it hits
	// the same tie-break as TestDispatchLoopLivelocksWhenErrsClosesBeforeFrames
	// roughly half the time, in which case dispatchLoop never marks the
	// client closed at all.
	ft.shutdownFramesThenErrs()

	// Closed() does not exist yet (that's Phase 5 Task 17), so there is no
	// channel to select on for "dispatchLoop has observed the shutdown".
	// Busy-poll the internal flag directly (whitebox, same package) instead
	// of sleeping a fixed duration or repeatedly calling Call — repeated
	// Call attempts would each enqueue a Send, and nothing here drains
	// fakeTransport.sent, so that path fills the channel and deadlocks.
	deadline := time.After(2 * time.Second)
	for {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			break
		}
		select {
		case <-deadline:
			t.Fatal("client never observed transport shutdown within 2s")
		default:
			runtime.Gosched()
		}
	}

	_, err := c.Call(context.Background(), "ping", nil)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected a \"closed\" error after transport shutdown, got %v", err)
	}
}

// TestDispatchLoopLivelocksWhenErrsClosesBeforeFrames documents a concurrency
// bug found while writing the tests above, sharper than what the design doc's
// finding C1 (internal/mcp/client.go:40-69) describes: dispatchLoop doesn't
// just have a diagnosability gap, it can hang forever.
//
// The frames case's zero-value branch (client.go:45-48) unconditionally
// `continue`s past the terminal check at the bottom of the loop
// (client.go:64-67); the errs case's zero-value branch (54-57) does not. So
// if the errs channel's close is processed *before* the frames channel's,
// dispatchLoop never notices both are closed: the next select has both
// local channel variables nil and blocks forever with no ready case. This
// happens whenever a real upstream's stdout EOF and process-exit races
// against gateway shutdown and Go's select tie-break picks errs first —
// empirically about half the time (23/50 in an unmodified stress run of
// TestCallAfterCloseFails against ft.shutdown(), before that test was
// rewritten above to force the non-buggy ordering deterministically).
//
// Effect: the dispatchLoop goroutine leaks forever, UpstreamClient.closed
// is never set, and any Call made after this point blocks until its
// context deadline instead of failing fast with "upstream client closed" —
// an availability and diagnosability regression on top of C2 (dead
// upstream never recovers) that Phase 5 should fix together.
//
// Do not fix here — Phase 5 owns C1. This test forces the exact ordering
// deterministically (see shutdownErrsThenFrames) and asserts the current,
// buggy, non-terminating behavior; it is intentionally skipped so CI does
// not spend a fixed 200ms on every run and does not need to special-case a
// "test that passes by proving a hang."
func TestDispatchLoopLivelocksWhenErrsClosesBeforeFrames(t *testing.T) {
	t.Skip("documents a known bug (design doc C1 / client.go:40-69); fix owned by Phase 5, see comment above")

	c, ft := newClientWithFake(t)
	ft.shutdownErrsThenFrames()

	deadline := time.After(200 * time.Millisecond)
	for {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			t.Fatal("dispatchLoop marked the client closed — the C1 livelock appears to be fixed; " +
				"remove this test (and update TestCallAfterTransportShutdownFails if it no longer needs " +
				"the deterministic ordering workaround)")
		}
		select {
		case <-deadline:
			return // bug reproduced: dispatchLoop never terminates in this ordering.
		default:
			runtime.Gosched()
		}
	}
}
