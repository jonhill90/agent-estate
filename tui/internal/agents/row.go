// Package agents is docs/SPEC-shell.md's S6: "List agents from the
// supervisor daemon: id, model, state, current task, cost." No existing
// package in this module lists agents estate-wide -- internal/rail shows
// lane state, one session at a time by default (NewMultiSession for all of
// them), but has no per-lane model or cost column at all. This package
// does not invent a new supervisor read: Derive (below) assembles Row from
// the exact same seams internal/rail already reads (the "sessions" MCP
// tool via lane.Session, the ledger's task rows via board.TaskRow) plus one
// more the rail does not: the ledger's own `lanes` table joined to a
// per-session `ccusage session --json` cost, same "reuse the existing
// read, never a second one" discipline internal/rail/work.go's own doc
// comment states for its own task join.
//
// 2026-08-22 update (SPEC-shell.md S6/S7 depth): all three of Model/Task/
// Cost are now real, measured seams, not permanent gaps -- see each field's
// own doc comment below for exactly what still renders "unknown" and why.
//
//   - Model: lane.Lane.Model, agent-supervisor#115's own status-line scrape
//     (lanes.sh's 7th JSON column) -- "unknown" (lanes.sh's own sentinel,
//     for a harness with no model regex, or a pane that has not yet shown
//     one) maps to Row.Model == nil; anything else is a real reported name.
//   - Task: fixed a join-key bug found by reading the ledger's own dispatch
//     path (dispatch.sh's `--lane "$LANE"`, `$LANE` = `"$SESSION:$idx"`,
//     `$idx` the tmux WINDOW INDEX -- scripts/supervisor/dispatch.sh's own
//     `WINDOW_NAME_BY_INDEX` array, keyed by that index, populated from
//     `lanes.sh`'s numeric first column, not the descriptive window name).
//     TaskRow.Lane and lanes.lane are BOTH in that "<session>:<index>" form
//     -- confirmed against a live ledger.sqlite3 copy, 2026-08-22
//     (`tasks.lane` values observed: "agent-supervisor:3", "agent-supervisor:4",
//     never a descriptive name). The join below now builds that same key
//     from lane.Lane.Window (the numeric column), not lane.Lane.Name (the
//     descriptive one) -- the bug this file shipped with, and the reason
//     Task rendered "(no task)" for every real dispatched lane regardless
//     of whether the ledger actually had a row for it.
//   - Cost: lanes.harness_session_id (agent-supervisor's own per-lane
//     resolved Claude Code session id, written by dispatch.sh's
//     `record-dispatch --harness-session-id`) joined against `ccusage
//     session --json`'s own per-session totalCost, keyed by that same
//     session id -- confirmed live against this box's own ledger and
//     ccusage install, 2026-08-22: lane "agent-supervisor:4" carries
//     harness_session_id "014b3e7e-...", and `ccusage session --json`
//     reports totalCost 0.5612212... for a session whose "period" is that
//     exact id. This is genuinely per-AGENT cost, not the per-HARNESS total
//     `internal/cost` otherwise reads (ccusage totals claude/codex/pi
//     across the whole box; showing that next to one lane would misattribute
//     shared spend to one row, exactly the fabricated-metric AGENTS.md
//     rules out). A lane whose harness has no resolver (codex: the
//     harness_session_id column is NOT NULL DEFAULT ” specifically because
//     "codex has no resolver", per the ledger's own schema comment) or that
//     has not yet completed a turn stays nil -- a real, nameable gap, not
//     an oversight.
//
// NOTED BUT NOT FIXED HERE (out of this package's scope): internal/rail's
// own task join (readings_view.go's `m.tasksByLane()[sel.Name]`) appears to
// have the SAME by-descriptive-name bug Task's join had -- `sel.Name` is
// the pane's display name, not the ledger's "<session>:<index>" key. Not
// touched in this change (SPEC-shell.md S6/S7 depth is this package's own
// scope); flagged here because whoever looks at that file next should not
// have to re-derive this from nothing.
//
// 2026-08-22 update (SPEC-shell.md S12 depth, LOCAL side): Mode joins
// Model/Task/Cost as a real per-row read instead of a permanent gap --
// except this one arrived the other direction. It used to be hardcoded to
// session.ExecutionLocal for every row unconditionally (no reference to
// that row's own Command/State at all); modeFor now reads the same two
// fields Model/Task already derive from and answers "unknown" when
// neither Command nor State supports a real answer. See modeFor's own doc
// comment for the evidence and Row.Mode's for why the old constant was
// wrong even though it happened to be correct for every row seen so far.
package agents

