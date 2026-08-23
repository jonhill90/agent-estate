package agents

import (
	"reflect"
	"testing"

	"github.com/jonhill90/agent-tui/internal/board"
	"github.com/jonhill90/agent-tui/internal/cost"
	"github.com/jonhill90/agent-tui/internal/lane"
	"github.com/jonhill90/agent-tui/internal/session"
)

// TestDeriveJoinsSessionsAndTasksByLedgerLaneKey pins the fix this file's
// own package doc comment describes: the join key is "<session>:<window
// INDEX>" (dispatch.sh's own "$LANE", built from lanes.sh's numeric window
// column), never the pane's descriptive Name. w1/w2 below stand for real
// descriptive names (e.g. "fix225-brief") on purpose -- if Derive ever
// regresses to joining by l.Name again, this still passes only by
// coincidence for a fixture using Name as the join key, which is exactly
// why Window is set to a DIFFERENT-looking value (1, 2) than Name (w1, w2)
// here: a l.Name-keyed join would produce "director:w1" and never match
// "director:1", the real ledger's own key.
func TestDeriveJoinsSessionsAndTasksByLedgerLaneKey(t *testing.T) {
	sessions := []lane.Session{
		{
			Name: "director",
			Lanes: []lane.Lane{
				{Window: 1, Name: "w1", State: "busy", Command: "claude"},
				{Window: 2, Name: "w2", State: "free", Command: "codex"},
			},
		},
	}
	tasks := []board.TaskRow{
		{Lane: "director:1", SourceRef: "26", UpdatedAt: 100},
	}

	got := Derive(sessions, tasks, nil)
	want := []Row{
		{ID: "director:w1", Session: "director", State: "busy", Command: "claude", Task: "#26", Mode: modePtr(session.ExecutionLocal)},
		{ID: "director:w2", Session: "director", State: "free", Command: "codex", Task: "(no task)", Mode: modePtr(session.ExecutionLocal)},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Derive() =\n%+v\nwant\n%+v", got, want)
	}
}

// TestDeriveDoesNotJoinByDescriptiveName is the mutation-check direction on
// the fix above: a task row keyed by the pane's descriptive NAME ("w1", the
// pre-fix shape) must NOT match, because the real ledger never writes a
// lane key that way (see this file's own package doc comment, "confirmed
// against a live ledger.sqlite3 copy"). If Derive's join reverts to l.Name,
// this goes red the other direction -- Task would read "#26" instead of
// staying "(no task)".
func TestDeriveDoesNotJoinByDescriptiveName(t *testing.T) {
	sessions := []lane.Session{
		{Name: "director", Lanes: []lane.Lane{{Window: 1, Name: "w1", State: "busy"}}},
	}
	tasks := []board.TaskRow{
		{Lane: "w1", SourceRef: "26", UpdatedAt: 100}, // old, wrong shape
	}
	got := Derive(sessions, tasks, nil)
	if len(got) != 1 || got[0].Task != "(no task)" {
		t.Fatalf("Derive() = %+v, want Task=\"(no task)\" -- a lane key of bare descriptive name must not match", got)
	}
}

// TestDeriveSkipsUnreadableSessionsWithoutDroppingOthers is lane.Session's
// own contract: a non-empty Error means "could not read this session,"
// never "this session has no lanes" -- Derive must produce no rows for
// that session's own (necessarily absent) lanes, but must not let one
// unreadable session blank out every other session's rows.
func TestDeriveSkipsUnreadableSessionsWithoutDroppingOthers(t *testing.T) {
	sessions := []lane.Session{
		{Name: "broken", Error: "tmux: no such session"},
		{Name: "ok", Lanes: []lane.Lane{{Name: "w1", State: "busy", Command: "claude"}}},
	}

	got := Derive(sessions, nil, nil)
	want := []Row{{ID: "ok:w1", Session: "ok", State: "busy", Command: "claude", Task: "(no task)", Mode: modePtr(session.ExecutionLocal)}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Derive() =\n%+v\nwant\n%+v", got, want)
	}
}

// TestDerivePicksFreshestTaskRowPerLane mirrors internal/rail/work.go's own
// tasksByLane rule: the same lane can have more than one ledger row over
// its lifetime (retried tasks); the one with the latest UpdatedAt wins.
func TestDerivePicksFreshestTaskRowPerLane(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Window: 1, Name: "w1", State: "busy"}}},
	}
	tasks := []board.TaskRow{
		{Lane: "s:1", SourceRef: "1", UpdatedAt: 100},
		{Lane: "s:1", SourceRef: "2", UpdatedAt: 200}, // freshest
	}

	got := Derive(sessions, tasks, nil)
	if len(got) != 1 || got[0].Task != "#2" {
		t.Fatalf("Derive() = %+v, want the freshest task row (#2) to win", got)
	}
}

