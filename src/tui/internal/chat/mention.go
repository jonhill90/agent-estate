// Mention support -- agent-tui#114's "Chat should be a room": @-mentions
// that address one participant, checked against real, live estate state at
// compose time. Kept in its own file, no lipgloss import, the same
// plain-text/colour split render.go's own doc comment documents for this
// package -- highlightMentions (model.go) is the only place a Participant
// or a parsed mention gets a colour.
package chat

import (
	"fmt"
	"regexp"
	"strings"
)

// Participant is one name @-mentions can address -- the room's participant
// model, richer than "the one agent this thread belongs to" (agent-tui#114's own
// framing). Built from the estate's live lane state, the same "sessions"
// MCP read internal/agents.Fetcher already projects into a Row (see
// cmd/estate/chat.go's buildParticipantsFetch) -- never a hardcoded list,
// per the issue's own explicit requirement.
type Participant struct {
	Name string // the mention token, without "@" -- lane.Lane.Name, or a ledger dispatch id (see Session below)

	// Session is which tmux session this lane runs in, or "" for a
	// participant sourced from src/estate's own dispatch ledger instead of
	// a live tmux/MCP read (agent-estate#930: that MCP server no longer
	// exists in this repository, so cmd/estate's buildParticipantsFetch
	// falls back to the ledger when it has nothing else to offer). "" is
	// never a real tmux session name here -- a ledger-sourced Participant's
	// Name is the dispatch's own id, not a pane name, so it cannot collide
	// with one built from a real session.
	Session string

	// Running is false for a lane whose own state is "dead" or "stale" --
	// the same read internal/agents' modeFor already uses for "is a
	// process actually here to address" (that package's own doc comment
	// explains the evidence) -- or, for a ledger-sourced Participant,
	// whether the dispatch is still IN FLIGHT (src/estate's own
	// dispatched/unknown states -- internal/estatus.Status.InFlight's own
	// filter). A participant can be a real, known name and still not be
	// running; ValidateMentions treats the two as distinct refusal reasons
	// so the composer's error says which one it is.
	Running bool
}

// ParticipantsFetcher retrieves every addressable participant in the
// estate right now. cmd/estate wires this from the exact same "sessions"
// MCP call internal/rail and internal/agents already make (AGENTS.md's
// adapter discipline: never a second reader of the same tmux/ledger state).
// nil is a valid, silent "no participant source configured" default -- the
// same convention every other optional seam in this module follows
// (agents.TaskFetcher, cost.QuotaRunner); ValidateMentions then refuses
// every mention as "not in this room" against an empty list, which is the
// honest answer for a room with nothing to check against, not a crash or a
// silently-accepted mention.
type ParticipantsFetcher func() ([]Participant, error)

// mentionPattern matches an @-mention token: "@" followed by the characters
// a tmux pane name can actually contain (lanes.sh names panes with
// letters, digits, "-", "_" -- lane.Lane.Name's own doc comment). No word
// boundary is required before "@" -- "hi@bob" naming "bob" is worth
// flagging, not silently ignored, and no real participant name starts with
// a character this would misparse.
var mentionPattern = regexp.MustCompile(`@[A-Za-z0-9][A-Za-z0-9_-]*`)

// ParseMentions returns every distinct @-mention token in text, in
// first-seen order, without the leading "@" and de-duplicated
// case-insensitively. The one parse both ValidateMentions (compose time)
// and highlightMentions (render time, model.go) use -- so "what gets
// validated" and "what gets highlighted" can never read text differently.
func ParseMentions(text string) []string {
	matches := mentionPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		name := strings.TrimPrefix(m, "@")
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

// findParticipant looks up name case-insensitively -- a mention typed with
// different casing than lanes.sh reports a name in should still resolve.
func findParticipant(name string, participants []Participant) (Participant, bool) {
	for _, p := range participants {
		if strings.EqualFold(p.Name, name) {
			return p, true
		}
	}
	return Participant{}, false
}

// ValidateMentions is the composer's compose-time gate -- agent-tui#114's
// own failure mode to engineer against: "an @-mention that names a
// participant who is not in the room, or not running, must say so at
// compose time... not silently go nowhere." Returns nil when text has no
// @-mentions, or every one resolves to a real, currently-running
// Participant; otherwise a single error naming every mention that did not
// (both reasons distinguished, and every bad mention listed at once rather
// than one round trip per typo). trySend (model.go) checks this BEFORE
// calling Sender at all -- a rejected mention never reaches the daemon
// transport, the same "provably reached, or refused" discipline
// SPEC-shell.md S7 already holds sends to.
func ValidateMentions(text string, participants []Participant) error {
	var bad []string
	for _, name := range ParseMentions(text) {
		p, ok := findParticipant(name, participants)
		switch {
		case !ok:
			bad = append(bad, "@"+name+" (not in this room)")
		case !p.Running:
			bad = append(bad, "@"+name+" (not running)")
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("cannot send -- %s", strings.Join(bad, ", "))
}
