package rail

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
)

// TestPlainNewHasNoLedgerLine is New()'s backward-compatibility contract,
// matching TestPlainNewHasNoCostLine: no ledgerFetch means no "ledger:"
// line at all.
func TestPlainNewHasNoLedgerLine(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	if strings.Contains(m.View(), "ledger:") {
		t.Fatalf("New() (no ledger fetcher) rendered a ledger line:\n%s", m.View())
	}
}

// TestWithLedgerRendersInFlightCount is agent-estate#930's own headline
// fix at the rail level: sessionsFetch/lanesFetch both read the deleted
// Python MCP server, so this pane could never show real agent data. The
// ledger line must show what the Go ledger says is in flight.
func TestWithLedgerRendersInFlightCount(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil }).
		WithLedger(func() estatus.Status {
			return estatus.Status{
				Ledger:   estatus.Present,
				InFlight: []estatus.Dispatch{{ID: "930-1", State: "dispatched"}},
			}
		})
	next, _ := m.Update(ledgerFetchResultMsg{status: estatus.Status{
		Ledger:   estatus.Present,
		InFlight: []estatus.Dispatch{{ID: "930-1", State: "dispatched"}},
	}})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "ledger: 1 in flight") {
		t.Fatalf("View() missing the ledger in-flight count:\n%s", out)
	}
}

// TestLedgerLineDistinguishesAbsentFromUnreadable is agent-estate#930's own
// "second-order lesson" applied at the rail level -- a ledger that has
// never been written to and one that exists but cannot be parsed must
// render visibly different text.
func TestLedgerLineDistinguishesAbsentFromUnreadable(t *testing.T) {
	base := New(func() ([]lane.Lane, error) { return nil, nil })

	absent, _ := base.WithLedger(func() estatus.Status { return estatus.Status{Ledger: estatus.Absent} }).
		Update(ledgerFetchResultMsg{status: estatus.Status{Ledger: estatus.Absent}})
	// View() wraps the 28-column rail's text, so compare the UNWRAPPED
	// line renderLedgerLine actually produced rather than the wrapped
	// screen output -- the same value the View()'s "ledger:" line is built
	// from.
	absentLine := renderLedgerLine(absent.(Model))

	unreadable, _ := base.WithLedger(func() estatus.Status { return estatus.Status{Ledger: estatus.Unreadable} }).
		Update(ledgerFetchResultMsg{status: estatus.Status{Ledger: estatus.Unreadable}})
	unreadableLine := renderLedgerLine(unreadable.(Model))

	if !strings.Contains(absentLine, "no dispatch ever recorded") {
		t.Errorf("absent ledger line = %q, want it to name the first-run case", absentLine)
	}
	if !strings.Contains(unreadableLine, "not zero") {
		t.Errorf("unreadable ledger line = %q, want it to warn this is not zero", unreadableLine)
	}
	if absentLine == unreadableLine {
		t.Fatal("absent and unreadable rendered identically")
	}
}