// TestDeriveModelFromLaneReport pins the fix: lane.Lane.Model, when it is a
// real reported name, flows straight to Row.Model.
func TestDeriveModelFromLaneReport(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy", Model: "sonnet"}}},
	}
	got := Derive(sessions, nil, nil)
	if got[0].Model == nil || *got[0].Model != "sonnet" {
		t.Fatalf("Model = %v, want \"sonnet\"", got[0].Model)
	}
}

// TestDeriveModelUnknownSentinelStaysNil is the mutation-check contrast:
// lanes.sh's own "unknown" sentinel (never a real model name) must still
// render as Row.Model == nil, not as a pointer to the literal word
// "unknown" -- see modelPtr's own doc comment for why that distinction
// matters.
func TestDeriveModelUnknownSentinelStaysNil(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{
			{Name: "w1", State: "busy", Model: "unknown"},
			{Name: "w2", State: "busy", Model: ""},
		}},
	}
	got := Derive(sessions, nil, nil)
	for _, r := range got {
		if r.Model != nil {
			t.Errorf("Row %q Model = %q, want nil", r.ID, *r.Model)
		}
	}
}

// TestDeriveCostFromLedgerLaneJoin pins Cost's own join: costsByLedgerLane
// is keyed by the SAME "<session>:<window-index>" string tasks use, not by
// Row.ID -- exactly the shape cmd/estate/agents.go's buildAgentCostFetch
// produces.
func TestDeriveCostFromLedgerLaneJoin(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Window: 4, Name: "w1", State: "busy"}}},
	}
	costs := map[string]cost.Figure{"s:4": cost.KnownFigure(0.561221)}
	got := Derive(sessions, nil, costs)
	if got[0].Cost == nil || *got[0].Cost != "$0.56" {
		t.Fatalf("Cost = %v, want \"$0.56\"", got[0].Cost)
	}
}

// TestDeriveCostMissingFromJoinStaysNil is the mutation-check contrast: a
// lane with no entry in costsByLedgerLane (no resolved harness session id,
// or ccusage has no session total for the one it has) must render Cost as
// nil, never as "$0.00" or any other fabricated figure.
func TestDeriveCostMissingFromJoinStaysNil(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Window: 9, Name: "w1", State: "busy"}}},
	}
	got := Derive(sessions, nil, map[string]cost.Figure{"s:1": cost.KnownFigure(1.23)})
	if got[0].Cost != nil {
		t.Errorf("Cost = %v, want nil -- no join entry for this lane's own key", *got[0].Cost)
	}
}

func TestDeriveEmptyInputsProduceNoRows(t *testing.T) {
	if got := Derive(nil, nil, nil); got != nil {
		t.Errorf("Derive(nil, nil, nil) = %+v, want nil", got)
	}
}

// modePtr is this test file's own convenience for a session.ExecutionMode
// literal used as *session.ExecutionMode -- session.ExecutionLocal is a
// const and therefore not addressable directly in a struct literal.
func modePtr(m session.ExecutionMode) *session.ExecutionMode { return &m }

// TestDeriveModeLocalFromLiveProcessEvidence is modeFor's affirmative case:
// a real Command and a live-ish State together are what execution_mode.go's
// own ExecutionLocal doc comment defines as "local" -- read from this row's
// own fields, not asserted regardless of them.
func TestDeriveModeLocalFromLiveProcessEvidence(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy", Command: "claude"}}},
	}
	got := Derive(sessions, nil, nil)
	if got[0].Mode == nil || *got[0].Mode != session.ExecutionLocal {
		t.Fatalf("Mode = %v, want a pointer to ExecutionLocal", got[0].Mode)
	}
}

// TestDeriveModeUnknownWhenNoCommand is the mutation-check contrast this
// package's own history needed: estate-loop/w2d.md called out a Mode that
// was ExecutionLocal for every row regardless of the row's own data as the
// fabricated-value failure AGENTS.md forbids for a read. A pane lanes.sh
// reports NO foreground process for at all is exactly the case that
// constant could never have distinguished -- modeFor must render nil
// (unknown) here, not silently keep calling it local.
func TestDeriveModeUnknownWhenNoCommand(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy", Command: ""}}},
	}
	got := Derive(sessions, nil, nil)
	if got[0].Mode != nil {
		t.Fatalf("Mode = %v, want nil -- no Command means no process evidence to read", *got[0].Mode)
	}
}

// TestDeriveModeUnknownWhenDeadOrStale: lanes.sh's own "dead"/"stale"
// verdict means the harness process itself is gone (a bare shell left
// behind) -- there is nothing left running to call "local", so Mode must
// be unknown even though Command is still a real (residual) shell name.
func TestDeriveModeUnknownWhenDeadOrStale(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{
			{Name: "w1", State: "dead", Command: "-zsh"},
			{Name: "w2", State: "stale", Command: "-zsh"},
		}},
	}
	got := Derive(sessions, nil, nil)
	for _, r := range got {
		if r.Mode != nil {
			t.Errorf("Row %q Mode = %q, want nil for a %q lane", r.ID, *r.Mode, r.State)
		}
	}
}
