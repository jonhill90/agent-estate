package tick

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// agent-estate#1358: Check's verdict is a pure function of the last Window
// entries with no reference to their age, so once a log stops being
// appended to, the verdict freezes at whatever it last reported -- a dead
// loop and a healthy one produce identical output. CheckWithStaleness gates
// on the newest entry's own age, derived from the log's own recorded
// gap_seconds history, before any window logic runs.
//
// THE REPRODUCTION. This is the exact shape #1358 measured live: a log
// whose newest entries are old enough that nothing has appended to it in a
// long time, reported through a `now` this test controls (never time.Now()
// -- these tests would otherwise race the real clock and rot the moment
// they were written, the same problem the fixed 2026-09-02 dates elsewhere
// in this package already have relative to whenever they are actually
// run). Before CheckWithStaleness existed, the only entry point
// (CheckWithEscalation) had no way to see this at all -- see
// TestOldStalledWindowReportsMovingThroughTheOldEntryPointUnchanged below,
// which pins that CheckWithEscalation itself is deliberately UNCHANGED by
// this fix (so none of the dozens of existing tests in tick_test.go, all
// keyed to fixed 2026-09-02 dates, silently start failing against a moving
// wall clock).
func writeAt(t *testing.T, entries ...Entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tick-log.jsonl")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := Record(p, e, nil); err != nil {
			t.Fatalf("writeAt: %v", err)
		}
	}
	return p
}

func gapPtr(seconds int64) *int64 { return &seconds }

