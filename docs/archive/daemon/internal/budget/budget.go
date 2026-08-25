// Package budget is a HARD spend cap that stops work.
//
// STOLEN FROM: paperclip server/src/services/budgets.ts. Two findings from
// reading their source that change the design:
//
//  1. Their `budgetMonthlyCents` column has NO enforcement path -- it is read
//     for display only. Porting that field alone gets you a number that does
//     nothing. The real cap is a policy row evaluated on every cost event.
//  2. The cap is evaluated BETWEEN cost events, so a single expensive call can
//     overshoot. The bound on overshoot is your event granularity.
//
// WHY IT EXISTS HERE: $22,281 of notional spend, 84 billion tokens, and a loop
// that never ticked. There was no ceiling anywhere in the estate. A stand-down
// once burned $45 of credits with nothing to show. This is the missing floor.
package budget

import (
	"fmt"
	"sync"
	"time"
)

type Decision string

const (
	Allow Decision = "allow" // under the soft threshold
	Warn  Decision = "warn"  // over soft, under hard -- proceed, but say so
	Block Decision = "block" // over hard -- refuse to dispatch
)

type Policy struct {
	// LimitUSD is the hard ceiling for the window. Zero disables the cap,
	// which must be an explicit choice, never a default.
	LimitUSD float64
	// WarnPercent of LimitUSD at which to start warning. 0 => 80.
	WarnPercent float64
	// Window is the rolling period the spend is summed over.
	Window time.Duration
}

func Default() Policy {
	return Policy{LimitUSD: 50, WarnPercent: 80, Window: 24 * time.Hour}
}

type event struct {
	at  time.Time
	usd float64
}

// Tracker sums spend over a rolling window. Truth is always re-derived by
// summing events, never incremented into a cached counter -- paperclip
// re-runs SUM() on every evaluation for exactly this reason: a drifted counter
// silently raises the ceiling.
type Tracker struct {
	mu     sync.Mutex
	policy Policy
	events []event
	now    func() time.Time // injectable for tests
}

func New(p Policy) *Tracker {
	if p.WarnPercent <= 0 {
		p.WarnPercent = 80
	}
	if p.Window <= 0 {
		p.Window = 24 * time.Hour
	}
	return &Tracker{policy: p, now: time.Now}
}

// Record adds an observed cost. Call once per model call, not per run --
// granularity here is the bound on how far a single call can overshoot.
func (t *Tracker) Record(usd float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event{at: t.now(), usd: usd})
}

// SpentUSD re-derives the window total by summation.
func (t *Tracker) SpentUSD() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sumLocked()
}

func (t *Tracker) sumLocked() float64 {
	cutoff := t.now().Add(-t.policy.Window)
	var total float64
	keep := t.events[:0]
	for _, e := range t.events {
		if e.at.After(cutoff) {
			keep = append(keep, e)
			total += e.usd
		}
	}
	t.events = keep
	return total
}

type Verdict struct {
	Decision Decision
	SpentUSD float64
	LimitUSD float64
	Reason   string
}

func (v Verdict) String() string {
	return fmt.Sprintf("%s: $%.2f of $%.2f -- %s", v.Decision, v.SpentUSD, v.LimitUSD, v.Reason)
}

// Check is called BEFORE every dispatch. Re-derived each time, so raising the
// limit unblocks immediately with no manual resume (paperclip's
// getInvocationBlock does the same, deliberately).
func (t *Tracker) Check() Verdict {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.policy.LimitUSD <= 0 {
		return Verdict{Decision: Allow, LimitUSD: 0, Reason: "no cap configured"}
	}
	spent := t.sumLocked()
	hard := t.policy.LimitUSD
	soft := hard * t.policy.WarnPercent / 100

	switch {
	case spent >= hard:
		return Verdict{Block, spent, hard,
			fmt.Sprintf("hard cap reached over the last %s -- refusing to dispatch", t.policy.Window)}
	case spent >= soft:
		return Verdict{Warn, spent, hard,
			fmt.Sprintf("past %.0f%% of the cap", t.policy.WarnPercent)}
	default:
		return Verdict{Allow, spent, hard, "under cap"}
	}
}
