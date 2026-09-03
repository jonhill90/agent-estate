package tick

import "testing"

func i64(v int64) *int64     { return &v }
func f64(v float64) *float64 { return &v }

// The four coherent shapes a writer of this package produces, and the
// incoherent ones it does not but a log read off disk may still carry. The
// incoherent rows are the point: agent-estate#997's reviewer produced the
// first of them with one hand-written JSON line, and the printer that
// trusted the pairing panicked on it.
func TestReadSpendClassifiesEveryCombination(t *testing.T) {
	cases := []struct {
		name          string
		turns         *int64
		usd           *float64
		turnsWithCost *int64
		want          SpendKind
	}{
		// Coherent: what this package's own write path produces.
		{"no window at all (first tick, or predates #982)", nil, nil, nil, SpendNoWindow},
		{"real window, nothing finished", i64(0), nil, nil, SpendNoTurns},
		{"turns finished, none reported a cost", i64(2), nil, nil, SpendNoneReported},
		{"spend and its own count, both present", i64(2), f64(2.5), i64(1), SpendReported},
		{"every turn reported a cost", i64(2), f64(4.0), i64(2), SpendReported},

		// Incoherent: only reachable by reading a log this code did not write.
		{"dollar figure with no count -- the #997 crash", i64(2), f64(2.5), nil, SpendUnreadable},
		{"count with no dollar figure", i64(2), nil, i64(1), SpendUnreadable},
		{"spend figures with no window to bound them", nil, f64(2.5), i64(1), SpendUnreadable},
		{"dollar figure with no turn count at all", nil, f64(2.5), nil, SpendUnreadable},
		{"count with no turn count at all", nil, nil, i64(1), SpendUnreadable},
		{"a dollar figure produced by zero turns", i64(2), f64(2.5), i64(0), SpendUnreadable},
		{"more paying turns than turns", i64(1), f64(2.5), i64(2), SpendUnreadable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ReadSpend(c.turns, c.usd, c.turnsWithCost)
			if got.Kind != c.want {
				t.Fatalf("ReadSpend = kind %v, want %v (reading: %+v)", got.Kind, c.want, got)
			}
			if c.want == SpendUnreadable && got.Why == "" {
				t.Fatal("an unreadable entry must say which pairing broke -- a bare refusal is not a report")
			}
			if c.want != SpendUnreadable && got.Why != "" {
				t.Fatalf("a readable entry must not carry a reason: %q", got.Why)
			}
		})
	}
}

// The value most worth protecting: an unreadable entry must never hand a
// caller a number it could print as though it had been measured.
func TestReadSpendInventsNoNumberForABrokenPair(t *testing.T) {
	got := ReadSpend(i64(2), f64(2.5), nil)
	if got.Kind != SpendUnreadable {
		t.Fatalf("a dollar figure with no count must be unreadable, got %v", got.Kind)
	}
	if got.TurnsWithCost != 0 || got.USD != 0 || got.Turns != 0 {
		t.Fatalf("an unreadable reading must carry no printable figures, got %+v", got)
	}
}

// Old entries -- written before any of these fields existed -- must keep
// reading exactly as they did: no window, no crash, no reason string.
func TestReadSpendOldEntryStillReadsAsNoWindow(t *testing.T) {
	got := ReadSpend(nil, nil, nil)
	if got.Kind != SpendNoWindow {
		t.Fatalf("an entry predating agent-estate#982 must read as SpendNoWindow, got %v", got.Kind)
	}
}

// Both wrappers must agree with the free function -- LastRecorded's is the
// one that matters, since its values came off disk.
func TestSpendMethodsMatchReadSpend(t *testing.T) {
	turns, usd, twc := i64(2), f64(2.5), i64(1)
	e := Entry{ObservedTurns: turns, ObservedSpendUSD: usd, ObservedTurnsWithCost: twc}
	l := LastRecorded{ObservedTurns: turns, ObservedSpendUSD: usd, ObservedTurnsWithCost: twc}
	want := ReadSpend(turns, usd, twc)
	if e.Spend() != want {
		t.Fatalf("Entry.Spend() = %+v, want %+v", e.Spend(), want)
	}
	if l.Spend() != want {
		t.Fatalf("LastRecorded.Spend() = %+v, want %+v", l.Spend(), want)
	}

	broken := LastRecorded{ObservedTurns: turns, ObservedSpendUSD: usd}
	if broken.Spend().Kind != SpendUnreadable {
		t.Fatal("LastRecorded.Spend() must classify a broken pair read off disk, not trust it")
	}
}
