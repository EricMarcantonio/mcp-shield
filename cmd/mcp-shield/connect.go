package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// This file implements `mcp-shield connect <server>`: the stdio bridge that
// lets a client which can only spawn a subprocess and speak newline-delimited
// JSON-RPC over its stdin/stdout (Claude Desktop's classic config, among
// others) talk to a gateway that speaks HTTP. Each inbound frame becomes one
// POST to {gateway}/mcp/{server}; each response comes back as one outbound
// frame.
//
// The one rule everything here bends to: stdout carries the protocol and
// nothing else. Every diagnostic goes to stderr, and every failure the shim
// can attribute to a request with an id comes back as a well-formed JSON-RPC
// error frame, so a client sees a refusal instead of a silent hang.
//
// Not supported, by design: server-initiated messages. The gateway re-fetches
// and re-gates capabilities on every call, so there is no `listChanged` push
// to relay — see decision D3.

const (
	defaultGatewayURL = "http://localhost:8080"

	// shimRequestTimeout bounds one forwarded request. It is generous because
	// the gateway may be waiting on a human approval decision upstream; the
	// point is only that a wedged gateway cannot pin a goroutine forever.
	shimRequestTimeout = 5 * time.Minute

	// codeParseError is the JSON-RPC 2.0 standard code for an unparseable
	// request, the one failure mode that is the client's fault rather than
	// the gateway's.
	codeParseError = -32700
)

// parseConnectArgs pulls the server name and gateway URL out of the argv
// tail. The server name is accepted on either side of the flags: the
// documented form is `connect <server> --gateway URL`, but Go's flag package
// stops at the first non-flag argument, so the tail is parsed twice.
func parseConnectArgs(args []string) (server, gateway string, err error) {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	gatewayFlag := fs.String("gateway", getenv("MCP_SHIELD_PROXY", defaultGatewayURL),
		"base URL of the mcp-shield gateway proxy")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return "", "", errors.New("usage: mcp-shield connect <server> [--gateway URL]")
	}
	server = rest[0]
	if err := fs.Parse(rest[1:]); err != nil {
		return "", "", err
	}
	if fs.NArg() > 0 {
		return "", "", fmt.Errorf("unexpected argument %q\nusage: mcp-shield connect <server> [--gateway URL]", fs.Arg(0))
	}
	return server, *gatewayFlag, nil
}

func cliConnect(args []string) error {
	server, gateway, err := parseConnectArgs(args)
	if err != nil {
		return err
	}
	return newShim(gateway, server).run(os.Stdin, os.Stdout)
}

// shim bridges one client's stdio session to one gateway-proxied server.
type shim struct {
	gatewayBase string
	server      string
	client      *http.Client

	// diag is where every human-readable message goes. It is never stdout.
	diag io.Writer

	// writeMu serializes frame writes. Requests are forwarded concurrently
	// because clients pipeline them, so without this two responses would
	// interleave mid-frame and hand the client garbage.
	writeMu sync.Mutex
}

func newShim(gatewayBase, server string) *shim {
	return &shim{
		gatewayBase: gatewayBase,
		server:      server,
		client:      &http.Client{Timeout: shimRequestTimeout},
		diag:        os.Stderr,
	}
}

// run reads frames from in until EOF, forwarding each one, and returns once
// every in-flight request has finished writing to out.
func (s *shim) run(in io.Reader, out io.Writer) error {
	defer s.client.CloseIdleConnections()

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, mcp.FrameBufferSize), mcp.MaxFrameSize)

	var wg sync.WaitGroup
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		frame := make([]byte, len(line))
		copy(frame, line)

		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handle(frame, out)
		}()
	}
	wg.Wait()

	return s.scanError(scanner.Err(), out)
}

// scanError turns a read failure into something the client can see. An
// oversized frame is the interesting case: the alternative to failing here
// is handing the client a truncated capability list, which is worse than a
// dead session.
func (s *shim) scanError(err error, out io.Writer) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, bufio.ErrTooLong):
		msg := fmt.Sprintf("mcp-shield: client frame exceeds the %d MiB limit; refusing to forward a truncated request",
			mcp.MaxFrameSize/(1<<20))
		s.logf("%s", msg)
		s.writeErrorFrame(out, nil, codeParseError, msg)
		return errors.New(msg)
	default:
		s.logf("mcp-shield: reading stdin: %v", err)
		return fmt.Errorf("read stdin: %w", err)
	}
}

