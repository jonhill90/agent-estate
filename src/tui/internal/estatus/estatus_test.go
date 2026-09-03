package estatus

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// Role and PID are agent-facing fields (agent-estate#930): the Agents/Lanes
// views need them to describe a turn without inventing tmux-shaped data.
// Both must decode from the same JSON src/estate's ledger.Record writes.
func TestDispatchDecodesRoleAndPID(t *testing.T) {
	s := Read(write(t, "l.jsonl",
		`{"id":"930-1","issue":"#930","role":"reviewer","state":"dispatched","at":"2026-09-03T10:00:00Z","pid":4242}`),
		missing(t, "t.jsonl"))
	if len(s.Dispatches) != 1 {
		t.Fatalf("want 1 dispatch, got %d", len(s.Dispatches))
	}
	d := s.Dispatches[0]
	if d.Role != "reviewer" {
		t.Errorf("Role = %q, want %q", d.Role, "reviewer")
	}
	if d.PID != 4242 {
		t.Errorf("PID = %d, want 4242", d.PID)
	}
}

// A record written before Role/PID existed must decode as empty/zero, never
// as a fabricated default -- this package's own reader does not apply
// src/estate's EffectiveRole default; a caller that wants that must ask for
// it explicitly.
func TestDispatchWithoutRoleOrPIDDecodesZero(t *testing.T) {
	s := Read(write(t, "l.jsonl",
		`{"id":"old-1","issue":"#1","state":"complete","at":"2026-09-01T00:00:00Z"}`),
		missing(t, "t.jsonl"))
	d := s.Dispatches[0]
	if d.Role != "" {
		t.Errorf("Role = %q, want empty for a pre-Role record", d.Role)
	}
	if d.PID != 0 {
		t.Errorf("PID = %d, want 0 for a pre-PID record", d.PID)
	}
}

// Worktree only ever reports ok=true for the exact "worktree <path>" shape
// src/estate's dispatch path writes (main.go: `Note: "worktree " + wt.Path`)
// -- any other Note (a failure reason, empty, or free text that merely
// starts differently) must report not-found rather than a guessed path.
func TestDispatchWorktree(t *testing.T) {
	cases := []struct {
		name     string
		note     string
		wantOK   bool
		wantPath string
	}{
		{"real worktree note", "worktree /tmp/estate-dispatch/repo-abc/930-1", true, "/tmp/estate-dispatch/repo-abc/930-1"},
		{"failure note", "failed to start: exec: not found", false, ""},
		{"empty note", "", false, ""},
		{"prefix with no path", "worktree ", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Dispatch{Note: c.note}
			path, ok := d.Worktree()
			if ok != c.wantOK || path != c.wantPath {
				t.Errorf("Worktree() = (%q, %v), want (%q, %v)", path, ok, c.wantPath, c.wantOK)
			}
		})
	}
}

// TestParsePressureLine pins ParsePressureLine against src/estate/main.go's
// own printed line (its "pressure" case) verbatim, plus the shapes that
// must fail rather than silently decode to zero.
func TestParsePressureLine(t *testing.T) {
	r, ok := ParsePressureLine("load 0.14/core  free 4396MB  inflight 3  weekly budget 94% left")
	if !ok {
		t.Fatal("a well-formed pressure line must parse")
	}
	want := PressureReading{LoadPerCore: 0.14, FreeMemMB: 4396, InFlight: 3, WeeklyRemaining: 94}
	if !reflect.DeepEqual(r, want) {
		t.Errorf("ParsePressureLine() = %+v, want %+v", r, want)
	}

	for _, bad := range []string{
		"",
		"within limits",
		"load 0.14/core free 4396MB inflight 3", // missing weekly budget field
		"garbage output from a binary of a totally different shape",
	} {
		if _, ok := ParsePressureLine(bad); ok {
			t.Errorf("ParsePressureLine(%q) unexpectedly parsed; a malformed line must fail closed, not decode to a zero reading", bad)
		}
	}
}

