package spend

import (
	"testing"
	"time"

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

// TestAggregate_ByModelSumsAcrossTurnsAndNeverForCodex covers #981: a
// harness that reports SpendByModel gets a per-model breakdown, summed
// correctly when two turns both name the same model; a harness that never
// reports one (codex) must leave TurnsWithModelBreakdown at 0 and ByModel
// empty, never inventing a breakdown from its scalar cost/tokens.
func TestAggregate_ByModelSumsAcrossTurnsAndNeverForCodex(t *testing.T) {
	rep := Aggregate([]ledger.Record{
		{ID: "a1", Harness: "claude", State: ledger.Complete, SpendCostUSD: f64(0.1883826), SpendByModel: map[string]ledger.ModelSpend{
			"claude-haiku-4-5-20251001": {CostUSD: f64(0.000591), InputTokens: i64(1)},
			"claude-sonnet-5":           {CostUSD: f64(0.1877916), InputTokens: i64(1)},
		}},
		{ID: "a2", Harness: "claude", State: ledger.Complete, SpendCostUSD: f64(0.05), SpendByModel: map[string]ledger.ModelSpend{
			"claude-sonnet-5": {CostUSD: f64(0.05), InputTokens: i64(3)},
		}},
		{ID: "a3", Harness: "claude", State: ledger.Complete, SpendCostUSD: f64(0.02)}, // no breakdown -- envelope predates modelUsage
		{ID: "c1", Harness: "codex", State: ledger.Complete, SpendInputTokens: i64(27131)},
	})
	byName := map[string]HarnessSpend{}
	for _, g := range rep.ByHarness {
		byName[g.Harness] = g
	}

	claude := byName["claude"]
	if claude.TurnsWithModelBreakdown != 2 {
		t.Fatalf("want 2 claude turns with a model breakdown (a3 has none), got %d", claude.TurnsWithModelBreakdown)
	}
	if len(claude.ByModel) != 2 {
		t.Fatalf("want 2 distinct models, got %+v", claude.ByModel)
	}
	models := map[string]ModelSpend{}
	for _, m := range claude.ByModel {
		models[m.Model] = m
	}
	haiku := models["claude-haiku-4-5-20251001"]
	if haiku.TurnsWithCost != 1 || haiku.TotalCostUSD != 0.000591 {
		t.Fatalf("unexpected haiku sum: %+v", haiku)
	}
	sonnet := models["claude-sonnet-5"]
	if sonnet.TurnsWithCost != 2 || sonnet.TotalCostUSD < 0.23779 || sonnet.TotalCostUSD > 0.23780 {
		t.Fatalf("sonnet must sum across both turns that named it, got %+v", sonnet)
	}
	if sonnet.InputTokens != 4 {
		t.Fatalf("sonnet InputTokens should sum 1+3=4 across the two turns, got %d", sonnet.InputTokens)
	}

	codex := byName["codex"]
	if codex.TurnsWithModelBreakdown != 0 {
		t.Fatalf("codex must never report a model breakdown, got TurnsWithModelBreakdown=%d", codex.TurnsWithModelBreakdown)
	}
	if len(codex.ByModel) != 0 {
		t.Fatalf("codex.ByModel must be empty, got %+v", codex.ByModel)
	}
}

func harnessNames(gs []HarnessSpend) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.Harness
	}
	return out
}

