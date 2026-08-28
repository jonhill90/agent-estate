package dashboard

import "testing"

// TestRenderAgentsOrdersByLaneAllStates pins renderAgents' own ordering
// contract: stable, matching internal/lane.AllStates -- not map iteration
// order, which Go deliberately randomizes.
func TestRenderAgentsOrdersByLaneAllStates(t *testing.T) {
	s := Stats{AgentsKnown: true, AgentsByState: map[string]int{
		"broken": 1, "free": 2, "busy": 1,
	}}
	got := renderAgents(s)
	want := "4 total (free:2 busy:1 broken:1)"
	if got != want {
		t.Fatalf("renderAgents = %q, want %q", got, want)
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
