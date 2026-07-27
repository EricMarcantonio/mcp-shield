package mcp

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
)

// Frame sizing for newline-delimited JSON-RPC. Every reader of the stdio
// convention — the upstream transport here and the `mcp-shield connect`
// shim — shares these so a frame that one side will emit is a frame the
// other side will accept.
const (
	// FrameBufferSize is the initial read buffer for one frame. It grows on
	// demand up to MaxFrameSize.
	FrameBufferSize = 64 * 1024
	// MaxFrameSize caps a single frame. bufio.Scanner's own 64 KiB default
	// is far too small for a tools/list reply from a server with many tools,
	// and overflowing it truncates capability data silently — which in a
	// gateway that gates on capabilities would be a correctness hole, not a
	// performance nuisance.
	MaxFrameSize = 8 * 1024 * 1024
)

// Transport is a duplex byte-frame channel to a single MCP peer.
// Frames are newline-delimited JSON-RPC messages (the stdio MCP convention).
type Transport interface {
	Start(ctx context.Context) error
	Send(msg []byte) error
	// Recv returns channels streaming decoded frames and terminal errors.
	// Both channels are closed when the transport shuts down.
	Recv() (<-chan []byte, <-chan error)
	Close() error
}

// StdioTransport spawns a child process and speaks newline-delimited
// JSON-RPC over its stdin/stdout.
type StdioTransport struct {
	cmdPath string
	args    []string
	env     []string

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	frames chan []byte
	errs   chan error

	// done is closed by Close to release readLoop if it is parked on a send
	// to a full frames channel. closeOnce keeps a second Close from panicking
	// on a double close.
	done      chan struct{}
	closeOnce sync.Once
}

func NewStdioTransport(cmdPath string, args, env []string) *StdioTransport {
	return &StdioTransport{
		cmdPath: cmdPath,
		args:    args,
		env:     env,
	}
}

func (t *StdioTransport) Start(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	cmd := exec.CommandContext(ctx, t.cmdPath, t.args...) //nolint:gosec // G204: launching the operator-configured upstream MCP server is this transport's purpose
	if len(t.env) > 0 {
		cmd.Env = append(cmd.Environ(), t.env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdio transport: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdio transport: stdout pipe: %w", err)
	}
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("stdio transport: start %s: %w", t.cmdPath, err)
	}

	t.cmd = cmd
	t.stdin = stdin
	t.frames = make(chan []byte, 16)
	t.errs = make(chan error, 1)
	t.done = make(chan struct{})

	go t.readLoop(stdout)

	return nil
}

func (t *StdioTransport) readLoop(stdout io.ReadCloser) {
	defer close(t.frames)
	defer close(t.errs)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, FrameBufferSize), MaxFrameSize)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		frame := make([]byte, len(line))
		copy(frame, line)
		// A bare send here wedged the goroutine forever once nothing was
		// draining frames (cap 16) — which is exactly the state the client
		// leaves behind after dispatchLoop terminates — leaking a goroutine
		// and a pipe per dead upstream. The deferred channel closes below
		// still run when this returns via done.
		select {
		case t.frames <- frame:
		case <-t.done:
			return
		}
	}
	if err := scanner.Err(); err != nil {
		t.sendTerminal(fmt.Errorf("stdio transport: read: %w", err))
		return
	}
	t.sendTerminal(io.EOF)
}

// sendTerminal reports the reason the read loop ended, giving up if Close
// already happened — errs has capacity 1 and nothing is guaranteed to be
// listening, so an unconditional send is another way to wedge here.
func (t *StdioTransport) sendTerminal(err error) {
	select {
	case t.errs <- err:
	case <-t.done:
	}
}

func (t *StdioTransport) Send(msg []byte) error {
	t.mu.Lock()
	stdin := t.stdin
	t.mu.Unlock()
	if stdin == nil {
		return fmt.Errorf("stdio transport: not started")
	}
	if _, err := stdin.Write(append(msg, '\n')); err != nil {
		return fmt.Errorf("stdio transport: write: %w", err)
	}
	return nil
}

func (t *StdioTransport) Recv() (<-chan []byte, <-chan error) {
	return t.frames, t.errs
}

func (t *StdioTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closeOnce.Do(func() {
		if t.done != nil {
			close(t.done)
		}
	})
	if t.stdin != nil {
		_ = t.stdin.Close()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
		_ = t.cmd.Wait()
	}
	return nil
}
