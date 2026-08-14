package lane

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestAllStatesCoversLanesShStates is agent-tui#3's actual deliverable: a
// guard that fails when lanes.sh emits a state AllStates does not know,
// rather than one that re-asserts AllStates's own contents back at itself.
// TestEveryVariantNamesEveryState in lane_test.go proves every GlyphSet
// covers AllStates; this proves AllStates covers lanes.sh. Neither test can
// stand in for the other -- #3 happened precisely because only the first
// existed.
//
// This repo deliberately imports no agent-supervisor internals (README:
// "It does not import supervisor internals"), so this cannot import a Go
// symbol to check against. What it CAN do without breaking that boundary is
// read lanes.sh's own source as data and parse the state literals its
// classifier actually assigns -- the same posture scripts/verify-lanes-
// unaffected.sh already takes toward AGENT_SUPERVISOR_REPO, reading the
// checkout as a file tree rather than a package.
//
// Skips (does not fail) when AGENT_SUPERVISOR_REPO is unset or lanes.sh
// cannot be found there -- this repo must still build and test standalone,
// per its own "imports no supervisor internals" boundary, on a machine or
// CI job with no agent-supervisor checkout at hand. CI is wired (see
// .github/workflows/ci.yml) to set AGENT_SUPERVISOR_REPO to a sibling
// checkout of jonhill90/agent-supervisor specifically so this guard is not
// merely available but actually enforced on every push and PR, not just
// when a developer happens to have both repos checked out locally.
func TestAllStatesCoversLanesShStates(t *testing.T) {
	repo := os.Getenv("AGENT_SUPERVISOR_REPO")
	if repo == "" {
		t.Skip("AGENT_SUPERVISOR_REPO not set -- skipping the lanes.sh cross-check; " +
			"set it to an agent-supervisor checkout to run it (CI does this automatically)")
	}
	lanesSh := filepath.Join(repo, "scripts", "supervisor", "lanes.sh")
	src, err := os.ReadFile(lanesSh)
	if err != nil {
		t.Skipf("could not read %s: %v -- skipping the lanes.sh cross-check", lanesSh, err)
	}

	got := statesEmittedBy(string(src))
	if len(got) == 0 {
		t.Fatalf("parsed zero state= assignments out of %s -- the regex below no longer matches lanes.sh's shape, "+
			"which means this guard is not checking anything; fix the parser, don't ignore this", lanesSh)
	}

	known := make(map[string]bool, len(AllStates))
	for _, s := range AllStates {
		known[s] = true
	}

	var missing []string
	for _, s := range got {
		if !known[s] {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("lanes.sh assigns state(s) %v that AllStates (internal/lane/states.go) does not list -- "+
			"a variant can render every state in AllStates and still go silently Unmapped for a real lanes.sh state. "+
			"Add the missing state(s) to AllStates and to every GlyphSet in variants.go.", missing)
	}
}

// stateAssignRe matches lanes.sh's own shape for setting the classifier's
// result variable, e.g. `state=busy`, `      state=menu-blocked`, or two on
// one line as `then state=hung; else state=busy; fi`. Every branch in
// emit_rows's state machine (see lanes.sh) uses this exact bareword form --
// no quoting, no variable expansion -- which is what makes parsing it as
// data reliable rather than a heuristic.
//
// The original version of this regex was `^\s*state=IDENT\s*$`, anchored to
// the whole line. lanes.sh:383 assigns two states on one line --
// `if [ "$age" -ge "$HUNG_AFTER" ]; then state=hung; else state=busy; fi` --
// and the end-of-line anchor never saw either of them. A guard that reports
// 10 states out of 13 still went green, because TestAllStatesCoversLanesShStates
// only fails on a MISSING state, and `busy`/`hung`/`broken` were never
// offered to it in the first place: this is agent-tui#3 again, one layer
// down, in the guard #3 asked for.
//
// The fix: require `state=IDENT` to start a shell statement -- preceded by
// start-of-line, whitespace, or `;` -- rather than requiring it to END the
// line. That covers both the trailing-assignment shape (`state=scrolled`)
// and the inline-`;`-separated shape (`then state=hung; else state=busy;`)
// with one pattern, because both are "state=" starting a new statement; only
// the terminator differs, and this pattern does not care what the terminator
// is.
var stateAssignRe = regexp.MustCompile(`(?:^|[;\s])state=([A-Za-z][A-Za-z0-9_-]*)`)

// statesEmittedBy returns the deduplicated set of state literals lanes.sh's
// source assigns to `state`, in first-seen order. Exported at package level
// (lowercase, but a standalone function rather than inlined into the test)
// so it is one thing to point a debugger or a future caller at, not logic
// buried in a test body.
//
// Comments: matched line by line, and any line whose first non-whitespace
// character is `#` is skipped before stateAssignRe ever sees it. lanes.sh
// documents its states in header comments (`#   broken   the pane's cwd...`)
// and in prose elsewhere (`# ... not busy, hung, blocked, dead, scrolled,
// or another harness ...`) -- neither of those lines contains the literal
// text `state=` today, so this guard's old behavior and new behavior agree
// on the current file either way. The line-comment filter is here anyway,
// deliberately, because "no comment happens to collide today" is not a
// property worth depending on -- a future header rewritten as
// `# state=broken -> re-home it` would silently inflate the parsed count
// without it, and that is exactly the "passes for the wrong reason" failure
// mode #3 and this guard both exist to close. What this does NOT do is
// strip inline trailing comments after real code on the same line (e.g.
// `foo=bar # state=baz`) -- lanes.sh has none of those today, matching this
// guard's non-goal of full shell parsing (see the package doc above); a
// whole-line comment filter is the cheap, correct-for-lanes.sh middle
// ground, not a general shell tokenizer.
func statesEmittedBy(src string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		for _, m := range stateAssignRe.FindAllStringSubmatch(line, -1) {
			s := m[1]
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
