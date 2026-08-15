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

	result := evaluateLanesSh(string(src))
	if len(result.literal)+len(result.unresolved) == 0 {
		t.Fatalf("parsed zero state= assignments out of %s -- the regex below no longer matches lanes.sh's shape, "+
			"which means this guard is not checking anything; fix the parser, don't ignore this", lanesSh)
	}

	if len(result.unresolved) > 0 {
		t.Errorf("lanes.sh has %d state= assignment(s) this guard cannot statically resolve to a bareword state "+
			"name, so it cannot tell whether AllStates (internal/lane/states.go) covers them: %v -- "+
			"this is the failure mode agent-tui#19 was filed about (state=$(...) command substitution is "+
			"invisible to a bareword regex, so AllStates can be incomplete while this guard stays green). "+
			"Resolve what the assignment yields, or teach the parser to statically resolve it -- do not widen "+
			"the regex until this passes.", len(result.unresolved), result.unresolved)
	}

	if len(result.missing) > 0 {
		t.Errorf("lanes.sh assigns state(s) %v that AllStates (internal/lane/states.go) does not list -- "+
			"a variant can render every state in AllStates and still go silently Unmapped for a real lanes.sh state. "+
			"Add the missing state(s) to AllStates and to every GlyphSet in variants.go.", result.missing)
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
//
// agent-tui#19: that fix still only recognized the bareword shape. lanes.sh
// (via agent-supervisor#149) grew a second shape at the same "state=" seam:
// `state=$(never_busy_or_unknown "$age")`, command substitution. A regex
// requiring `[A-Za-z][A-Za-z0-9_-]*` immediately after `=` simply does not
// match that line at all -- not "matches and gets it wrong", but doesn't
// match -- so the assignment vanished from statesEmittedBy's output with no
// signal anywhere that anything was missed. That is #3's exact failure
// shape one layer down again: AllStates can be incomplete while every test
// checking it stays green, because the parser silently skipped an
// assignment it didn't recognize instead of saying so.
//
// The fix this time is not to widen the bareword class to also match
// `$(...)` -- resolving what an arbitrary shell command substitution
// evaluates to is a real static-analysis problem (it would require
// interpreting the called function, which lanes.sh does not even guarantee
// is defined in the same file), and pretending a wider regex "solves" it
// would just reproduce the defect a third time with a different unmatched
// shape next. Instead, stateAssignStartRe matches every `state=` assignment
// *start*, resolved or not, capturing everything up to the next `;` or
// end of line (or nothing at all, for `state=;` / `state=` at EOL --
// `*` not `+`, so an empty right-hand side is still captured rather than
// falling through stateAssignStartRe entirely the way an unmatched
// command substitution used to; an empty capture is not a bareword either,
// so it is reported unresolved same as any other unrecognized shape,
// instead of reopening the "vanished with no signal" hole this guard
// exists to close). statesEmittedBy then classifies each capture: a
// bareword becomes a literal state (the original behavior); anything else
// -- `$(...)`, `"$x"`, arithmetic, empty, whatever shell can put on the
// right of `=` -- becomes "unresolved" and is reported back to the caller
// so the guard can fail loudly on it, rather than silently dropping it.
var stateAssignStartRe = regexp.MustCompile(`(?:^|[;\s])state=([^;\n]*)`)

// trailingCommentRe strips a shell inline comment (whitespace then `#` to
// end of line) off a captured `state=` value before classifying it. Without
// this, `state=busy # deliberately not scrolled` would capture
// `busy # deliberately not scrolled`, fail bareStateRe, and get reported as
// unresolved -- a false positive regression against the original
// bareword-only regex, which only ever captured identifier characters and
// so never saw a trailing comment at all. The doc above statesEmittedBy
// already says lanes.sh has none of these today and stripping general
// inline comments after real code is a non-goal (full shell tokenizing is
// explicitly out of scope) -- but "doesn't happen today" is not something
// to depend on, per this same guard's own reasoning about drift, so a
// resolvable literal followed by a comment must still resolve.
var trailingCommentRe = regexp.MustCompile(`\s+#.*$`)

// bareStateRe matches the one shape statesEmittedBy can statically resolve
// to a concrete state name: an unquoted, unexpanded identifier, exactly the
// class stateAssignRe (agent-tui#3's fix) originally required inline.
var bareStateRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

// unresolvedAssignment is one `state=` assignment statesEmittedBy found but
// could not statically resolve to a bareword -- e.g. a command substitution.
// line is the trimmed source line it came from, kept for error messages so
// a failing guard points at something a human can find in lanes.sh.
type unresolvedAssignment struct {
	value string // raw text captured after `state=`, e.g. `$(never_busy_or_unknown "$age")`
	line  string // trimmed source line it was found on
}

// statesEmittedBy returns the deduplicated set of state literals lanes.sh's
// source assigns to `state` in first-seen order, plus every `state=`
// assignment it found but could not resolve to one of those literals.
// Exported at package level (lowercase, but a standalone function rather
// than inlined into the test) so it is one thing to point a debugger or a
// future caller at, not logic buried in a test body.
//
// Comments: matched line by line, and any line whose first non-whitespace
// character is `#` is skipped before stateAssignStartRe ever sees it.
// lanes.sh documents its states in header comments (`#   broken   the
// pane's cwd...`) and in prose elsewhere (`# ... not busy, hung, blocked,
// dead, scrolled, or another harness ...`) -- neither of those lines
// contains the literal text `state=` today, so this guard's old behavior
// and new behavior agree on the current file either way. The line-comment
// filter is here anyway, deliberately, because "no comment happens to
// collide today" is not a property worth depending on -- a future header
// rewritten as `# state=broken -> re-home it` would silently inflate the
// parsed count without it, and that is exactly the "passes for the wrong
// reason" failure mode #3 and this guard both exist to close. What this
// does NOT do is treat a `#` mid-line as anything special beyond stripping
// it off the captured value of a `state=` match (trailingCommentRe, below)
// so a resolvable literal followed by a trailing comment (e.g.
// `state=busy # not scrolled`) still resolves to `busy` instead of being
// misreported unresolved. It is still possible to construct a comment that
// itself contains the text `state=` on an otherwise-code line (e.g.
// `foo=bar # state=baz`) and have that misread as a real assignment --
// lanes.sh has none of those today, and disambiguating "is this `#`
// starting a comment or part of a string" in general is full shell
// parsing, which is out of scope (see the package doc above). A whole-line
// comment filter plus stripping a trailing `#...` off just the captured
// value is the cheap, correct-for-lanes.sh middle ground, not a general
// shell tokenizer.
func statesEmittedBy(src string) (literal []string, unresolved []unresolvedAssignment) {
	seen := map[string]bool{}
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		for _, m := range stateAssignStartRe.FindAllStringSubmatch(line, -1) {
			value := strings.TrimSpace(trailingCommentRe.ReplaceAllString(m[1], ""))
			if bareStateRe.MatchString(value) {
				if seen[value] {
					continue
				}
				seen[value] = true
				literal = append(literal, value)
				continue
			}
			unresolved = append(unresolved, unresolvedAssignment{value: value, line: trimmed})
		}
	}
	return literal, unresolved
}

