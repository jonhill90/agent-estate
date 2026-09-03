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

	if err := Record(p, Entry{At: at, PhaseItem: "phase-0", SrcHead: "aaa"}); err != nil {
		t.Fatal(err)
	}
	if err := Record(p, Entry{At: at, PhaseItem: "phase-0", SrcHead: "aaa", Artifact: "docs/x.md"}); err != nil {
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
		if err := Record(p, Entry{At: at.Add(time.Duration(i) * time.Minute), PhaseItem: "phase-0", SrcHead: "aaa"}); err != nil {
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
	if err := Record(p, Entry{At: time.Now(), PhaseItem: "p", SrcHead: "a", Artifact: "null"}); err == nil {
		t.Fatal("Record must refuse an artifact that is absence spelled as presence")
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("a refused Record must not have written anything")
	}
}

// A placeholder list is not enough: an independent review defeated it with
// "working on it" and "still going" -- plausible prose naming no output. An
// artifact must point at something a reader can open.
func TestArtifactMustNameSomethingLocatable(t *testing.T) {
	refused := []string{
		"working on it", "still going", "made progress", "done",
		"fixed the bug", "improved things", "see above",
	}
	for _, s := range refused {
		if Locatable(s) {
			t.Errorf("%q names nothing openable; must not count as an artifact", s)
		}
		p := filepath.Join(t.TempDir(), "t.jsonl")
		if err := Record(p, Entry{At: time.Now(), PhaseItem: "p", SrcHead: "a", Artifact: s}); err == nil {
			t.Errorf("Record must refuse artifact %q", s)
		}
	}
	accepted := []string{
		"docs/phase-plan.md", "src/estate/internal/tick/tick.go",
		"PR #913", "merged #907 to main (04793cd)", "04793cd",
		"https://github.com/jonhill90/agent-estate/pull/913",
		"docs/tick-log.jsonl + green estate-ci",
	}
	for _, s := range accepted {
		if !Locatable(s) {
			t.Errorf("%q points at something openable and must be accepted", s)
		}
		p := filepath.Join(t.TempDir(), "t.jsonl")
		if err := Record(p, Entry{At: time.Now(), PhaseItem: "p", SrcHead: "a", Artifact: s}); err != nil {
			t.Errorf("Record refused a real artifact %q: %v", s, err)
		}
	}
}
