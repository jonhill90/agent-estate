package budget

import (
	"testing"
	"time"
)

func fixed(t0 time.Time) func() time.Time { return func() time.Time { return t0 } }

func TestBlocksAtHardCap(t *testing.T) {
	tr := New(Policy{LimitUSD: 10, WarnPercent: 80, Window: time.Hour})
	t0 := time.Unix(1_700_000_000, 0)
	tr.now = fixed(t0)

	tr.Record(5)
	if v := tr.Check(); v.Decision != Allow {
		t.Fatalf("at $5 of $10 want allow, got %v", v)
	}
	tr.Record(3) // 8 => 80% soft
	if v := tr.Check(); v.Decision != Warn {
		t.Fatalf("at $8 of $10 want warn, got %v", v)
	}
	tr.Record(2) // 10 => hard
	v := tr.Check()
	if v.Decision != Block {
		t.Fatalf("at $10 of $10 want BLOCK, got %v", v)
	}
	if v.SpentUSD != 10 {
		t.Fatalf("want spent 10, got %v", v.SpentUSD)
	}
}

// Spend must age out of the window, or the cap becomes permanent.
func TestSpendAgesOutOfWindow(t *testing.T) {
	tr := New(Policy{LimitUSD: 10, Window: time.Hour})
	t0 := time.Unix(1_700_000_000, 0)
	tr.now = fixed(t0)
	tr.Record(20)
	if tr.Check().Decision != Block {
		t.Fatal("want block immediately after overspend")
	}
	tr.now = fixed(t0.Add(2 * time.Hour))
	if v := tr.Check(); v.Decision != Allow {
		t.Fatalf("after the window want allow, got %v", v)
	}
}

// A zero limit disables the cap -- but only as an explicit choice.
func TestZeroLimitDisables(t *testing.T) {
	tr := New(Policy{LimitUSD: 0})
	tr.Record(1e6)
	if v := tr.Check(); v.Decision != Allow {
		t.Fatalf("zero limit must allow, got %v", v)
	}
}

// Truth is re-derived by summation, never from a cached counter.
func TestSumIsRederived(t *testing.T) {
	tr := New(Policy{LimitUSD: 100, Window: time.Hour})
	t0 := time.Unix(1_700_000_000, 0)
	tr.now = fixed(t0)
	for i := 0; i < 10; i++ {
		tr.Record(1)
	}
	if got := tr.SpentUSD(); got != 10 {
		t.Fatalf("want 10, got %v", got)
	}
}
