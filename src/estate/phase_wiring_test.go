package main

import (
	"regexp"
	"strings"
	"testing"
)

// A field nothing writes is a schema change, not a feature. ledger.Record
// gained Phase so that what ACTUALLY RAN could be joined to what we were
// trying to do (agent-estate#1012), and that join exists only if dispatch
// stamps the field on every record it appends. This checks the wiring the
// same way mirror_wiring_test.go checks the mirror's, and with the same
// stated limit: a source check catches the stamp being ABSENT, not the stamp
// being wrong. The live evidence for the latter is in the pull request.

// Every ledger.Record composite literal in main.go's dispatch case.
var recordLiteralRE = regexp.MustCompile(`ledger\.Record\{[^}]*\}`)

func TestEveryDispatchedRecordCarriesThePhaseField(t *testing.T) {
	src := mainSource(t)
	lits := recordLiteralRE.FindAllString(src, -1)
	if len(lits) < 3 {
		t.Fatalf("found %d ledger.Record literal(s) in main.go; the dispatch path has three (failed-to-start, dispatched, outcome)", len(lits))
	}
	for _, lit := range lits {
		if !strings.Contains(lit, "Phase:") {
			t.Errorf("a ledger.Record literal is written without a Phase, so its turn is unattributable:\n  %s", lit)
		}
	}
}

// The phase must be resolved by the ESTATE, from its own flag/env, exactly
// the path --harness= takes -- never read back out of the brief, the agent's
// result, or anything the turn said about itself.
func TestThePhaseIsEstateObservedNotAgentClaimed(t *testing.T) {
	src := mainSource(t)
	if !strings.Contains(src, `strings.HasPrefix(a, "--phase=")`) {
		t.Error("dispatch does not accept --phase=; the phase would have to come from somewhere less trustworthy")
	}
	if !strings.Contains(src, `os.Getenv("ESTATE_PHASE")`) {
		t.Error("dispatch does not read $ESTATE_PHASE, so --phase= has no default the way --harness= does")
	}
	if !strings.Contains(src, "tick.CheckPhaseItem(phaseName") {
		t.Error("a stated phase is not checked against the plan, so a typo like `ph` would reach the ledger")
	}
}

// There is no honest default. Picking one would fabricate an attribution
// indistinguishable from a stated one -- the exact defect the field exists to
// measure -- so an unstated phase stays empty and is counted as unattributed.
func TestAnUnstatedPhaseIsNotDefaultedToARealPhase(t *testing.T) {
	src := mainSource(t)
	for _, bad := range []string{`phaseName = "phase-`, `phaseName == "" {
			phaseName = "phase`} {
		if strings.Contains(src, bad) {
			t.Errorf("dispatch defaults an unstated phase to a real one: %q", bad)
		}
	}
}

// `estate status` must not grow a cache. Everything it reports is derived at
// call time; a stored summary is the drift it exists to remove.
func TestStatusWritesNothing(t *testing.T) {
	src := mainSource(t)
	start := strings.Index(src, `case "status":`)
	if start < 0 {
		t.Fatal("no status case in main.go")
	}
	end := strings.Index(src[start:], `case "tasks"`)
	if end < 0 {
		t.Fatal("could not bound the status case")
	}
	body := src[start : start+end]
	for _, w := range []string{"os.WriteFile", "os.Create", "os.OpenFile", "l.Append"} {
		if strings.Contains(body, w) {
			t.Errorf("the status case calls %s -- it must derive, never store", w)
		}
	}
}
