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

// WindowedByDispatch is dispatch-attributable spend for one window: every
// task whose OWN dispatch-time record (ledger.Dispatched, whose At is the
// instant the subprocess was launched -- see ledger.Ledger.All's doc
// comment for why this is not the same as that task's Current() record)
// falls strictly after since and no later than until.
//
// This is deliberately not Aggregate's per-harness Report: a tick-log entry
// is one compact JSON line, not a report, so it carries a single total plus
// the coverage it was drawn from. turns is every task dispatched in the
// window, whatever it reported; turnsWithCost is how many of those turns'
// Current() record carries a non-nil SpendCostUSD; totalCostUSD sums only
// those. A caller must print turnsWithCost alongside totalCostUSD ("$X
// across N of M dispatched turns") -- printing totalCostUSD alone reads as
// the window's whole cost when a harness reporting no dollar figure (codex,
// as of this writing) may have run turns inside the same window that this
// total silently excludes. See docs/spend-observation.md for which
// harnesses report a dollar figure at all.
//
// current is expected to be Ledger.Current() (latest record per task id):
// Spend fields are only ever set on a turn's terminal record, so a task
// still in flight when this is computed contributes to turns but not to
// turnsWithCost, however it eventually finishes -- this is a snapshot at
// tick-record time, not re-derived later.
func WindowedByDispatch(all, current []ledger.Record, since, until time.Time) (turns, turnsWithCost int, totalCostUSD float64) {
	latest := map[string]ledger.Record{}
	for _, r := range current {
		latest[r.ID] = r
	}
	seen := map[string]bool{}
	for _, r := range all {
		if r.State != ledger.Dispatched || r.ID == "" || seen[r.ID] {
			continue
		}
		if !r.At.After(since) || r.At.After(until) {
			continue
		}
		seen[r.ID] = true
		turns++
		if cur, ok := latest[r.ID]; ok && cur.SpendCostUSD != nil {
			turnsWithCost++
			totalCostUSD += *cur.SpendCostUSD
		}
	}
	return
}
