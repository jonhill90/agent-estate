package lane

import "encoding/json"

// Session is one row of the supervisor's "sessions" MCP tool -- agent-tui#13,
// wrapping agent-supervisor's sessions.sh --json, which itself wraps
// lanes.sh --json once per tmux session (see that script's header for the
// full trace: lanes.sh is single-session BY CONSTRUCTION, and #13 is the
// regression that shipped a rail that could not show more than one).
//
// Supervised is NOT agent-supervisor#153's own supervised/unsupervised
// marker -- #153 had not landed when this was written. It is sessions.sh's
// interim, evidence-based stand-in (has the ledger ever registered a lane
// in this session), and it fails CLOSED: unknown reads false, never true.
// See sessions.sh's own module comment for exactly what it does and does
// not prove. Replace this doc (and the renderer that reads it) once #153
// lands its own signal -- do not silently keep both.
//
// Error and a nil Lanes together mean sessions.sh could not read this one
// session (e.g. it closed between being listed and being read) -- that must
// render as "unreadable", never as "no lanes", the same "blind, not quiet"
// rule internal/rail already applies to a whole-fetch failure.
type Session struct {
	Name       string `json:"session"`
	Supervised bool   `json:"supervised"`
	Lanes      []Lane `json:"lanes"`
	Error      string `json:"error,omitempty"`
}

type sessionsPayload struct {
	Sessions []Session `json:"sessions"`
	Count    int       `json:"count"`
}

// DecodeSessions parses the "sessions" tool's text payload --
// {"sessions": [...], "count": N} -- the same shape convention Decode above
// uses for "lanes".
func DecodeSessions(text string) ([]Session, error) {
	var payload sessionsPayload
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return nil, err
	}
	return payload.Sessions, nil
}
