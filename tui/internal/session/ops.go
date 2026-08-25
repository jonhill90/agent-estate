// Package session is agent-tui#14's write path: attach, detach, add and
// remove a tmux session. Every operation here is a supervisor MCP
// tools/call -- this package contains no os/exec, no tmux invocation, and
// no knowledge of tmux's own CLI. That is the architectural rule agent-tui#14's own
// issue states as non-negotiable: "The TUI must never manipulate tmux
// directly. Every operation goes through the supervisor over the same MCP
// surface it already reads from." internal/mcp.Client is the transport;
// this package only knows the five new tool names and their JSON shapes,
// contracted against agent-supervisor's companion PR
// (feat/14-session-write-tools) -- see that PR for the guard logic behind
// session_remove_check/session_remove. If that contract ever drifts, the
// fakes in ops_test.go and internal/rail's own tests are what catch it,
// same discipline as lane.DecodeSessions/DecodeSessions already apply to
// the read side.
package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CallTooler is the two methods this package needs from *mcp.Client --
// naming it here (instead of importing internal/mcp) keeps this package's
// tests free of a real MCP subprocess, the same reason internal/rail's own
// Fetcher/SessionsFetcher types take a plain function rather than a client.
// CallToolTimeout is the second method, added for Send alone (see its own
// doc comment): every other method here still calls CallTool, unchanged.
type CallTooler interface {
	CallTool(name string, arguments map[string]any) (string, error)
	CallToolTimeout(name string, arguments map[string]any, timeout time.Duration) (string, error)
}

// Interface is what internal/rail depends on -- Ops below is the only
// production implementation, but a fake implementing this directly (no MCP
// involved at all) is what rail's own tests use to exercise every refusal
// path without a supervisor subprocess.
type Interface interface {
	// Attach switches the invoking client to session. No risk: reversible
	// by attaching elsewhere (agent-tui#14's own risk table).
	Attach(session string) error
	// Detach detaches the invoking client. Leaves every lane running.
	Detach() error
	// Add creates a brand-new, supervised session via bootstrap-session.sh
	// (never a raw tmux new-session, never --add-lanes against an existing
	// one). lanes <= 0 means "let bootstrap-session.sh use its own
	// default"; agent and cwd empty means the same for those flags.
	Add(session string, lanes int, agent, cwd string) (AddResult, error)
	// AddWithMode is Add plus SPEC-shell.md S12's ExecutionMode -- see
	// execution_mode.go's own doc comment for why ExecutionContainer
	// returns ErrContainerNotImplemented rather than silently behaving
	// like ExecutionLocal. Add itself is unchanged and still the minimal
	// entry point rail's own "n" key uses (see internal/rail/ops.go's own
	// doc comment on doAdd for why that flow deliberately stays minimal);
	// this method exists for a caller that DOES want to name a mode.
	AddWithMode(session string, lanes int, agent, cwd string, mode ExecutionMode) (AddResult, error)
	// RemoveCheck reports, without mutating anything, whether session is
	// safe to remove right now and names every reason it is not.
	RemoveCheck(session string) (RemoveCheck, error)
	// Remove kills session. The supervisor re-evaluates every guard at the
	// moment of this call (never trusting a prior RemoveCheck) and refuses
	// unless confirm is literally true.
	Remove(session string, confirm bool) (RemoveResult, error)
	// Send posts an ad-hoc message into an EXISTING agent session --
	// agent-supervisor#508/agent-supervisor#509's session_send, and the capability
	// SPEC-shell.md S7 was blocked on. sessionID is the harness's own
	// session id (e.g. Claude Code's session_id -- internal/chat.Thread.ID
	// already is exactly this, per that field's own doc comment), never a
	// tmux session name. A nil error means the daemon confirmed delivery
	// as an observed fact; errors.Is(err, ErrSendUnknown) means the
	// daemon (or this client's own round trip) could not confirm the
	// outcome before its deadline -- see ErrSendUnknown's own doc comment.
	// Any other non-nil error is a confirmed failure.
	Send(sessionID, message string) (SendResult, error)
}

// Worktree is one lane's working directory as agent-supervisor's
// session_remove_check reported it. Clean/Unpushed are pointers because
// "could not determine" is a real, distinct third answer -- nil must never
// be read as false (agent-tui#14 requirement 3: "cannot tell must never
// mean safe").
type Worktree struct {
	Path     string `json:"path"`
	Clean    *bool  `json:"clean"`
	Unpushed *bool  `json:"unpushed"`
	Reason   string `json:"reason,omitempty"`
}