import (
	"fmt"
	"strconv"

	"github.com/jonhill90/agent-tui/internal/board"
	"github.com/jonhill90/agent-tui/internal/cost"
	"github.com/jonhill90/agent-tui/internal/lane"
	"github.com/jonhill90/agent-tui/internal/session"
)

// Row is one agent -- one tmux window inside one supervised session, the
// same unit internal/rail calls a "lane." ID is "<session>:<name>" (unique
// across every session, unlike lane.Lane.Name alone, which internal/rail's
// own tasksByLane join already assumes is unique only WITHIN one session).
// ID is a DISPLAY identifier built from the pane's own descriptive name --
// it is deliberately NOT the ledger's own "<session>:<window-index>" key
// (see this file's own package doc comment); Derive computes that key
// separately, only to look up Task/Cost, and never exposes it on Row.
type Row struct {
	ID      string
	Session string
	State   string
	Command string

	// Model is nil when lanes.sh reported "unknown" or nothing at all --
	// see this file's own package doc comment and lane.Lane.Model's own
	// doc comment for exactly when that is.
	Model *string

	// Task is the same "repo#number" / "(no task)" summary internal/rail's
	// own taskSummary already renders for one lane -- reproduced here
	// rather than exported from internal/rail/work.go, matching this
	// repo's stated convention of a documented literal copy over a
	// cross-package dependency on another package's internal decision
	// (internal/rail/work.go's own doc comment on blockedLaneStates).
	Task string

	// Cost is nil when this lane's harness has no resolved session id, or
	// ccusage has no session--total for the id it does have -- see this
	// file's own package doc comment for the join Derive performs.
	Cost *string

	// Mode is SPEC-shell.md S12's ExecutionMode, READ from this row's own
	// evidence (modeFor), not assumed. nil means the evidence available
	// for THIS row does not support either answer -- see modeFor's own
	// doc comment for exactly what is checked and why. Never defaulted to
	// ExecutionLocal: an earlier version of this file did that
	// unconditionally, for every row, regardless of what that row's own
	// Command/State said, and estate-loop/w2d.md called it out by name as
	// the fabricated-value failure AGENTS.md's "never a fabricated
	// metric" rule exists to stop -- a value that would not have changed
	// no matter what the read returned was never a read.
	Mode *session.ExecutionMode
}

// Derive assembles Row from lane.Session (the "sessions" MCP tool,
// estate-wide), board.TaskRow (the ledger's task rows, the same read
// internal/rail's WithTasks wires in), and costsByLedgerLane (the ledger's
// lanes table joined to ccusage's per-session totals, pre-computed by the
// caller -- see cmd/estate/agents.go's buildAgentCostFetch -- and keyed by
// the SAME "<session>:<window-index>" string board.TaskRow.Lane already
// uses, not by Row.ID). No fetch of its own; Model (below) owns when each
// of those reads happens. A session with a non-empty Error still
// contributes no rows for its own lanes (lane.Session's own doc comment:
// unreadable, not "no lanes" -- there is nothing to derive a Row from), but
// does not stop other sessions' rows from appearing.
func Derive(sessions []lane.Session, tasks []board.TaskRow, costsByLedgerLane map[string]cost.Figure) []Row {
	byLane := make(map[string]board.TaskRow, len(tasks))
	for _, t := range tasks {
		if t.Lane == "" {
			continue
		}
		// Freshest-wins, the same rule internal/rail/work.go's own
		// tasksByLane uses.
		if cur, ok := byLane[t.Lane]; !ok || t.UpdatedAt > cur.UpdatedAt {
			byLane[t.Lane] = t
		}
	}

	var out []Row
	for _, sess := range sessions {
		if sess.Error != "" {
			continue
		}
		for _, l := range sess.Lanes {
			// ledgerLane is dispatch.sh's own "<session>:<window-index>"
			// key -- see this file's own package doc comment for why this
			// is l.Window (numeric), never l.Name (the descriptive one).
			ledgerLane := sess.Name + ":" + strconv.Itoa(l.Window)
			t, haveTask := byLane[ledgerLane]

			out = append(out, Row{
				ID:      sess.Name + ":" + l.Name,
				Session: sess.Name,
				State:   l.State,
				Command: l.Command,
				Model:   modelPtr(l.Model),
				Task:    taskSummary(t, haveTask),
				Cost:    costPtr(costsByLedgerLane[ledgerLane]),
				Mode:    modeFor(l),
			})
		}
	}
	return out
}

