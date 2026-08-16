// Package mcp is a minimal MCP client: JSON-RPC 2.0 framed as newline-delimited
// JSON over a child process's stdio. It speaks exactly the subset
// agent-supervisor's mcp_server.py implements (initialize, tools/call) and
// nothing else -- this package has no knowledge of lanes, lanes.sh, or any
// other supervisor internal. It only knows MCP's wire shape.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// callTimeout bounds every request/response round-trip through call --
// at#22 review: a non-responding supervisor subprocess must surface as a
// visible, honest error, not an indefinite hang (the prior bare
// `resp, ok := <-ch` only ever unblocked if the child crashed and closed its
// pipes). 10s is chosen because every real tool this client calls today
// (session_attach/detach/add/remove/remove_check, each a subprocess spawn
// plus a tmux/git round-trip per internal/session's own doc comments) is a
// local operation with no network hop -- a few seconds is the honest budget
// for that, and 10s leaves generous headroom above it without leaving a
// human staring at a frozen "attaching…" footer for minutes. It is a var,
// not a const, so tests can shrink it rather than actually waiting out a
// production-length deadline.
var callTimeout = 10 * time.Second

// Client owns one child process speaking MCP over stdio. Callers must call
// Close to release it.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr io.ReadCloser

	mu     sync.Mutex // guards writes to stdin and the pending map
	nextID int64
	pend   map[int64]chan rpcResponse
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// timeoutError reports that call's round trip crossed callTimeout with no
// reply -- distinct from a JSON-RPC error (the request was routable but
// refused, e.g. an unknown tool name) and from a tool that ran and could
// not see (ToolContent.IsError). agent-tui#55: a caller above this package
// (internal/rail, without importing mcp -- see this package's own doc
// comment on why nothing above it may know it is talking to a subprocess)
// needs to tell "no reply within the deadline" apart from "the server
// rejected this call" to render an honest, runtime-checked message instead
// of a hardcoded blocker issue that stops being true the day it merges.
// Implements the standard net.Error-style `Timeout() bool` so that
// classification, not this error's wording, is the contract callers rely
// on.
type timeoutError struct{ err error }

func (e *timeoutError) Error() string { return e.err.Error() }
func (e *timeoutError) Timeout() bool { return true }
func (e *timeoutError) Unwrap() error { return e.err }

// ToolContent mirrors the MCP tools/call result shape: a list of content
// blocks (this server only ever emits one, of type "text") plus isError,
// the channel mcp_server.py uses for "the tool ran and could not see" per
// its own docstring -- distinct from a JSON-RPC error, which means the
// request itself was unroutable.
type ToolContent struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

// Start launches the given command (e.g. python3 mcp_server.py) and speaks
// MCP `initialize` against it before returning.
func Start(name string, arg ...string) (*Client, error) {
	cmd := exec.Command(name, arg...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %s: %w", name, err)
	}

	c := &Client{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
		stderr: stderr,
		pend:   make(map[int64]chan rpcResponse),
	}
	go c.readLoop()

	if _, err := c.call("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "agent-tui", "version": "0.1.0"},
	}); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}
	return c, nil
}

// readLoop demultiplexes responses to their waiting caller by id. mcp_server.py
// writes exactly one JSON object per line and nothing else to stdout (its own
// docstring: "nothing but responses ever reaches stdout"), so a line that
// fails to decode is this client's bug or the server's, not a framing choice
// to paper over.
func (c *Client) readLoop() {
	for {
		line, err := c.stdout.ReadBytes('\n')
		if len(line) > 0 {
			var resp rpcResponse
			if jsonErr := json.Unmarshal(line, &resp); jsonErr == nil {
				c.mu.Lock()
				ch, ok := c.pend[resp.ID]
				if ok {
					delete(c.pend, resp.ID)
				}
				c.mu.Unlock()
				if ok {
					ch <- resp
					close(ch)
				}
			}
		}
		if err != nil {
			c.mu.Lock()
			for id, ch := range c.pend {
				delete(c.pend, id)
				close(ch)
			}
			c.mu.Unlock()
			return
		}
	}
}

func (c *Client) call(method string, params any) (json.RawMessage, error) {
	id := atomic.AddInt64(&c.nextID, 1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	ch := make(chan rpcResponse, 1)
	c.mu.Lock()
	c.pend[id] = ch
	c.mu.Unlock()

	c.mu.Lock()
	_, writeErr := c.stdin.Write(append(body, '\n'))
	c.mu.Unlock()
	if writeErr != nil {
		c.mu.Lock()
		delete(c.pend, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: write request: %w", writeErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()

	select {
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("mcp: connection closed before a reply to %q arrived", method)
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp: %s: %s (code %d)", method, resp.Error.Message, resp.Error.Code)
		}
		return resp.Result, nil
	case <-ctx.Done():
		// The subprocess is still alive (readLoop would have closed every
		// pending channel and returned if it weren't) -- it simply has not
		// answered in time. Deregister the id so a late reply, if one ever
		// arrives, finds nothing in c.pend and is dropped rather than sent
		// to a channel nobody is reading.
		c.mu.Lock()
		delete(c.pend, id)
		c.mu.Unlock()
		return nil, &timeoutError{fmt.Errorf("mcp: %s: no reply within %s", method, callTimeout)}
	}
}

// CallTool invokes tools/call for the given tool name and returns the decoded
// text payload. A tool that ran and could not see its backing store (the
// SupervisorUnavailable channel documented in mcp_server.py) surfaces here as
// an error too -- this client treats IsError the same as a transport error,
// because a caller polling for lane state has no use for the distinction.
func (c *Client) CallTool(name string, arguments map[string]any) (string, error) {
	raw, err := c.call("tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	var content ToolContent
	if err := json.Unmarshal(raw, &content); err != nil {
		return "", fmt.Errorf("mcp: decode tools/call result: %w", err)
	}
	if len(content.Content) == 0 {
		return "", fmt.Errorf("mcp: %s returned no content blocks", name)
	}
	text := content.Content[0].Text
	if content.IsError {
		return "", fmt.Errorf("mcp: %s: %s", name, text)
	}
	return text, nil
}

// Close terminates the child process and releases its pipes.
func (c *Client) Close() error {
	_ = c.stdin.Close()
	_ = c.stderr.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	return c.cmd.Wait()
}