// TestCheckWithStalenessReportsStaleOnAnOldWindowThatWouldOtherwiseReadMoving
// is the direct reproduction of the issue's own measurement: a log with a
// real, tight recorded cadence (so a threshold IS derivable) whose newest
// entry is now old enough, relative to that cadence, that the verdict must
// become Stale -- neither "moving" nor "stalled". Before CheckWithStaleness
// existed this could not be expressed at all; this is the test that could
// not pass before this change.
func TestCheckWithStalenessReportsStaleOnAnOldWindowThatWouldOtherwiseReadMoving(t *testing.T) {
	base := time.Date(2026, 9, 2, 23, 0, 0, 0, time.UTC)
	p := writeAt(t,
		Entry{At: base, PhaseItem: "phase-0", SrcHead: "aaa"},
		Entry{At: base.Add(3 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa", GapSeconds: gapPtr(180), Artifact: "docs/thing.md"},
		Entry{At: base.Add(6 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa", GapSeconds: gapPtr(180)},
	)
	// Sanity: without staleness, this window reads "moving" -- the newest
	// entry has an artifact one tick back, matching the exact shape #1358's
	// own measured `tick check` output described ("the last 3 ticks
	// include one that produced an artifact"). Confirms the fixture
	// reproduces the issue's own starting condition before asserting on
	// the new behaviour.
	if v, err := CheckWithEscalation(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	} else if v.Stalled {
		t.Fatalf("fixture must read moving under the unmodified check; got stalled: %s", v.Reason)
	}

	// Derived threshold is 2x the largest recorded gap (180s) = 360s. 8 days
	// later is many orders of magnitude past that, the same shape as the
	// issue's own 188-hour measurement against a ~3-minute loop.
	now := base.Add(6*time.Minute + 8*24*time.Hour)
	v, err := CheckWithStaleness(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stale {
		t.Fatalf("an 8-day-old window against a ~3-minute derived cadence must report Stale; got %+v", v)
	}
	if v.Stalled {
		t.Fatal("Stale and Stalled must never both be true -- a stale window is not evidence of a stall either")
	}
	if v.Reason == "" {
		t.Error("a Stale verdict must say why; reason was empty")
	}
}

// TestCheckWithStalenessAlsoSuppressesAGenuineStallVerdict proves the other
// direction named explicitly in the issue: staleness must pre-empt BOTH
// "moving" and "stalled". A window that would read Stalled under the
// unmodified check must read Stale instead once it is old enough -- the
// point is not "stale windows only hide false positives for moving", it is
// that NEITHER claim survives an old enough window.
func TestCheckWithStalenessAlsoSuppressesAGenuineStallVerdict(t *testing.T) {
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	p := writeAt(t,
		Entry{At: base, PhaseItem: "phase-0", SrcHead: "aaa"},
		Entry{At: base.Add(3 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa", GapSeconds: gapPtr(180)},
		Entry{At: base.Add(6 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa", GapSeconds: gapPtr(180)},
	)
	if v, err := CheckWithEscalation(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	} else if !v.Stalled {
		t.Fatalf("fixture must read stalled under the unmodified check; got %+v", v)
	}

	now := base.Add(6*time.Minute + 8*24*time.Hour)
	v, err := CheckWithStaleness(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Stalled {
		t.Fatal("an old window must not assert Stalled -- Stale must pre-empt it")
	}
	if !v.Stale {
		t.Fatal("an old window that would otherwise read stalled must read Stale")
	}
}

// TestCheckWithStalenessWithinThresholdIsUnaffected is the negative case:
// a window whose newest entry is recent, relative to its own derived
// cadence, must behave exactly as CheckWithEscalation already does --
// Stale must never fire on a genuinely live log.
func TestCheckWithStalenessWithinThresholdIsUnaffected(t *testing.T) {
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	p := writeAt(t,
		Entry{At: base, PhaseItem: "phase-0", SrcHead: "aaa"},
		Entry{At: base.Add(3 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa", GapSeconds: gapPtr(180), Artifact: "docs/thing.md"},
		Entry{At: base.Add(6 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa", GapSeconds: gapPtr(180)},
	)
	// One minute after the newest entry -- well inside a ~3-minute cadence.
	now := base.Add(7 * time.Minute)
	v, err := CheckWithStaleness(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Stale {
		t.Fatalf("a window one minute stale against a 3-minute cadence must not report Stale; got %+v", v)
	}
	if v.Stalled {
		t.Fatalf("this window has a recent artifact; must not be stalled either: %s", v.Reason)
	}
}

// TestCheckWithStalenessUsesTheLargestGapNotTheMedian is the case the real
// docs/tick-log.jsonl history forced: a handful of tight ~3-minute ticks
// plus one much longer, already-recovered-from gap (the loop deliberately
// widened its own interval and then resumed, per
// docs/canonical/director-loop.md's "blocked on operator review... widen"
// state). A median-based threshold would flag that same-shaped widening as
// stale every time it recurs; the largest-gap-ever-recovered-from threshold
// must not, because the log has already proven that gap survivable.
func TestCheckWithStalenessUsesTheLargestGapNotTheMedian(t *testing.T) {
	base := time.Date(2026, 9, 2, 23, 0, 0, 0, time.UTC)
	p := writeAt(t,
		Entry{At: base, PhaseItem: "phase-0", SrcHead: "aaa"},
		Entry{At: base.Add(3 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa", GapSeconds: gapPtr(180)},
		Entry{At: base.Add(6 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa", GapSeconds: gapPtr(180)},
		// A real, already-survived widening: ~20 hours, matching the scale
		// of docs/tick-log.jsonl's own recovered 71783s entry.
		Entry{At: base.Add(6*time.Minute + 20*time.Hour), PhaseItem: "phase-1", SrcHead: "bbb", GapSeconds: gapPtr(20 * 3600)},
	)
	// A median of the three recorded gaps (180, 180, 72000) would be 180s,
	// whose margin (2x = 360s) is far shorter than the 20-hour gap already
	// on record -- if the threshold were median-based, this "now" (right
	// at the newest entry, zero additional elapsed time) would still need
	// to not be stale, which is trivially true and does not distinguish
	// the two derivations. The real test is the NEXT one: something
	// shaped like ANOTHER 20-hour-plus gap must not itself be flagged,
	// because the largest-gap threshold (2x 72000s = 144000s, 40h) covers
	// it; a median-based threshold (2x 180s = 360s) would not.
	now := base.Add(6*time.Minute + 20*time.Hour + 30*time.Hour)
	v, err := CheckWithStaleness(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Stale {
		t.Fatalf("a 30-hour-since-last-entry gap must not be Stale when the log has already recorded and recovered from a 20-hour gap (threshold is 2x40h boundary of 40h... see comment): got %+v", v)
	}

	// Now push well past the largest-gap-derived threshold (2 * 72000s =
	// 144000s = 40h) -- this must report Stale.
	farNow := base.Add(6*time.Minute + 20*time.Hour + 41*time.Hour)
	v2, err := CheckWithStaleness(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil, farNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v2.Stale {
		t.Fatalf("41 hours since the newest entry, past 2x the largest recorded gap (40h), must report Stale; got %+v", v2)
	}
}

// TestCheckWithStalenessCannotDeriveAThresholdWithNoRecordedGaps is the
// bootstrapping case: a log whose entries predate agent-estate#982 (no
// gap_seconds ever recorded) has no historical cadence to derive a
// threshold from at all. Staleness must not be asserted from data that
// does not exist -- the same "not enough history" shape checkImpl's own
// len(entries) < Window branch already takes. This falls through to
// CheckWithEscalation unchanged, however old the entries are.
func TestCheckWithStalenessCannotDeriveAThresholdWithNoRecordedGaps(t *testing.T) {
	base := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	p := writeAt(t,
		Entry{At: base, PhaseItem: "phase-0", SrcHead: "aaa"},
		Entry{At: base.Add(3 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa"},
		Entry{At: base.Add(6 * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa"},
	)
	now := base.Add(8 * 24 * time.Hour)
	v, err := CheckWithStaleness(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Stale {
		t.Fatalf("no recorded gap_seconds anywhere in the log means no derivable cadence -- must not assert Stale: %+v", v)
	}
	// Falls through to the ordinary, unchanged verdict.
	if !v.Stalled {
		t.Fatalf("with no staleness signal available, this window must still be judged on its own merits (three identical empty ticks): got %+v", v)
	}
}

// TestCheckWithStalenessMissingLogIsNotStale pins the same "a loop that has
// not ticked is not a stall" shape checkImpl already takes: no log at all
// has no history to be stale, and CheckWithStaleness must fall through
// rather than inventing a claim from nothing.
func TestCheckWithStalenessMissingLogIsNotStale(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.jsonl")
	v, err := CheckWithStaleness(missing, filepath.Join(t.TempDir(), "esc.jsonl"), nil, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Stale {
		t.Fatal("a missing log has no history at all -- must not report Stale")
	}
	if v.Stalled {
		t.Fatal("a missing log is a loop that has not ticked, not a stall")
	}
}

// TestCheckWithStalenessDoesNotErrorOnAnUnparsableNewestTimestamp is the
// exact regression this fix's own development caught: checkImpl's window
// logic has never required `at` to be present or parseable (its `parsed`
// struct doesn't even read the field), so a garbage timestamp on the
// newest entry must not turn CheckWithStaleness into a harder failure than
// the pre-existing check ever had -- src/estate's own
// TestTickCheckNewestEntryAgeHonestWhenUnparsable depends on `tick check`
// still producing a verdict (with its own honest "age: unknown"
// disclosure) in exactly this shape.
func TestCheckWithStalenessDoesNotErrorOnAnUnparsableNewestTimestamp(t *testing.T) {
	p := write(t, `{"at":"not-a-timestamp","phase_item":"phase-0","src_head":"deadbeef","artifact":null}`)
	v, err := CheckWithStaleness(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil, time.Now())
	if err != nil {
		t.Fatalf("an unparsable newest timestamp must not error out of CheckWithStaleness: %v", err)
	}
	if v.Stale {
		t.Fatal("staleness cannot be assessed without a parseable newest timestamp -- must not report Stale")
	}
	// One entry, fewer than Window -- checkImpl's own "not enough to
	// establish a stall" branch, reached exactly as it would be without
	// this fix.
	if v.Stalled {
		t.Fatalf("one entry cannot establish a stall: %+v", v)
	}
}

// TestOldStalledWindowReportsMovingThroughTheOldEntryPointUnchanged pins
// that this fix deliberately leaves Check/CheckWithResolver/
// CheckWithEscalation untouched: they carry no time dependency at all, so
// the dozens of existing tests in tick_test.go and tick_escalation_test.go
// keyed to fixed 2026-09-02 dates keep passing regardless of when they
// actually run, rather than silently breaking the moment those dates age
// past whatever threshold a wall-clock-aware Check would have derived.
// Staleness is reached ONLY through the new, explicitly-clocked
// CheckWithStaleness -- this test is the guarantee that choice held.
func TestOldStalledWindowReportsMovingThroughTheOldEntryPointUnchanged(t *testing.T) {
	p := write(t, stalledA, stalledB,
		`{"at":"2026-09-02T10:06:00Z","phase_item":"phase-0","src_head":"aaa","artifact":"docs/thing.md"}`)
	v, err := CheckWithEscalation(p, filepath.Join(t.TempDir(), "esc.jsonl"), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Stale {
		t.Fatal("CheckWithEscalation must never set Stale -- that field is reachable only through CheckWithStaleness")
	}
	if v.Stalled {
		t.Fatalf("unaffected by this change: an artifact on the newest tick still clears the stall: %s", v.Reason)
	}
}
