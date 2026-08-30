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
//
// Re-justified, not just re-measured, for agent-tui#177: callSem (below)
// now means a call's own 10s budget also has to cover any time spent
// queued behind other callers on this same Client, not just its own round
// trip. cmd/estate/main.go's worst realistic fan-out sharing one Client's
// sessionsFetch/lanesFetch closures is five: internal/rail's own two 2s
// tickers (lanes, sessions), internal/agents' 2s ticker, internal/monitor's
// 2s ticker, and internal/chat's participants-roster 2s ticker
// (participantsRefreshInterval) -- all independently armed, all liable to
// land in the same tick window. At the measured ~1s per call (#175/#176's
// five consecutive "sessions" calls: 1.21s/1.03s/1.19s/1.03s/0.99s), five
// queued calls tail out around 5s -- comfortably inside 10s, with the
// second half of the budget as headroom for a genuinely slower call rather
// than the ordinary cross-pane queue depth this estate produces today.
// 10s stays the number; it was not derived for queueing before, and now it
// has been.
var callTimeout = 10 * time.Second

// Client owns one child process speaking MCP over stdio. Callers must call
// Close to release it.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr io.ReadCloser

	mu     sync.Mutex // guards ONLY pend/nextID -- see writeMu's doc comment for why stdin writes are deliberately NOT under this lock
	nextID int64
	pend   map[int64]chan rpcResponse

	// writeMu serializes writes to stdin, acquired (and released) only
	// across the Write call itself -- deliberately a SEPARATE lock from mu
	// (agent-tui#167). Before that split, callTimeout held mu across the
	// blocking c.stdin.Write call, and readLoop needs that same mu to
	// deliver every response it reads (even ones already fully read off
	// the wire). A real live supervisor with several sessions can produce
	// a large enough (or slow enough) "sessions"/"lanes" response that
	// mcp_server.py's own stdout write blocks on a full pipe while
	// readLoop is the one goroutine that would drain it -- but with one
	// shared mutex, a single stdin Write() stuck behind that backpressure
	// held mu for its entire blocked duration, so readLoop could never
	// re-acquire mu to loop back to c.stdout.ReadBytes, stdout stopped
	// being drained, and both pipes wedged permanently.
	//
	// A buffered channel of capacity 1, not a sync.Mutex (agent-tui#171,
	// the review that reopened this issue): the reviewer reproduced the
	// identical 200-goroutine plateau against the writeMu split above,
	// just relocated -- readLoop's own mu stayed free, but all 200 flood
	// calls piled up forever on writeMu.Lock() instead, because a plain
	// mutex has no way to give up waiting once a call's own context
	// deadline passes. Acquiring this channel (`c.writeMu <- struct{}{}`)
	// is done in a select alongside `<-ctx.Done()` in callTimeout, so a
	// caller that cannot get the token before its own deadline gives up
	// and returns a timeout error instead of queuing forever. If the
	// token holder is itself wedged inside a permanently blocking
	// c.stdin.Write (child not draining its stdin, OS pipe full), that
	// one goroutine stays blocked in the write syscall -- there is no way
	// to cancel an in-flight blocking write short of closing stdin
	// entirely -- but every later caller now waits only its own bounded
	// timeout for the token, not forever, so the pile does not grow
	// past that single already-stuck writer. See client_test.go's
	// TestFloodOfCallsDoesNotPileUpOnWriteMu for the reproduction this
	// fixes (previously: 200 goroutines still parked past every call's
	// deadline; now: goroutines return to baseline).
	writeMu chan struct{}

	// callSem admits exactly one callTimeout round trip (registration,
	// write AND the wait for its reply) at a time -- agent-tui#177. Every
	// tool this client calls is served by mcp_server.py,
	// which "serves exactly one tools/call at a time" by its own docstring
	// (a plain read-a-line loop, no concurrency of its own); agent-tui#175
	// found that internal/agents' own 2s ticker re-issuing "sessions" while
	// its previous call was still outstanding queued behind itself at that
	// single-threaded server until one aged past callTimeout. #176 fixed
	// that pane's own self-overlap with a per-pane fetchInFlight guard, but
	// #177 (the reviewer of #176) reproduced the identical timeout on a
	// loaded box anyway: internal/rail, internal/agents, internal/monitor
	// and internal/chat's own participants roster are FOUR independent
	// tea.Model tickers (2s each, cmd/estate/main.go's sessionsFetch/
	// lanesFetch closures, all sharing this one *Client) that have never
	// heard of each other -- a guard inside any one of them single-flights
	// only its own pane's calls, so three or four panes ticking together
	// still hand the server three or four concurrent requests. That defect
	// now has three prior homes fixed locally and separately (#55, #139,
	// #175) -- the constraint being violated ("one call in flight") belongs
	// to the transport this Client owns, not to a fourth per-pane copy of
	// the same guard, so it is enforced here instead.
	//
	// A buffered channel of capacity 1, the same pattern as writeMu above
	// and for the identical reason (agent-tui#171): acquiring it is done in
	// a select alongside `<-ctx.Done()`, so a caller queued behind a slow
	// or stuck predecessor gives up once ITS OWN deadline passes rather
	// than piling up on an un-abortable channel send forever. Released via
	// `defer` immediately after acquisition (see callTimeout), so every
	// return path -- success, a JSON-RPC error, a write error, or a
	// timeout -- frees the slot; see client_test.go's
	// TestCallSemReleasedAfterFailedCall for the regression test proving a
	// failing call does not leak this slot (a leaked slot deadlocks every
	// future caller on this Client permanently, which is worse than the
	// timeout this fix closes), and TestCallSemSerialisesConcurrentCalls
	// for the cross-pane reproduction the timeout itself was measured
	// against.
	callSem chan struct{}
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
		cmd:     cmd,
		stdin:   stdin,
		stdout:  bufio.NewReader(stdout),
		stderr:  stderr,
		pend:    make(map[int64]chan rpcResponse),
		writeMu: make(chan struct{}, 1),
		callSem: make(chan struct{}, 1),
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
	return c.callTimeout(method, params, callTimeout)
}

