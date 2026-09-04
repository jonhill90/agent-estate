package status

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

var now = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// The plan as it actually read at b45f917, headings only: Phase 0 claims NOT
// ON MAIN and #914 is on main.
const planAtB45f917 = "# Phase plan\n\n" +
	"## Phase 0 — The verifier, not the loop `IN #914, NOT ON MAIN`\n\n" +
	"## Phase 1 — Dispatch isolation `IN #909, NOT ON MAIN`\n\n" +
	"## Phase 4 — Cross-harness budget accounting `NEW`\n"

func writePlan(t *testing.T, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "phase-plan.md")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func phase(t *testing.T, r Report, id string) Phase {
	t.Helper()
	for _, p := range r.Phases {
		if p.Plan.ID == id {
			return p
		}
	}
	t.Fatalf("no %s in the report", id)
	return Phase{}
}

func merged(prs ...int) func() (map[int]bool, string, error) {
	m := map[int]bool{}
	for _, pr := range prs {
		m[pr] = true
	}
	return func() (map[int]bool, string, error) { return m, "origin/main", nil }
}

// THE WHOLE POINT, IN BOTH DIRECTIONS. Same plan text, same "NOT ON MAIN"
// claim, two different git histories -- and the verdict must follow git, not
// the claim. If either half of this test ever passes for the wrong reason,
// the command has gone back to reading the prose.
func TestPhaseProgressFollowsMainNotThePlansStatusString(t *testing.T) {
	plan := writePlan(t, planAtB45f917)

	// git says #914 landed. The plan says NOT ON MAIN. git wins.
	onMain := Build(Config{Now: now, PlanPath: plan, MergedPRs: merged(914)})
	if got := phase(t, onMain, "phase-0").State; got != PhaseAllOnMain {
		t.Errorf("phase-0 with #914 in main's history = %v, want ON MAIN (the plan's own string said NOT ON MAIN)", got)
	}
	// The mutation: take #914 back out of main's history and nothing else
	// changes. The verdict must flip, or it was never reading git at all.
	notOnMain := Build(Config{Now: now, PlanPath: plan, MergedPRs: merged()})
	if got := phase(t, notOnMain, "phase-0").State; got != PhaseNoneOnMain {
		t.Errorf("phase-0 with #914 absent from main = %v, want NOT ON MAIN", got)
	}
	// And phase-1, whose string happens to be right, is reached the same way.
	if got := phase(t, onMain, "phase-1").State; got != PhaseNoneOnMain {
		t.Errorf("phase-1 = %v, want NOT ON MAIN", got)
	}
}

func TestPartlyOnMainIsItsOwnState(t *testing.T) {
	plan := writePlan(t, "## Phase 2 — visible `IN #910 AND #920`\n")
	r := Build(Config{Now: now, PlanPath: plan, MergedPRs: merged(910)})
	p := phase(t, r, "phase-2")
	if p.State != PhaseSomeOnMain {
		t.Fatalf("state = %v, want PARTLY ON MAIN", p.State)
	}
	if len(p.Merged) != 1 || p.Merged[0] != 910 || len(p.Unmerged) != 1 || p.Unmerged[0] != 920 {
		t.Fatalf("merged=%v unmerged=%v", p.Merged, p.Unmerged)
	}
}

