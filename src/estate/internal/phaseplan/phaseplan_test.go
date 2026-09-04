package phaseplan

import (
	"path/filepath"
	"reflect"
	"testing"
)

// THE DEFECT THIS FILE PINS. At b45f917 docs/phase-plan.md said Phase 0 was
// `IN #914, NOT ON MAIN`, and #914 was on main. Three of its four status
// strings were right and one was wrong -- which reads as maintained and is
// therefore worse than all four being wrong. The fix is not to correct the
// string; it is to stop reading it. These tests are what "stop reading it"
// means mechanically: the same heading with the opposite claim written in it
// must parse to exactly the same result, because the claim is not an input.

const b45f917Headings = "# Phase plan\n\n" +
	"## Phase 0 — The verifier, not the loop `IN #914, NOT ON MAIN`\n\n" +
	"Some prose about the phase.\n\n" +
	"## Phase 3 — Cross-**model** orchestration `HARNESS HALF IN #911, NOT ON MAIN`\n\n" +
	"Codex and claude reached different verdicts on #913 for a real reason.\n\n" +
	"## Phase 4 — Cross-harness budget accounting `NEW`\n\n" +
	"## Phase 5 — Intent as a live system\n"

func parse(t *testing.T, src string) []Phase {
	t.Helper()
	ps, err := ParseText(src)
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	return ps
}

func find(t *testing.T, ps []Phase, id string) Phase {
	t.Helper()
	for _, p := range ps {
		if p.ID == id {
			return p
		}
	}
	t.Fatalf("no %s in %v", id, IDs(ps))
	return Phase{}
}

func TestPullRequestNumbersAreTakenFromTheStatusSpan(t *testing.T) {
	ps := parse(t, b45f917Headings)
	if got, want := IDs(ps), []string{"phase-0", "phase-3", "phase-4", "phase-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("IDs = %v, want %v", got, want)
	}
	if got, want := find(t, ps, "phase-0").PRs, []int{914}; !reflect.DeepEqual(got, want) {
		t.Fatalf("phase-0 PRs = %v, want %v", got, want)
	}
	if got, want := find(t, ps, "phase-3").PRs, []int{911}; !reflect.DeepEqual(got, want) {
		t.Fatalf("phase-3 PRs = %v, want %v", got, want)
	}
}

// The mutation that matters: rewrite every status WORD to its opposite,
// leaving the numbers alone. Nothing about the parse may change. If this ever
// fails, some reader has started believing the prose again.
func TestTheStatusWordsAreNotAnInput(t *testing.T) {
	honest := parse(t, b45f917Headings)
	lying := parse(t, "## Phase 0 — The verifier, not the loop `SHIPPED #914, ON MAIN, DONE`\n\n"+
		"## Phase 3 — Cross-**model** orchestration `#911 ALL MERGED`\n\n"+
		"## Phase 4 — Cross-harness budget accounting `COMPLETE`\n\n"+
		"## Phase 5 — Intent as a live system\n")
	for _, id := range []string{"phase-0", "phase-3", "phase-4", "phase-5"} {
		h, l := find(t, honest, id), find(t, lying, id)
		if !reflect.DeepEqual(h.PRs, l.PRs) {
			t.Errorf("%s: PRs changed when only the status words changed: %v vs %v", id, h.PRs, l.PRs)
		}
	}
}

// Phase 3's body cites #913 -- the CI retirement, which belongs to no phase.
// Sweeping the section body would attribute it to phase-3 and then report
// phase-3 as partly-on-main on the strength of somebody else's work.
func TestBodyReferencesAreNotAttributedToAPhase(t *testing.T) {
	p := find(t, parse(t, b45f917Headings), "phase-3")
	for _, pr := range p.PRs {
		if pr == 913 {
			t.Fatalf("phase-3 picked up #913 from its body prose: PRs = %v", p.PRs)
		}
	}
}

// Absence is a state. `NEW` and a heading with no span both name no pull
// request, and a caller must be able to tell that from "named some, none
// merged" -- see status.PhaseUnknown.
func TestAPhaseMayNameNoPullRequestAtAll(t *testing.T) {
	ps := parse(t, b45f917Headings)
	for _, id := range []string{"phase-4", "phase-5"} {
		if got := find(t, ps, id).PRs; len(got) != 0 {
			t.Errorf("%s PRs = %v, want none", id, got)
		}
	}
}

func TestTitlesDropTheSpanAndTheMarkdown(t *testing.T) {
	if got, want := find(t, parse(t, b45f917Headings), "phase-3").Title, "Cross-model orchestration"; got != want {
		t.Fatalf("title = %q, want %q", got, want)
	}
}

func TestDuplicateNumbersInOneSpanCollapse(t *testing.T) {
	p := find(t, parse(t, "## Phase 1 — x `IN #909, STILL #909`\n"), "phase-1")
	if got, want := p.PRs, []int{909}; !reflect.DeepEqual(got, want) {
		t.Fatalf("PRs = %v, want %v", got, want)
	}
}

// A document naming no phase is an error, never an empty list: a caller
// asking "which phases exist" would read [] as "none" and accept any label.
func TestNoPhasesIsAnErrorNotAnEmptyList(t *testing.T) {
	if _, err := ParseText("# Not a phase plan\n\nnothing here\n"); err == nil {
		t.Fatal("ParseText accepted a document with no phases")
	}
	if _, err := Parse(filepath.Join(t.TempDir(), "absent.md")); err == nil {
		t.Fatal("Parse accepted a missing file")
	}
}

func TestKnownRefusesAnythingThePlanDoesNotName(t *testing.T) {
	ps := parse(t, b45f917Headings)
	if !Known(ps, "phase-0") {
		t.Error("phase-0 is in the plan and was not recognised")
	}
	// The three real offenders from the production tick log.
	for _, bad := range []string{"ph", "delivery-unblock", "phase-plan", "phase-9"} {
		if Known(ps, bad) {
			t.Errorf("%q was accepted as a phase", bad)
		}
	}
}

// The real document, so a rewrite of the plan that breaks the parse fails
// here rather than silently reporting every phase UNKNOWN.
func TestTheRealPlanParses(t *testing.T) {
	ps, err := Parse(filepath.Join("..", "..", "..", "..", "docs", "phase-plan.md"))
	if err != nil {
		t.Fatalf("parsing the real plan: %v", err)
	}
	if len(ps) < 4 {
		t.Fatalf("the real plan parsed to %d phase(s): %v", len(ps), IDs(ps))
	}
	withPRs := 0
	for _, p := range ps {
		if len(p.PRs) > 0 {
			withPRs++
		}
	}
	if withPRs == 0 {
		t.Fatalf("no phase in the real plan names a pull request; every phase would report UNKNOWN: %+v", ps)
	}
}
