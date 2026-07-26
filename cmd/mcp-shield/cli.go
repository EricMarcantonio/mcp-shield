package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"text/tabwriter"
	"time"
)

func apiBase() string {
	if v := os.Getenv("MCP_SHIELD_API"); v != "" {
		return v
	}
	return "http://localhost:8081"
}

// usage is the single source of truth for what subcommands exist; main
// routes anything that is not "serve" here so the list cannot drift between
// two switch statements.
const usage = "usage: mcp-shield [serve|servers|manifests|approve <id>|reject <id>|diff <id>|version]"

func runCLI(cmd string, args []string) error {
	switch cmd {
	case "version":
		fmt.Println("mcp-shield " + version)
		return nil
	case "servers":
		return cliServers()
	case "manifests":
		return cliManifests()
	case "approve":
		return cliDecision("approve", args)
	case "reject":
		return cliDecision("reject", args)
	case "diff":
		return cliDiff(args)
	default:
		return fmt.Errorf("unknown command %q\n%s", cmd, usage)
	}
}

type serverRow struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Endpoint  string    `json:"endpoint"`
	CreatedAt time.Time `json:"created_at"`
}

func cliServers() error {
	var rows []serverRow
	if err := getJSON("/api/servers", &rows); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tNAME\tENDPOINT\tREGISTERED"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.ID, r.Name, r.Endpoint, r.CreatedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

type pendingRow struct {
	ID        int64     `json:"id"`
	Server    string    `json:"server"`
	Hash      string    `json:"hash"`
	Changes   []string  `json:"changes"`
	CreatedAt time.Time `json:"created_at"`
}

func cliManifests() error {
	var rows []pendingRow
	if err := getJSON("/api/manifests/pending", &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("no pending manifests")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ID\tSERVER\tHASH\tCHANGES"); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(tw, "%d\t%s\t%.12s\t%d change(s)\n", r.ID, r.Server, r.Hash, len(r.Changes)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func cliDecision(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ExitOnError)
	username := fs.String("username", "cli", "who is making this decision")
	reason := fs.String("reason", "", "why")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: mcp-shield %s <manifest-id>", action)
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid manifest id: %w", err)
	}

	body, _ := json.Marshal(map[string]string{"username": *username, "reason": *reason})
	// The target is the operator's own mcp-shield API (MCP_SHIELD_API env
	// var, defaulting to localhost:8081), not attacker-controlled input.
	url := fmt.Sprintf("%s/api/manifests/%d/%s", apiBase(), id, action)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s failed: %s: %s", action, resp.Status, string(b))
	}
	fmt.Printf("manifest %d: %sd\n", id, action)
	return nil
}

func cliDiff(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: mcp-shield diff <manifest-id>")
	}
	raw, err := apiGet(fmt.Sprintf("/api/manifests/%s/diff", args[0]))
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		fmt.Println(string(raw))
		return nil
	}
	fmt.Println(pretty.String())
	return nil
}

func getJSON(path string, out any) error {
	b, err := apiGet(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

// apiGet issues a GET against the operator-configured mcp-shield API
// (MCP_SHIELD_API env var, defaulting to localhost:8081), not
// attacker-controlled input, and returns the response body. Every non-2xx
// is an error carrying the server's own message, so no caller has to
// re-derive that.
func apiGet(path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, apiBase()+path, nil) //nolint:gosec // G704: URL is built from the operator-configured API base, not external input
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // G704: request targets the operator-configured API base, not external input
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s: read response: %w", path, err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s: %s", path, resp.Status, string(body))
	}
	return body, nil
}
