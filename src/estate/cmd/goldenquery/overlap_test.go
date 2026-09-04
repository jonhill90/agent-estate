package main

import (
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

func TestOverlapFractionCountsVerbatimContentWordHits(t *testing.T) {
	frac, ok := overlapFraction(
		"Which starred dotfiles repo sets sensible hacker defaults for macOS?",
		"mathiasbynens/dotfiles -- .files, including ~/.macos -- sensible hacker defaults for macOS.",
	)
	if !ok {
		t.Fatal("overlapFraction ok = false, want true (targetText is non-empty)")
	}
	// content words in the question: starred, dotfiles, repo, sets,
	// sensible, hacker, defaults, macos (8) -- dotfiles, sensible, hacker,
	// defaults, macos (5) appear verbatim in the target text.
	if got, want := frac, 5.0/8.0; got != want {
		t.Errorf("overlapFraction = %.4f, want %.4f", got, want)
	}
}

func TestOverlapFractionNotOKWithoutTargetText(t *testing.T) {
	if _, ok := overlapFraction("any question", ""); ok {
		t.Error("overlapFraction ok = true with empty targetText, want false -- agent-estate#1115 requires this reported as not-measured, never a false 0%")
	}
}

func TestOverlapFractionZeroWhenNoWordsShared(t *testing.T) {
	frac, ok := overlapFraction("completely unrelated wording", "some other target entirely")
	if !ok {
		t.Fatal("overlapFraction ok = false, want true")
	}
	if frac != 0 {
		t.Errorf("overlapFraction = %.4f, want 0", frac)
	}
}

func TestMeanOverlapSkipsCasesWithoutTargetText(t *testing.T) {
	cases := []goldenset.Case{
		{ID: "a", Question: "aws local test", TargetText: "aws local test cloud"},
		{ID: "b", Question: "no target text here"},
	}
	mean, measured, total := meanOverlap(cases)
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
	if measured != 1 {
		t.Fatalf("measured = %d, want 1 -- case b has no TargetText and must not count", measured)
	}
	if mean != 1.0 {
		t.Fatalf("mean = %.4f, want 1.0 (case a's every content word appears in its own target)", mean)
	}
}

func TestMeanOverlapReportsZeroMeasuredWhenNoCaseHasTargetText(t *testing.T) {
	cases := []goldenset.Case{
		{ID: "a", Question: "no target text"},
		{ID: "b", Question: "still no target text"},
	}
	mean, measured, total := meanOverlap(cases)
	if measured != 0 {
		t.Fatalf("measured = %d, want 0", measured)
	}
	if mean != 0 {
		t.Fatalf("mean = %.4f, want 0 when nothing was measured", mean)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
}
