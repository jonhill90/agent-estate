package rail

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/board"
	"github.com/jonhill90/agent-tui/internal/lane"
)

// driveKey sends msg through Update the same way bubbletea's runtime would
// -- every test below goes through this, not a struct literal poking
// m.reading/m.tasks directly, per the issue's own functionally-driven bar
// (agent-tui#23's whole lesson: "not one check pressed a key").
func driveKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	next, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update did not return a rail.Model")
	}
	return next
}

func TestWKeyCyclesReading(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	if m.reading != 0 {
		t.Fatalf("default reading must be 0 (readings[0]) with no selection made, got %d", m.reading)
	}

	m = driveKey(t, m, "w")
	if m.reading != 1 {
		t.Fatalf("pressing 'w' once should select readings[1], got reading=%d", m.reading)
	}

	m = driveKey(t, m, "w")
	if m.reading != 0 {
		t.Fatalf("pressing 'w' twice should wrap back to readings[0], got reading=%d", m.reading)
	}
}

// deliverLane drives a Model through the real message path a running rail
// would see: a fetchResultMsg for the lane list, then a taskFetchResultMsg
// for the ledger rows -- never a struct literal setting m.lanes/m.tasks by
// hand.
func deliverLane(t *testing.T, m Model, lanes []lane.Lane, rows []board.TaskRow) Model {
	t.Helper()
	updated, _ := m.Update(fetchResultMsg{lanes: lanes})
	m = updated.(Model)
	updated, _ = m.Update(taskFetchResultMsg{rows: rows})
	return updated.(Model)
}

// TestReadingsRenderDifferentContent is agent-tui#26's core acceptance bar:
// the two readings must differ in WHAT they show, not just how it is
// arranged. Driven through Update with a real key event (not a struct
// literal poking m.reading), against one real lane+task fixture rendered
// twice.
func TestReadingsRenderDifferentContent(t *testing.T) {
	dispatched := time.Now().Add(-90 * time.Minute).Unix()
	// Window (2) is the join key; Name ("fix225-brief") is deliberately a
	// DIFFERENT-looking descriptive display name, not the numeric index --
	// see work.go's tasksByLane doc comment. If the join ever regresses to
	// keying by Name again, this fixture would not coincidentally still
	// pass the way a Name-equals-index fixture could.
	lanes := []lane.Lane{{Window: 2, Name: "fix225-brief", State: "busy", IdleSeconds: 5}}
	rows := []board.TaskRow{{
		Lane: "agent-tui:2", TaskStatus: "running", CreatedAt: dispatched, UpdatedAt: dispatched,
		Repo: board.Repo{Owner: "jonhill90", Name: "agent-tui"}, Number: "26",
	}}

	m := New(func() ([]lane.Lane, error) { return nil, nil }).
		WithTasks(func() ([]board.TaskRow, error) { return nil, nil }).
		WithSessionName("agent-tui")
	m = deliverLane(t, m, lanes, rows)

	work := m.View()
	m = driveKey(t, m, "w")
	status := m.View()

	if work == status {
		t.Fatalf("work and status readings rendered identically:\n%s", work)
	}

	// work-centric: the task itself and how long it has been open.
	if !strings.Contains(work, "on:    agent-tui#26") {
		t.Errorf("work reading missing the task ref:\n%s", work)
	}
	if !strings.Contains(work, "open:") {
		t.Errorf("work reading missing an open-duration line:\n%s", work)
	}

	// status-centric: health first, task ref absent -- a genuinely different
	// answer to "what is this screen for", not the same lines re-labelled.
	if !strings.Contains(status, "health: ok") {
		t.Errorf("status reading missing a health line:\n%s", status)
	}
	if strings.Contains(status, "agent-tui#26") {
		t.Errorf("status reading still names the task ref -- readings must differ in content, not just labels:\n%s", status)
	}
}

// TestNeedsHumanReflectsDeliveredNotAccepted asserts the status reading's
// "needs human" flag tracks the ACTUAL ledger record, not a fixture that
// happens to agree -- built two ways from the same lane/state, differing
// only in delivered_at/accepted_at, and the rendered health line must
// differ with it.
func TestNeedsHumanReflectsDeliveredNotAccepted(t *testing.T) {
	lanes := []lane.Lane{{Window: 3, Name: "fix225-brief", State: "free", IdleSeconds: 12}}
	delivered := time.Now().Add(-30 * time.Minute).Unix()

	running := []board.TaskRow{{Lane: "agent-tui:3", TaskStatus: "running", CreatedAt: delivered - 3600}}
	awaitingReview := []board.TaskRow{{
		Lane: "agent-tui:3", TaskStatus: "running", CreatedAt: delivered - 3600,
		DeliveredAt: &delivered,
	}}

	base := New(func() ([]lane.Lane, error) { return nil, nil }).
		WithTasks(func() ([]board.TaskRow, error) { return nil, nil }).
		WithSessionName("agent-tui")

	mRunning := deliverLane(t, base, lanes, running)
	mRunning = driveKey(t, mRunning, "w") // switch to the status reading
	runningOut := mRunning.View()
	if !strings.Contains(runningOut, "health: ok") {
		t.Errorf("a running, undelivered task must read healthy:\n%s", runningOut)
	}
	if strings.Contains(runningOut, "needs human") {
		t.Errorf("a running, undelivered task must not flag needs-human:\n%s", runningOut)
	}

	mDelivered := deliverLane(t, base, lanes, awaitingReview)
	mDelivered = driveKey(t, mDelivered, "w")
	deliveredOut := mDelivered.View()
	if !strings.Contains(deliveredOut, "health: needs human") {
		t.Errorf("a delivered-not-accepted task must flag needs-human:\n%s", deliveredOut)
	}
	if !strings.Contains(deliveredOut, "delivered, unreviewed") {
		t.Errorf("the needs-human reason must name why:\n%s", deliveredOut)
	}

	if runningOut == deliveredOut {
		t.Fatalf("changing only delivered_at/accepted_at produced identical output -- the render is reading a fixture, not the record")
	}
}

