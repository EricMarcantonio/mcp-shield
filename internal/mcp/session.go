package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ServerConfig describes one upstream MCP server the gateway proxies to.
type ServerConfig struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
}

// upstream is the subset of *UpstreamClient that serverSession depends on.
// It exists so tests can substitute a fake upstream without spawning a real
// subprocess; *UpstreamClient satisfies it unmodified.
type upstream interface {
	// Closed reports whether the connection is dead and can only be
	// recovered by starting a new one.
	Closed() bool
	Initialize(ctx context.Context) (*InitializeResult, error)
	ListTools(ctx context.Context) ([]Tool, error)
	ListPrompts(ctx context.Context) ([]Prompt, error)
	ListResources(ctx context.Context) ([]Resource, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error)
	Call(ctx context.Context, method string, params any) (*Response, error)
	Close() error
}

// upstreamFactory constructs the upstream connection for a serverSession.
// Production code always uses stdioUpstreamFactory; tests substitute a
// factory that returns a fake.
type upstreamFactory func(ctx context.Context, cfg ServerConfig) (upstream, error)

func stdioUpstreamFactory(ctx context.Context, cfg ServerConfig) (upstream, error) {
	return NewStdioUpstreamClient(ctx, cfg.Command, cfg.Args, cfg.Env)
}

// Restart policy for an upstream that will not come up. Consecutive failures
// back the retry off exponentially from restartBackoffBase to
// restartBackoffCap, so a crash-looping server cannot be respawned once per
// request. Past circuitOpenAfter consecutive failures the session stops
// describing the upstream as transiently broken and reports it as
// unavailable; the backoff is the breaker's open period, and the single
// attempt allowed once it elapses is the half-open probe.
//
// Requests are refused for the whole time the breaker is open. That is
// deliberate: an unreachable upstream must fail closed, never become an
// implicit allow.
const (
	restartBackoffBase = time.Second
	restartBackoffCap  = 30 * time.Second
	circuitOpenAfter   = 5
)

// serverSession holds the lazily-started upstream connection for one
// configured server, and the restart policy that governs respawning it.
type serverSession struct {
	cfg       ServerConfig
	newClient upstreamFactory
	now       func() time.Time // nil means time.Now; injected by tests

	mu          sync.Mutex
	client      upstream
	inited      bool
	failures    int
	lastFailure time.Time
	lastErr     error
}

func (s *serverSession) ensureStarted(ctx context.Context) (upstream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// A dead subprocess used to be kept forever: s.client was only ever
	// assigned, never cleared, so every later request got the same closed
	// client back until the gateway process itself was restarted.
	if s.client != nil && s.client.Closed() {
		s.discardClient()
	}

	if s.client == nil {
		if err := s.restartPermitted(); err != nil {
			return nil, err
		}
		// The subprocess must outlive this one HTTP request — it's a
		// persistent per-server session reused across many requests —
		// so it's started against context.Background(), not the
		// request's context, which would otherwise kill the process the
		// moment this request's context is done.
		client, err := s.newClient(context.Background(), s.cfg)
		if err != nil {
			s.recordFailure(err)
			return nil, fmt.Errorf("upstream %q failed to start: %w", s.cfg.Name, err)
		}
		s.client = client
	}

	if !s.inited {
		if _, err := s.client.Initialize(ctx); err != nil {
			// The process is up but never completed the handshake, so it
			// will never answer. Discard it rather than wedging the
			// session on a peer that cannot serve.
			s.discardClient()
			s.recordFailure(err)
			return nil, fmt.Errorf("upstream %q handshake failed: %w", s.cfg.Name, err)
		}
		s.inited = true
	}

	s.failures = 0
	s.lastErr = nil
	return s.client, nil
}

func (s *serverSession) discardClient() {
	if s.client != nil {
		_ = s.client.Close()
	}
	s.client = nil
	s.inited = false
}

func (s *serverSession) recordFailure(err error) {
	s.failures++
	s.lastFailure = s.clock()
	s.lastErr = err
	slog.Warn("upstream connection failed",
		"server", s.cfg.Name, "consecutive_failures", s.failures, "error", err)
}

// restartPermitted reports whether a new connection attempt is allowed right
// now, or an error naming the upstream's state and when it will next be
// probed.
func (s *serverSession) restartPermitted() error {
	wait := s.restartWait()
	if wait <= 0 {
		return nil
	}
	if s.failures >= circuitOpenAfter {
		return fmt.Errorf("upstream %q is unavailable after %d consecutive connection failures (next attempt in %s); last error: %w",
			s.cfg.Name, s.failures, wait.Round(time.Second), s.lastErr)
	}
	return fmt.Errorf("upstream %q failed to start, retrying in %s; last error: %w",
		s.cfg.Name, wait.Round(time.Second), s.lastErr)
}

// restartWait returns how long is left of the current backoff window, or 0
// when a connection attempt may proceed.
func (s *serverSession) restartWait() time.Duration {
	if s.failures == 0 {
		return 0
	}
	backoff := restartBackoffBase << min(s.failures-1, 5)
	if backoff > restartBackoffCap {
		backoff = restartBackoffCap
	}
	if remaining := backoff - s.clock().Sub(s.lastFailure); remaining > 0 {
		return remaining
	}
	return 0
}

func (s *serverSession) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}
