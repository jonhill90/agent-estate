// Package session is agent-tui#14's write path: attach, detach, add and
// remove a tmux session. Every operation here is a supervisor MCP
// tools/call -- this package contains no os/exec, no tmux invocation, and
// no knowledge of tmux's own CLI. That is the architectural rule #14's own
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
	"fmt"
)

// CallTooler is the one method this package needs from *mcp.Client --
// naming it here (instead of importing internal/mcp) keeps this package's
// tests free of a real MCP subprocess, the same reason internal/rail's own
// Fetcher/SessionsFetcher types take a plain function rather than a client.
type CallTooler interface {
	CallTool(name string, arguments map[string]any) (string, error)
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
	// RemoveCheck reports, without mutating anything, whether session is
	// safe to remove right now and names every reason it is not.
	RemoveCheck(session string) (RemoveCheck, error)
	// Remove kills session. The supervisor re-evaluates every guard at the
	// moment of this call (never trusting a prior RemoveCheck) and refuses
	// unless confirm is literally true.
	Remove(session string, confirm bool) (RemoveResult, error)
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

var _ Interface = Ops{}