// RemoveCheck is session_remove_check's full payload: everything
// internal/rail needs to render a refusal that names why, before ever
// attempting the mutating call. SafeToRemove is the supervisor's own
// verdict -- this package never re-derives it from the other fields, the
// same "safety rules live in one place" rule the supervisor-side PR
// documents for its own remove_guard.
type RemoveCheck struct {
	Session      string     `json:"session"`
	Exists       bool       `json:"exists"`
	Supervision  string     `json:"supervision"` // "supervised" | "unknown"
	BusyLanes    []string   `json:"busy_lanes"`
	Worktrees    []Worktree `json:"worktrees"`
	SafeToRemove bool       `json:"safe_to_remove"`
	Refusals     []string   `json:"refusals"`
}

// AddResult is session_add's response.
type AddResult struct {
	Session         string `json:"session"`
	Created         bool   `json:"created"`
	State           string `json:"state"` // the resulting session_state, "supervised" or "unknown"
	BootstrapOutput string `json:"bootstrap_output"`
}

// RemoveResult is session_remove's response on success. Guard is the exact
// remove_guard payload that authorized the kill -- kept so a caller can
// show what was true at the moment of removal, not re-derive it.
type RemoveResult struct {
	Session string      `json:"session"`
	Removed bool        `json:"removed"`
	Guard   RemoveCheck `json:"guard"`
}

// SendResult is session_send's response on confirmed delivery -- mirrors
// agent-supervisor's own SessionSendSource.write() success dict
// (scripts/supervisor/supervisor_view.py).
type SendResult struct {
	SessionID string  `json:"session_id"`
	Delivered bool    `json:"delivered"`
	Turns     int     `json:"turns"`
	CostUSD   float64 `json:"cost_usd"`
}

// ErrSendUnknown marks a Send outcome that could not be confirmed as
// either delivered or failed -- agent-supervisor#488's own distinction,
// which daemon/internal/sendmsg (agent-supervisor#509) already makes on
// the far side of this call and which this package must not throw away.
// errors.Is(err, ErrSendUnknown) is true in exactly two cases, both
// genuinely "we do not know", never "it failed":
//
//  1. SessionSendSource.write() itself raised because the daemon's own
//     `supervisord send` reported {"status":"unknown"} (a turn that did
//     not confirm before ITS deadline) -- detected by unknownMarker
//     below, the literal substring that source's exception text always
//     carries for this case (there is no structured field for it: MCP's
//     write-tool contract is return-a-dict-or-raise, nothing between).
//  2. This client's OWN round trip did not get a reply within sendTimeout
//     (mcp.Client's timeoutError, Timeout() == true) -- e.g. the
//     supervisor's own Python process hung. Genuinely unknown for the
//     exact same reason: nothing here observed a definite outcome.
var ErrSendUnknown = errors.New("session_send: outcome could not be confirmed")

// unknownMarker is the literal substring agent-supervisor's own
// SessionSendSource.write() puts in the exception text it raises for a
// timeout (scripts/supervisor/supervisor_view.py: "send outcome UNKNOWN,
// not failed"). This is a real, brittle coupling to that file's own
// wording -- recorded here rather than hidden -- because the write-tool
// contract this client calls through (SupervisorView: return a dict, or
// raise, nothing else) has no structured field to carry a third state
// across the MCP boundary. If agent-supervisor's own wording changes,
// TestSend_UnknownOutcome... in ops_test.go is what goes red first.
const unknownMarker = "outcome UNKNOWN"

// sendTimeout bounds Send's own round trip. Wider than mcp.callTimeout's
// 10s (CallToolTimeout's own doc comment: session_send drives a live
// agent turn, not a local op) and wider than agent-supervisor's own
// SEND_DEFAULT_TIMEOUT_SECONDS (960s, scripts/supervisor/
// supervisor_view.py) plus its own headroom over the daemon's 15-minute
// agent.DefaultTimeout -- so THIS client is never what times out first;
// the daemon (or the Python runner wrapping it) always gets to answer
// before this deadline does, and an ErrSendUnknown from THIS timeout
// firing anyway is a genuinely separate, worse signal (the round trip
// itself is stuck) worth keeping distinct in the doc comment above even
// though both map to the same typed error.
const sendTimeout = 20 * time.Minute

