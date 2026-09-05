package provenance

import "testing"

// TestSourceStateZeroValueIsUnknownNotEmpty guards the doc comment's own
// claim: a zero-initialized SourceHealth must read as SourceStateUnknown,
// never SourceStateEmpty -- the same "absence is typed, never a bare zero"
// discipline this repo already applies to cost.Figure.Known.
func TestSourceStateZeroValueIsUnknownNotEmpty(t *testing.T) {
	var h SourceHealth
	if h.State != SourceStateUnknown {
		t.Fatalf("zero-value SourceHealth.State = %v, want SourceStateUnknown", h.State)
	}
	if h.State == SourceStateEmpty {
		t.Fatal("zero-value SourceHealth.State must never equal SourceStateEmpty")
	}
}

func TestSourceStateStringCoversEveryState(t *testing.T) {
	cases := map[SourceState]string{
		SourceStateUnknown:    "unknown",
		SourceStateMissing:    "missing",
		SourceStateUnreadable: "unreadable",
		SourceStateEmpty:      "empty",
		SourceStatePopulated:  "populated",
	}
	for state, want := range cases {
		if got := state.String(); got != want {
			t.Errorf("SourceState(%d).String() = %q, want %q", state, got, want)
		}
	}
}

// TestFreshnessZeroValueIsUnknown mirrors the SourceState test for Freshness:
// a zero-value Freshness must never be mistaken for "captured just now".
func TestFreshnessZeroValueIsUnknown(t *testing.T) {
	var f Freshness
	if f.Known {
		t.Fatal("zero-value Freshness.Known = true, want false")
	}
}