// handle forwards one client frame and writes back at most one response
// frame. A frame with no id is an MCP notification: it expects no reply, so
// nothing is written for it under any outcome, success or failure.
func (s *shim) handle(frame []byte, out io.Writer) {
	id, method, err := peekEnvelope(frame)
	if err != nil {
		msg := "mcp-shield: client sent a frame that is not valid JSON-RPC: " + err.Error()
		s.logf("%s", msg)
		s.writeErrorFrame(out, nil, codeParseError, msg)
		return
	}

	body, err := s.forward(frame)
	if err != nil {
		s.logf("mcp-shield: %s failed: %v", describe(id, method), err)
		if id != nil {
			s.writeErrorFrame(out, id, mcp.CodeUpstreamError, "mcp-shield: "+err.Error())
		}
		return
	}
	if id == nil {
		return
	}
	s.writeFrame(out, body)
}

// forward POSTs one frame to the gateway and returns the response body,
// compacted to a single line. Every way this can go wrong — connection
// refused, non-2xx, a body that is not a JSON-RPC response — becomes an
// error naming what happened, because a shim that swallows any of them
// leaves the client waiting forever.
func (s *shim) forward(frame []byte) ([]byte, error) {
	url := s.gatewayBase + "/mcp/" + s.server

	ctx, cancel := context.WithTimeout(context.Background(), shimRequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(frame)) //nolint:gosec // G704: the URL is built from the operator-supplied --gateway flag (or MCP_SHIELD_PROXY) plus the server name they named on the command line, not from anything on the wire
	if err != nil {
		return nil, fmt.Errorf("building request for %s: %w", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", mcp.ProtocolVersion)

	resp, err := s.client.Do(req) //nolint:gosec // G704: see above — the target is the operator-configured gateway, not external input
	if err != nil {
		return nil, fmt.Errorf("gateway unreachable at %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading gateway response from %s: %w", url, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gateway at %s returned %s: %s", url, resp.Status, snippet(body))
	}
	return compactJSONRPC(body, url)
}

// compactJSONRPC validates that body really is a JSON-RPC response and
// squeezes it onto one line, since a pretty-printed body would otherwise
// land on stdout as several broken frames.
func compactJSONRPC(body []byte, url string) ([]byte, error) {
	var probe struct {
		JSONRPC string `json:"jsonrpc"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("gateway at %s returned a body that is not JSON-RPC: %s", url, snippet(body))
	}
	if probe.JSONRPC != mcp.JSONRPCVersion {
		return nil, fmt.Errorf("gateway at %s returned a body that is not JSON-RPC (no jsonrpc %q member): %s",
			url, mcp.JSONRPCVersion, snippet(body))
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, body); err != nil {
		return nil, fmt.Errorf("gateway at %s returned a body that is not JSON-RPC: %w", url, err)
	}
	return compact.Bytes(), nil
}

// peekEnvelope reads just enough of a client frame to route it: the raw id
// (nil for a notification) and the method name for diagnostics. A JSON null
// id counts as absent — nothing useful can be addressed to it.
func peekEnvelope(frame []byte) (id json.RawMessage, method string, err error) {
	var envelope struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(frame, &envelope); err != nil {
		return nil, "", err
	}
	if len(envelope.ID) == 0 || string(envelope.ID) == "null" {
		return nil, envelope.Method, nil
	}
	return envelope.ID, envelope.Method, nil
}

func describe(id json.RawMessage, method string) string {
	if method == "" {
		method = "request"
	}
	if id == nil {
		return "notification " + method
	}
	return fmt.Sprintf("%s (id %s)", method, id)
}

func (s *shim) writeErrorFrame(out io.Writer, id json.RawMessage, code int, message string) {
	frame, err := json.Marshal(mcp.Response{
		JSONRPC: mcp.JSONRPCVersion,
		ID:      idOrNull(id),
		Error:   &mcp.RPCError{Code: code, Message: message},
	})
	if err != nil {
		s.logf("mcp-shield: could not encode error frame: %v", err)
		return
	}
	s.writeFrame(out, frame)
}

// idOrNull keeps the id member present even when there is nothing to put in
// it: JSON-RPC requires a null id on a response to a request that could not
// be parsed, and mcp.Response omits a nil id entirely.
func idOrNull(id json.RawMessage) any {
	if id == nil {
		return json.RawMessage("null")
	}
	return id
}

// writeFrame emits one newline-terminated frame in a single locked write.
// Building the whole frame first is deliberate: one Write under the lock is
// what makes concurrent responses non-interleaving.
func (s *shim) writeFrame(out io.Writer, frame []byte) {
	line := make([]byte, 0, len(frame)+1)
	line = append(line, frame...)
	line = append(line, '\n')

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := out.Write(line); err != nil {
		s.logf("mcp-shield: writing to stdout: %v", err)
	}
}

func (s *shim) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.diag, format+"\n", args...)
}

// snippet bounds untrusted gateway output so an HTML error page cannot push
// a megabyte of noise into a JSON-RPC error message.
func snippet(body []byte) string {
	const maxSnippetBytes = 200
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > maxSnippetBytes {
		return string(trimmed[:maxSnippetBytes]) + "..."
	}
	return string(trimmed)
}
