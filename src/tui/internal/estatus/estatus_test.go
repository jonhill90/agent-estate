package estatus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name string, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	body := ""
	for _, l := range lines {
		body += l + "\n"
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func missing(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

// The case that was true when this package was written: no dispatch has ever
// run. That must read as Absent, never as a present-and-empty estate.
func TestAbsentLedgerIsNotAnEmptyEstate(t *testing.T) {
	s := Read(missing(t, "ledger.jsonl"), missing(t, "tick-log.jsonl"))
	if s.Ledger != Absent {
		t.Errorf("Ledger = %v, want Absent", s.Ledger)
	}
	if s.LedgerErr != nil {
		t.Errorf("an absent ledger is not an error: %v", s.LedgerErr)
	}
	if s.Ticks != Absent {
		t.Errorf("Ticks = %v, want Absent", s.Ticks)
	}
}

func TestPresentButEmptyIsDistinctFromAbsent(t *testing.T) {
	s := Read(write(t, "ledger.jsonl"), write(t, "tick-log.jsonl"))
	if s.Ledger != Present {
		t.Errorf("an existing empty ledger is Present, got %v", s.Ledger)
	}
	if len(s.Dispatches) != 0 {
		t.Errorf("want no dispatches, got %d", len(s.Dispatches))
	}
	if s.LastTick != nil {
		t.Error("an empty tick log has no last tick")
	}
}

// A corrupt ledger must never read as a healthy empty one.
func TestUnreadableLedgerIsNeverEmpty(t *testing.T) {
	s := Read(write(t, "ledger.jsonl", `{"id":"a","state":"dispatched"}`, "{not json"), missing(t, "t.jsonl"))
	if s.Ledger != Unreadable {
		t.Fatalf("Ledger = %v, want Unreadable", s.Ledger)
	}
	if s.LedgerErr == nil {
		t.Error("Unreadable must carry the reason")
	}
	if len(s.Dispatches) != 0 {
		t.Error("an unreadable ledger must not yield partial rows that look complete")
	}
}

// The ledger is append-only: the last line for an id is its current state.
func TestLatestRecordPerTaskWins(t *testing.T) {
	s := Read(write(t, "ledger.jsonl",
		`{"id":"907-1","issue":"#907","state":"dispatched","at":"2026-09-02T10:00:00Z"}`,
		`{"id":"908-1","issue":"#908","state":"dispatched","at":"2026-09-02T10:01:00Z"}`,
		`{"id":"907-1","issue":"#907","state":"complete","at":"2026-09-02T10:05:00Z"}`,
	), missing(t, "t.jsonl"))

	if len(s.Dispatches) != 2 {
		t.Fatalf("want 2 tasks, got %d", len(s.Dispatches))
	}
	byID := map[string]Dispatch{}
	for _, d := range s.Dispatches {
		byID[d.ID] = d
	}
	if got := byID["907-1"].State; got != "complete" {
		t.Errorf("907-1 state = %q, want complete (the later record)", got)
	}
	if len(s.InFlight) != 1 || s.InFlight[0].ID != "908-1" {
		t.Errorf("InFlight = %v, want only 908-1", s.InFlight)
	}
}

// unknown is not failed and must keep occupying a slot -- src/estate records a
// turn it could not observe as unknown precisely so nothing frees it.
func TestUnknownStillOccupiesASlot(t *testing.T) {
	s := Read(write(t, "ledger.jsonl",
		`{"id":"x","state":"unknown","at":"2026-09-02T10:00:00Z"}`,
		`{"id":"y","state":"failed","at":"2026-09-02T10:00:00Z"}`,
		`{"id":"z","state":"complete","at":"2026-09-02T10:00:00Z"}`,
	), missing(t, "t.jsonl"))

	if len(s.InFlight) != 1 || s.InFlight[0].ID != "x" {
		t.Fatalf("InFlight = %v; unknown must occupy a slot, failed and complete must not", s.InFlight)
	}
}

func TestLastTickAndArtifactAbsence(t *testing.T) {
	s := Read(missing(t, "l.jsonl"), write(t, "tick-log.jsonl",
		`{"at":"2026-09-02T10:00:00Z","phase_item":"phase-0","src_head":"aaa","artifact":"docs/x.md"}`,
		`{"at":"2026-09-02T10:03:00Z","phase_item":"phase-1","src_head":"aaa","artifact":null}`,
	))
	if s.TickRuns != 2 {
		t.Errorf("TickRuns = %d, want 2", s.TickRuns)
	}
	if s.LastTick == nil {
		t.Fatal("want a last tick")
	}
	if s.LastTick.PhaseItem != "phase-1" {
		t.Errorf("LastTick.PhaseItem = %q, want phase-1 (the newest)", s.LastTick.PhaseItem)
	}
	if s.LastTick.HasArtifact() {
		t.Error("a null artifact must not report as present")
	}
}

func TestEmptyArtifactStringIsAlsoAbsent(t *testing.T) {
	empty := ""
	if (Tick{Artifact: &empty}).HasArtifact() {
		t.Error(`artifact "" must count as absent, same as null`)
	}
	blank := "   "
	if (Tick{Artifact: &blank}).HasArtifact() {
		t.Error("a whitespace-only artifact must count as absent")
	}
}

// One source failing must not take the other down with it.
func TestSourcesFailIndependently(t *testing.T) {
	s := Read(write(t, "ledger.jsonl", "{broken"), write(t, "tick-log.jsonl",
		`{"at":"2026-09-02T10:00:00Z","phase_item":"phase-0","src_head":"aaa","artifact":"x"}`))
	if s.Ledger != Unreadable {
		t.Errorf("Ledger = %v, want Unreadable", s.Ledger)
	}
	if s.Ticks != Present || s.LastTick == nil {
		t.Errorf("a broken ledger must not hide a readable tick log: %v", s.Ticks)
	}
}

func TestEmptyPathIsUnreadableNotAbsent(t *testing.T) {
	s := Read("", "")
	if s.Ledger != Unreadable || s.Ticks != Unreadable {
		t.Errorf("an unconfigured path is Unreadable, not Absent: %v %v", s.Ledger, s.Ticks)
	}
}

// The rendering must never spell an absent source the same way as an empty
// one. These are the two lines a human would otherwise misread.
func TestLinesDistinguishAbsentFromEmptyFromUnreadable(t *testing.T) {
	joined := func(s Status) string {
		out := ""
		for _, l := range Lines(s) {
			out += l + "\n"
		}
		return out
	}

	absent := joined(Read(missing(t, "l.jsonl"), missing(t, "t.jsonl")))
	empty := joined(Read(write(t, "l.jsonl"), write(t, "t.jsonl")))
	broken := joined(Read(write(t, "l.jsonl", "{bad"), write(t, "t.jsonl", "{bad")))

	if absent == empty {
		t.Fatal("an absent ledger renders identically to an empty one")
	}
	if !strings.Contains(absent, "none recorded yet") {
		t.Errorf("absent ledger must say so; got:\n%s", absent)
	}
	if !strings.Contains(empty, "0 task(s)") {
		t.Errorf("an empty but present ledger must report a count; got:\n%s", empty)
	}
	if !strings.Contains(broken, "UNREADABLE") {
		t.Errorf("an unreadable ledger must say so, not show zero; got:\n%s", broken)
	}
	if strings.Contains(broken, "0 task(s)") {
		t.Errorf("an unreadable ledger must never render as a count; got:\n%s", broken)
	}
}

func TestLinesShowsInFlightDispatches(t *testing.T) {
	s := Read(write(t, "l.jsonl",
		`{"id":"907-1","issue":"#907","state":"dispatched","at":"2026-09-02T10:00:00Z"}`),
		missing(t, "t.jsonl"))
	out := strings.Join(Lines(s), "\n")
	if !strings.Contains(out, "907-1") || !strings.Contains(out, "dispatched") {
		t.Errorf("an in-flight dispatch must be listed; got:\n%s", out)
	}
}

// Issue jonhill90/agent-estate#920, found by a council convened after
// jonhill90/agent-estate#918 merged: readLines
// mapped ANY os.IsNotExist to Absent, so a wiped or mistyped path rendered
// as the calm "no turn has ever been dispatched" message.
//
// The backend this views, src/estate's ledger, deliberately distinguishes
// those cases -- a missing file at a default path is a first run; a missing
// file whose DIRECTORY is also missing means we were pointed somewhere that
// does not exist. The viewer was softer than the thing it views, on exactly
// the case the backend calls dangerous.
func TestMissingParentIsUnreadableNotAbsent(t *testing.T) {
	// Parent exists, file does not: a legitimate first run.
	firstRun := filepath.Join(t.TempDir(), "ledger.jsonl")
	s := Read(firstRun, firstRun)
	if s.Ledger != Absent {
		t.Errorf("a missing file in an existing directory is Absent, got %v", s.Ledger)
	}

	// Parent does not exist: we were pointed somewhere that is not there.
	bogus := filepath.Join(t.TempDir(), "no-such-dir", "ledger.jsonl")
	b := Read(bogus, bogus)
	if b.Ledger != Unreadable {
		t.Errorf("a missing PARENT directory is Unreadable, got %v", b.Ledger)
	}
	if b.LedgerErr == nil {
		t.Error("Unreadable must carry the reason")
	}
	joined := strings.Join(Lines(b), "\n")
	if strings.Contains(joined, "no turn has ever been dispatched") {
		t.Errorf("a path that does not exist must not render as a calm first run:\n%s", joined)
	}
	if !strings.Contains(joined, "UNREADABLE") {
		t.Errorf("it must say it could not look:\n%s", joined)
	}
}