// lanesShEvaluation is the outcome of checking one lanes.sh source's
// state= assignments against AllStates: which resolved literals AllStates
// doesn't know about, and which assignments couldn't be resolved at all.
// Either being non-empty means the guard must fail -- missing is #3's
// original check, unresolved is #19's.
type lanesShEvaluation struct {
	literal    []string // resolved state literals found (regardless of AllStates coverage)
	missing    []string // resolved state literals AllStates does not list
	unresolved []string // source lines with a state= assignment this parser could not resolve
}

// evaluateLanesSh runs statesEmittedBy against src and checks the result
// against AllStates. Factored out of the test body so the mutation-check
// fixtures in TestStatesEmittedBy_* can drive the exact same logic the real
// lanes.sh cross-check uses, without needing an AGENT_SUPERVISOR_REPO
// checkout.
func evaluateLanesSh(src string) lanesShEvaluation {
	literalStates, unresolved := statesEmittedBy(src)

	known := make(map[string]bool, len(AllStates))
	for _, s := range AllStates {
		known[s] = true
	}

	var missing []string
	for _, s := range literalStates {
		if !known[s] {
			missing = append(missing, s)
		}
	}
	sort.Strings(missing)

	var unresolvedLines []string
	for _, u := range unresolved {
		unresolvedLines = append(unresolvedLines, u.line)
	}

	return lanesShEvaluation{literal: literalStates, missing: missing, unresolved: unresolvedLines}
}

// The three tests below are the mutation check agent-tui#19 asks for, run
// against fixtures this test controls rather than against whatever
// agent-supervisor's lanes.sh happens to say this hour (per #19's "Note on
// timing": agent-supervisor#149 may switch to a literal assignment, which
// would remove today's trigger without closing this issue -- the guard
// itself, not that one call site, is what these fixtures pin down). None of
// them touch AGENT_SUPERVISOR_REPO, so they run unconditionally in CI and
// locally.

// TestStatesEmittedBy_DynamicAssignmentGoesRed is the acceptance test #19
// names directly: a lanes.sh-shaped fixture with a `state=$(...)` command
// substitution assigning a state absent from AllStates must make the guard
// fail (unresolved, not silently dropped), not pass while blind to it.
func TestStatesEmittedBy_DynamicAssignmentGoesRed(t *testing.T) {
	fixture := `emit_rows() {
  if [ "$age" -ge "$NEVER_BUSY_AFTER" ]; then
    state=$(never_busy_or_unknown "$age")
  else
    state=busy
  fi
}
`
	result := evaluateLanesSh(fixture)

	if len(result.unresolved) == 0 {
		t.Fatalf("evaluateLanesSh did not flag the state=$(...) assignment as unresolved -- "+
			"got literal=%v missing=%v; the guard would pass while blind to it, which is agent-tui#19 exactly", result.literal, result.missing)
	}
	if !strings.Contains(result.unresolved[0], "state=$(never_busy_or_unknown") {
		t.Errorf("unresolved[0] = %q, want it to reference the state=$(...) line so a human can find it in lanes.sh", result.unresolved[0])
	}

	// "goes red" is the acceptance criterion: assert the guard's own
	// pass/fail condition, the same one TestAllStatesCoversLanesShStates
	// checks, actually fires for this fixture.
	if len(result.missing) == 0 && len(result.unresolved) == 0 {
		t.Fatalf("guard would report PASS on a fixture containing an unresolved dynamic assignment -- that is exactly the defect #19 was filed about")
	}
}

