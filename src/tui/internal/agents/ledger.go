// ledger.go is agent-estate#930's fix for this package's own defect: Model
// (model.go) has always been fed exclusively by Fetcher, the "sessions" MCP
// call agent-supervisor's Python server used to answer. That server does
// not exist in this repository (`git ls-files scripts/supervisor` is empty;
// only `reference/` -- never maintained, run, tested, or fixed -- has it),
// so Fetcher's client is nil in every real launch and this pane has never
// once been able to show a real agent. It failed HONESTLY (fetchErr names
// "no supervisor connection") -- which is exactly why nobody noticed: an
// unavailable-for-one-tick seam and a seam deleted from the repository
// render identically, forever.
//
// This file adds a second, independent, additive source: src/estate's own
// Go dispatch ledger, the same file Home's internal/estatus already reads.
// It does NOT replace Fetcher/Row/Derive above -- if a real tmux/MCP source
// is ever restored, its rows keep rendering exactly as before. It also does
// NOT force ledger data into Row/lane.Session's shape: a ledger record
// carries no tmux window, no foreground command, no model self-report and
// no per-lane cost, and inventing those columns to make a LedgerRow look
// like a Row is precisely the "synthesise a tmux-shaped lane" AGENTS.md
// rules out. LedgerRow instead states only what the ledger actually
// records -- id, issue, role, state, when it started, and (agent-estate#944)
// the dispatched subprocess's own pid -- and leaves everything else for a
// reader to see is simply not there.
package agents

import (
	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
)

// LedgerRow is one dispatched turn as src/estate's own ledger records it --
// deliberately a different, narrower shape than Row (above): every field
// here is something the ledger genuinely wrote down, never a tmux-shaped
// guess. See this file's own package doc comment for why the two types do
// not merge.
type LedgerRow struct {
	ID    string
	Issue string
	// Role defaults to "author" for a record written before this field
	// existed, matching src/estate/internal/ledger.Record.EffectiveRole's
	// own convention -- see roleFor's doc comment.
	Role  string
	State string
	// Started is the record's own At timestamp, RFC3339 -- rendering
	// picks the display format so this package's tests can assert on the
	// value without depending on wall-clock "Ns ago" text.
	Started string
	// PID is nil when the record predates agent-estate#944 or the turn
	// failed before a pid was ever assigned -- never 0 rendered as if it
	// were a real pid.
	PID *int
}

// roleFor mirrors src/estate/internal/ledger.Record.EffectiveRole: an empty
// Role on a record written before the field existed reads as "author," the
// only role every pre-existing dispatch actually was. Never applied to
// distinguish a genuine third role -- there are only two (ledger.go's own
// RoleAuthor/RoleReviewer).
func roleFor(d estatus.Dispatch) string {
	if d.Role == "" {
		return "author"
	}
	return d.Role
}

// DeriveLedger turns the ledger's own in-flight dispatches into LedgerRows,
// newest first (Status.InFlight's own order, unchanged). Only IN-FLIGHT
// turns -- "dispatched" or "unknown" (Status.InFlight's own filter,
// internal/estatus.inFlightStates) -- are shown: this pane answers "what is
// the estate doing right now," and a terminal complete/failed record is a
// history entry, not a currently-running agent. internal/workflows already
// exists for a task's own full path through the estate; this file does not
// duplicate that.
func DeriveLedger(status estatus.Status) []LedgerRow {
	out := make([]LedgerRow, 0, len(status.InFlight))
	for _, d := range status.InFlight {
		var pid *int
		if d.PID != 0 {
			p := d.PID
			pid = &p
		}
		out = append(out, LedgerRow{
			ID:      d.ID,
			Issue:   d.Issue,
			Role:    roleFor(d),
			State:   d.State,
			Started: d.At.Format("2006-01-02T15:04:05Z07:00"),
			PID:     pid,
		})
	}
	// Order is Status.InFlight's own -- newest dispatch first
	// (readDispatches' own doc comment on Read, internal/estatus.go).
	return out
}