// callTimeout is call with an explicit round-trip budget -- see
// CallToolTimeout's own doc comment for why one tool (session_send) needs
// a budget minutes wide instead of callTimeout's 10s, without touching
// what every other tool call waits for.
func (c *Client) callTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// callSem -- see its own doc comment on Client (agent-tui#177). Acquired
	// before this call even registers a pend entry, so a caller that never
	// gets its turn never has anything to unregister; released via defer
	// the moment acquisition succeeds, so EVERY return path below --
	// success, a JSON-RPC error, a write error, or either of the two
	// timeout branches -- frees the slot for the next queued caller. A
	// caller queued here that loses the race against its own ctx returns
	// the same honest timeoutError the write-wait and reply-wait branches
	// below already return, never silently drops the call: it is simply
	// never sent, and the caller (a tea.Model's own doFetch) is free to
	// retry on its next tick exactly as it does for any other timeout.
	select {
	case c.callSem <- struct{}{}:
	case <-ctx.Done():
		return nil, &timeoutError{fmt.Errorf("mcp: %s: no reply within %s (still waiting for a prior call to finish)", method, timeout)}
	}
	defer func() { <-c.callSem }()

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

	// writeMu, not mu -- see writeMu's own doc comment (agent-tui#167,
	// reopened by agent-tui#171). Acquiring the token is itself bounded by
	// this call's own ctx: a plain Lock() here has no way to give up, so if
	// the current holder is stuck inside a permanently blocking
	// c.stdin.Write (child not draining stdin, OS pipe full), every later
	// caller would queue on the mutex forever -- the exact same unbounded
	// goroutine pile this fix set out to close, one lock over. Losing the
	// race against ctx.Done() here deregisters the pend entry and returns
	// the same honest timeout error as losing the reply-wait race below.
	select {
	case c.writeMu <- struct{}{}:
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pend, id)
		c.mu.Unlock()
		return nil, &timeoutError{fmt.Errorf("mcp: %s: no reply within %s (still waiting for a prior write to finish)", method, timeout)}
	}
	_, writeErr := c.stdin.Write(append(body, '\n'))
	<-c.writeMu
	if writeErr != nil {
		c.mu.Lock()
		delete(c.pend, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: write request: %w", writeErr)
	}

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
		return nil, &timeoutError{fmt.Errorf("mcp: %s: no reply within %s", method, timeout)}
	}
}

// CallTool invokes tools/call for the given tool name and returns the decoded
// text payload. A tool that ran and could not see its backing store (the
// SupervisorUnavailable channel documented in mcp_server.py) surfaces here as
// an error too -- this client treats IsError the same as a transport error,
// because a caller polling for lane state has no use for the distinction.
func (c *Client) CallTool(name string, arguments map[string]any) (string, error) {
	return c.CallToolTimeout(name, arguments, callTimeout)
}

// CallToolTimeout is CallTool with an explicit round-trip budget instead of
// callTimeout's fixed 10s. Every tool this client calls today except one
// (session_attach/detach/add/remove/remove_check, lanes, sessions, digest,
// ledger, events) is a local, no-network-hop operation -- callTimeout's own
// doc comment explains why 10s is the honest budget for those. session_send
// (jonhill90/agent-supervisor#508/jonhill90/agent-supervisor#509) is not one of those: it drives a live agent
// turn through `supervisord send`, which can legitimately run for the
// daemon's own per-turn budget (agent.DefaultTimeout, 15 minutes, on the
// jonhill90/agent-supervisor side) before it can even report an honest "unknown".
// Forcing that call through callTimeout's 10s window would not surface a
// real problem -- it would misreport nearly every real send as a client-side
// timeout well before the daemon ever gets the chance to answer honestly.
// internal/session.Ops.Send is the one caller that uses this; every other
// caller keeps using CallTool's fixed budget unchanged.
//
// session_send and callSem (agent-tui#177): this call goes through the same
// callSem as every other tool, so a live send genuinely does hold the slot
// for as long as the daemon takes to answer, and every periodic ticker
// sharing this Client (rail/agents/monitor/chat) queues behind it and times
// out at its own 10s budget for the duration. This is not a regression
// callSem introduces: mcp_server.py already "serves exactly one tools/call
// at a time" (its own docstring) before this fix existed, so a slow send
// already made the server unable to answer anyone else while it ran --
// those tickers' calls were already being written and then timing out
// unanswered at their own 10s mark. callSem changes WHERE that wait is
// visible (client-side, before the request is even sent, instead of
// server-side after it queues) but not whether it happens or how long it
// takes to fail. A caller that needed session_send to not block routine
// refreshes would need a second Client (a second subprocess), not a carve-
// out here -- not attempted in this fix; no evidence today that routine
// refreshes stalling for the length of one live send is worse than the
// alternative of a second mcp_server.py process per agent-tui.
func (c *Client) CallToolTimeout(name string, arguments map[string]any, timeout time.Duration) (string, error) {
	raw, err := c.callTimeout("tools/call", map[string]any{"name": name, "arguments": arguments}, timeout)
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
