package agents

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/cost"
	"github.com/jonhill90/agent-tui/internal/lane"
)

// TestFetchResultPopulatesRows drives Update directly (cheaper than a full
// teatest.Program, same two-tier discipline internal/nav's own test suite
// uses) to confirm a fetchResultMsg actually reaches View() through Rows().
func TestFetchResultPopulatesRows(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{sessions: []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy"}}},
	}})
	m = next.(Model)

	rows := m.Rows()
	if len(rows) != 1 || rows[0].ID != "s:w1" {
		t.Fatalf("Rows() = %+v, want one row \"s:w1\"", rows)
	}
	if !strings.Contains(m.View(), "s:w1") {
		t.Fatalf("View() missing the fetched row:\n%s", m.View())
	}
}

// TestCostFetchResultPopulatesCostColumn drives Update directly, mirroring
// TestFetchResultPopulatesRows above, to confirm a costFetchResultMsg
// actually reaches View() through Rows()/m.costs.
func TestCostFetchResultPopulatesCostColumn(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{sessions: []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Window: 4, Name: "w1", State: "busy"}}},
	}})
	m = next.(Model)
	next, _ = m.Update(costFetchResultMsg{costs: map[string]cost.Figure{"s:4": cost.KnownFigure(1.5)}})
	m = next.(Model)

	rows := m.Rows()
	if len(rows) != 1 || rows[0].Cost == nil || *rows[0].Cost != "$1.50" {
		t.Fatalf("Rows() = %+v, want Cost \"$1.50\"", rows)
	}
	if !strings.Contains(m.View(), "$1.50") {
		t.Fatalf("View() missing the fetched cost:\n%s", m.View())
	}
}

// TestCostFetchErrorLeavesCostsUnchanged mirrors taskFetchResultMsg's own
// silent-degrade rule (model.go's Update case): an error must not stamp a
// stale/zero costs map over one already populated from a prior successful
// fetch.
func TestCostFetchErrorLeavesCostsUnchanged(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(costFetchResultMsg{costs: map[string]cost.Figure{"s:4": cost.KnownFigure(1.5)}})
	m = next.(Model)
	next, _ = m.Update(costFetchResultMsg{err: errors.New("ccusage unreadable")})
	m = next.(Model)

	if len(m.costs) != 1 {
		t.Fatalf("costs = %+v, want the prior successful fetch preserved", m.costs)
	}
}

// TestViewRendersModeColumnAsLocalWhenEvidenceSupportsIt is SPEC-shell.md
// S12 depth: the MODE column shows "local" once modeFor's own evidence
// (a real Command, a live-ish State) supports it -- read, not asserted
// regardless of the row.
func TestViewRendersModeColumnAsLocalWhenEvidenceSupportsIt(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{sessions: []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy", Command: "claude"}}},
	}})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "local") {
		t.Fatalf("View() does not render \"local\" in the MODE column:\n%s", out)
	}
}

// TestViewRendersModeColumnAsUnknownWithoutEvidence is the mutation-check
// contrast: a lane lanes.sh reports no Command for renders MODE as
// "unknown," never as a guessed "local" -- see modeFor's own doc comment.
func TestViewRendersModeColumnAsUnknownWithoutEvidence(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{sessions: []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy"}}},
	}})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "unknown") {
		t.Fatalf("View() does not render \"unknown\" in the MODE column for a Command-less lane:\n%s", out)
	}
}

// TestFetchErrorRendersVisibly is this pane's own "blind, not quiet" case
// (AGENTS.md: never look like a healthy, empty estate when the read
// failed) -- a fetchResultMsg carrying an error must show it, not render
// "(no agents)" as if there genuinely were none.
func TestFetchErrorRendersVisibly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("mcp: no supervisor")})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "! sessions unavailable") || !strings.Contains(out, "no supervisor") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
}

