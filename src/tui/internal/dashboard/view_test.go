package dashboard

import (
	"strings"
	"testing"
)

// TestRenderAgentsOrdersByLedgerState pins renderAgents' own ordering
// contract: stable, matching ledgerStateOrder (src/estate's own
// dispatched/unknown/complete/failed) -- not map iteration order, which Go
// deliberately randomizes. agent-estate#930 moved AgentsByState from tmux
// lane states to the Go ledger's own States; this pins the new vocabulary.
func TestRenderAgentsOrdersByLedgerState(t *testing.T) {
	s := Stats{AgentsKnown: true, AgentsByState: map[string]int{
		"failed": 1, "dispatched": 2, "unknown": 1,
	}}
	got := renderAgents(s)
	want := "4 total (dispatched:2 unknown:1 failed:1)"
	if got != want {
		t.Fatalf("renderAgents = %q, want %q", got, want)
	}
}

// TestRenderAgentsDistinguishesAbsentFromUnreadable is agent-estate#930's
// own "second-order lesson": a ledger that has never been written to and a
// ledger that exists but could not be parsed must never render the same
// bare "unknown" -- one is a first-run estate, the other is an instrument
// failure that must not be mistaken for zero agents.
func TestRenderAgentsDistinguishesAbsentFromUnreadable(t *testing.T) {
	absent := renderAgents(Stats{AgentsKnown: false, AgentsUnavailable: "absent"})
	unreadable := renderAgents(Stats{AgentsKnown: false, AgentsUnavailable: "unreadable"})
	if absent == unreadable {
		t.Fatalf("absent and unreadable rendered identically: %q", absent)
	}
	if !strings.Contains(absent, "no dispatch has ever been recorded") {
		t.Errorf("absent = %q, want it to name the first-run case", absent)
	}
	if !strings.Contains(unreadable, "not zero") {
		t.Errorf("unreadable = %q, want it to warn this is not zero", unreadable)
	}
}

// TestRenderAgentsZeroTotalIsARealAnswer -- AgentsKnown true with an empty
// map means the fetch succeeded and found no lanes, a real "0," never
// confused with AgentsKnown false ("unknown").
func TestRenderAgentsZeroTotalIsARealAnswer(t *testing.T) {
	got := renderAgents(Stats{AgentsKnown: true, AgentsByState: map[string]int{}})
	if got != "0" {
		t.Fatalf("renderAgents = %q, want \"0\"", got)
	}
}

func TestRenderAgentsUnknownWhenFetchFailed(t *testing.T) {
	got := renderAgents(Stats{AgentsKnown: false})
	if got != unknown {
		t.Fatalf("renderAgents = %q, want %q", got, unknown)
	}
}

func TestRenderCountUnknownVsRealZero(t *testing.T) {
	if got := renderCount(Count{}); got != unknown {
		t.Fatalf("renderCount(zero value) = %q, want %q", got, unknown)
	}
	if got := renderCount(KnownCount(0)); got != "0" {
		t.Fatalf("renderCount(KnownCount(0)) = %q, want \"0\"", got)
	}
}

func TestRenderUSDFormatsTwoDecimals(t *testing.T) {
	if got := renderUSD(KnownUSD(12.3)); got != "$12.30" {
		t.Fatalf("renderUSD = %q, want \"$12.30\"", got)
	}
	if got := renderUSD(USD{}); got != unknown {
		t.Fatalf("renderUSD(zero value) = %q, want %q", got, unknown)
	}
}
