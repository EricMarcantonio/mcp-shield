package main

import (
	"bytes"
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

func runCLI(cmd string, args []string) error {
	switch cmd {
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
		return fmt.Errorf("unknown subcommand %q", cmd)
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
	fmt.Fprintln(tw, "ID\tNAME\tENDPOINT\tREGISTERED")
	for _, r := range rows {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", r.ID, r.Name, r.Endpoint, r.CreatedAt.Format(time.RFC3339))
	}
	return tw.Flush()
}

type pendingRow struct {
	ID        int64     `json:"id"`
	Server    string    `json:"server"`
	Hash      string    `json:"hash"`
	Risk      string    `json:"risk"`
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
	fmt.Fprintln(tw, "ID\tSERVER\tRISK\tHASH\tCHANGES")
	for _, r := range rows {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%.12s\t%d change(s)\n", r.ID, r.Server, r.Risk, r.Hash, len(r.Changes))
	}
	return tw.Flush()
}

func cliDecision(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ExitOnError)
	username := fs.String("username", "cli", "who is making this decision")
	reason := fs.String("reason", "", "why")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: mcp-shield %s <manifest-id>", action)
	}
	id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
	if err != nil {
		return fmt.Errorf("invalid manifest id: %w", err)
	}

	body, _ := json.Marshal(map[string]string{"username": *username, "reason": *reason})
	url := fmt.Sprintf("%s/api/manifests/%d/%s", apiBase(), id, action)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
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
	var out bytes.Buffer
	if err := getRaw(fmt.Sprintf("/api/manifests/%s/diff", args[0]), &out); err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, out.Bytes(), "", "  "); err != nil {
		fmt.Println(out.String())
		return nil
	}
	fmt.Println(pretty.String())
	return nil
}

func getJSON(path string, out any) error {
	resp, err := http.Get(apiBase() + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func getRaw(path string, out *bytes.Buffer) error {
	resp, err := http.Get(apiBase() + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s: %s", path, resp.Status, string(b))
	}
	_, err = out.ReadFrom(resp.Body)
	return err
}