// Absence is a state. Neither of these two may report as "not started".
func TestUnresolvablePhasesAreUnknownNotUnstarted(t *testing.T) {
	// The plan names no pull request for phase-4 (`NEW`).
	r := Build(Config{Now: now, PlanPath: writePlan(t, planAtB45f917), MergedPRs: merged(914)})
	if got := phase(t, r, "phase-4").State; got != PhaseUnknown {
		t.Errorf("phase-4 (`NEW`, no PR named) = %v, want UNKNOWN", got)
	}

	// git could not be read at all.
	blind := Build(Config{
		Now: now, PlanPath: writePlan(t, planAtB45f917),
		MergedPRs: func() (map[int]bool, string, error) {
			return nil, "", errors.New("not a git repository")
		},
	})
	for _, id := range []string{"phase-0", "phase-1"} {
		p := phase(t, blind, id)
		if p.State != PhaseUnknown {
			t.Errorf("%s with main unreadable = %v, want UNKNOWN", id, p.State)
		}
		if !strings.Contains(p.Why, "unmeasured") {
			t.Errorf("%s Why = %q, want it to say the history was unmeasured", id, p.Why)
		}
	}
	if blind.Unresolved() == 0 {
		t.Error("a report that could not read main's history reported nothing unresolved")
	}
}

// A forge that could not be asked must never render as "nothing is waiting".
func TestAForgeFailureIsNotZeroOpenItems(t *testing.T) {
	r := Build(Config{
		Now:        now,
		OpenPRs:    func() ([]Item, error) { return nil, errors.New("gh: not authenticated") },
		OpenIssues: func() ([]Item, error) { return nil, errors.New("gh: not authenticated") },
	})
	if r.OpenPRsErr == nil || r.OpenIssuesErr == nil {
		t.Fatal("forge errors were swallowed")
	}
	var b bytes.Buffer
	Render(&b, r)
	out := b.String()
	if !strings.Contains(out, "could not ask the forge") {
		t.Errorf("render does not say the forge could not be asked:\n%s", out)
	}
	if strings.Contains(out, "no open pull requests -- asked") {
		t.Errorf("render reported zero open pull requests after a failed ask:\n%s", out)
	}
	if r.Unresolved() != 2 {
		t.Errorf("Unresolved = %d, want 2", r.Unresolved())
	}
}

func TestAskedAndFoundNothingReadsDifferentlyFromCouldNotAsk(t *testing.T) {
	r := Build(Config{
		Now:        now,
		OpenPRs:    func() ([]Item, error) { return nil, nil },
		OpenIssues: func() ([]Item, error) { return nil, nil },
	})
	var b bytes.Buffer
	Render(&b, r)
	if !strings.Contains(b.String(), "asked, and there are none") {
		t.Errorf("a genuine zero does not say it was asked:\n%s", b.String())
	}
	if r.Unresolved() != 0 {
		t.Errorf("Unresolved = %d, want 0", r.Unresolved())
	}
}

// Ages are what turn `estate inflight`'s list into a signal.
func TestInFlightCarriesAgesOldestFirst(t *testing.T) {
	r := Build(Config{
		Now: now,
		InFlight: func() ([]ledger.Record, error) {
			return []ledger.Record{
				{ID: "young", State: ledger.Dispatched, At: now.Add(-3 * time.Minute)},
				{ID: "old", State: ledger.Unknown, At: now.Add(-50 * time.Hour)},
			}, nil
		},
	})
	if len(r.InFlight) != 2 {
		t.Fatalf("in flight = %d, want 2", len(r.InFlight))
	}
	if r.InFlight[0].Record.ID != "old" {
		t.Errorf("order = %s first, want the oldest first", r.InFlight[0].Record.ID)
	}
	if got := Age(r.InFlight[0].Age); got != "2d02h" {
		t.Errorf("age = %q, want 2d02h", got)
	}
	if got := Age(r.InFlight[1].Age); got != "3m" {
		t.Errorf("age = %q, want 3m", got)
	}
}

func TestAgeRefusesToRenderAFutureTimestampAsPlausible(t *testing.T) {
	if got := Age(-5 * time.Minute); got != "future?" {
		t.Errorf("Age(-5m) = %q, want future?", got)
	}
}