// TestParsePressureReasons pins the stderr decode against the exact
// "refuse: <reason>" shape src/estate/main.go's "pressure" case writes.
func TestParsePressureReasons(t *testing.T) {
	got := ParsePressureReasons("refuse: load 3.10 at or above limit 3.00\nrefuse: 6 lanes in flight, cap is 6\n")
	want := []string{"load 3.10 at or above limit 3.00", "6 lanes in flight, cap is 6"}
	if len(got) != len(want) {
		t.Fatalf("ParsePressureReasons() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reason %d = %q, want %q", i, got[i], want[i])
		}
	}
	if r := ParsePressureReasons(""); r != nil {
		t.Errorf("ParsePressureReasons(\"\") = %v, want nil", r)
	}
}

// TestReadPressureNilFetchLeavesUnconfigured is the fabrication guard this
// whole design exists for: a Status built by any caller that never wired a
// pressure source in (every existing caller before agent-estate#987) must never render
// a Present, all-zero reading -- Lines() must omit the section entirely.
func TestReadPressureNilFetchLeavesUnconfigured(t *testing.T) {
	p := ReadPressure(nil)
	if p.Configured {
		t.Fatalf("ReadPressure(nil) = %+v, want Configured false", p)
	}
	s := Status{Pressure: p}
	out := strings.Join(Lines(s), "\n")
	if strings.Contains(out, "Pressure") {
		t.Errorf("an unconfigured pressure source must not render a Pressure line at all; got:\n%s", out)
	}
}

// TestReadPressurePresent and TestReadPressureUnreadable pin the two real
// outcomes Home can show, and that they render distinct, honest text --
// never a fabricated zero for the failure case.
func TestReadPressurePresent(t *testing.T) {
	fetch := func() (PressureReading, error) {
		return PressureReading{LoadPerCore: 0.5, FreeMemMB: 1000, InFlight: 1, WeeklyRemaining: 80, OK: true}, nil
	}
	s := Status{Pressure: ReadPressure(fetch)}
	out := strings.Join(Lines(s), "\n")
	if !strings.Contains(out, "within limits") {
		t.Errorf("a measured, OK reading must say so; got:\n%s", out)
	}
	if strings.Contains(out, "UNREADABLE") {
		t.Errorf("a real reading must never render as UNREADABLE; got:\n%s", out)
	}
}

func TestReadPressureRefusing(t *testing.T) {
	fetch := func() (PressureReading, error) {
		return PressureReading{LoadPerCore: 3.5, FreeMemMB: 100, InFlight: 6, WeeklyRemaining: 1, OK: false,
			Reasons: []string{"load 3.50 at or above limit 3.00"}}, nil
	}
	s := Status{Pressure: ReadPressure(fetch)}
	out := strings.Join(Lines(s), "\n")
	if !strings.Contains(out, "REFUSING new work") {
		t.Errorf("a measured refusal must say so distinctly from both OK and UNREADABLE; got:\n%s", out)
	}
	if !strings.Contains(out, "load 3.50 at or above limit 3.00") {
		t.Errorf("a refusal's own reason must be shown, not swallowed; got:\n%s", out)
	}
}

func TestReadPressureUnreadable(t *testing.T) {
	fetch := func() (PressureReading, error) { return PressureReading{}, errors.New("exec: \"estate\": executable file not found in $PATH") }
	s := Status{Pressure: ReadPressure(fetch)}
	if s.Pressure.Avail != Unreadable {
		t.Fatalf("Pressure.Avail = %v, want Unreadable", s.Pressure.Avail)
	}
	out := strings.Join(Lines(s), "\n")
	if !strings.Contains(out, "Pressure: UNREADABLE -- this is not zero.") {
		t.Errorf("a pressure fetch failure must render UNREADABLE, never a blank or zero reading; got:\n%s", out)
	}
	if strings.Contains(out, "0.00/core") {
		t.Errorf("an unreadable pressure source must never render fabricated zero figures; got:\n%s", out)
	}
}
