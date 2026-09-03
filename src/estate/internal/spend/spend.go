// Package spend reads back what internal/harness.Turn.Spend recorded onto
// ledger.Record -- the estate observes cost at dispatch time (#977); this
// package is what lets it be reported (#975's second half).
//
// WHY THIS IS SPLIT FROM main.go's PRINTING. Aggregate is the part that must
// never lie: it groups strictly by harness and never sums a dollar figure
// across harnesses, because claude reports one and codex never does (see
// docs/spend-observation.md). Keeping that rule in one function, tested on
// its own, means the CLI formatter in main.go cannot accidentally reintroduce
// the mixed total this package exists to refuse.
package spend

import (
	"sort"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// unknownHarness labels any record written before ledger.Record.Harness
// existed, or dispatched with no --harness= resolved (should not happen
// going forward, but a reader must not silently drop such a record from the
// count just because it cannot be grouped precisely).
const unknownHarness = "(unrecorded)"

// HarnessSpend is what one harness's dispatched turns show for spend,
// summed only over what was actually observed.
type HarnessSpend struct {
	Harness string

	Turns int // every record in this harness's group, regardless of what it recorded

	// TurnsWithCost / TotalCostUSD cover only turns whose SpendCostUSD was
	// non-nil. TotalCostUSD is meaningless (and not printed) when
	// TurnsWithCost is 0 -- summing zero non-nil pointers would silently
	// read as "cost $0.00", exactly the confusion SpendCostUSD's own doc
	// comment says this repo must never produce.
	TurnsWithCost int
	TotalCostUSD  float64

	// TurnsWithTokens / the four token sums cover only turns that reported
	// at least one non-nil token field. Each sum only adds the turns that
	// actually reported that specific field -- a turn missing, say,
	// cache-creation tokens does not turn its silence into a zero.
	TurnsWithTokens     int
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64

	// TurnsWithNeither is turns in this group whose SpendCostUSD and all
	// four token fields are nil -- almost certainly a record written before
	// #977, or a turn whose harness output could not be read at all. This
	// is coverage, not zero spend, and Report says so.
	TurnsWithNeither int

	// TurnsWithModelBreakdown is how many of this group's Turns had a
	// non-nil SpendByModel -- almost always fewer than Turns, and the gap
	// is coverage, not a claim those turns cost nothing per model. Every
	// codex turn leaves this at 0 (agent-estate#981: codex reports no
	// per-model breakdown at all); a claude turn whose envelope omitted
	// modelUsage does too.
	TurnsWithModelBreakdown int

	// ByModel is this harness's spend broken down further by model id,
	// summed only over the TurnsWithModelBreakdown turns above -- never
	// inferred from TotalCostUSD by guessing which model ran. Sorted by
	// model name. Empty whenever TurnsWithModelBreakdown is 0; a caller
	// must check that count before reading ByModel as complete coverage of
	// this harness's turns.
	ByModel []ModelSpend
}

// ModelSpend is one model's contribution to a harness's turns, summed only
// from the turns whose SpendByModel reported that model -- same
// pointer-means-absent discipline upstream (internal/harness.ModelSpend,
// ledger.ModelSpend) collapses into these plain sums, since by aggregation
// time "did this turn report a figure" is a per-field TurnsWith* count, not
// a pointer.
type ModelSpend struct {
	Model string

	TurnsWithCost int
	TotalCostUSD  float64

	TurnsWithTokens     int
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
}

// Report is spend grouped by harness, plus the coverage figures that keep a
// caller from reading a small sample as the whole picture.
type Report struct {
	ByHarness []HarnessSpend // sorted by Harness name, "(unrecorded)" last

	TotalTurns                   int
	TurnsWithAnySpend            int      // TotalTurns minus every group's TurnsWithNeither
	HarnessesReportingCost       []string // harness names with at least one TurnsWithCost > 0
	HarnessesReportingTokensOnly []string // harness names with TurnsWithTokens > 0 but TurnsWithCost == 0
}

// Aggregate groups records by Harness and sums what each turn actually
// reported. Records are expected to be the ledger's Current() (latest per
// task id) -- Spend fields are only ever set on a turn's terminal record, so
// passing the full append-only history instead would double count nothing
// here, but would also count Dispatched/no-longer-current records that never
// carry Spend fields, so Current() is the intended input.
func Aggregate(records []ledger.Record) Report {
	groups := map[string]*HarnessSpend{}
	order := []string{}
	get := func(h string) *HarnessSpend {
		if h == "" {
			h = unknownHarness
		}
		if g, ok := groups[h]; ok {
			return g
		}
		g := &HarnessSpend{Harness: h}
		groups[h] = g
		order = append(order, h)
		return g
	}

	// modelGroups is keyed harness -> model id, kept separate from groups
	// above so a harness with zero model breakdowns (codex, always) ends up
	// with a nil modelGroups[h] rather than an empty-but-present map.
	modelGroups := map[string]map[string]*ModelSpend{}
	modelOrder := map[string][]string{}
	getModel := func(h, model string) *ModelSpend {
		if h == "" {
			h = unknownHarness
		}
		byModel, ok := modelGroups[h]
		if !ok {
			byModel = map[string]*ModelSpend{}
			modelGroups[h] = byModel
		}
		if m, ok := byModel[model]; ok {
			return m
		}
		m := &ModelSpend{Model: model}
		byModel[model] = m
		modelOrder[h] = append(modelOrder[h], model)
		return m
	}

	for _, r := range records {
		g := get(r.Harness)
		g.Turns++

		hasCost := r.SpendCostUSD != nil
		hasTokens := r.SpendInputTokens != nil || r.SpendOutputTokens != nil ||
			r.SpendCacheReadTokens != nil || r.SpendCacheCreationTokens != nil

		if hasCost {
			g.TurnsWithCost++
			g.TotalCostUSD += *r.SpendCostUSD
		}
		if hasTokens {
			g.TurnsWithTokens++
			if r.SpendInputTokens != nil {
				g.InputTokens += *r.SpendInputTokens
			}
			if r.SpendOutputTokens != nil {
				g.OutputTokens += *r.SpendOutputTokens
			}
			if r.SpendCacheReadTokens != nil {
				g.CacheReadTokens += *r.SpendCacheReadTokens
			}
			if r.SpendCacheCreationTokens != nil {
				g.CacheCreationTokens += *r.SpendCacheCreationTokens
			}
		}
		if !hasCost && !hasTokens {
			g.TurnsWithNeither++
		}

		if r.SpendByModel != nil {
			g.TurnsWithModelBreakdown++
			for model, ms := range r.SpendByModel {
				m := getModel(r.Harness, model)
				if ms.CostUSD != nil {
					m.TurnsWithCost++
					m.TotalCostUSD += *ms.CostUSD
				}
				hasModelTokens := ms.InputTokens != nil || ms.OutputTokens != nil ||
					ms.CacheReadTokens != nil || ms.CacheCreationTokens != nil
				if hasModelTokens {
					m.TurnsWithTokens++
					if ms.InputTokens != nil {
						m.InputTokens += *ms.InputTokens
					}
					if ms.OutputTokens != nil {
						m.OutputTokens += *ms.OutputTokens
					}
					if ms.CacheReadTokens != nil {
						m.CacheReadTokens += *ms.CacheReadTokens
					}
					if ms.CacheCreationTokens != nil {
						m.CacheCreationTokens += *ms.CacheCreationTokens
					}
				}
			}
		}
	}

	sort.Strings(order)
	// unrecorded last regardless of where it sorts alphabetically -- it is
	// a fallback bucket, not a harness, and burying it under real harness
	// names would make the report read as if every dispatch names one.
	sorted := make([]string, 0, len(order))
	for _, h := range order {
		if h != unknownHarness {
			sorted = append(sorted, h)
		}
	}
	if _, ok := groups[unknownHarness]; ok {
		sorted = append(sorted, unknownHarness)
	}

	rep := Report{}
	for _, h := range sorted {
		g := *groups[h]
		if models := modelOrder[h]; len(models) > 0 {
			sortedModels := append([]string(nil), models...)
			sort.Strings(sortedModels)
			for _, model := range sortedModels {
				g.ByModel = append(g.ByModel, *modelGroups[h][model])
			}
		}
		rep.ByHarness = append(rep.ByHarness, g)
		rep.TotalTurns += g.Turns
		rep.TurnsWithAnySpend += g.Turns - g.TurnsWithNeither
		if g.TurnsWithCost > 0 {
			rep.HarnessesReportingCost = append(rep.HarnessesReportingCost, g.Harness)
		} else if g.TurnsWithTokens > 0 {
			rep.HarnessesReportingTokensOnly = append(rep.HarnessesReportingTokensOnly, g.Harness)
		}
	}
	return rep
}

// WindowedByObservation is observed spend for one window: every task whose
// own TERMINAL record (ledger.State.Terminal -- Complete or Failed; Unknown
// is deliberately excluded because it may still be running, see
// ledger.State.Terminal's doc comment) has an At falling strictly after
// since and no later than until.
//
// WHY OBSERVATION TIME, NOT DISPATCH TIME. An earlier version of this
// function (agent-estate#982) keyed the window on the task's OWN
// Dispatched-state record instead -- the instant it was launched, not the
// instant its outcome became known. The Director ticks every few minutes
// and a dispatched lane routinely runs far longer than that, so a task
// dispatched in window N that does not finish until window N+2 had its
// dispatch instant permanently behind every later window's own `since`: its
// cost was never re-tried in a later window and never landed in the window
// it dispatched in either, because that window had already been recorded
// before the task finished. It was not deferred, it was dropped, silently,
// for the common case rather than an edge case (agent-estate#989's review of
// #982's own PR). Keying on the terminal record's own At instead means every
// task's cost lands in EXACTLY ONE window -- the one during which its
// outcome became known -- because Current() keeps one terminal record per
// task id, its At is fixed once written, and consecutive tick windows are
// contiguous and non-overlapping. Nothing is deferred and nothing is ever
// dropped.
//
// This is why the field this feeds is named "observed", not "dispatched":
// it answers "what did we learn happened, in this window", not "what did we
// start, in this window."
//
// This is deliberately not Aggregate's per-harness Report: a tick-log entry
// is one compact JSON line, not a report, so it carries a single total plus
// the coverage it was drawn from. turns is every task whose outcome became
// known in the window, whatever it reported; turnsWithCost is how many of
// those carry a non-nil SpendCostUSD; totalCostUSD sums only those. A
// caller must print turnsWithCost alongside totalCostUSD ("$X across N of M
// observed turns") -- printing totalCostUSD alone reads as the window's
// whole cost when a harness reporting no dollar figure (codex, as of this
// writing) may have completed turns inside the same window that this total
// silently excludes. See docs/spend-observation.md for which harnesses
// report a dollar figure at all.
//
// current is expected to be Ledger.Current() (latest record per task id): a
// task still in flight when this is computed has no terminal record yet, so
// it contributes to neither turns nor turnsWithCost in this window -- it
// will contribute to whichever later window it actually finishes in.
func WindowedByObservation(current []ledger.Record, since, until time.Time) (turns, turnsWithCost int, totalCostUSD float64) {
	for _, r := range current {
		if !r.State.Terminal() {
			continue
		}
		if !r.At.After(since) || r.At.After(until) {
			continue
		}
		turns++
		if r.SpendCostUSD != nil {
			turnsWithCost++
			totalCostUSD += *r.SpendCostUSD
		}
	}
	return
}