// Ops is the production Interface implementation: five tools/call
// invocations against whatever CallTooler it is built with (in practice a
// *mcp.Client to a live agent-supervisor checkout).
type Ops struct {
	client CallTooler
}

// New builds an Ops bound to client.
func New(client CallTooler) Ops {
	return Ops{client: client}
}

func (o Ops) Attach(session string) error {
	_, err := o.client.CallTool("session_attach", map[string]any{"session": session})
	if err != nil {
		return fmt.Errorf("session_attach %s: %w", session, err)
	}
	return nil
}

func (o Ops) Detach() error {
	if _, err := o.client.CallTool("session_detach", map[string]any{}); err != nil {
		return fmt.Errorf("session_detach: %w", err)
	}
	return nil
}

func (o Ops) Add(session string, lanes int, agent, cwd string) (AddResult, error) {
	return o.addSession(session, lanes, agent, cwd)
}

// AddWithMode implements Interface.AddWithMode -- see that method's own
// doc comment and execution_mode.go's package comment for
// ErrContainerNotImplemented's rationale. ExecutionLocal (including the
// zero value, "") delegates to the exact same session_add call Add
// already makes -- there is no separate "local" tool, because local IS
// what session_add has only ever done.
func (o Ops) AddWithMode(session string, lanes int, agent, cwd string, mode ExecutionMode) (AddResult, error) {
	switch mode {
	case "", ExecutionLocal:
		return o.addSession(session, lanes, agent, cwd)
	case ExecutionContainer:
		return AddResult{}, ErrContainerNotImplemented
	default:
		return AddResult{}, fmt.Errorf("session: unknown ExecutionMode %q", mode)
	}
}

func (o Ops) addSession(session string, lanes int, agent, cwd string) (AddResult, error) {
	args := map[string]any{"session": session}
	if lanes > 0 {
		args["lanes"] = lanes
	}
	if agent != "" {
		args["agent"] = agent
	}
	if cwd != "" {
		args["cwd"] = cwd
	}
	text, err := o.client.CallTool("session_add", args)
	if err != nil {
		return AddResult{}, fmt.Errorf("session_add %s: %w", session, err)
	}
	var result AddResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return AddResult{}, fmt.Errorf("session_add %s: decode: %w", session, err)
	}
	return result, nil
}

func (o Ops) RemoveCheck(session string) (RemoveCheck, error) {
	text, err := o.client.CallTool("session_remove_check", map[string]any{"session": session})
	if err != nil {
		return RemoveCheck{}, fmt.Errorf("session_remove_check %s: %w", session, err)
	}
	var check RemoveCheck
	if err := json.Unmarshal([]byte(text), &check); err != nil {
		return RemoveCheck{}, fmt.Errorf("session_remove_check %s: decode: %w", session, err)
	}
	return check, nil
}

func (o Ops) Remove(session string, confirm bool) (RemoveResult, error) {
	text, err := o.client.CallTool("session_remove", map[string]any{"session": session, "confirm": confirm})
	if err != nil {
		return RemoveResult{}, fmt.Errorf("session_remove %s: %w", session, err)
	}
	var result RemoveResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return RemoveResult{}, fmt.Errorf("session_remove %s: decode: %w", session, err)
	}
	return result, nil
}

// timeouter mirrors mcp's own net.Error-style Timeout() bool contract
// (internal/mcp/client.go's timeoutError) -- named locally rather than
// importing internal/mcp, the same "seam, not a concrete dependency"
// reason CallTooler above exists at all.
type timeouter interface{ Timeout() bool }

func (o Ops) Send(sessionID, message string) (SendResult, error) {
	text, err := o.client.CallToolTimeout("session_send", map[string]any{
		"session_id": sessionID, "message": message,
	}, sendTimeout)
	if err != nil {
		if te, ok := err.(timeouter); ok && te.Timeout() {
			return SendResult{}, fmt.Errorf("%w: session_send %s: %v", ErrSendUnknown, sessionID, err)
		}
		if strings.Contains(err.Error(), unknownMarker) {
			return SendResult{}, fmt.Errorf("%w: session_send %s: %v", ErrSendUnknown, sessionID, err)
		}
		return SendResult{}, fmt.Errorf("session_send %s: %w", sessionID, err)
	}
	var result SendResult
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return SendResult{}, fmt.Errorf("session_send %s: decode: %w", sessionID, err)
	}
	return result, nil
}

var _ Interface = Ops{}