// TestStatesEmittedBy_LiteralAssignmentsStillWork is #19's second
// requirement: the existing literal-assignment coverage (agent-tui#3's
// fix -- both the trailing-assignment shape and the inline `;`-separated
// shape on one line) must keep working unchanged.
func TestStatesEmittedBy_LiteralAssignmentsStillWork(t *testing.T) {
	fixture := `emit_rows() {
  if [ "$age" -ge "$HUNG_AFTER" ]; then state=hung; else state=busy; fi
  state=scrolled # deliberately not "stale"
}
`
	literal, unresolved := statesEmittedBy(fixture)

	if len(unresolved) != 0 {
		t.Fatalf("literal-only fixture produced unresolved assignments: %+v -- literal parsing regressed", unresolved)
	}

	want := []string{"hung", "busy", "scrolled"}
	if strings.Join(literal, ",") != strings.Join(want, ",") {
		t.Errorf("statesEmittedBy(literal fixture) = %v, want %v (first-seen order, deduplicated)", literal, want)
	}
}

// TestStatesEmittedBy_TrailingCommentDoesNotFalselyUnresolve guards the
// independent review's first finding on this PR: capturing everything up
// to `;`/EOL (so `state=$(...)` isn't silently dropped) means a bareword
// assignment followed by a trailing shell comment, e.g. `state=busy # ...`,
// would sweep the comment text into the captured value and wrongly report
// it unresolved -- a regression the original bareword-only regex never had,
// since it only ever captured identifier characters. trailingCommentRe
// exists specifically to strip that off before classification; this test
// pins that down independent of TestStatesEmittedBy_LiteralAssignmentsStillWork
// so a future edit can't silently drop the comment-stripping and still
// pass the "no comments" fixture.
func TestStatesEmittedBy_TrailingCommentDoesNotFalselyUnresolve(t *testing.T) {
	literal, unresolved := statesEmittedBy("state=busy # deliberately not scrolled\n")

	if len(unresolved) != 0 {
		t.Errorf("statesEmittedBy reported unresolved=%+v for a bareword assignment with a trailing comment -- "+
			"the comment should be stripped before classification, not swept into the value", unresolved)
	}
	if want := []string{"busy"}; strings.Join(literal, ",") != strings.Join(want, ",") {
		t.Errorf("statesEmittedBy(%q) literal = %v, want %v", "state=busy # deliberately not scrolled", literal, want)
	}
}

// TestStatesEmittedBy_EmptyAssignmentGoesUnresolved guards the independent
// review's second finding: `state=;` or `state=` at end of line (a valid
// bash empty-string assignment) has zero characters after `=`, and the
// original `[^;\n]+` capture (one-or-more) simply didn't match those lines
// at all -- reproducing this issue's exact "vanished with no signal"
// failure mode for a different unmatched shape. `[^;\n]*` (zero-or-more)
// fixes that: an empty capture isn't a bareword either, so it's reported
// unresolved like any other unrecognized shape instead of disappearing.
func TestStatesEmittedBy_EmptyAssignmentGoesUnresolved(t *testing.T) {
	for _, src := range []string{"state=;\n", "state=\n"} {
		literal, unresolved := statesEmittedBy(src)
		if len(literal) != 0 {
			t.Errorf("statesEmittedBy(%q) literal = %v, want none -- empty is not a resolvable state name", src, literal)
		}
		if len(unresolved) == 0 {
			t.Errorf("statesEmittedBy(%q) reported nothing at all -- an empty state= assignment vanished with no "+
				"signal, which is the exact defect agent-tui#19 was filed about, just for a different shape", src)
		}
	}
}

// TestAllStatesCoversLanesShStates_KnownStatesPass is #19's third
// requirement: a lanes.sh whose states all appear in AllStates must still
// pass -- a guard that fails on everything (including its own fix) is not
// a guard. Uses every literal in AllStates so this also doubles as a check
// that the guard doesn't false-positive on the full real enumeration.
func TestAllStatesCoversLanesShStates_KnownStatesPass(t *testing.T) {
	var b strings.Builder
	b.WriteString("emit_rows() {\n")
	for _, s := range AllStates {
		b.WriteString("  state=" + s + "\n")
	}
	b.WriteString("}\n")

	result := evaluateLanesSh(b.String())

	if len(result.missing) != 0 {
		t.Errorf("evaluateLanesSh reported missing states %v for a fixture built entirely from AllStates -- false positive", result.missing)
	}
	if len(result.unresolved) != 0 {
		t.Errorf("evaluateLanesSh reported unresolved assignments %v for an all-literal fixture -- false positive", result.unresolved)
	}
}