// TestNKeySetsANamedNoticeRatherThanSilentlyDoingNothing is SPEC-shell.md
// S6's "read-only until S7" line, made concrete: [n] must be visibly a
// documented no-op.
func TestNKeySetsANamedNoticeRatherThanSilentlyDoingNothing(t *testing.T) {
	m := New(nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("[n] returned a non-nil cmd, want nil (read-only until S7)")
	}
	if !strings.Contains(m.View(), "not built yet (S7)") {
		t.Fatalf("[n] did not render a visible notice:\n%s", m.View())
	}
}

// TestRKeyRefetches confirms manual refresh actually asks Fetcher again --
// board/rail/cost's own "[r] refresh" convention.
func TestRKeyRefetches(t *testing.T) {
	calls := 0
	fetch := func() ([]lane.Session, error) { calls++; return nil, nil }
	m := New(fetch)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("[r] returned a nil cmd, want a fetch")
	}
	cmd()
	if calls != 1 {
		t.Fatalf("fetch called %d times after [r], want 1", calls)
	}
}

// TestQuittingRendersNothing matches internal/cost.Model's own convention.
func TestQuittingRendersNothing(t *testing.T) {
	m := New(nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("\"q\" did not return tea.Quit")
	}
	if m.View() != "" {
		t.Fatalf("View() after quitting = %q, want empty", m.View())
	}
}

// TestAgentsCostRefreshIntervalIsFiveMinutes pins this pane's cost-fetch
// cadence to internal/rail's own costRefreshInterval (5m) -- agent-tui#139:
// the cost fetch (buildAgentCostFetch's sqlite-read + `ccusage session
// --json` join) must not ride the same 2s cadence as the cheap sessions/
// task reads. A future edit that folds this back onto refreshInterval (2s)
// must turn this test red -- see this file's own mutation-check note in the
// PR description for confirmation this test actually catches that.
func TestAgentsCostRefreshIntervalIsFiveMinutes(t *testing.T) {
	if costRefreshInterval != 5*time.Minute {
		t.Errorf("costRefreshInterval = %s, want 5m", costRefreshInterval)
	}
}

// TestRefreshMsgDoesNotFetchCost is refreshInterval's own half of agent-tui#139: the
// 2s ticker must drive the sessions/task fetch only, never m.costFetch --
// folding costFetch back into this case is exactly the regression this
// guards, independent of the constant-value pin above (a mutant that
// re-adds doCostFetch(m.costFetch) to the refreshMsg case without touching
// costRefreshInterval's value would pass
// TestAgentsCostRefreshIntervalIsFiveMinutes but fail this one).
func TestRefreshMsgDoesNotFetchCost(t *testing.T) {
	costCalls := 0
	costFetch := func() (map[string]cost.Figure, error) { costCalls++; return nil, nil }
	m := New(func() ([]lane.Session, error) { return nil, nil }).WithCosts(costFetch)

	// Clear the in-flight guard Init()/WithCosts() would otherwise leave set
	// so a stray refreshMsg-triggered cost fetch isn't masked by the guard
	// itself -- this test asserts refreshMsg issues no cost cmd at all, not
	// merely that the guard suppressed one.
	m.costFetchInFlight = false

	_, cmd := m.Update(refreshMsg(time.Time{}))
	if cmd == nil {
		t.Fatal("refreshMsg returned a nil cmd, want at least refreshCmd")
	}
	// refreshMsg's own cmd is a tea.Batch of >1 sub-command (refreshCmd +
	// doFetch, at least); tea.Batch's own returned func does NOT itself run
	// those sub-commands -- it just wraps them into a tea.BatchMsg for the
	// runtime to fan out later (see compactCmds, bubbletea/commands.go).
	// Calling cmd() therefore only unwraps one layer; each sub-command must
	// be run in turn to actually reach doCostFetch, if it were (wrongly)
	// bundled in. refreshCmd() itself is a real 2s tea.Tick and must not be
	// awaited, so every sub-command runs with a short deadline instead of a
	// direct call.
	runBatch(t, cmd())
	if costCalls != 0 {
		t.Fatalf("refreshMsg triggered %d cost fetch(es), want 0 -- cost has its own cadence (costRefreshMsg)", costCalls)
	}
}