// agent-estate#989: only a task OBSERVED (its own terminal record's At)
// inside the window counts, and only against that record's own reported
// cost -- never a flat per-task assumption, and never keyed on when the
// task was dispatched (agent-estate#982's original, and wrong, keying).
func TestWindowedByObservation_OnlyCountsOutcomesObservedInsideTheWindow(t *testing.T) {
	since := mustTime(t, "2026-09-03T10:00:00Z")
	until := mustTime(t, "2026-09-03T10:03:00Z")

	current := []ledger.Record{
		// Observed (completed) before the window: must not count, however
		// much it cost.
		{ID: "before", State: ledger.Complete, At: mustTime(t, "2026-09-03T09:59:00Z"), SpendCostUSD: f64(9.0)},
		// Observed inside the window, reported a cost.
		{ID: "in-a", State: ledger.Complete, At: mustTime(t, "2026-09-03T10:01:00Z"), SpendCostUSD: f64(0.25)},
		// Observed inside the window, no cost reported (e.g. codex).
		{ID: "in-b", State: ledger.Complete, At: mustTime(t, "2026-09-03T10:02:00Z"), SpendInputTokens: i64(100)},
		// Observed exactly at until: inclusive.
		{ID: "in-c", State: ledger.Failed, At: until},
		// Still in flight inside the window (dispatched here, but no
		// terminal record yet): must not count until it actually finishes.
		{ID: "in-flight", State: ledger.Dispatched, At: mustTime(t, "2026-09-03T10:02:30Z")},
		// Observed after the window: must not count.
		{ID: "after", State: ledger.Complete, At: mustTime(t, "2026-09-03T10:04:00Z"), SpendCostUSD: f64(9.0)},
	}

	turns, turnsWithCost, total := WindowedByObservation(current, since, until)
	if turns != 3 {
		t.Fatalf("want 3 turns observed in the window (in-a, in-b, in-c), got %d", turns)
	}
	if turnsWithCost != 1 {
		t.Fatalf("want 1 turn with a reported cost (in-a only), got %d", turnsWithCost)
	}
	if total != 0.25 {
		t.Fatalf("want total 0.25 (before/after excluded by window, in-b/in-c excluded by no reported cost), got %v", total)
	}
}

// The since boundary is exclusive and until is inclusive -- an outcome
// observed exactly at `since` belongs to the PREVIOUS tick's window, not
// this one.
func TestWindowedByObservation_SinceIsExclusive(t *testing.T) {
	since := mustTime(t, "2026-09-03T10:00:00Z")
	until := mustTime(t, "2026-09-03T10:03:00Z")
	current := []ledger.Record{
		{ID: "at-since", State: ledger.Complete, At: since, SpendCostUSD: f64(1.0)},
	}
	turns, _, _ := WindowedByObservation(current, since, until)
	if turns != 0 {
		t.Fatalf("an outcome observed exactly at `since` belongs to the previous window; want 0 turns, got %d", turns)
	}
}

// agent-estate#989's own scenario: a task dispatched in window N that does
// not finish until window N+1 must have its cost land in window N+1, not be
// dropped, and not double-count in window N.
func TestWindowedByObservation_SlowTaskLandsInTheWindowItFinishes(t *testing.T) {
	tick1 := mustTime(t, "2026-09-03T10:00:00Z")
	tick2 := mustTime(t, "2026-09-03T10:03:00Z")
	tick3 := mustTime(t, "2026-09-03T10:06:00Z")

	// At tick 2's window (tick1, tick2], the task is still running: no
	// terminal record exists yet.
	stillRunning := []ledger.Record{
		{ID: "slow", State: ledger.Dispatched, At: mustTime(t, "2026-09-03T10:01:00Z")},
	}
	turns, turnsWithCost, total := WindowedByObservation(stillRunning, tick1, tick2)
	if turns != 0 || turnsWithCost != 0 || total != 0 {
		t.Fatalf("an in-flight task must not be counted until it has a terminal record, got turns=%d turnsWithCost=%d total=%v", turns, turnsWithCost, total)
	}

	// It finishes inside tick 3's window (tick2, tick3], with a cost.
	finished := []ledger.Record{
		{ID: "slow", State: ledger.Complete, At: mustTime(t, "2026-09-03T10:04:30Z"), SpendCostUSD: f64(1.5)},
	}
	turns, turnsWithCost, total = WindowedByObservation(finished, tick2, tick3)
	if turns != 1 || turnsWithCost != 1 || total != 1.5 {
		t.Fatalf("the task's cost must land in the window it finished in, got turns=%d turnsWithCost=%d total=%v", turns, turnsWithCost, total)
	}

	// And it must not ALSO be counted in tick 2's window when re-evaluated
	// against the same (now-terminal) record -- since is what changed, not
	// the record.
	turns, _, _ = WindowedByObservation(finished, tick1, tick2)
	if turns != 0 {
		t.Fatalf("a task observed in a LATER window must not also count in an earlier one, got %d", turns)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}