// TestTaskJoinUsesWindowIndexNotDescriptiveName is the mutation-check
// direction on the fix itself: a TaskRow keyed by the pane's DESCRIPTIVE
// name (the pre-fix shape every real dispatch would have failed to match --
// see work.go's tasksByLane doc comment and agent-tui#86) must NOT join,
// even though a lane with that exact name exists. If ledgerLaneKey/the
// sessions.go call site regress to l.Name, this goes red the other
// direction: "on:" would read the task ref instead of "(no task)".
func TestTaskJoinUsesWindowIndexNotDescriptiveName(t *testing.T) {
	lanes := []lane.Lane{{Window: 5, Name: "fix225-brief", State: "busy"}}
	rows := []board.TaskRow{{Lane: "agent-tui:fix225-brief", TaskStatus: "running"}} // old, wrong shape

	m := New(func() ([]lane.Lane, error) { return nil, nil }).
		WithTasks(func() ([]board.TaskRow, error) { return nil, nil }).
		WithSessionName("agent-tui")
	m = deliverLane(t, m, lanes, rows)

	if !strings.Contains(m.View(), "on:    (no task)") {
		t.Errorf("a lane key of bare descriptive name must not match:\n%s", m.View())
	}
}

// TestTaskJoinDegradesHonestlyWithNoSessionName covers the single-session
// render path's own real gap (rail.Model.sessionName's doc comment):
// without WithSessionName, ledgerLaneKey cannot build a real join key at
// all, and the task column must degrade to "(no task)" -- never guess a
// session name, and never crash on an empty lookup key.
func TestTaskJoinDegradesHonestlyWithNoSessionName(t *testing.T) {
	lanes := []lane.Lane{{Window: 2, Name: "fix225-brief", State: "busy"}}
	rows := []board.TaskRow{{Lane: "agent-tui:2", TaskStatus: "running"}}

	m := New(func() ([]lane.Lane, error) { return nil, nil }).
		WithTasks(func() ([]board.TaskRow, error) { return nil, nil }) // no WithSessionName
	m = deliverLane(t, m, lanes, rows)

	if !strings.Contains(m.View(), "on:    (no task)") {
		t.Errorf("with no session name known, the task column must read \"(no task)\", not guess:\n%s", m.View())
	}
}

// TestNeedsHumanFlagsBlockedLaneEvenWithNoLedgerTask asserts the same
// health flag fires from live lane state alone (agent-tui#26's third
// signal), independent of whether a taskFetch is even wired -- a Model
// with no ledger data at all must still surface a hung/menu-blocked lane as
// needing a human, never silently read "ok" for lack of a ledger row.
func TestNeedsHumanFlagsBlockedLaneEvenWithNoLedgerTask(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil }) // no WithTasks at all
	updated, _ := m.Update(fetchResultMsg{lanes: []lane.Lane{{Name: "agent-tui:9", State: "hung", IdleSeconds: 40}}})
	m = updated.(Model)
	m = driveKey(t, m, "w")

	out := m.View()
	if !strings.Contains(out, "health: needs human") {
		t.Errorf("a hung lane must flag needs-human even with no ledger task wired:\n%s", out)
	}
	if !strings.Contains(out, "lane is hung") {
		t.Errorf("the reason must name the lane state:\n%s", out)
	}
}

// TestPlainViewHasNoTaskDataWithNoTaskFetch is the backward-compatibility
// contract every WithXxx addition in this package carries: a Model built
// without WithTasks (every pre-agent-tui#26 rail test, and cmd/agent-tui with no
// -ledger set) must render sane, never a template with a hole in it.
func TestPlainViewHasNoTaskDataWithNoTaskFetch(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	updated, _ := m.Update(fetchResultMsg{lanes: []lane.Lane{{Name: "solo:1", State: "busy"}}})
	m = updated.(Model)

	out := m.View()
	if !strings.Contains(out, "on:    (no task)") {
		t.Errorf("with no taskFetch wired, the work reading must say '(no task)', not omit or fabricate one:\n%s", out)
	}
	if strings.Contains(out, "! ledger unavailable") {
		t.Errorf("a Model with no taskFetch must never render the ledger-unavailable note (nothing was ever asked for):\n%s", out)
	}
}

// TestInitFetchesTasksWhenWired proves the wiring end to end: Init()'s
// returned command, when a taskFetch is set, actually includes a call to
// it -- not just that WithTasks sets a field nothing reads.
func TestInitFetchesTasksWhenWired(t *testing.T) {
	called := false
	m := New(func() ([]lane.Lane, error) { return nil, nil }).
		WithTasks(func() ([]board.TaskRow, error) { called = true; return nil, nil })

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned a nil command with taskFetch wired")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init()'s command did not produce a tea.BatchMsg, got %T", msg)
	}
	for _, c := range batch {
		c()
	}
	if !called {
		t.Fatal("Init()'s batch never invoked the wired taskFetch")
	}
}