// runBatch drives every sub-command of a tea.BatchMsg (or a lone tea.Cmd's
// msg) concurrently, each bounded by a short deadline so a real tea.Tick
// among them (refreshCmd/costRefreshCmd, both real timers far longer than
// this deadline) cannot hang the test -- it is simply left running in its
// own goroutine and never awaited. Used only to prove a fetch-triggering
// sub-command is (or is not) present in a batch; the goroutines it leaves
// behind on the "still ticking" branch are harmless test-process litter,
// the same shape teatest's own drivers accept.
func runBatch(t *testing.T, msg tea.Msg) {
	t.Helper()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return
	}
	for _, sub := range batch {
		sub := sub
		done := make(chan struct{})
		go func() {
			sub()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
			// Still blocked on its own real timer (refreshCmd/costRefreshCmd)
			// -- not a fetch cmd, which returns immediately.
		}
	}
}

// TestCostRefreshMsgSingleFlightsCostFetch is agent-tui#139's core fix: an
// in-flight cost fetch must not be joined by a second one when
// costRefreshMsg fires again before the first has answered -- the part an
// interval alone does not fix (this file's own refreshInterval doc
// comment). Mirrors internal/rail's own fetchInFlight discipline
// (doFetchAll's doc comment there).
func TestCostRefreshMsgSingleFlightsCostFetch(t *testing.T) {
	costFetch := func() (map[string]cost.Figure, error) { return nil, nil }
	m := New(nil).WithCosts(costFetch)
	if !m.costFetchInFlight {
		t.Fatal("WithCosts(non-nil) must seed costFetchInFlight = true (Init() always issues the first fetch)")
	}

	// A costRefreshMsg landing while the guard is still set (the first
	// fetch has not answered) must re-arm the ticker but issue no second
	// doCostFetch. tea.Batch does not itself run its sub-commands (it just
	// wraps them into a tea.BatchMsg for the runtime to fan out later), so
	// calling the returned cmd is safe EXCEPT when it collapses to exactly
	// one non-nil command -- compactCmds then returns that command
	// directly rather than a batch wrapper, and the lone command here would
	// be the real 5-minute tea.Tick, which blocks on a real timer. Run it
	// in a goroutine with a short deadline: if the guard held (no
	// doCostFetch bundled in), the only command is that raw, still-ticking
	// Tick and nothing arrives before the deadline; if the guard failed to
	// hold, doCostFetch would have been bundled into a tea.BatchMsg that
	// returns immediately.
	next, cmd := m.Update(costRefreshMsg(time.Time{}))
	m = next.(Model)
	if !m.costFetchInFlight {
		t.Fatal("costFetchInFlight must stay true while the first fetch is still outstanding")
	}
	if cmd == nil {
		t.Fatal("costRefreshMsg while in-flight returned a nil cmd, want at least a re-armed ticker")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case msg := <-done:
		if _, isBatch := msg.(tea.BatchMsg); isBatch {
			t.Fatal("costRefreshMsg while a fetch is already in-flight must not issue a second doCostFetch (got a batch, want the lone re-armed ticker)")
		}
	case <-time.After(50 * time.Millisecond):
		// Expected: cmd() is the raw re-armed ticker, still blocked on its
		// own (real) 5-minute timer -- no second fetch was bundled in.
	}

	// Once the first fetch's result lands, the guard clears and the next
	// costRefreshMsg is free to issue a real fetch again.
	next, _ = m.Update(costFetchResultMsg{costs: nil})
	m = next.(Model)
	if m.costFetchInFlight {
		t.Fatal("costFetchResultMsg must clear costFetchInFlight regardless of success/failure")
	}
}
