// Command server is a fake MCP server used to exercise mcp-shield's
// manifest/diff/approval pipeline. Its tool set is selected one of two
// ways:
//   - -version / TEST_SERVER_VERSION picks a fixed built-in tool set
//     (v1/v2/v3), used by the automated integration test.
//   - -tools-file / TOOLS_FILE points at a JSON file of tool definitions
//     that is re-read on every tools/list call — no restart needed. This
//     is the knob for manual testing: edit the file, hit the gateway
//     again, watch a new PENDING manifest appear.
//
// -tools-file takes precedence over -version when both are set.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/EricMarcantonio/mcp-shield/internal/mcp"
)

func main() {
	version := flag.String("version", "", "tool set version: v1, v2, or v3")
	toolsFile := flag.String("tools-file", "", "path to a JSON file of tool definitions, reloaded on every tools/list call")
	flag.Parse()

	v := *version
	if v == "" {
		v = os.Getenv("TEST_SERVER_VERSION")
	}
	if v == "" {
		v = "v1"
	}

	tf := *toolsFile
	if tf == "" {
		tf = os.Getenv("TOOLS_FILE")
	}

	src := &toolSource{static: toolsForVersion(v), filePath: tf}

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			handleLine(line, src.Tools())
		}
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintln(os.Stderr, "read error:", err)
			return
		}
	}
}

// toolSource resolves the current tool set for each request. When
// filePath is set, it reads and parses that file fresh every time —
// deliberately no caching — so a manual edit takes effect on the very
// next tools/list call.
type toolSource struct {
	static   []mcp.Tool
	filePath string
}

func (s *toolSource) Tools() []mcp.Tool {
	if s.filePath == "" {
		return s.static
	}
	b, err := os.ReadFile(s.filePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tools-file: read:", err)
		return s.static
	}
	var tools []mcp.Tool
	if err := json.Unmarshal(b, &tools); err != nil {
		fmt.Fprintln(os.Stderr, "tools-file: parse:", err)
		return s.static
	}
	return tools
}

func handleLine(line []byte, tools []mcp.Tool) {
	var req mcp.Request
	if err := json.Unmarshal(line, &req); err != nil {
		return
	}

	var resp mcp.Response
	resp.JSONRPC = mcp.JSONRPCVersion
	resp.ID = req.ID

	switch req.Method {
	case mcp.MethodInitialize:
		result, _ := json.Marshal(mcp.InitializeResult{
			ProtocolVersion: mcp.ProtocolVersion,
			ServerInfo:      mcp.ServerInfo{Name: "mcp-shield-testserver", Version: "0.1.0"},
		})
		resp.Result = result
	case mcp.MethodToolsList:
		result, _ := json.Marshal(mcp.ToolsListResult{Tools: tools})
		resp.Result = result
	case mcp.MethodPromptsList:
		result, _ := json.Marshal(mcp.PromptsListResult{Prompts: []mcp.Prompt{}})
		resp.Result = result
	case mcp.MethodResourcesList:
		result, _ := json.Marshal(mcp.ResourcesListResult{Resources: []mcp.Resource{}})
		resp.Result = result
	case mcp.MethodToolsCall:
		var params mcp.CallToolParams
		_ = json.Unmarshal(req.Params, &params)
		result, _ := json.Marshal(mcp.CallToolResult{
			Content: []mcp.ContentBlock{{Type: "text", Text: "ok: " + params.Name}},
		})
		resp.Result = result
	default:
		resp.Error = &mcp.RPCError{Code: -32601, Message: "method not found: " + req.Method}
	}

	out, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Println(string(out))
}

func toolsForVersion(v string) []mcp.Tool {
	calendarRead := mcp.Tool{
		Name:        "calendar_read",
		Description: "Read events from the calendar",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"eventId":{"type":"string"}},"required":["eventId"]}`),
	}
	calendarCreate := mcp.Tool{
		Name:        "calendar_create",
		Description: "Create a new calendar event",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
	}
	uploadAttachment := mcp.Tool{
		Name:        "upload_attachment",
		Description: "Upload a file attachment to an event",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"eventId":{"type":"string"},"data":{"type":"string"}},"required":["eventId","data"]}`),
	}
	deleteCalendar := mcp.Tool{
		Name:        "delete_calendar",
		Description: "Permanently delete a calendar and all its events",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"calendarId":{"type":"string"}},"required":["calendarId"]}`),
	}
	executeCommand := mcp.Tool{
		Name:        "execute_command",
		Description: "Execute an arbitrary shell command on the host",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}},"required":["command"]}`),
	}

	switch v {
	case "v1":
		return []mcp.Tool{calendarRead, calendarCreate}
	case "v2":
		return []mcp.Tool{calendarRead, calendarCreate, uploadAttachment}
	case "v3":
		return []mcp.Tool{calendarRead, calendarCreate, uploadAttachment, deleteCalendar, executeCommand}
	default:
		return []mcp.Tool{calendarRead, calendarCreate}
	}
}
