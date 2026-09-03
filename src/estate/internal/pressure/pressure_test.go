package pressure

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// hostIsMeasurable reports whether this host exposes the readings the gate
// needs. They are read with macOS tools (`sysctl vm.loadavg`, `vm_stat`), so
// on Linux -- including the ubuntu-latest runner that gates every merge --
// they are simply absent.
//
// This is NOT a reason to skip. An unmeasurable host is a real, important
// case: the gate must REFUSE there, never allow. So each test below asserts
// the correct behaviour for the host it is actually running on, and the
// fail-closed property gets asserted on exactly the machines where it
// matters most.
func hostIsMeasurable(t *testing.T) bool {
	t.Helper()
	if _, err := loadPerCore(); err != nil {
		return false
	}
	if _, err := freeMemMB(); err != nil {
		return false
	}
	return true
}

// A ledger that cannot be read must refuse. Blindness is not capacity.
func TestUnreadableLedgerRefuses(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.jsonl")
	if err := os.WriteFile(p, []byte("{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := ledger.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	v := Check(l, Default())
	if v.OK {
		t.Fatal("Check() allowed dispatch with an unreadable ledger; it must fail closed")
	}
}

func TestAtCapRefuses(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lim := Default()
	lim.MaxInFlight = 2
	lim.MaxLoadPerCore = 1e9 // isolate the cap from real host load
	lim.MinFreeMemMB = 0
	for _, id := range []string{"a", "b"} {
		if err := l.Append(ledger.Record{ID: id, State: ledger.Dispatched}); err != nil {
			t.Fatal(err)
		}
	}
	if v := Check(l, lim); v.OK {
		t.Fatalf("Check() allowed dispatch at the cap; reading=%+v", v.Reading)
	}
}

func TestBelowCapAllowsOrRefusesForTheRightReason(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lim := Default()
	lim.MaxInFlight = 2
	lim.MaxLoadPerCore = 1e9
	lim.MinFreeMemMB = 0
	// Swap is neutralised for the same reason load and memory are: this test
	// isolates the IN-FLIGHT cap. Without this it fails on any host that is
	// genuinely paging -- which is a true reading, not a broken gate, and is
	// exactly the state that killed the host on 2026-09-03.
	lim.MaxSwapoutsPerSample = 1e9
	lim.MaxWorktrees = 1e9
	if err := l.Append(ledger.Record{ID: "a", State: ledger.Complete}); err != nil {
		t.Fatal(err)
	}
	v := Check(l, lim)

	if hostIsMeasurable(t) {
		if !v.OK {
			t.Fatalf("Check() refused below the cap on a measurable host: %v", v.Reasons)
		}
		return
	}
	// Unmeasurable host. The gate MUST refuse -- blindness is never capacity --
	// and it must say that it could not measure, not invent a cap it hit.
	if v.OK {
		t.Fatal("Check() allowed dispatch on a host whose load and memory cannot be read; blindness is not capacity")
	}
	joined := strings.Join(v.Reasons, " ")
	if !strings.Contains(joined, "could not measure") {
		t.Errorf("a refusal for unmeasurable state must say so; got: %v", v.Reasons)
	}
}

// The memory floor was never exercised: every test that called Check zeroed
// MinFreeMemMB, so a gate reading SWAP instead of RAM shipped green and
// refused 100% of dispatches on an 18 GB machine.
func TestMemoryFloorIsMeasuredAgainstRealRAM(t *testing.T) {
	mb, err := freeMemMB()
	if err != nil {
		// The reader is unavailable on this host. That must surface as an
		// ERROR from freeMemMB -- which the caller turns into a refusal --
		// and never as a plausible-looking zero.
		if mb != 0 {
			t.Errorf("freeMemMB errored but still returned %.0fMB; a failed reading must not carry a number", mb)
		}
		t.Skipf("host cannot report free memory (%v); the fail-closed path is asserted by TestBelowCapAllowsOrRefusesForTheRightReason", err)
	}
	if mb <= 0 {
		t.Fatalf("freeMemMB reported %.0fMB -- a machine running this test has memory; the instrument is wrong", mb)
	}
}

func TestBelowMemoryFloorRefuses(t *testing.T) {
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lim := Default()
	lim.MaxLoadPerCore = 1e9
	lim.MaxInFlight = 1e9
	lim.MinFreeMemMB = 1e12 // no machine has a petabyte free
	if v := Check(l, lim); v.OK {
		t.Fatal("Check allowed dispatch below an unreachable memory floor")
	}
}

// The swap gate is tested in BOTH directions on purpose. A guard that only
// refuses is the bug this replaced (vm.swapusage read as memory, refusing
// 100% of dispatches); a guard that only passes is the bug that killed the
// host on 2026-09-03. Neither direction alone is evidence.
func TestSwapoutsParsedAndDeltaIsWhatMatters(t *testing.T) {
	quiet := []byte("Mach Virtual Memory Statistics:\nSwapins:  6587.\nSwapouts:  49904.\n")
	n, err := parseSwapouts(quiet)
	if err != nil || n != 49904 {
		t.Fatalf("parse: got %v %v, want 49904", n, err)
	}
	// A swap-disabled host: the counter is present and pinned at zero. This must
	// PARSE as a real reading of 0, never error -- an error here would refuse
	// every dispatch forever on a machine with swap off, which is the original
	// vm.swapusage bug arriving by a new route. `b3de7d3` had a fixture for this
	// case and the gauge rewrite dropped it; it is restored here.
	//
	// Note what this fixture cannot do: it pins the PARSER, not the host. Nobody
	// has run this gate on a genuinely swap-disabled machine.
	off, err := parseSwapouts([]byte("Mach Virtual Memory Statistics:\nSwapins:  0.\nSwapouts:  0.\n"))
	if err != nil || off != 0 {
		t.Fatalf("swap-disabled host: got %v %v, want a clean 0", off, err)
	}
	// The directions -- a quiet host passes, a paging host refuses -- are
	// asserted against Check and swapoutRate in
	// TestSwapoutRateRefusesAtTheDefaultLimitWhenPaging and
	// TestSwapLimitAloneRefusesWithItsOwnReason. They were comparisons between
	// two constants here, which held no matter what the gate did.
}

func TestSwapoutsUnreadableRefusesRatherThanPasses(t *testing.T) {
	if _, err := parseSwapouts([]byte("no such line")); err == nil {
		t.Fatal("an unreadable paging counter must error so Check() fails closed")
	}
}

// Everything below drives Check() itself.
//
// Why this section exists: the tests above it are all true and all narrow.
// They verify the PARSERS -- parseSwapouts, countWorktreeLines -- and then
// compare constants against Default(). PR #999's review ran three fail-open
// mutants against that suite and it stayed green on every one:
//
//	if false && rate >= lim.MaxSwapoutsPerSample {   // swap gate never refuses
//	if false && n > lim.MaxWorktrees {               // ceiling never refuses
//	func swapoutRate(...) { return 0, nil }          // gauge dead
//
// The first of those is the 2026-09-03 outage restored verbatim. A guard that
// closes a fail-open hole has to be able to prove it stays closed, so each
// limit below is driven through Check with EVERY OTHER limit neutralised and
// its own threshold set to 0. Asserting the reason STRING, not just !OK, is
// what makes it evidence of independence: a refusal alone could have come
// from any of the six limits.

// neutralised returns limits under which no limit can refuse, so a test can
// arm exactly one and know what any refusal it sees came from.
func neutralised() Limits {
	return Limits{
		MaxLoadPerCore:       1e9,
		MinFreeMemMB:         0,
		MaxSwapoutsPerSample: 1e9,
		MaxWorktrees:         1e9,
		MaxInFlight:          1e9,
	}
}

const (
	swapReason     = "actively paging"
	worktreeReason = "worktrees above ceiling"
)

func emptyLedger(t *testing.T) *ledger.Ledger {
	t.Helper()
	l, err := ledger.Open(filepath.Join(t.TempDir(), "l.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Arms the swap limit alone. Kills the "swap gate never refuses" mutant.
func TestSwapLimitAloneRefusesWithItsOwnReason(t *testing.T) {
	lim := neutralised()
	lim.MaxSwapoutsPerSample = 0 // any reading at all is at or above this
	v := Check(emptyLedger(t), lim)

	if v.OK {
		t.Fatalf("Check() allowed dispatch with the swap limit set to 0; the paging gate is not wired. reading=%+v", v.Reading)
	}
	joined := strings.Join(v.Reasons, " ")
	if !hostCanMeasureSwap(t) {
		// The gate must still refuse here, and say it could not measure --
		// blindness is not capacity. This is the Linux/CI path.
		if !strings.Contains(joined, "could not measure paging") {
			t.Fatalf("a host that cannot read vm_stat must refuse for that reason; got %v", v.Reasons)
		}
		return
	}
	if !strings.Contains(joined, swapReason) {
		t.Errorf("refusal did not name the paging limit that caused it; got %v", v.Reasons)
	}
	if strings.Contains(joined, worktreeReason) {
		t.Errorf("the swap limit's refusal is contaminated by the worktree ceiling; the two are not independent: %v", v.Reasons)
	}
}

// Arms the worktree ceiling alone. Kills the "ceiling never refuses" mutant --
// the state in which the limit is defined, documented, and calls nothing.
func TestWorktreeCeilingAloneRefusesWithItsOwnReason(t *testing.T) {
	n, err := worktreeCount()
	if err != nil {
		t.Skipf("cannot count worktrees on this host (%v); Check turns that error into a refusal, which TestNeitherNewLimitRefusesWhenNotArmed would not mask", err)
	}
	if n < 1 {
		t.Fatalf("git worktree list reported %d worktrees while running inside a checkout; the instrument is wrong", n)
	}

	lim := neutralised()
	lim.MaxWorktrees = 0 // the repo running this test has at least one
	v := Check(emptyLedger(t), lim)

	if v.OK {
		t.Fatalf("Check() allowed dispatch with %d worktrees against a ceiling of 0; the ceiling is not wired. reading=%+v", n, v.Reading)
	}
	joined := strings.Join(v.Reasons, " ")
	if !strings.Contains(joined, worktreeReason) {
		t.Errorf("refusal did not name the worktree ceiling that caused it; got %v", v.Reasons)
	}
	if strings.Contains(joined, swapReason) {
		t.Errorf("the ceiling's refusal is contaminated by the paging limit; the two are not independent: %v", v.Reasons)
	}
	if v.Reading.Worktrees != n {
		t.Errorf("Check reported %d worktrees, worktreeCount() reports %d", v.Reading.Worktrees, n)
	}
}

// Neither limit fires when neither is armed -- the other direction, and the
// thing that stops the two tests above from being satisfied by a gate that
// refuses everything.
func TestNeitherNewLimitRefusesWhenNotArmed(t *testing.T) {
	v := Check(emptyLedger(t), neutralised())
	joined := strings.Join(v.Reasons, " ")
	if strings.Contains(joined, swapReason) {
		t.Errorf("the paging limit refused at a threshold of 1e9 swapouts: %v", v.Reasons)
	}
	if strings.Contains(joined, worktreeReason) {
		t.Errorf("the worktree ceiling refused at a ceiling of 1e9: %v", v.Reasons)
	}
}

// hostIsMeasurable's swap-shaped sibling: vm_stat carries the Swapouts line on
// macOS and is absent on the Linux runner.
func hostCanMeasureSwap(t *testing.T) bool {
	t.Helper()
	_, err := swapouts()
	return err == nil
}

// swapoutRate's own arithmetic, driven through the injected sampler. This is
// what kills the "gauge dead, always returns 0" mutant: no host state can make
// a rising counter read as quiet.
func TestSwapoutRateIsTheDeltaBetweenSamples(t *testing.T) {
	for _, tc := range []struct {
		name    string
		samples []float64
		want    float64
	}{
		{"quiet host", []float64{49904, 49904}, 0},
		{"actively paging", []float64{49904, 59444}, 9540}, // the deltas measured during #999's review
		{"swap disabled, counter pinned at zero", []float64{0, 0}, 0},
		{"single page out still counts", []float64{7, 8}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := swapoutRate(0, sampler(tc.samples...))
			if err != nil {
				t.Fatalf("swapoutRate: %v", err)
			}
			if got != tc.want {
				t.Fatalf("rate = %v, want %v -- the delta is the whole gauge", got, tc.want)
			}
		})
	}
}

// A gauge that always reads 0 passes every threshold. Pin the direction
// separately from the arithmetic so a constant-returning mutant cannot hide.
func TestSwapoutRateRefusesAtTheDefaultLimitWhenPaging(t *testing.T) {
	lim := Default()
	quiet, err := swapoutRate(0, sampler(49904, 49904))
	if err != nil {
		t.Fatal(err)
	}
	if quiet >= lim.MaxSwapoutsPerSample {
		t.Errorf("a host that paged nothing read %v and would be refused; that is the permanent-refusal bug", quiet)
	}
	paging, err := swapoutRate(0, sampler(49904, 49914))
	if err != nil {
		t.Fatal(err)
	}
	if paging < lim.MaxSwapoutsPerSample {
		t.Errorf("a host that paged out 10 pages read %v and would be allowed; that is the 2026-09-03 outage", paging)
	}
}

// The counter-reset branch, which had no coverage and used to return the
// PASSING value on an anomaly.
func TestSwapoutCounterGoingBackwardsRefuses(t *testing.T) {
	rate, err := swapoutRate(0, sampler(59444, 12))
	if err == nil {
		t.Fatalf("a counter that went backwards returned rate %v and no error; an untrustworthy reading must fail closed", rate)
	}
	if rate != 0 {
		t.Errorf("a failed reading must not carry a number; got %v", rate)
	}
}

// A sampler failure must propagate, not degrade into a quiet-host 0.
func TestSwapoutSamplerFailureRefuses(t *testing.T) {
	for _, at := range []int{0, 1} {
		calls := 0
		_, err := swapoutRate(0, func() (float64, error) {
			defer func() { calls++ }()
			if calls == at {
				return 0, errors.New("vm_stat unavailable")
			}
			return 100, nil
		})
		if err == nil {
			t.Errorf("a sampler that failed on read %d produced no error; Check would read that as a quiet host", at)
		}
	}
}

// sampler returns readings in order, then fails rather than silently repeating
// the last one -- an over-called sampler is a test bug, not a passing reading.
func sampler(readings ...float64) func() (float64, error) {
	i := 0
	return func() (float64, error) {
		if i >= len(readings) {
			return 0, fmt.Errorf("sampler called %d times, only %d readings supplied", i+1, len(readings))
		}
		v := readings[i]
		i++
		return v, nil
	}
}

func TestWorktreeCountIsCountedNotEstimated(t *testing.T) {
	// Both directions: a small repo must not trip the ceiling, and the runaway
	// state observed on 2026-09-03 must.
	one := []byte("worktree /repo\nHEAD abc\nbranch refs/heads/main\n")
	if got := countWorktreeLines(one); got != 1 {
		t.Fatalf("single worktree counted as %d", got)
	}
	var many []byte
	for i := 0; i < 176; i++ {
		many = append(many, []byte("worktree /tmp/wt\nHEAD abc\n\n")...)
	}
	if got := countWorktreeLines(many); got != 176 {
		t.Fatalf("the outage state counted as %d, want 176", got)
	}
	if !(176 > Default().MaxWorktrees) {
		t.Fatal("176 worktrees must exceed the default ceiling -- that is the state that went unnoticed")
	}
	if 1 > Default().MaxWorktrees {
		t.Fatal("a healthy repo must pass; a ceiling that always refuses is the bug being fixed")
	}
}
