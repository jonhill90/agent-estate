package rail

import (
	"errors"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/lane"
)

// runCmd executes cmd (and, recursively, every sub-command of any
// tea.BatchMsg it returns) and reports every resulting tea.Msg -- the test
// harness both TestRefreshMsgDoesNotOverlapInFlightSessionsFetch below and
// any future rail test need to actually observe what a batched Cmd does,
// not just that Update() returned a non-nil one.
func runCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if msg == nil {
		return nil
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, sub := range batch {
			out = append(out, runCmd(sub)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// TestRefreshMsgDoesNotOverlapInFlightSessionsFetch is agent-tui#55's core
// reproduction: measured against the real supervisor, a "sessions" MCP call
// (sessions.sh fanning out to lanes.sh once per tmux session) took 4-5s wall
// clock with six sessions up -- slower than rail's own 2s refreshInterval
// re-firing doFetchAll. Before this fix, every refreshMsg unconditionally
// queued another sessions fetch regardless of whether the last one had
// answered; against mcp_server.py's own strictly-serial "one tools/call at a
// time" stdio loop (see model.go's doFetchAll doc comment), that pile-up
// guaranteed every request eventually crossed mcp.Client's 10s callTimeout.
// This slow fetch (never itself completing within the test) simulates
// exactly that: two refreshMsg ticks land before the first sessions fetch
// answers, and the fix must issue exactly one sessions fetch, not two.
func TestRefreshMsgDoesNotOverlapInFlightSessionsFetch(t *testing.T) {
	var calls int32
	fetch := func() ([]lane.Session, error) {
		atomic.AddInt32(&calls, 1)
		// Never answers within the test -- stands in for a real fetch still
		// in flight when the next poll tick lands.
		return nil, errors.New("slow fetch, not yet answered")
	}

	m := NewMultiSession(fetch, nil, nil, "")

	// NewMultiSession seeds sessionsFetchInFlight true (see its own doc
	// comment): Init() always issues the first sessions fetch
	// unconditionally, so the guard starts in the state that first fetch
	// leaves it in. Fire that first fetch directly here rather than through
	// Init() itself, which also schedules tickCmd/refreshCmd -- real
	// tea.Tick timers this test has no reason to wait out.
	if !m.sessionsFetchInFlight {
		t.Fatal("NewMultiSession must seed sessionsFetchInFlight true -- Init() always fires the first fetch")
	}
	runCmd(doSessionsFetch(fetch))
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("the seeded first fetch must have run exactly once, got %d", got)
	}

	// refreshMsg fires again before that first fetch has answered -- the
	// at#55 scenario. doFetchAll (called through Update's refreshMsg case,
	// exactly as a live poll tick would) must see sessionsFetchInFlight
	// still true (seeded above, and no sessionsFetchResultMsg has landed
	// yet) and skip re-issuing. Called directly here, not via
	// m.Update(refreshMsg(...)), only to avoid also executing
	// refreshCmd()'s real 2s tea.Tick timer inside the returned Cmd -- the
	// guard under test lives entirely in doFetchAll, which Update's
	// refreshMsg case calls unchanged.
	next, cmd := m.doFetchAll()
	m = next
	if !m.sessionsFetchInFlight {
		t.Fatal("sessionsFetchInFlight must still be true -- no result has landed yet")
	}
	runCmd(cmd)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("a refresh while a sessions fetch is still in flight must not queue a second one against the "+
			"single-threaded supervisor MCP server (agent-tui#55) -- got %d calls, want 1", got)
	}

	// A second overlapping refresh, still with nothing resolved -- must
	// still be a no-op for the sessions fetch specifically.
	next, cmd = m.doFetchAll()
	m = next
	runCmd(cmd)
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("a second overlapping refresh must also be skipped -- got %d calls, want 1", got)
	}

	// Now the slow fetch finally answers. The flag clears, and the NEXT
	// refresh is free to issue a new one -- the guard must not wedge the
	// rail into never polling again.
	resolved, _ := m.Update(sessionsFetchResultMsg{err: errors.New("slow fetch, not yet answered")})
	m = resolved.(Model)
	if m.sessionsFetchInFlight {
		t.Fatal("sessionsFetchInFlight must clear once its result message lands")
	}

	next, cmd = m.doFetchAll()
	m = next
	runCmd(cmd)
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("once the prior fetch resolved, the next refresh must issue a new one -- got %d calls, want 2", got)
	}
}
