package monitor

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestNewSeedsFetchInFlightTrue mirrors internal/agents/model_test.go's
// TestRKeyRefetches seed check: Init() always fires the first fetch
// unconditionally, so New must already reflect that before the first
// refreshMsg (refreshInterval later) can check it.
func TestNewSeedsFetchInFlightTrue(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	if !m.fetchInFlight {
		t.Fatal("New must seed fetchInFlight true -- Init() always fires the first fetch")
	}
}

// TestNewWithNilFetchDoesNotSeedInFlight confirms a genuinely unconfigured
// monitor (no Fetcher) never reports a fetch outstanding -- New's own
// fetch != nil seed.
func TestNewWithNilFetchDoesNotSeedInFlight(t *testing.T) {
	m := New(nil)
	if m.fetchInFlight {
		t.Fatal("New(nil) must not seed fetchInFlight true -- there is no fetch to be in flight")
	}
}

// TestRefreshMsgDoesNotOverlapInFlightFetch is agent-tui#177's core
// mutation check: a refreshMsg tick landing while the previous fetch has
// not yet answered must NOT queue a second "sessions" call against
// mcp_server.py's single-threaded stdio loop. Without the fetchInFlight
// guard in the refreshMsg case, this test goes red (calls == 2).
func TestRefreshMsgDoesNotOverlapInFlightFetch(t *testing.T) {
	calls := 0
	fetch := func() (Snapshot, error) {
		calls++
		return Snapshot{}, errors.New("slow, not yet answered")
	}
	m := New(fetch) // seeds fetchInFlight true, matching Init's own first fetch

	next, cmd := m.Update(refreshMsg{})
	m = next.(Model)
	for _, msg := range runCmd(cmd) {
		next, _ = m.Update(msg)
		m = next.(Model)
	}
	if calls != 0 {
		t.Fatalf("refreshMsg while a fetch is already in flight called fetch %d times, want 0 -- the in-flight guard should have blocked it", calls)
	}
}

// TestRefreshKeyNoopsWhileFetchInFlight is the "r" key's own version of
// TestRefreshMsgDoesNotOverlapInFlightFetch above -- a manual refresh must
// respect the same guard the periodic ticker does, not bypass it.
func TestRefreshKeyNoopsWhileFetchInFlight(t *testing.T) {
	calls := 0
	fetch := func() (Snapshot, error) { calls++; return Snapshot{}, nil }
	m := New(fetch) // seeds fetchInFlight true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Fatal("[r] while a fetch is already in flight must return a nil cmd, not queue a second fetch")
	}
	if calls != 0 {
		t.Fatalf("fetch called %d times, want 0 -- the in-flight guard should have blocked it", calls)
	}
}

// TestFetchResultMsgClearsInFlightOnSuccess and
// TestFetchResultMsgClearsInFlightOnError together prove the guard is
// cleared on every exit path, both directions of the brief's mutation
// check: a guard that leaks true after either outcome stops fetching
// forever and freezes this pane on stale data.
func TestFetchResultMsgClearsInFlightOnSuccess(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	if !m.fetchInFlight {
		t.Fatal("New must seed fetchInFlight true")
	}
	next, _ := m.Update(fetchResultMsg{snapshot: Snapshot{}, err: nil})
	m = next.(Model)
	if m.fetchInFlight {
		t.Fatal("a successful fetchResultMsg must clear fetchInFlight")
	}
}

func TestFetchResultMsgClearsInFlightOnError(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	if !m.fetchInFlight {
		t.Fatal("New must seed fetchInFlight true")
	}
	next, _ := m.Update(fetchResultMsg{err: errors.New("no reply within 10s")})
	m = next.(Model)
	if m.fetchInFlight {
		t.Fatal("a failed fetchResultMsg must also clear fetchInFlight -- a caller that leaks true on error stops fetching forever")
	}
}

// runCmd executes cmd (and, recursively, every sub-command of any
// tea.BatchMsg it returns) and reports every resulting tea.Msg -- same
// helper internal/rail/inflight_test.go's own runCmd provides, repeated
// here rather than imported (this package has no dependency on
// internal/rail and shouldn't gain one just for a test helper).
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
