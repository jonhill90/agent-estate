// agent-tui#26: "the rail is too thin to QA" -- Jon attached, saw a session
// list with a detail block and two cyclers, pressed the one interactive key
// that did anything (agent-tui#23's now-fixed no-op aside), and was done. The rail
// had nothing behind it: no answer to what a lane is actually working on,
// how long that has been open, or whether it needs a human.
//
// That data already exists -- internal/board's ledger read (source_tasks
// joined to tasks) already answers exactly those three questions for the
// board screen. This file reuses that SAME read (board.ReadTaskRows,
// board.TaskRow) rather than inventing a second way to reach the ledger, per
// the issue's own constraint. It does not reuse board.Derive: that also
// calls `gh issue list`/`gh pr list` per repo, and agent-tui#28/agent-tui#29 measured that
// GraphQL cost against a slow refresh interval deliberately chosen for the
// board screen -- wiring it into the rail's own tighter refresh tick would
// reintroduce the exact rate problem agent-tui#28/agent-tui#29 just fixed. What this file adds
// is ledger-only and needs no GitHub call: a task's own source ref
// (source_kind/source_ref, e.g. "agent-tui#26"), its created_at for age, and
// delivered_at/accepted_at for the review/verdict signal -- three columns
// the ledger already wrote, read the same way board.go already reads them.
package rail

import (
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/tui/internal/board"
	"github.com/jonhill90/agent-estate/tui/internal/lane"
)

// TaskFetcher retrieves every source_tasks/tasks row the ledger currently
// holds -- board.ReadTaskRows's own signature, wrapped exactly as Fetcher
// wraps the "lanes" MCP call. nil in every test and in any run with no
// -ledger configured (WithTasks is never called), so a Model built without
// it renders exactly as it did before this file existed: readings fall back
// to "(no task)" rather than reading a field that was never fetched.
type TaskFetcher func() ([]board.TaskRow, error)

type taskFetchResultMsg struct {
	rows []board.TaskRow
	err  error
}

func doTaskFetch(fetch TaskFetcher) tea.Cmd {
	return func() tea.Msg {
		rows, err := fetch()
		return taskFetchResultMsg{rows: rows, err: err}
	}
}

// WithTasks returns a copy of m with a ledger read wired in -- the rail's
// equivalent of board.go's -ledger wiring. cmd/agent-tui calls this only
// when -ledger (or $AGENT_TUI_LEDGER) is set; passing a nil fetch is the
// same as never calling it (Init/doFetchAll both guard on taskFetch != nil).
func (m Model) WithTasks(fetch TaskFetcher) Model {
	m.taskFetch = fetch
	return m
}

// WithSessionName returns a copy of m with the single-session render path's
// own tmux session name wired in -- see the sessionName field's own doc
// comment for why this is needed at all and why "" is a safe, honest
// default. cmd/agent-tui calls this with the same -session flag value
// lanesFetch (main.go) already reads to build the "lanes" MCP call.
func (m Model) WithSessionName(name string) Model {
	m.sessionName = name
	return m
}

// ledgerLaneKey builds the ledger's own "<session>:<window-index>" task-join
// key for a lane.Lane read from the single-session Fetcher -- l.Window
// (numeric), never l.Name (the descriptive one), matching dispatch.sh's own
// "$SESSION:$idx" (see tasksByLane's own doc comment for the confirmation).
// Returns "" when m.sessionName is unknown (WithSessionName never called, or
// called with ""); tasksByLane's map is keyed by real, non-empty ledger
// strings, so an "" lookup key can never match a row -- the task column
// degrades to "(no task)", never a wrong or a guessed one.
func (m Model) ledgerLaneKey(l lane.Lane) string {
	if m.sessionName == "" {
		return ""
	}
	return m.sessionName + ":" + strconv.Itoa(l.Window)
}

