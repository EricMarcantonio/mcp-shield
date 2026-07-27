// Command gateway is mcp-shield: run with no arguments (or "serve") to
// start the proxy+API daemon, or with a subcommand (servers, manifests,
// approve, reject, diff) to act as the `mcp-shield` CLI against a running
// gateway's API.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/EricMarcantonio/mcp-shield/internal/app"
	"github.com/EricMarcantonio/mcp-shield/internal/approval"
	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

// version is stamped by GoReleaser via -ldflags at release time
// (-X main.version={{.Version}}); "dev" identifies a local, non-release build.
var version = "dev"

// resolveVersion reports the build's version string.
//
// GoReleaser stamps `version` via -ldflags, so release archives and the Docker
// image are already correct. `go install module@vX.Y.Z` applies no ldflags,
// which left an installed binary reporting "dev" and unable to say which
// release it came from. The module version the toolchain recorded in the build
// info is exactly that answer, so prefer it when nothing was stamped.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return version
	}
	return strings.TrimPrefix(info.Main.Version, "v")
}

func main() {
	if len(os.Args) > 1 && os.Args[1] != "serve" {
		if err := runCLI(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}

	if err := runDaemon(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func runDaemon() error {
	cfg := app.Config{
		DatabasePath:     getenv("DATABASE_PATH", "data/mcp.db"),
		ProxyAddr:        getenv("PROXY_ADDR", ":8080"),
		APIAddr:          getenv("API_ADDR", ":8081"),
		FailMode:         approval.FailMode(getenv("FAIL_MODE", string(approval.FailModeBlock))),
		TemplatesDir:     os.Getenv("TEMPLATES_DIR"),
		NotifyConfigPath: getenv("NOTIFY_CONFIG_PATH", "config/notify.json"),
	}

	if raw := os.Getenv("UPSTREAM_TIMEOUT"); raw != "" {
		timeout, err := time.ParseDuration(raw)
		if err != nil {
			return fmt.Errorf("UPSTREAM_TIMEOUT %q: %w", raw, err)
		}
		cfg.UpstreamTimeout = timeout
	}

	servers, err := loadServerConfig(getenv("CONFIG_PATH", "config/servers.json"))
	if err != nil {
		return fmt.Errorf("load server config: %w", err)
	}
	cfg.Servers = servers

	a, err := app.New(cfg)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := a.Start(ctx); err != nil {
		return err
	}
	slog.Info("mcp-shield gateway started", "proxy", a.ProxyAddr, "api", a.APIAddr, "fail_mode", cfg.FailMode)

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return a.Shutdown(shutdownCtx)
}

func loadServerConfig(path string) ([]mcp.ServerConfig, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: path is the operator-supplied --config flag, not attacker input
	if os.IsNotExist(err) {
		slog.Warn("no server config found; gateway will proxy no servers", "path", path)
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var servers []mcp.ServerConfig
	if err := json.Unmarshal(b, &servers); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return servers, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
