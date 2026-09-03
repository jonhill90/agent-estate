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

// bogus returns a path whose PARENT directory does not exist either -- a
// wiped state directory or a mistyped flag, never a legitimate first run.
func bogus(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "no-such-dir-at-all", name)
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

// TestLinesDirectorStatesAreDistinct pins the Director/tick-log line itself,
// not just the Ledger line beside it. TestLinesDistinguishAbsentFromEmptyFromUnreadable
// above breaks the ledger and tick fixtures together, so every one of its
// assertions is satisfiable from the Ledger half of Lines() alone -- verified
// by mutating the Director branch of the switch in Lines to render identical
// text for Absent and Unreadable and re-running that test: it still passed.
// Here the ledger side is held IDENTICAL (present, empty) across all three
// cases, so only the Ticks availability can account for any difference in
// the rendered output -- the Director text has nowhere else to come from.
func TestLinesDirectorStatesAreDistinct(t *testing.T) {
	ledger := write(t, "l.jsonl") // present, empty -- identical in all three cases below

	absent := strings.Join(Lines(Read(ledger, missing(t, "t.jsonl"))), "\n")
	unreadable := strings.Join(Lines(Read(ledger, write(t, "t.jsonl", "{bad"))), "\n")
	present := strings.Join(Lines(Read(ledger, write(t, "t.jsonl",
		`{"at":"2026-09-03T00:00:00Z","phase_item":"repro","src_head":"deadbeef01","artifact":"pr#1"}`))), "\n")

	if !strings.Contains(absent, "Director: no tick log at") || !strings.Contains(absent, "not running") {
		t.Errorf("an absent tick log must render its own Director text; got:\n%s", absent)
	}
	if !strings.Contains(unreadable, "Director: tick log UNREADABLE") {
		t.Errorf("an unreadable tick log must render its own Director text; got:\n%s", unreadable)
	}
	if !strings.Contains(present, "Director: 1 tick(s); last on repro") {
		t.Errorf("a present tick log must render its own Director text; got:\n%s", present)
	}

	if absent == unreadable || absent == present || unreadable == present {
		t.Fatalf("the three Director states must render distinct text with an identical ledger side:\nabsent:\n%s\nunreadable:\n%s\npresent:\n%s", absent, unreadable, present)
	}
}

// TestBogusLedgerParentIsUnreadableNotAbsent is the reproduction from agent-estate#920:
// a ledger path whose parent directory does not exist either -- a wiped
// state dir or a typo'd flag -- must never render as the calm first-run
// message. Only a missing file with an EXISTING parent is a genuine first
// run (TestAbsentLedgerIsNotAnEmptyEstate above pins that case).
func TestBogusLedgerParentIsUnreadableNotAbsent(t *testing.T) {
	s := Read(bogus(t, "ledger.jsonl"), missing(t, "t.jsonl"))
	if s.Ledger != Unreadable {
		t.Fatalf("Ledger = %v, want Unreadable for a path whose parent directory does not exist", s.Ledger)
	}
	if s.LedgerErr == nil {
		t.Error("Unreadable must carry the reason -- a wiped/mistyped state dir, not a bare nil")
	}
}

// TestBogusTickParentIsUnreadableNotAbsent is the same case on the tick-log
// side: readLines is shared, so both callers must get the same treatment.
func TestBogusTickParentIsUnreadableNotAbsent(t *testing.T) {
	s := Read(missing(t, "l.jsonl"), bogus(t, "tick-log.jsonl"))
	if s.Ticks != Unreadable {
		t.Fatalf("Ticks = %v, want Unreadable for a path whose parent directory does not exist", s.Ticks)
	}
	if s.TickErr == nil {
		t.Error("Unreadable must carry the reason -- a wiped/mistyped state dir, not a bare nil")
	}
}

// TestLinesBogusLedgerRendersAsUnreadableNotFirstRun is the end-to-end
// reproduction of agent-estate#920's actual bug report: Lines() must never print "first
// run" reassurance for a path whose parent directory does not exist.
func TestLinesBogusLedgerRendersAsUnreadableNotFirstRun(t *testing.T) {
	s := Read(bogus(t, "ledger.jsonl"), missing(t, "t.jsonl"))
	out := strings.Join(Lines(s), "\n")
	if strings.Contains(out, "first-run state") || strings.Contains(out, "none recorded yet") {
		t.Errorf("a bogus path must never render as a calm first-run state; got:\n%s", out)
	}
	if !strings.Contains(out, "UNREADABLE") {
		t.Errorf("a bogus path must render UNREADABLE; got:\n%s", out)
	}
}

// TestLinesThreeLedgerStatesAreDistinct pins all three typed ledger states'
// rendered text against each other -- absent (genuine first run), the new
// bogus-parent case (Unreadable), and an existing corrupt file (Unreadable
// for a different reason). Mutate any one of these strings and this test
// must go red.
func TestLinesThreeLedgerStatesAreDistinct(t *testing.T) {
	tick := missing(t, "t.jsonl")
	absent := strings.Join(Lines(Read(missing(t, "l.jsonl"), tick)), "\n")
	bogusParent := strings.Join(Lines(Read(bogus(t, "l.jsonl"), tick)), "\n")
	corrupt := strings.Join(Lines(Read(write(t, "l.jsonl", "{bad"), tick)), "\n")

	if absent == bogusParent {
		t.Fatal("a genuine first-run state must not render identically to a wiped/mistyped state dir")
	}
	if absent == corrupt || bogusParent == corrupt {
		t.Fatal("the three ledger states must all render distinct text")
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