// tasksByLane indexes m.tasks by the ledger's own "<session>:<window INDEX>"
// key (TaskRow.Lane, the same string card.go's bestTaskRow already trusts as
// the join key) picking, per lane, the row with the latest updated_at -- the
// exact "freshest wins" rule bestTaskRow uses for retried issues, applied
// here by lane instead of by (repo, issue number) since a lane has at most
// one ledger task open against it at a time.
//
// The key is the tmux window's numeric INDEX (lanes.sh's own first JSON
// column, lane.Lane.Window), never its descriptive display name --
// confirmed against agent-supervisor's own dispatch.sh
// (`WINDOW_NAME_BY_INDEX["$idx"]="$wname"`, `--lane "$LANE"` where
// `$LANE="$SESSION:$idx"`) and against a live ledger.sqlite3 copy
// (`tasks.lane` values observed: "agent-supervisor:3", never a descriptive
// name). A caller building a lookup key against this map MUST use
// session+Window (ledgerLaneKey, readings_view.go), not session+Name --
// agent-tui#86 found and fixed the identical bug in internal/agents' own
// copy of this join; this file had the same defect.
func (m Model) tasksByLane() map[string]board.TaskRow {
	out := make(map[string]board.TaskRow, len(m.tasks))
	for _, t := range m.tasks {
		if t.Lane == "" {
			continue
		}
		if cur, ok := out[t.Lane]; !ok || t.UpdatedAt > cur.UpdatedAt {
			out[t.Lane] = t
		}
	}
	return out
}

// taskSummary is the "what is this lane actually working on" line -- the
// task's own source reference, e.g. "agent-tui#26", never a fetched GitHub
// title (that would need a `gh` call this file deliberately avoids; see the
// package doc comment). Falls back to the raw source_ref when parseSourceURL
// (ledger.go) couldn't resolve a repo, and to "(no task)" when the lane has
// no ledger row at all -- both real, nameable states, never blank.
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

// taskOpenAge is now - created_at, the "how long has this been open" half
// of the issue's ask, mirroring board/card.go's own unixOrZero treatment of
// the same column: zero means "unknown", not "just now", so ok reports
// false rather than returning a huge or misleadingly-small duration.
func taskOpenAge(t board.TaskRow, haveTask bool, now time.Time) (time.Duration, bool) {
	if !haveTask || t.CreatedAt == 0 {
		return 0, false
	}
	return now.Sub(time.Unix(t.CreatedAt, 0)), true
}

// taskStatusClosed matches board/card.go's taskOpen negation exactly (the
// same three literals: "complete", "failed", "cancelled") -- duplicated
// rather than exported from board because this is the one place outside
// card.go that needs it, and this repo's convention (see
// internal/lane/states.go's own doc comment) is a documented literal copy
// over a cross-package dependency on an internal decision.
func taskStatusClosed(status string) bool {
	switch status {
	case "complete", "failed", "cancelled":
		return true
	}
	return false
}

// blockedLaneStates mirrors board/card.go's own blockedLaneStates map
// exactly (same two entries) -- the one place this repo already decided
// which lane states mean "blocked", so a lane rendering "needs human" here
// must agree with the same lane rendering Blocked on the board screen. board
// package keeps its own copy unexported; this is a second literal, not a
// second decision.
var blockedLaneStates = map[string]bool{
	"menu-blocked": true,
	"hung":         true,
}

// taskNeedsHuman is the issue's third ask -- "whether anything needs a
// human" -- read from two already-written signals, never a new one: a lane
// state the board already calls Blocked, or a ledger row that was delivered
// (agent finished and handed it back) but not yet accepted (no human has
// signed off), which is the closest thing this ledger records to a
// review/verdict state -- see ledger.go's TaskRow doc comment for
// delivered_at/accepted_at's own meaning. A task already closed
// (taskStatusClosed) never reads as needing a human even if accepted_at
// happens to be unset, since closed means the loop already ended one way or
// another.
func taskNeedsHuman(t board.TaskRow, haveTask bool, laneState string) (bool, string) {
	if blockedLaneStates[laneState] {
		return true, "lane is " + laneState
	}
	if haveTask && t.DeliveredAt != nil && t.AcceptedAt == nil && !taskStatusClosed(t.TaskStatus) {
		return true, "delivered, unreviewed"
	}
	return false, ""
}
