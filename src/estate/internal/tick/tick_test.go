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

// The other direction. Each mutation below breaks exactly one of the three
// conditions, and each must clear the stall -- otherwise the check is not
// measuring what it claims and would fire on a healthy loop.
func TestCheckClearsWhenAnySingleConditionBreaks(t *testing.T) {
	cases := map[string]string{
		"artifact present on the newest tick": `{"at":"2026-09-02T10:06:00Z","phase_item":"phase-0","src_head":"aaa","artifact":"docs/thing.md"}`,
		"src_head moved on the newest tick":   `{"at":"2026-09-02T10:06:00Z","phase_item":"phase-0","src_head":"bbb","artifact":null}`,
		"phase_item changed on newest tick":   `{"at":"2026-09-02T10:06:00Z","phase_item":"phase-1","src_head":"aaa","artifact":null}`,
	}
	for name, third := range cases {
		t.Run(name, func(t *testing.T) {
			v, err := Check(write(t, stalledA, stalledB, third))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v.Stalled {
				t.Fatalf("must not report stalled when %s: %s", name, v.Reason)
			}
		})
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
