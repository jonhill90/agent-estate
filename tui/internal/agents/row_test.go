package agents

import (
	"reflect"
	"testing"

	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/lane"
	"github.com/jonhill90/keelson/internal/session"
)

func TestDeriveJoinsSessionsAndTasksByLaneName(t *testing.T) {
	sessions := []lane.Session{
		{
			Name: "director",
			Lanes: []lane.Lane{
				{Name: "w1", State: "busy", Command: "claude"},
				{Name: "w2", State: "free", Command: "codex"},
			},
		},
	}
	tasks := []board.TaskRow{
		{Lane: "w1", SourceRef: "26", UpdatedAt: 100},
	}

	got := Derive(sessions, tasks)
	want := []Row{
		{ID: "director:w1", Session: "director", State: "busy", Command: "claude", Task: "#26", Mode: session.ExecutionLocal},
		{ID: "director:w2", Session: "director", State: "free", Command: "codex", Task: "(no task)", Mode: session.ExecutionLocal},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Derive() =\n%+v\nwant\n%+v", got, want)
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
		{Name: "ok", Lanes: []lane.Lane{{Name: "w1", State: "busy"}}},
	}

	got := Derive(sessions, nil)
	want := []Row{{ID: "ok:w1", Session: "ok", State: "busy", Task: "(no task)", Mode: session.ExecutionLocal}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Derive() =\n%+v\nwant\n%+v", got, want)
	}
}

// TestDerivePicksFreshestTaskRowPerLane mirrors internal/rail/work.go's own
// tasksByLane rule: the same lane can have more than one ledger row over
// its lifetime (retried tasks); the one with the latest UpdatedAt wins.
func TestDerivePicksFreshestTaskRowPerLane(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy"}}},
	}
	tasks := []board.TaskRow{
		{Lane: "w1", SourceRef: "1", UpdatedAt: 100},
		{Lane: "w1", SourceRef: "2", UpdatedAt: 200}, // freshest
	}

	got := Derive(sessions, tasks)
	if len(got) != 1 || got[0].Task != "#2" {
		t.Fatalf("Derive() = %+v, want the freshest task row (#2) to win", got)
	}
}

// TestDeriveModelAndCostAreAlwaysUnknown is this file's own doc comment,
// made a test: nothing in this codebase can fill either in yet, and a
// future accidental fabrication (e.g. an "improvement" that guesses a
// model from Command) should fail this test loudly rather than silently
// start showing a made-up value.
func TestDeriveModelAndCostAreAlwaysUnknown(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy", Command: "claude"}}},
	}
	got := Derive(sessions, nil)
	if got[0].Model != nil {
		t.Errorf("Model = %v, want nil (unknown) -- no seam in this codebase reports a per-lane model", *got[0].Model)
	}
	if got[0].Cost != nil {
		t.Errorf("Cost = %v, want nil (unknown) -- ccusage totals per harness, not per lane", *got[0].Cost)
	}
}

func TestDeriveEmptyInputsProduceNoRows(t *testing.T) {
	if got := Derive(nil, nil); got != nil {
		t.Errorf("Derive(nil, nil) = %+v, want nil", got)
	}
}

// TestDeriveModeIsAlwaysExecutionLocal is SPEC-shell.md S12's own
// contrast with TestDeriveModelAndCostAreAlwaysUnknown above: unlike
// Model/Cost, Mode is a KNOWN fact today (Row's own doc comment says
// why -- no AgentBox integration exists in agent-supervisor at all), so
// it must never render as this package's "unknown" -- pinned here so a
// future change cannot silently start guessing at a mode instead of
// stating the one this estate actually has.
func TestDeriveModeIsAlwaysExecutionLocal(t *testing.T) {
	sessions := []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy"}, {Name: "w2", State: "free"}}},
	}
	got := Derive(sessions, nil)
	for _, r := range got {
		if r.Mode != session.ExecutionLocal {
			t.Errorf("Row %q Mode = %q, want %q", r.ID, r.Mode, session.ExecutionLocal)
		}
	}
}
