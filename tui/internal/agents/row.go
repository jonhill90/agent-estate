// Package agents is docs/SPEC-shell.md's S6: "List agents from the
// supervisor daemon: id, model, state, current task, cost." No existing
// package in this module lists agents estate-wide -- internal/rail shows
// lane state, one session at a time by default (NewMultiSession for all of
// them), but has no per-lane model or cost column at all. This package
// does not invent a new supervisor read: Derive (below) assembles Row from
// the exact same two seams internal/rail already reads (the "sessions" MCP
// tool via lane.Session, and the ledger's task rows via board.TaskRow),
// same "reuse the existing read, never a second one" discipline
// internal/rail/work.go's own doc comment states for its own task join.
//
// Two of S6's five columns -- model and cost -- have no source anywhere in
// this codebase to assemble from, measured, not assumed:
//   - Model: lane.Lane carries Window/WindowID/Name/Command/State/
//     IdleSeconds (internal/lane/lane.go) -- Command is the tmux pane's
//     running command (e.g. "claude", "node"), the closest available
//     proxy, but agent-supervisor's own sessions.sh/lanes.sh report no
//     model identifier (e.g. "claude-sonnet-5") at all; grep for "model"
//     across agent-supervisor/scripts/supervisor's *.sh and
//     supervisor_view.py returns nothing that names one.
//   - Cost: internal/cost reads ccusage, which totals spend PER HARNESS
//     (claude/codex/pi) across the whole box, not per tmux lane/session --
//     there is no seam anywhere that attributes a dollar figure to one
//     agent. Showing the harness-wide total next to a single lane would
//     misattribute shared spend to one row, exactly the fabricated-metric
//     AGENTS.md rules out ("never a fabricated metric... invented because
//     the real source returned nothing").
//
// Both are therefore always unknown today (Row.Model/Row.Cost nil --
// "absence is a typed value, never a bare zero," AGENTS.md's own
// convention, the same shape session.Worktree.Clean's *bool already uses)
// rather than filled with a guess. This is a real, measured gap, not an
// oversight: closing it needs agent-supervisor to start reporting a model
// name and a per-lane cost attribution, neither of which exists yet.
package agents

import "github.com/jonhill90/keelson/internal/board"
import "github.com/jonhill90/keelson/internal/lane"

// Row is one agent -- one tmux window inside one supervised session, the
// same unit internal/rail calls a "lane." ID is "<session>:<name>" (unique
// across every session, unlike lane.Lane.Name alone, which internal/rail's
// own tasksByLane join already assumes is unique only WITHIN one session).
type Row struct {
	ID      string
	Session string
	State   string
	Command string

	// Model is nil -- see this file's own doc comment for why no seam in
	// this codebase can fill it in yet.
	Model *string

	// Task is the same "repo#number" / "(no task)" summary internal/rail's
	// own taskSummary already renders for one lane -- reproduced here
	// rather than exported from internal/rail/work.go, matching this
	// repo's stated convention of a documented literal copy over a
	// cross-package dependency on another package's internal decision
	// (internal/rail/work.go's own doc comment on blockedLaneStates).
	Task string

	// Cost is nil -- see this file's own doc comment for why no seam in
	// this codebase can attribute spend to one lane yet.
	Cost *string
}

// Derive assembles Row from lane.Session (the "sessions" MCP tool,
// estate-wide) and board.TaskRow (the ledger's task rows, the same read
// internal/rail's WithTasks wires in) -- no fetch of its own; Model (below)
// owns when each of those two reads happens. A session with a non-empty
// Error still contributes no rows for its own lanes (lane.Session's own
// doc comment: unreadable, not "no lanes" -- there is nothing to derive a
// Row from), but does not stop other sessions' rows from appearing.
func Derive(sessions []lane.Session, tasks []board.TaskRow) []Row {
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
			t, haveTask := byLane[l.Name]
			out = append(out, Row{
				ID:      sess.Name + ":" + l.Name,
				Session: sess.Name,
				State:   l.State,
				Command: l.Command,
				Model:   nil,
				Task:    taskSummary(t, haveTask),
				Cost:    nil,
			})
		}
	}
	return out
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
