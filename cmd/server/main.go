// Command server is a fake MCP server used to exercise mcp-shield's
// manifest/diff/approval pipeline. Its tool set is selected by -version
// (or TEST_SERVER_VERSION) so integration tests and manual testing can
// simulate an upstream server evolving from a benign v1 to a risky v3.
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
	flag.Parse()

	v := *version
	if v == "" {
		v = os.Getenv("TEST_SERVER_VERSION")
	}
	if v == "" {
		v = "v1"
	}

	tools := toolsForVersion(v)

	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			handleLine(line, tools)
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
			ProtocolVersion: "2024-11-05",
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
