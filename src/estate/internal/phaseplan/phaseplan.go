// Package phaseplan is the one reader of docs/phase-plan.md.
//
// WHAT IT TAKES FROM THE PLAN, AND WHAT IT REFUSES TO TAKE. The plan's phase
// headings carry a hand-written status span:
//
//	## Phase 0 — The verifier, not the loop `IN #914, NOT ON MAIN`
//
// This package takes the NUMBERS out of that span and throws the WORDS away.
// The words are a claim a human maintains by hand, and at b45f917 one of the
// four was wrong: the plan said Phase 0 was `NOT ON MAIN` while `git log
// origin/main | grep -c '(#914)'` returned 1. Three of four being right is
// worse than all four being wrong, because it reads as maintained. So the
// only durable fact in that span is which pull requests the phase names;
// whether they are on `main` is a question for `git log`, answered at call
// time by internal/status, never read from here.
//
// WHY ONLY THE HEADING'S SPAN. A phase's body prose cites pull requests that
// are not that phase's work -- Phase 3's body mentions #913, which is the CI
// retirement and belongs to no phase at all. Sweeping `#\d+` across a whole
// section would attribute other phases' work (and plain conversation) to
// whichever heading happened to precede it. The heading's own status span is
// where the plan states, deliberately and in one place, which pull requests
// a phase's work sits in. A phase whose heading names no pull request yields
// an empty PRs slice, and callers must treat that as UNKNOWN rather than as
// evidence of anything -- see internal/status.PhaseUnknown.
//
// WHY ONE PARSER. internal/tick already had to know which phases exist, to
// refuse a tick recording a phase item the plan does not name. Two regexps
// over the same file in two packages is the drift this whole issue
// (agent-estate#1012) exists to remove, so tick.KnownPhases delegates here.
package phaseplan

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Phase is one phase heading in the plan, as the plan itself states it.
type Phase struct {
	// ID is the form a tick log entry and a ledger record use: "phase-0".
	ID string
	// Number is the phase number, for ordering.
	Number int
	// Title is the heading text with the status span and the leading
	// "Phase N —" removed. Presentation only; nothing is decided from it.
	Title string
	// PRs is every pull request number named in the heading's status span,
	// ascending and deduplicated. Empty means the plan names no pull request
	// for this phase -- which is a real state (Phase 4 is `NEW`, Phase 5 has
	// no span at all), not zero PRs merged.
	PRs []int
}

var (
	// A phase heading: "## Phase 3 — Cross-**model** orchestration `...`".
	headingRE = regexp.MustCompile(`(?m)^##\s+Phase\s+(\d+)\b(.*)$`)
	// The trailing backtick span on a heading line, which is where the plan
	// puts its hand-written status. Only its numbers are read.
	statusSpanRE = regexp.MustCompile("`([^`]*)`\\s*$")
	// A pull request or issue reference inside that span.
	numberRE = regexp.MustCompile(`#(\d+)`)
	// Leading separators between "Phase N" and the title proper.
	titleLeadRE = regexp.MustCompile(`^[\s—–-]+`)
)

// Parse reads the plan at path. A plan that cannot be read, or that names no
// phase at all, is an error: a caller asking "which phases exist" must never
// be handed an empty list it would read as "none".
func Parse(path string) ([]Phase, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("phaseplan: cannot read %s to learn which phases exist: %w", path, err)
	}
	ps, err := ParseText(string(b))
	if err != nil {
		return nil, fmt.Errorf("phaseplan: %s: %w", path, err)
	}
	return ps, nil
}

// ParseText is Parse over an in-memory plan, so tests need no file.
func ParseText(src string) ([]Phase, error) {
	var out []Phase
	for _, m := range headingRE.FindAllStringSubmatch(src, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			// Unreachable: the regexp already matched \d+.
			return nil, fmt.Errorf("phase number %q is not a number: %w", m[1], err)
		}
		rest := m[2]
		title := rest
		var prs []int
		if span := statusSpanRE.FindStringSubmatch(rest); span != nil {
			title = strings.TrimSuffix(rest, span[0])
			// Numbers only. span[1] holds words like "NOT ON MAIN"; they are
			// deliberately not looked at -- see the package doc comment.
			seen := map[int]bool{}
			for _, nm := range numberRE.FindAllStringSubmatch(span[1], -1) {
				pr, err := strconv.Atoi(nm[1])
				if err != nil || seen[pr] {
					continue
				}
				seen[pr] = true
				prs = append(prs, pr)
			}
			sort.Ints(prs)
		}
		out = append(out, Phase{
			ID:     "phase-" + m[1],
			Number: n,
			Title:  cleanTitle(title),
			PRs:    prs,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("names no phases, so no phase item can be checked against it")
	}
	return out, nil
}

func cleanTitle(s string) string {
	s = titleLeadRE.ReplaceAllString(strings.TrimSpace(s), "")
	s = strings.ReplaceAll(s, "**", "")
	return strings.TrimSpace(s)
}

// IDs returns the phase ids in plan order -- what a tick log entry and a
// ledger record are checked against.
func IDs(ps []Phase) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.ID)
	}
	return out
}

// Known reports whether id is one of the plan's phases. An unrecognised
// value is never bucketed into a neighbouring phase; callers classify it as
// unknown and keep its literal text.
func Known(ps []Phase, id string) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}
