package spend

import (
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

// TestAggregate_ClaudeTurnWithDollarCost covers #975's first required case:
// a turn that reported a real dollar figure.
func TestAggregate_ClaudeTurnWithDollarCost(t *testing.T) {
	rep := Aggregate([]ledger.Record{
		{ID: "a1", Harness: "claude", State: ledger.Complete, SpendCostUSD: f64(0.1883826), SpendInputTokens: i64(2), SpendOutputTokens: i64(4)},
	})
	if len(rep.ByHarness) != 1 {
		t.Fatalf("want 1 harness group, got %d", len(rep.ByHarness))
	}
	g := rep.ByHarness[0]
	if g.Harness != "claude" || g.TurnsWithCost != 1 || g.TotalCostUSD != 0.1883826 {
		t.Fatalf("unexpected group: %+v", g)
	}
	if g.TurnsWithNeither != 0 {
		t.Fatalf("a turn with a recorded cost must not count as TurnsWithNeither, got %+v", g)
	}
	if len(rep.HarnessesReportingCost) != 1 || rep.HarnessesReportingCost[0] != "claude" {
		t.Fatalf("expected claude in HarnessesReportingCost, got %v", rep.HarnessesReportingCost)
	}
}

// TestAggregate_CodexTurnWithTokensNoDollars covers #975's second required
// case: codex never reports a dollar figure (docs/spend-observation.md), so
// this must show up as tokens with TurnsWithCost == 0, never as $0.00.
func TestAggregate_CodexTurnWithTokensNoDollars(t *testing.T) {
	rep := Aggregate([]ledger.Record{
		{ID: "c1", Harness: "codex", State: ledger.Complete, SpendInputTokens: i64(27131), SpendOutputTokens: i64(5)},
	})
	g := rep.ByHarness[0]
	if g.TurnsWithCost != 0 {
		t.Fatalf("codex turn must never be counted as having a cost, got TurnsWithCost=%d", g.TurnsWithCost)
	}
	if g.TurnsWithTokens != 1 || g.InputTokens != 27131 || g.OutputTokens != 5 {
		t.Fatalf("unexpected token totals: %+v", g)
	}
	if len(rep.HarnessesReportingCost) != 0 {
		t.Fatalf("codex must not appear in HarnessesReportingCost, got %v", rep.HarnessesReportingCost)
	}
	if len(rep.HarnessesReportingTokensOnly) != 1 || rep.HarnessesReportingTokensOnly[0] != "codex" {
		t.Fatalf("expected codex in HarnessesReportingTokensOnly, got %v", rep.HarnessesReportingTokensOnly)
	}
}

// TestAggregate_TurnWithNeither covers #975's third required case: a record
// predating #977 (or one whose harness output could not be read) carries no
// spend fields at all. It must be counted as coverage, not as zero spend.
func TestAggregate_TurnWithNeither(t *testing.T) {
	rep := Aggregate([]ledger.Record{
		{ID: "o1", Harness: "claude", State: ledger.Complete},
	})
	g := rep.ByHarness[0]
	if g.Turns != 1 || g.TurnsWithNeither != 1 {
		t.Fatalf("expected the one turn counted as TurnsWithNeither, got %+v", g)
	}
	if g.TurnsWithCost != 0 || g.TotalCostUSD != 0 {
		t.Fatalf("a turn with no recorded cost must not contribute to TotalCostUSD, got %+v", g)
	}
	if rep.TurnsWithAnySpend != 0 {
		t.Fatalf("TurnsWithAnySpend should be 0, got %d", rep.TurnsWithAnySpend)
	}
}

// TestAggregate_MixedHarnessSetNeverSumsAcrossHarnesses is the trap #975
// names directly: a claude turn's dollar cost must never be added to or
// presented alongside a codex turn's tokens as one combined figure.
func TestAggregate_MixedHarnessSetNeverSumsAcrossHarnesses(t *testing.T) {
	rep := Aggregate([]ledger.Record{
		{ID: "a1", Harness: "claude", State: ledger.Complete, SpendCostUSD: f64(0.20), SpendInputTokens: i64(100)},
		{ID: "a2", Harness: "claude", State: ledger.Complete, SpendCostUSD: f64(0.10), SpendInputTokens: i64(50)},
		{ID: "c1", Harness: "codex", State: ledger.Complete, SpendInputTokens: i64(27131)},
		{ID: "c2", Harness: "codex", State: ledger.Complete}, // neither
		{ID: "u1", State: ledger.Complete},                   // predates Harness field entirely
	})
	if len(rep.ByHarness) != 3 {
		t.Fatalf("want 3 groups (claude, codex, unrecorded), got %d: %+v", len(rep.ByHarness), rep.ByHarness)
	}
	byName := map[string]HarnessSpend{}
	for _, g := range rep.ByHarness {
		byName[g.Harness] = g
	}
	claude := byName["claude"]
	if claude.TurnsWithCost != 2 || claude.TotalCostUSD < 0.29999 || claude.TotalCostUSD > 0.30001 {
		t.Fatalf("unexpected claude group: %+v", claude)
	}
	codex := byName["codex"]
	if codex.TurnsWithCost != 0 {
		t.Fatalf("codex group must never carry a cost, got %+v", codex)
	}
	if codex.TurnsWithTokens != 1 || codex.InputTokens != 27131 {
		t.Fatalf("unexpected codex group: %+v", codex)
	}
	if codex.TurnsWithNeither != 1 {
		t.Fatalf("codex's neither-turn must be counted, got %+v", codex)
	}
	unrecorded := byName[unknownHarness]
	if unrecorded.Turns != 1 || unrecorded.TurnsWithNeither != 1 {
		t.Fatalf("unexpected unrecorded group: %+v", unrecorded)
	}
	// The trap itself: nothing in Report offers a single combined dollar
	// total, so there is nothing here for a careless caller to print as
	// "$X total" across harnesses that don't all report dollars.
	if len(rep.HarnessesReportingCost) != 1 || rep.HarnessesReportingCost[0] != "claude" {
		t.Fatalf("only claude should report cost, got %v", rep.HarnessesReportingCost)
	}
	if len(rep.HarnessesReportingTokensOnly) != 1 || rep.HarnessesReportingTokensOnly[0] != "codex" {
		t.Fatalf("only codex should be tokens-only, got %v", rep.HarnessesReportingTokensOnly)
	}
	if rep.TotalTurns != 5 {
		t.Fatalf("want 5 total turns, got %d", rep.TotalTurns)
	}
	if rep.TurnsWithAnySpend != 3 {
		t.Fatalf("want 3 turns with any spend recorded (a1,a2,c1), got %d", rep.TurnsWithAnySpend)
	}
	// unrecorded sorts last regardless of alphabetical order.
	if rep.ByHarness[len(rep.ByHarness)-1].Harness != unknownHarness {
		t.Fatalf("expected %q last, got order %v", unknownHarness, harnessNames(rep.ByHarness))
	}
}

func harnessNames(gs []HarnessSpend) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Harness
	}
	return out
}
