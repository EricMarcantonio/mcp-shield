// Package app wires the gateway's components (store, approval workflow,
// proxy, API/dashboard) into one runnable process. It exists so
// cmd/mcp-shield/main.go stays a thin entrypoint and so integration tests can
// start/stop a fully wired gateway in-process on ephemeral ports.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/api"
	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/database"
	"github.com/EricMarcantonio/mcp-shield/internal/manifest"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

type Config struct {
	DatabasePath string
	ProxyAddr    string // e.g. ":8080"; empty defaults to ":8080"
	APIAddr      string // e.g. ":8081"; empty defaults to ":8081"
	FailMode     approval.FailMode
	Servers      []mcp.ServerConfig
	TemplatesDir string // empty uses api.DefaultTemplatesDir

	// UpstreamTimeout bounds one proxied request's upstream work; zero
	// uses mcp.DefaultUpstreamTimeout.
	UpstreamTimeout time.Duration
}

type App struct {
	store      *database.SQLiteStore
	workflow   *approval.Workflow
	downstream *mcp.DownstreamHandler

	proxyLn  net.Listener
	apiLn    net.Listener
	proxySrv *http.Server
	apiSrv   *http.Server

	// ProxyAddr/APIAddr report the actual bound address, which matters
	// when the configured addr uses port 0 (as integration tests do).
	ProxyAddr string
	APIAddr   string
}

func New(cfg Config) (*App, error) {
	if cfg.ProxyAddr == "" {
		cfg.ProxyAddr = ":8080"
	}
	if cfg.APIAddr == "" {
		cfg.APIAddr = ":8081"
	}

	store, err := database.Open(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("app: open database: %w", err)
	}

	workflow := approval.New(store, cfg.FailMode)

	gate := &gateAdapter{store: store, workflow: workflow}
	downstream, err := mcp.NewDownstreamHandler(cfg.Servers, gate)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("app: init downstream handler: %w", err)
	}
	if cfg.UpstreamTimeout > 0 {
		downstream.UpstreamTimeout = cfg.UpstreamTimeout
	}

	var lc net.ListenConfig
	proxyLn, err := lc.Listen(context.Background(), "tcp", cfg.ProxyAddr)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("app: listen proxy: %w", err)
	}
	apiLn, err := lc.Listen(context.Background(), "tcp", cfg.APIAddr)
	if err != nil {
		_ = proxyLn.Close()
		_ = store.Close()
		return nil, fmt.Errorf("app: listen api: %w", err)
	}

	proxyMux := http.NewServeMux()
	proxyMux.Handle("/mcp/", downstream)

	apiHandler := api.NewServer(store, workflow, cfg.TemplatesDir)

	a := &App{
		store:      store,
		workflow:   workflow,
		downstream: downstream,
		proxyLn:    proxyLn,
		apiLn:      apiLn,
		proxySrv:   &http.Server{Handler: proxyMux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second},
		apiSrv:     &http.Server{Handler: apiHandler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second},
		ProxyAddr:  proxyLn.Addr().String(),
		APIAddr:    apiLn.Addr().String(),
	}
	return a, nil
}

// Start launches the proxy and API listeners in background goroutines and
// returns immediately. Serve errors after a clean Shutdown are swallowed;
// anything else is logged.
func (a *App) Start(_ context.Context) error {
	go func() {
		if err := a.proxySrv.Serve(a.proxyLn); err != nil && err != http.ErrServerClosed {
			slog.Error("proxy server exited", "error", err)
		}
	}()
	go func() {
		if err := a.apiSrv.Serve(a.apiLn); err != nil && err != http.ErrServerClosed {
			slog.Error("api server exited", "error", err)
		}
	}()
	return nil
}

func (a *App) Shutdown(ctx context.Context) error {
	err1 := a.proxySrv.Shutdown(ctx)
	err2 := a.apiSrv.Shutdown(ctx)
	err3 := a.store.Close()
	for _, err := range []error{err1, err2, err3} {
		if err != nil {
			return err
		}
	}
	return nil
}

// gateAdapter bridges mcp.Gate (defined in terms of mcp.Tool/Prompt/
// Resource, to avoid an import cycle) to approval.Workflow (defined in
// terms of manifest.Manifest). It also owns resolving/creating the
// database Server row for a given server name, since the mcp package has
// no notion of database identity.
type gateAdapter struct {
	store    database.Store
	workflow *approval.Workflow
}

func (g *gateAdapter) CheckAndRecord(ctx context.Context, serverName string, tools []mcp.Tool, prompts []mcp.Prompt, resources []mcp.Resource) (*mcp.GateDecision, error) {
	srv, err := g.store.GetServerByName(ctx, serverName)
	if err != nil {
		return nil, err
	}
	if srv == nil {
		srv, err = g.store.CreateServer(ctx, serverName, "")
		if err != nil {
			return nil, err
		}
	}

	// A capability set that violates the protocol's unique-identity rule is
	// rejected here, which fails the in-flight request closed rather than
	// fingerprinting a capability set the gate could not enforce.
	m, err := manifest.Build(tools, prompts, resources)
	if err != nil {
		return nil, err
	}
	result, err := g.workflow.CheckAndRecord(ctx, srv.ID, m)
	if err != nil {
		return nil, err
	}
	return &mcp.GateDecision{
		ManifestID:    result.ManifestID,
		State:         result.State,
		Warn:          result.Warn,
		SafeTools:     result.SafeTools,
		SafePrompts:   result.SafePrompts,
		SafeResources: result.SafeResources,
	}, nil
}
