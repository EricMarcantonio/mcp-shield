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

// shutdown closes both channels back-to-back, leaving the ordering
// dispatchLoop observes up to the scheduler — the unordered shutdown a real
// upstream produces. dispatchLoop must terminate either way.
func (t *fakeTransport) shutdown() {
	close(t.frames)
	close(t.errs)
}

// shutdownErrsThenFrames closes errs alone first, then frames. Because only
// one channel is ready at a time, this deterministically forces dispatchLoop
// to drain errs before frames — the ordering that used to livelock it, and
// the one StdioTransport's LIFO defers produce on every real shutdown (see
// TestDispatchLoopTerminatesWhenErrsClosesBeforeFrames).
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
	// nothing to fail.
	<-ft.sent
	// The terminal transport error must reach the caller: an operator
	// reading a failed tool call has to be able to tell "upstream crashed"
	// from "upstream idle".
	ft.errs <- errors.New("pipe burst")
	ft.shutdown()

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
		if !strings.Contains(resp.Error.Message, "pipe burst") {
			t.Fatalf("expected the underlying transport error to be preserved, got %q", resp.Error.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call was not failed within 2s of transport shutdown")
	}
}

func TestCallAfterTransportShutdownFails(t *testing.T) {
	c, ft := newClientWithFake(t)
	// Unordered shutdown: which channel dispatchLoop drains first is up to
	// the scheduler, and it must terminate either way.
	ft.shutdown()

	waitClosed(t, c)

	_, err := c.Call(context.Background(), "ping", nil)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected a \"closed\" error after transport shutdown, got %v", err)
	}
}

// TestDispatchLoopTerminatesWhenErrsClosesBeforeFrames guards the fix for a
// permanent hang. dispatchLoop used to set the drained channel to nil and
// `continue` past a terminal check placed at the *bottom* of the loop body,
// so if errs was drained before frames the next select blocked on two nil
// channels with no ready case: the goroutine leaked, failAllPending never
// ran, and every in-flight caller waited until its own context expired
// rather than failing fast.
//
// This was not a rare tie-break. StdioTransport.readLoop registers
// `defer close(t.frames)` before `defer close(t.errs)`, and defers run LIFO,
// so a real upstream shutting down closes errs *first* every time — the
// buggy ordering was the production ordering.
//
// Termination now lives in the loop condition instead of the body, so it is
// checked on every path. This test forces the once-fatal ordering
// deterministically (see shutdownErrsThenFrames).
func TestDispatchLoopTerminatesWhenErrsClosesBeforeFrames(t *testing.T) {
	c, ft := newClientWithFake(t)
	ft.shutdownErrsThenFrames()

	waitClosed(t, c)
}

// waitClosed blocks until dispatchLoop has observed the transport shutting
// down and marked the client closed. It polls the internal flag (whitebox,
// same package) rather than sleeping a fixed duration or retrying Call —
// each Call attempt enqueues a Send, and nothing drains fakeTransport.sent,
// so that path fills the channel and deadlocks.
func waitClosed(t *testing.T, c *UpstreamClient) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		select {
		case <-deadline:
			t.Fatal("dispatchLoop never observed the transport shutdown within 2s")
		default:
			runtime.Gosched()
		}
	}
}
