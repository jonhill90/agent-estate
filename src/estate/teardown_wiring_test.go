package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
)

// WHY THESE TESTS READ SOURCE. The dispatch case is one long inline block
// with no seam a test can drive: reaching the teardown decision means
// launching a real agent subprocess. The decision itself is nonetheless a
// contract agent-estate#1000 states explicitly -- remove on terminal state
// only, never on `unknown` -- and a contract nothing checks is a preference.
// So the wiring is asserted against main.go's own text, the same way
// agents_md_test.go asserts AGENTS.md's claims against the tree, and
// agent-estate#1003's TestDispatchHasNoTmuxControlPath asserts the absence
// of a control path in this same file.
//
// What this can and cannot establish: it proves the guard is present and
// that the removal call is inside it. It does not prove the block runs --
// that is only ever established by a real dispatch, and the pull request
// says which one.

// These read main.go through mirror_wiring_test.go's own mainSource, which
// strips // comments before returning the source. Sharing it is deliberate
// rather than incidental: the teardown block's comments name `wt.Remove()`
// and the sweep call in prose, so a guard reading the raw text would count
// the commentary as call sites and pass for the wrong reason.

// The one call site that deletes a dispatch's own worktree must sit inside
// a terminal-state guard.
func TestDispatchTearsDownOnlyAtATerminalState(t *testing.T) {
	src := mainSource(t)
	i := strings.Index(src, "wt.Remove()")
	if i < 0 {
		t.Fatal("main.go no longer removes the dispatch worktree at all -- that is the leak agent-estate#1000 exists to close")
	}
	if strings.Count(src, "wt.Remove()") != 1 {
		t.Fatalf("expected exactly one teardown call site in main.go, found %d -- a second one is a second place the guard can be forgotten", strings.Count(src, "wt.Remove()"))
	}
	// The guard must be the nearest enclosing condition, not merely present
	// somewhere in the file.
	guard := "if rec.State.Terminal() {"
	g := strings.LastIndex(src[:i], guard)
	if g < 0 {
		t.Fatalf("the teardown call in main.go is not guarded by %q -- a turn recorded `unknown` may still have work nobody has collected", guard)
	}
	between := src[g+len(guard) : i]
	if strings.Contains(between, "\n\t\t}") {
		t.Fatalf("the terminal-state guard closes before the teardown call, so the call is not inside it:\n%s", between)
	}
}

// The states that guard admits are exactly the two the issue names. This is
// ledger's rule, asserted here so that widening Terminal() to include
// `unknown` -- the change that would silently re-arm the danger this guard
// exists to prevent -- fails a test that says why.
func TestTerminalIsCompleteAndFailedOnly(t *testing.T) {
	if !ledger.Complete.Terminal() || !ledger.Failed.Terminal() {
		t.Fatal("a finished turn is not terminal, so its worktree would never be torn down")
	}
	if ledger.Unknown.Terminal() {
		t.Fatal("`unknown` became terminal -- a timed-out turn's worktree would now be eligible for deletion, and unknown is not failed")
	}
	if ledger.Dispatched.Terminal() {
		t.Fatal("`dispatched` became terminal -- a running turn's worktree would be eligible for deletion")
	}
}

// The sweep must actually be called from the dispatch path. A teardown
// mechanism that only a human can invoke is a documentation rule with a
// binary attached -- and the corpse case, which is the one that killed this
// host, is precisely the case where no human is watching.
func TestDispatchRunsTheSweepItself(t *testing.T) {
	src := mainSource(t)
	if !regexp.MustCompile(`sweepWorktrees\(l, repoRoot, true\)`).MatchString(src) {
		t.Fatal("the dispatch path does not run the worktree sweep in apply mode -- a worktree left by a killed dispatch would then wait for someone to notice")
	}
	// And it must run before the pressure gate, so the gate's own worktree
	// ceiling (agent-estate#999) is measured after housekeeping rather than
	// refusing work over worktrees this run was about to remove.
	dispatch := src[strings.Index(src, "\tcase \"dispatch\":"):]
	sweepAt := strings.Index(dispatch, "sweepWorktrees(l, repoRoot, true)")
	gateAt := strings.Index(dispatch, "pressure.Check(l, pressure.Default())")
	if sweepAt < 0 || gateAt < 0 || sweepAt > gateAt {
		t.Fatal("the sweep does not run before the pressure gate, so the gate can refuse a dispatch over worktrees the same run would have removed")
	}
}

// The facts teardown-by-a-third-party needs must be on the record a sweep
// actually reads. `estate sweep-worktrees` reads the ledger's CURRENT view
// -- one record per task, its latest -- so a path carried only by the
// Dispatched record is a path the sweep cannot see.
func TestTheOutcomeRecordCarriesWhatATeardownNeeds(t *testing.T) {
	src := mainSource(t)
	outcome := "rec := ledger.Record{"
	i := strings.Index(src, outcome)
	if i < 0 {
		t.Fatal("cannot find the outcome record in main.go")
	}
	line := src[i : strings.Index(src[i:], "\n")+i]
	for _, field := range []string{"Worktree:", "Branch:"} {
		if !strings.Contains(line, field) {
			t.Fatalf("the outcome record does not carry %s, so a sweep reading the ledger's current view cannot tear this worktree down:\n%s", field, line)
		}
	}
	// Base is set separately, and for every role -- a reviewer's abandoned
	// worktree leaks exactly like an author's, and without Base nothing can
	// tell what the turn committed.
	if !strings.Contains(src, "rec.Base = wt.Base") {
		t.Fatal("the outcome record does not carry the worktree's base commit")
	}
	// It must sit OUTSIDE the role=author block, not inside it: the same
	// nearest-enclosing-block reasoning as the teardown guard above, run in
	// the opposite direction.
	authorBlock := strings.Index(src, "if role == ledger.RoleAuthor {")
	baseAt := strings.Index(src, "rec.Base = wt.Base")
	if authorBlock < 0 || baseAt < authorBlock {
		t.Fatal("cannot locate the role=author block and the base assignment in main.go")
	}
	if !strings.Contains(src[authorBlock:baseAt], "\n\t\t}") {
		t.Fatal("Base is recorded only for role=author -- a reviewer turn's worktree could then never be torn down by a later process")
	}
}