// modelPtr turns lanes.sh's own model string into Row.Model's *string --
// "" and the literal sentinel "unknown" (lanes.sh's own, see lane.Lane.Model's
// doc comment) both mean the same thing here: nothing resolved, nil, never
// a pointer to the word "unknown" itself (view.go's own unknown constant
// renders that word for a nil Row.Model; a non-nil pointer TO "unknown"
// would print the same thing by coincidence, not by design, and would stop
// being distinguishable from a harness that genuinely named its model
// "unknown").
func modelPtr(m string) *string {
	if m == "" || m == "unknown" {
		return nil
	}
	return &m
}

// costPtr turns a cost.Figure into Row.Cost's *string -- nil when the
// figure was never found (Derive's map lookup misses, returning the zero
// Figure{Known: false}), a formatted dollar string otherwise. Two decimal
// places matches internal/cost/view.go's own formatFigure("%.2f") for the
// harness-wide totals, so a lane's cost and a harness's cost read in the
// same units next to each other if a human ever has both panes open.
func costPtr(f cost.Figure) *string {
	if !f.Known {
		return nil
	}
	s := fmt.Sprintf("$%.2f", f.Value)
	return &s
}

// modeFor is SPEC-shell.md S12 depth: Row.Mode read from this row's own
// evidence, in place of the session.ExecutionLocal every row got
// unconditionally before this change (see Row.Mode's and this package's
// own doc comments for why that was wrong even though it never once
// printed something false in practice).
//
// The only two fields this package has that speak to "is a process
// actually running here, and where" are Command (lanes.sh's own
// pane_current_command -- tmux's own live introspection of the pane's
// foreground process, not this package's guess) and State
// (lane.Lane.State, lanes.sh's own classification):
//
//   - Command == "" -- lanes.sh reported no foreground process for this
//     pane at all. No process, no evidence: unknown.
//   - State == "dead" or "stale" -- lanes.sh's own verdict that the
//     harness process itself is gone (a bare shell left behind, per
//     lanes.sh's own state= comments for both). Nothing is running to
//     attribute a location to: unknown, not local.
//   - Otherwise: Command names a real process tmux -- running on the
//     SAME host as the supervisor this MCP call reached -- just reported
//     as this pane's own foreground command. That is mechanically what
//     execution_mode.go's ExecutionLocal doc comment defines ("a
//     subprocess in a worktree, today's behaviour"), not an assumption
//     about what usually runs here.
//
// Deliberately never ExecutionContainer: nothing in lanes.sh's --json
// payload (window/window_id/name/command/state/idle_seconds/model --
// confirmed by reading lanes.sh's own --json emission directly,
// 2026-08-22) can distinguish a container-wrapped process from a native
// one, and nothing in agent-supervisor dispatches into AgentBox today
// (grepped agent-supervisor/scripts for "agentbox"/"docker", zero
// matches, 2026-08-22 -- the same check execution_mode.go's own doc
// comment already cites). Inventing a detection rule for a signal that
// does not exist yet would repeat the exact mistake this function exists
// to stop making, just one layer further in. See
// docs/SPEC-agentbox-execution-mode.md for what container mode actually
// needs before this function could ever honestly return it.
func modeFor(l lane.Lane) *session.ExecutionMode {
	if l.Command == "" {
		return nil
	}
	if l.State == "dead" || l.State == "stale" {
		return nil
	}
	m := session.ExecutionLocal
	return &m
}

// taskSummary mirrors internal/rail/work.go's own taskSummary exactly --
// see this file's own doc comment on Row.Task for why this is a literal
// copy rather than an import.
func taskSummary(t board.TaskRow, haveTask bool) string {
	if !haveTask {
		return "(no task)"
	}
	if t.Repo.Name != "" && t.Number != "" {
		return t.Repo.Name + "#" + t.Number
	}
	if t.SourceRef != "" {
		return "#" + t.SourceRef
	}
	return "(no task)"
}
