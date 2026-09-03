package pressure

import (
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
	// The bug this replaced: a HIGH-WATER MARK reads as pressure forever. The
	// host an hour after the outage had 619MB swap used, 52 percent free, and ZERO
	// new swapouts -- healthy. A rate of 0 must pass.
	if 0 >= Default().MaxSwapoutsPerSample {
		t.Fatal("a host that is not paging must pass; gating on cumulative swap would refuse forever")
	}
	// And real paging must refuse.
	if !(5 >= Default().MaxSwapoutsPerSample) {
		t.Fatal("active paging must refuse")
	}
}

func TestSwapoutsUnreadableRefusesRatherThanPasses(t *testing.T) {
	if _, err := parseSwapouts([]byte("no such line")); err == nil {
		t.Fatal("an unreadable paging counter must error so Check() fails closed")
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
