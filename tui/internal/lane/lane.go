// Package lane decodes the supervisor's "lanes" MCP tool payload and maps
// each state it names to a glyph. It knows nothing about tmux, lanes.sh, or
// how the payload was fetched -- that is internal/mcp and cmd/agent-tui's
// job. This package only knows the JSON shape lanes.sh --json produces
// (window, window_id, name, command, state, idle_seconds, model,
// execution_mode), because that is what mcp_server.py's "lanes" tool passes
// straight through.
package lane

import "encoding/json"

// Lane is one row of lanes.sh --json, decoded.
type Lane struct {
	Window      int    `json:"window"`
	WindowID    string `json:"window_id"`
	Name        string `json:"name"`
	Command     string `json:"command"`
	State       string `json:"state"`
	IdleSeconds int    `json:"idle_seconds"`

	// Model is agent-supervisor#115's own self-report field, the 7th column
	// lanes.sh --json appends (scripts/supervisor/lanes.sh's own emit_rows):
	// the harness's OWN status-line text, matched against a per-harness
	// regex (claude.sh's HARNESS_MODEL_RE) and lowercased to its first word
	// ("opus"/"sonnet"/"haiku"). Literal string "unknown" -- lanes.sh's own
	// sentinel, not this package's -- whenever the harness has no such
	// regex (codex.sh/copilot.sh both leave HARNESS_MODEL_RE empty, verified
	// against those files directly, 2026-08-22) or the pane's own text has
	// not yet shown a match. A caller MUST treat "unknown" the same as
	// empty, never render it as if it were a real model name.
	Model string `json:"model"`

	// ExecutionMode is SPEC-shell.md S12's container signal, the 8th column
	// lanes.sh --json appends (scripts/supervisor/lanes.sh's own execmode_for):
	// the `@hill90_lane_execution_mode` tmux pane USER OPTION, verbatim,
	// whenever it is exactly "local" or "container" -- and the literal
	// string "unknown" for every other case (the option was never set, the
	// common case for any lane bootstrap-session.sh has not yet touched;
	// or it holds a value neither side has agreed to). This is a RECORDED
	// fact, written by whatever created the lane (bootstrap-session.sh
	// today; a future container driver later -- see docs/SPEC-agentbox-
	// execution-mode.md), never inferred from Command or any other pane
	// content -- the same "written down, not pattern-matched" discipline
	// Model above does not have and this field specifically exists to add,
	// because a container entrypoint can re-exec into a harness process
	// whose Command looks identical to a native one. A caller MUST treat
	// "unknown" the same as empty, exactly as for Model.
	ExecutionMode string `json:"execution_mode"`
}

type lanesPayload struct {
	Lanes []Lane `json:"lanes"`
	Count int    `json:"count"`
}

// Decode parses the "lanes" tool's text payload -- {"lanes": [...], "count": N}.
func Decode(text string) ([]Lane, error) {
	var payload lanesPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, err
	}
	return payload.Lanes, nil
}
