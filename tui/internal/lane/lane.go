// Package lane decodes the supervisor's "lanes" MCP tool payload and maps
// each state it names to a glyph. It knows nothing about tmux, lanes.sh, or
// how the payload was fetched -- that is internal/mcp and cmd/agent-tui's
// job. This package only knows the JSON shape lanes.sh --json produces
// (window, window_id, name, command, state, idle_seconds), because that is
// what mcp_server.py's "lanes" tool passes straight through.
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
