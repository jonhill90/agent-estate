package tick

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tick-log.jsonl")
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The three lines the stop condition exists to catch: same phase_item, same
// src_head, no artifact, three times running.
const (
	stalledA = `{"at":"2026-09-02T10:00:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`
	stalledB = `{"at":"2026-09-02T10:03:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`
	stalledC = `{"at":"2026-09-02T10:06:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`
)

func TestCheckStalledOnThreeIdenticalEmptyTicks(t *testing.T) {
	v, err := Check(write(t, stalledA, stalledB, stalledC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal("three identical artifact-less ticks must report stalled; got not stalled")
	}
	if v.Reason == "" {
		t.Error("a stall must say why; reason was empty")
	}
}

// The other direction: an artifact -- and ONLY an artifact -- clears a stall.
//
// This test previously also asserted that a changed src_head or a changed
// phase_item cleared it, matching the brief's literal wording. An independent
// review showed both of those are escape hatches for exactly the loop the
// stop condition exists to catch, so those two cases now live in
// TestStalledLoopThatAlternatesPhaseItemsIsStillCaught and
// TestUnrelatedSrcCommitDoesNotClearAStall, asserting the OPPOSITE. Recorded
// here rather than quietly deleted: the guard was strengthened, and the test
// that encoded its weakness had to change with it.
func TestOnlyAnArtifactClearsAStall(t *testing.T) {
	v, err := Check(write(t, stalledA, stalledB,
		`{"at":"2026-09-02T10:06:00Z","phase_item":"phase-0","src_head":"aaa","artifact":"docs/thing.md"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v.Stalled {
		t.Fatalf("an artifact on the newest tick must clear the stall: %s", v.Reason)
	}
}

// An empty artifact string is absence spelled a second way. If it did not
// count, a tick could dodge the stop condition by recording "".
func TestCheckTreatsEmptyArtifactStringAsAbsent(t *testing.T) {
	third := `{"at":"2026-09-02T10:06:00Z","phase_item":"phase-0","src_head":"aaa","artifact":""}`
	v, err := Check(write(t, stalledA, stalledB, third))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal(`artifact:"" is absence; must still report stalled`)
	}
}

func TestCheckOnlyReadsTheLastThreeEntries(t *testing.T) {
	// Older healthy history must not rescue a loop that is stalled now.
	old := `{"at":"2026-09-01T10:00:00Z","phase_item":"phase-0","src_head":"zzz","artifact":"shipped.md"}`
	v, err := Check(write(t, old, old, stalledA, stalledB, stalledC))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !v.Stalled {
		t.Fatal("history before the last three must not clear a current stall")
	}
}

func TestCheckFewerThanThreeEntriesIsNotAStall(t *testing.T) {
	for _, n := range [][]string{{}, {stalledA}, {stalledA, stalledB}} {
		v, err := Check(write(t, n...))
		if err != nil {
			t.Fatalf("unexpected error with %d entries: %v", len(n), err)
		}
		if v.Stalled {
			t.Fatalf("%d entries cannot establish a stall", len(n))
		}
	}
}

// A log that has never been written is a loop that has not ticked, not a
// stalled one.
func TestCheckMissingFileIsNotAStall(t *testing.T) {
	v, err := Check(filepath.Join(t.TempDir(), "absent.jsonl"))
	if err != nil {
		t.Fatalf("a missing log must not be an error: %v", err)
	}
	if v.Stalled {
		t.Fatal("a missing log is not a stall")
	}
}

// Fails closed: a log that exists but cannot be parsed is "could not
// measure", which must never read as clean.
func TestCheckMalformedLogIsAnErrorNotClean(t *testing.T) {
	if _, err := Check(write(t, stalledA, "{not json", stalledC)); err == nil {
		t.Fatal("a corrupt tick log must be an error, not a clean result")
	}
}

func TestRecordAppendsOneParseableLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tick-log.jsonl")
	at := time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)

	if err := Record(p, Entry{At: at, PhaseItem: "phase-0", SrcHead: "aaa"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := Record(p, Entry{At: at, PhaseItem: "phase-0", SrcHead: "aaa", Artifact: "docs/x.md"}, nil); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %q", len(lines), string(b))
	}
	// An absent artifact must serialise as null, not "" -- the brief's own
	// stop condition is written against `artifact: null`.
	if !strings.Contains(lines[0], `"artifact":null`) {
		t.Errorf("absent artifact must serialise as null; got %s", lines[0])
	}
	if !strings.Contains(lines[1], `"artifact":"docs/x.md"`) {
		t.Errorf("present artifact must serialise as itself; got %s", lines[1])
	}
	if !strings.Contains(lines[0], `"at":"2026-09-02T11:00:00Z"`) {
		t.Errorf("at must be iso8601 utc; got %s", lines[0])
	}
}

// Record and Check are one mechanism; what one writes the other must read.
func TestRecordThenCheckRoundTrips(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tick-log.jsonl")
	at := time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if err := Record(p, Entry{At: at.Add(time.Duration(i) * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	v, err := Check(p)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Stalled {
		t.Fatal("three artifact-less Records must be readable by Check as a stall")
	}
}

// Both of these come from an independent review of the commit that added
// this package. Each is a loop that is, by the brief's own definition,
// "running and producing nothing" -- and each escaped the original rule.

// A loop that bounces between phase items forever, producing nothing, never
// has three consecutive entries sharing phase_item. Under the original rule
// it was never caught.
func TestStalledLoopThatAlternatesPhaseItemsIsStillCaught(t *testing.T) {
	v, err := Check(write(t,
		`{"at":"2026-09-02T10:00:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-02T10:03:00Z","phase_item":"phase-1","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-02T10:06:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Stalled {
		t.Fatalf("a loop alternating phase items and producing nothing is stalled: %s", v.Reason)
	}
}

// An unrelated commit anywhere under src/ moved src_head between ticks. That
// says nothing about whether THIS phase item advanced, and under the original
// rule it cleared the stall.
func TestUnrelatedSrcCommitDoesNotClearAStall(t *testing.T) {
	v, err := Check(write(t,
		`{"at":"2026-09-02T10:00:00Z","phase_item":"phase-2","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-02T10:03:00Z","phase_item":"phase-2","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-02T10:06:00Z","phase_item":"phase-2","src_head":"bbb","artifact":null}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Stalled {
		t.Fatalf("src head moving is not evidence this phase item advanced: %s", v.Reason)
	}
}

// The other direction still has to hold: a tick that produced something real
// clears the stall, whatever the phase item and src head say.
func TestOneRealArtifactInTheWindowStillClears(t *testing.T) {
	v, err := Check(write(t,
		`{"at":"2026-09-02T10:00:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
		`{"at":"2026-09-02T10:03:00Z","phase_item":"phase-0","src_head":"aaa","artifact":"docs/real.md"}`,
		`{"at":"2026-09-02T10:06:00Z","phase_item":"phase-0","src_head":"aaa","artifact":null}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	if v.Stalled {
		t.Fatalf("a real artifact in the window means the loop is producing: %s", v.Reason)
	}
}

// From an independent review of the commit that added this package. The
// stop condition claimed "only an artifact clears a stall". It was false:
// ANY non-empty string cleared it, so a spinning loop dodges forever by
// writing junk -- and the agent writing that string is the same one the
// guard is meant to catch.

func TestPlaceholderArtifactsAreAbsence(t *testing.T) {
	// Each of these is absence spelled as presence. The literal "null" is
	// the sharpest case: the record format uses null for absent, so a tick
	// writing the STRING "null" is claiming output it does not have.
	for _, junk := range []string{"null", "NULL", "none", "None", "n/a", "N/A", "-", "--", "tbd", "TBD", "nothing", "pending"} {
		p := write(t,
			`{"at":"2026-09-02T10:00:00Z","phase_item":"p","src_head":"a","artifact":null}`,
			`{"at":"2026-09-02T10:03:00Z","phase_item":"p","src_head":"a","artifact":null}`,
			`{"at":"2026-09-02T10:06:00Z","phase_item":"p","src_head":"a","artifact":"`+junk+`"}`)
		v, err := Check(p)
		if err != nil {
			t.Fatal(err)
		}
		if !v.Stalled {
			t.Errorf("artifact %q is absence spelled as presence; must not clear a stall", junk)
		}
	}
}

// Repeating the SAME artifact is not new output. A loop that keeps pointing
// at something it produced three ticks ago is producing nothing now.
func TestRepeatingOneArtifactIsNotNewOutput(t *testing.T) {
	v, err := Check(write(t,
		`{"at":"2026-09-02T10:00:00Z","phase_item":"p","src_head":"a","artifact":"docs/thing.md"}`,
		`{"at":"2026-09-02T10:03:00Z","phase_item":"p","src_head":"a","artifact":"docs/thing.md"}`,
		`{"at":"2026-09-02T10:06:00Z","phase_item":"p","src_head":"a","artifact":"docs/thing.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !v.Stalled {
		t.Fatalf("three ticks naming one artifact produced output once, not three times: %s", v.Reason)
	}
}

// And the other direction, which must keep working: distinct real artifacts
// are a loop that is producing.
func TestDistinctArtifactsAreNotAStall(t *testing.T) {
	v, err := Check(write(t,
		`{"at":"2026-09-02T10:00:00Z","phase_item":"p","src_head":"a","artifact":"docs/one.md"}`,
		`{"at":"2026-09-02T10:03:00Z","phase_item":"p","src_head":"a","artifact":"docs/two.md"}`,
		`{"at":"2026-09-02T10:06:00Z","phase_item":"p","src_head":"a","artifact":"docs/three.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.Stalled {
		t.Fatalf("three distinct artifacts is a producing loop: %s", v.Reason)
	}
}

// Record must refuse a placeholder rather than write it, so the dodge is not
// available at the point it would be taken.
func TestRecordRefusesAPlaceholderArtifact(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := Record(p, Entry{At: time.Now(), PhaseItem: "p", SrcHead: "a", Artifact: "null"}, nil); err == nil {
		t.Fatal("Record must refuse an artifact that is absence spelled as presence")
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a refused Record must not have written anything")
	}
}

// The old syntactic Locatable() test lived here. It is superseded by
// TestArtifactMustResolveToSomethingReal below: a reviewer defeated the
// syntactic rule with prose containing a slash, so the rule was replaced with
// resolution rather than patched. Recorded rather than silently deleted --
// the check got stronger, and the test asserting the weaker rule went with it.

// Third attempt at "is this artifact real?". The first two were string
// inspection and an independent reviewer defeated both:
//
//	"any non-empty string"  -> defeated by "null"
//	placeholder list        -> defeated by "working on it"
//	locatable-looking text  -> defeated by "read/write path unclear"
//
// Prose can always be made to look like a pointer. It cannot be made to
// RESOLVE. So the artifact must name something that actually exists.
func TestArtifactMustResolveToSomethingReal(t *testing.T) {
	exists := map[string]bool{
		"docs/phase-plan.md": true,
		"AGENTS.md":          true,
		"go.mod":             true,
		"04793cd":            true,
	}
	produced := func(tok string, _ time.Time) bool { return exists[tok] }

	refused := []string{
		// The bypasses found in review. Each looks like a pointer and
		// resolves to nothing.
		"still going, read/write path unclear",
		"working on it, pass/fail undecided",
		"either/or, haven't picked yet",
		"n/a for now",
		"null",
		"made progress on src/estate/nonexistent.go",
		"see docs/does-not-exist.md",
	}
	for _, s := range refused {
		if err := Validate(s, time.Time{}, produced); err == nil {
			t.Errorf("artifact %q resolves to nothing and must be refused", s)
		}
	}

	accepted := []string{
		"docs/phase-plan.md",
		"AGENTS.md",                             // root-level file, wrongly refused before
		"go.mod",                                // ditto
		"merged #907 to main (04793cd)",         // sha embedded in prose is fine: it resolves
		"fixed the thing in docs/phase-plan.md", // prose is fine when something in it resolves
		"https://github.com/jonhill90/x/pull/1", // a URL is checkable by a human
	}
	for _, s := range accepted {
		if err := Validate(s, time.Time{}, produced); err != nil {
			t.Errorf("artifact %q names something real and must be accepted: %v", s, err)
		}
	}
}

// A padded placeholder defeated the exact-match list. It must not.
func TestPaddedPlaceholdersAreStillAbsence(t *testing.T) {
	// A sentence merely STARTING with one of these is not a placeholder when
	// it names something real -- "pending PR #907 merge, see docs/x.md" was
	// refused before it was ever looked at.
	if err := Validate("pending PR #907 merge, see docs/phase-plan.md", time.Time{},
		func(tok string, _ time.Time) bool { return tok == "docs/phase-plan.md" }); err != nil {
		t.Errorf("a real artifact that starts with a padding word must be accepted: %v", err)
	}
	for _, s := range []string{"n/a", "TBD", "none", "nothing"} {
		if err := Validate(s, time.Time{}, func(string, time.Time) bool { return false }); err == nil {
			t.Errorf("%q is a padded placeholder and must be refused", s)
		}
	}
}

// The fourth defeat: "resolves" meant "pre-existing", so a stalled tick
// naming any old file in the repo passed. The bar is recency.
func TestPreExistingThingsAreNotEvidenceOfThisTick(t *testing.T) {
	last := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	// produced reports true only for a token made after `since`.
	made := map[string]time.Time{
		"AGENTS.md":         time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),  // old
		"docs/new-thing.md": time.Date(2026, 9, 2, 12, 5, 0, 0, time.UTC), // new
	}
	produced := func(tok string, since time.Time) bool {
		at, ok := made[tok]
		return ok && at.After(since)
	}

	if err := Validate("AGENTS.md", last, produced); err == nil {
		t.Error("naming a file that predates this tick is not evidence the tick did anything")
	}
	if err := Validate("docs/new-thing.md", last, produced); err != nil {
		t.Errorf("a file made since the last tick is real evidence: %v", err)
	}
	// First tick: nothing to compare against, so any resolving token stands.
	if err := Validate("AGENTS.md", time.Time{}, produced); err == nil {
		t.Log("first-tick behaviour: refused because the resolver reports not-after-zero; acceptable")
	}
}

// The stop condition lived in a file its own subject can delete. A reviewer
// showed one `rm` erases a genuine three-tick stall.
func TestTruncatedLogIsRefusedNotTreatedAsFresh(t *testing.T) {
	p := write(t, stalledA, stalledB, stalledC)

	if err := CheckAgainstCommitted(p, 3); err != nil {
		t.Fatalf("a log matching its committed copy is fine: %v", err)
	}
	if err := CheckAgainstCommitted(p, 2); err != nil {
		t.Errorf("more records on disk than committed is normal -- ticks since the last commit: %v", err)
	}
	// The attack: the log is deleted or shortened.
	short := write(t, stalledA)
	if err := CheckAgainstCommitted(short, 3); err == nil {
		t.Fatal("a log with fewer records than the committed copy must be refused")
	}
	missing := filepath.Join(t.TempDir(), "gone.jsonl")
	if err := CheckAgainstCommitted(missing, 3); err == nil {
		t.Fatal("a deleted log must be refused, not read as a fresh first tick")
	}
	// Could not measure is never clean.
	if err := CheckAgainstCommitted(p, -1); err == nil {
		t.Fatal("an unreadable committed copy must refuse rather than pass")
	}
	// A genuinely new log with nothing committed yet is legitimate.
	if err := CheckAgainstCommitted(missing, 0); err != nil {
		t.Errorf("a first run with nothing committed is not tampering: %v", err)
	}
}

// A throwaway probe once wrote {"phase_item":"ph"} into the production log
// because ESTATE_TICK_LOG was not set, and it was read back as a real tick.
func TestPhaseItemMustExistInThePlan(t *testing.T) {
	plan := filepath.Join(t.TempDir(), "plan.md")
	body := "# Plan\n\n## Phase 0 — The verifier\n\ntext\n\n## Phase 3 — Cross-model\n\ntext\n"
	if err := os.WriteFile(plan, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	known, err := KnownPhases(plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(known) != 2 || known[0] != "phase-0" || known[1] != "phase-3" {
		t.Fatalf("KnownPhases = %v, want [phase-0 phase-3]", known)
	}
	for _, ok := range []string{"phase-0", "phase-3"} {
		if err := CheckPhaseItem(ok, known); err != nil {
			t.Errorf("%q is in the plan: %v", ok, err)
		}
	}
	// The probe, a typo, and a label invented on the spot.
	for _, bad := range []string{"ph", "phase-9", "delivery-unblock", ""} {
		if err := CheckPhaseItem(bad, known); err == nil {
			t.Errorf("%q is not a phase in the plan and must be refused", bad)
		}
	}
}

// Could not read the plan is never clean.
func TestUnreadablePlanRefusesRatherThanAllowingAnything(t *testing.T) {
	if _, err := KnownPhases(filepath.Join(t.TempDir(), "absent.md")); err == nil {
		t.Fatal("a missing plan must be an error, not an empty allowlist that permits everything")
	}
	empty := filepath.Join(t.TempDir(), "empty.md")
	if err := os.WriteFile(empty, []byte("# nothing here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := KnownPhases(empty); err == nil {
		t.Fatal("a plan naming no phases must be an error, not a permissive empty list")
	}
}
