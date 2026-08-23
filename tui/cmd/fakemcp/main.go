// Command fakemcp is a deterministic, harmless stand-in for
// agent-supervisor's mcp_server.py -- built to drive S7's send flow
// through testdata/vhs/chat-send.tape against the REAL binary (real
// internal/mcp wire framing, real internal/session.Ops.Send
// classification, real internal/chat.Model async trySend/Update/render),
// without ever spawning a real `claude -p --resume` subprocess or
// touching a real tmux session.
//
// It speaks just enough of MCP's JSON-RPC-over-stdio wire shape
// (internal/mcp/client.go's own rpcRequest/rpcResponse) to answer
// "initialize" and "tools/call" for exactly one tool, session_send. The
// message argument itself picks the canned outcome, so one fixture drives
// all three states this PR adds:
//
//   - message containing "FAKE_FAIL"    -> an isError tools/call result,
//     the same text shape SessionSendSource.write() raises for a
//     confirmed failure (agent-supervisor's own supervisor_view.py).
//   - message containing "FAKE_UNKNOWN" -> an isError result carrying the
//     literal "outcome UNKNOWN" marker internal/session.Ops.Send's own
//     unknownMarker looks for.
//   - anything else -> a delivered result.
//
// Every branch sleeps briefly before replying (fakeReplyDelay) so a VHS
// run has time to capture the composer's own sendInFlight state before
// it resolves -- this is the one thing a real supervisor round trip
// cannot offer safely (a real session_send genuinely takes minutes; this
// fixture takes seconds, on purpose).
//
// Never wired into cmd/keelson's own launch path -- only ever named via
// `-mcp-cmd "go run ./cmd/fakemcp"` for a VHS run. Not a second MCP
// server implementation of anything real: it understands one tool name
// and nothing else, and exists solely so testdata/vhs/chat-send.tape can
// drive real code instead of asserting it works from a unit test alone.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const fakeReplyDelay = 1500 * time.Millisecond

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Result  any    `json:"result,omitempty"`
}

type toolCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolContent struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError"`
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	for in.Scan() {
		line := in.Bytes()
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		reply(out, req)
		out.Flush()
	}
}

func reply(out *bufio.Writer, req rpcRequest) {
	var result any
	switch req.Method {
	case "initialize":
		result = map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "fakemcp", "version": "0.0.0"},
		}
	case "notifications/initialized":
		return // notification, no response expected
	case "tools/call":
		result = handleToolsCall(req.Params)
	default:
		result = map[string]any{}
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	body, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintln(out, string(body))
}

func handleToolsCall(raw json.RawMessage) toolContent {
	var params toolCallParams
	if err := json.Unmarshal(raw, &params); err != nil || params.Name != "session_send" {
		return toolContent{IsError: true, Content: []contentBlock{{Type: "text", Text: "fakemcp: unknown tool"}}}
	}
	message, _ := params.Arguments["message"].(string)
	sessionID, _ := params.Arguments["session_id"].(string)

	time.Sleep(fakeReplyDelay)

	switch {
	case strings.Contains(message, "FAKE_FAIL"):
		return toolContent{IsError: true, Content: []contentBlock{{
			Type: "text",
			Text: "send failed: agent: turn reported is_error subtype=\"error_max_turns\"",
		}}}
	case strings.Contains(message, "FAKE_UNKNOWN"):
		return toolContent{IsError: true, Content: []contentBlock{{
			Type: "text",
			Text: "send outcome UNKNOWN, not failed -- a turn did not confirm before its deadline (agent-supervisor#488): fakemcp simulated timeout",
		}}}
	default:
		body, _ := json.Marshal(map[string]any{
			"session_id": sessionID, "delivered": true, "turns": 1, "cost_usd": 0.01,
		})
		return toolContent{Content: []contentBlock{{Type: "text", Text: string(body)}}}
	}
}