// The join the whole command needs: what ran, against what we were trying to
// do. A record with no phase is counted as unattributed, never spread across
// the phases to make the totals look complete.
func TestUnattributedLedgerRecordsAreCountedNotDistributed(t *testing.T) {
	r := Build(Config{
		Now:      now,
		PlanPath: writePlan(t, planAtB45f917),
		Current: func() ([]ledger.Record, error) {
			return []ledger.Record{
				{ID: "a", Phase: "phase-0", At: now},
				{ID: "b", Phase: "phase-0", At: now},
				{ID: "c", At: now}, // predates the field, or named no phase
				{ID: "d", Phase: "  ", At: now},
			}, nil
		},
		MergedPRs: merged(914),
	})
	if got := phase(t, r, "phase-0").LedgerTurns; got != 2 {
		t.Errorf("phase-0 ledger turns = %d, want 2", got)
	}
	if got := phase(t, r, "phase-1").LedgerTurns; got != 0 {
		t.Errorf("phase-1 ledger turns = %d, want 0 -- unattributed turns must not leak into a phase", got)
	}
	if r.Unattributed != 2 {
		t.Errorf("Unattributed = %d, want 2", r.Unattributed)
	}
	var b bytes.Buffer
	Render(&b, r)
	if !strings.Contains(b.String(), "2 of 4 ledger record(s) carry no phase") {
		t.Errorf("render does not name the unattributed count:\n%s", b.String())
	}
}

func TestALedgerPhaseThePlanDoesNotNameGetsItsOwnBucket(t *testing.T) {
	r := Build(Config{
		Now:      now,
		PlanPath: writePlan(t, planAtB45f917),
		Current: func() ([]ledger.Record, error) {
			return []ledger.Record{{ID: "a", Phase: "phase-77", At: now}}, nil
		},
		MergedPRs: merged(),
	})
	p := phase(t, r, "phase-77")
	if p.State != PhaseUnknown || p.LedgerTurns != 1 {
		t.Fatalf("phase-77 = %v with %d turn(s), want UNKNOWN with 1", p.State, p.LedgerTurns)
	}
	if r.Unattributed != 0 {
		t.Errorf("Unattributed = %d, want 0 -- it named a phase, just not one the plan has", r.Unattributed)
	}
}

// History is append-only. `ph` is a typo the Director wrote and it stays in
// the log; it is classified unknown at READ time and shown as itself.
func TestUnrecognisedTickPhaseItemsAreUnknownAndKeepTheirText(t *testing.T) {
	r := Build(Config{
		Now:      now,
		PlanPath: writePlan(t, planAtB45f917),
		TickPhaseItems: func() ([]string, error) {
			return []string{"phase-0", "phase-0", "ph", "delivery-unblock", "phase-plan", "phase-1"}, nil
		},
	})
	if len(r.TickKnown) != 2 {
		t.Fatalf("known labels = %+v, want phase-0 and phase-1", r.TickKnown)
	}
	if r.TickKnown[0].Label != "phase-0" || r.TickKnown[0].Count != 2 {
		t.Errorf("TickKnown[0] = %+v, want phase-0 x2", r.TickKnown[0])
	}
	got := map[string]int{}
	for _, c := range r.TickUnknown {
		got[c.Label] = c.Count
	}
	for _, want := range []string{"ph", "delivery-unblock", "phase-plan"} {
		if got[want] != 1 {
			t.Errorf("%q missing from the unknown labels: %+v", want, r.TickUnknown)
		}
	}
	var b bytes.Buffer
	Render(&b, r)
	if !strings.Contains(b.String(), "ph ") || !strings.Contains(b.String(), "UNKNOWN -- not a phase the plan names") {
		t.Errorf("render does not show the unrecognised labels as themselves:\n%s", b.String())
	}
}

func TestBuildNeverPanicsWithNoSourcesAtAll(t *testing.T) {
	r := Build(Config{})
	if r.Now.IsZero() {
		t.Error("Now was left zero")
	}
	var b bytes.Buffer
	Render(&b, r)
	if !strings.Contains(b.String(), "estate status") {
		t.Error("an empty report rendered nothing")
	}
}
