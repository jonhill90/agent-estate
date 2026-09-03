package agents

import (
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
)

// TestLedgerSectionAbsentWhenNoFetcherWired: WithLedger never called must
// add nothing to View() -- the same silent-default convention WithTasks(nil)
// and WithCosts(nil) already follow in this package.
func TestLedgerSectionAbsentWhenNoFetcherWired(t *testing.T) {
	m := New(nil)
	if strings.Contains(m.View(), "from the dispatch ledger") {
		t.Fatalf("View() shows a ledger section with no LedgerFetcher wired:\n%s", m.View())
	}
}

// TestLedgerSectionRendersInFlightTurns is agent-estate#930's own headline
// fix: an Agents pane that can never show real data (Fetcher's MCP server
// no longer exists) must show what the Go ledger says is in flight instead
// of staying permanently blank.
func TestLedgerSectionRendersInFlightTurns(t *testing.T) {
	m := New(nil).WithLedger(func() estatus.Status {
		return estatus.Status{
			Ledger: estatus.Present,
			InFlight: []estatus.Dispatch{
				{ID: "930-1", Issue: "#930", Role: "author", State: "dispatched", At: time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC), PID: 4242},
			},
		}
	})
	next, _ := m.Update(ledgerFetchResultMsg{status: estatus.Status{
		Ledger: estatus.Present,
		InFlight: []estatus.Dispatch{
			{ID: "930-1", Issue: "#930", Role: "author", State: "dispatched", At: time.Date(2026, 9, 3, 10, 0, 0, 0, time.UTC), PID: 4242},
		},
	}})
	m = next.(Model)

	out := m.View()
	for _, want := range []string{"from the dispatch ledger", "930-1", "#930", "author", "dispatched", "4242"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q:\n%s", want, out)
		}
	}
}

// TestLedgerSectionDistinguishesAbsentFromUnreadable: agent-estate#930's
// "second-order lesson" -- a ledger that has never been written to and one
// that exists but cannot be parsed must render visibly different text, not
// the same silence or the same bare "unknown."
func TestLedgerSectionDistinguishesAbsentFromUnreadable(t *testing.T) {
	m := New(nil).WithLedger(nil) // placeholder fetch replaced per case below

	absent := setLedgerFetcher(m, estatus.Status{Ledger: estatus.Absent, LedgerPath: "/no/ledger.jsonl"})
	absentOut := drive(absent)
	if !strings.Contains(absentOut, "no dispatch has ever been recorded") {
		t.Errorf("absent ledger section = %q, want it to name the first-run case", absentOut)
	}

	unreadable := setLedgerFetcher(m, estatus.Status{Ledger: estatus.Unreadable})
	unreadableOut := drive(unreadable)
	if !strings.Contains(unreadableOut, "not zero") {
		t.Errorf("unreadable ledger section = %q, want it to warn this is not zero", unreadableOut)
	}

	if absentOut == unreadableOut {
		t.Fatalf("absent and unreadable rendered identically")
	}
}

func setLedgerFetcher(m Model, status estatus.Status) Model {
	return m.WithLedger(func() estatus.Status { return status })
}

func drive(m Model) string {
	next, _ := m.Update(ledgerFetchResultMsg{status: mustLedgerStatus(m)})
	return next.(Model).View()
}

// mustLedgerStatus calls the wired fetcher directly -- these tests drive
// Update by hand rather than through Init()'s tea.Cmd machinery, matching
// this package's own TestFetchResultPopulatesRows convention.
func mustLedgerStatus(m Model) estatus.Status {
	return m.ledgerFetch()
}
