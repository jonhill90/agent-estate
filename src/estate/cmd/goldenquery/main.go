// Command goldenquery is the runner for agent-estate#1023: it executes
// every case in internal/knowledge/goldenset's fixed golden set against
// a REAL, already-compiled `estate knowledge` index via the real
// `estate knowledge query` command -- never a reimplementation of its
// ranking, never an LLM judge, never a latency proxy -- and reports
// hits/total against the council's own acceptance criterion:
//
//	pass a case only if `estate knowledge query` returns the case's
//	pre-recorded authoritative source in the first 3 cited results
//	and the command's own state is "matched" (exit 0) -- or, for a
//	case whose recorded answer is "nothing answers this", pass only
//	if the command's own state is "no_match" (exit 1).
//
// This binary shells out to the estate CLI (-bin, default "estate" on
// PATH) rather than importing internal/knowledge's Query function
// directly, so that it measures the same command surface a human or
// another agent would actually run, and so this runner keeps compiling
// on a checkout where `estate knowledge query`/`get` (#1022) have not
// yet landed -- it has no compile-time dependency on that code, only a
// runtime one on the binary it is pointed at.
//
// agent-estate#1040: #1037's publishability enforcement means one run of
// the golden set no longer measures one property. The default mode of
// `estate knowledge query` withholds private items, and 11 of the set's
// 15 cases expect a private identifier (vault-fact, corpus-parameter and
// loops-research are all classified private by #1030; only github-stars
// is publishable) -- so a default-mode-only run reports a policy fact
// (how much of the index a caller who may not see private material can
// reach) dressed up as a ranking score. This runner therefore always
// runs every case TWICE, once in default mode and once with --private,
// and reports two independent, separately labelled numbers:
//
//   - retrieval score  -- private mode, comparable to the 12/15 baseline
//     measured on 6f28626 before enforcement landed. This is the ranking
//     quality signal #1023 was built to produce.
//   - publishable-only score -- default mode, answering a different and
//     also real question: how much can a caller who may not see private
//     material actually find? A low number here is expected and correct
//     whenever most of the golden set's answers are private -- it is not
//     a ranking regression.
//
// Neither number stands in for the other. Do not collapse them into one
// printed "score" -- that is the exact conflation this runner exists to
// prevent (see #1040).
//
// agent-estate#1073: a THIRD, separately labelled pair of numbers reports
// internal/knowledge/goldenset's natural-language stratum -- twelve
// questions phrased the way a dispatched lane actually asks them, targets
// chosen from each answer's own section title before any query ran (#1069).
// cases.json measures whether retrieval can find a KNOWN item; this
// stratum measures whether a caller who does not already know the answer
// LANDS on it -- a different property, run in default (public) mode only,
// reporting both top-3 and top-10 hit rates. #1069 settled that this
// number must never be averaged with cases.json's two: report all three
// separately, leading with the weakest.
//
// agent-estate#1077: this stratum is itself run TWICE over the same
// twelve cases -- unscoped (the question exactly as written) and scoped
// ("source:repo-docs " prepended to every question, the same source:
// filter #1071/#1069 wired into Query). Source scoping is what took this
// stratum from roughly 4-5/12 to roughly 8-9/12 by hand, and that gain had
// no ratchet before this: nothing would fail if source: filtering
// silently stopped narrowing results, since the unscoped line alone would
// not move. Both lines print always; neither replaces or averages with
// the other.
//
// agent-estate#1162: the scoped run above moved from default/public mode
// to --private. #1077's original scoped arm ran the filter in the one mode
// it provably cannot matter: default/public has only two publishable
// sources, so source:repo-docs can only ever remove github-stars items,
// and github-stars never outranked repo-docs on any of these twelve
// repo-oriented questions -- the public scoped and unscoped arms were
// measured identical on all twelve cases, at every rank, on c8e0b59.
// --private has seven competing sources, so the same filter has real work
// to do there. See runNaturalStratum's own doc comment for the argued cost
// of this change (public-mode source: scoping is now unguarded).
//
// agent-estate#1111: a FOURTH, separately labelled number reports
// internal/knowledge/goldenset's github-stars stratum -- #1063's own
// comment measured that repo-docs (#1073/#1077's own stratum) is only 23%
// of the public store (118 of 509 items) and github-stars the other 77%
// (391 of 509), so a github-stars regression moved none of the numbers
// above it. Eight cases, each targeting a specific starred repo, chosen
// from the repo's own description before any query ran, run unscoped
// only in default (public) mode -- #1063's own comment measured that
// source: scoping does not move this stratum, unlike repo-docs. This
// number is never averaged with cases.json's, the repo-docs stratum's, or
// the two mode scores below it.
//
// agent-estate#1133: the "publishable-only score" line this runner used
// to print (default-mode hits over ALL 17 of cases.json's cases) was a
// perfect result rendered as a failure. 11 of the 17 cases have an
// ExpectedSource internal/knowledge/classify.go classifies private
// (vault-fact, corpus-parameter, loops-research) -- those cases CANNOT be
// found in default mode by construction, disclosure policy, not ranking,
// keeps them out (see internal/knowledge/disclosure.go). Counting them in
// the denominator reported "5/17 = 29%" for a run that in fact found
// every one of the 5 publishable-reachable cases (github-stars,
// repo-docs) it was possible to find. This runner now reports that
// reachable-set score directly -- hits over the reachable subset only --
// and prints the excluded count and reason on the same line, per the
// issue's own requirement that a corrected number must not hide how much
// of the stratum was skipped (a bare "5/5" with the exclusion unstated
// would be the same defect inverted). The 17-total denominator and the
// two counts (11 private, 1 none-01) are still printed; nothing is
// silently dropped from the report, only from the score's own arithmetic.
// none-01 (the single ExpectedSource=="none" case) is excluded from the
// reachable-set score on its own line, separately, rather than folded
// into either the reachable numerator or the excluded-private count: it
// is not a disclosure exclusion (a no_match case has no identifier to
// disclose) and its default-mode outcome is reported honestly rather than
// silently counted as a win for the reachable score.
//
// agent-estate#1066: three prior tickets each disclosed an accepted cost in
// prose and had it rediscovered downstream as a surprise (#1043's vault
// bodies, #1060's dropped floor, the same drop's control contamination).
// This runner now carries a RATCHET (buildRatchets, below) over a subset of
// the lines it already measures: a line included there fails the whole run
// (exit 1, distinct from the operational exit 2) if it drops below a floor
// recorded, with its accepted-cost reason, in the assertion message itself
// -- never only in a PR body. Two known-drifting lines (natural-language
// top-10, both unscoped and scoped -- agent-estate#1112 measured them
// moving with the live corpus alone, no code change) and both term-overlap
// lines (fixture-honesty signals, not quality ones) are deliberately NOT
// ratcheted; buildRatchets' own doc comment is the one place that list and
// its reasoning are allowed to live.
//
// agent-estate#1152: the floor each ratcheted line compares against is
// derived from a fixed miss budget (ratchet.maxMisses) and the CURRENT
// total, not a hardcoded hit count -- a hardcoded floor tolerated more and
// more misses every time a passing case was added to the stratum it
// guarded (agent-estate#1150 grew the private retrieval stratum 17->22
// cases and silently took its floor-16 ratchet from tolerating 1 miss to
// tolerating 6, with every line still printing [OK]). See ratchet.ok's own
// doc comment for the comparison and buildRatchets' for each line's budget.
//
// Usage:
//
//	go run ./cmd/goldenquery [-bin estate] [-v]
//
// Exit code is 0 when the measurement ran to completion and every
// ratcheted line held at or above its accepted floor. Exit code is 1 when
// the measurement ran but a ratcheted line regressed below its floor --
// distinct from operational failure; the ratchet section printed at the end
// of the report names which line and why that floor was accepted. A low
// score on a line this runner does NOT ratchet is still a successful
// measurement and does not affect the exit code on its own -- see
// buildRatchets' doc comment for exactly which lines those are. Exit code
// is 2 only when the measurement could not be attempted at all (binary not
// found, every single case errored out before returning a real state in
// EITHER mode, or the golden set itself failed to load).
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

// matchHeader parses one of Query's own printed match header lines --
// see printKnowledgeQuery in main.go: "[%s] %s (score %d: %s)". Never
// re-derives a score; only reads what the real command already printed.
var matchHeader = regexp.MustCompile(`^\[(.+)\] (\S+) \(score (\d+): (.*)\)$`)

// parsedMatch is one Match this runner recovered from the real CLI's own
// stdout -- ID/Source straight off the header line, Permalink from the
// line the CLI prints two lines below it (Tier1 sits between them).
type parsedMatch struct {
	ID        string
	Source    string
	Score     int
	Permalink string
}

// runQuery execs `<bin> knowledge query [--private] <question>` and
// returns its raw stdout, stderr and exit code -- never parsed logic
// here, just the subprocess boundary, so parseMatches (below) is
// independently testable against a captured string. private selects
// which of #1040's two measurement modes this invocation belongs to;
// it is never inferred, only passed straight through to the real CLI's
// own opt-in --private flag (agent-estate#1033).
func runQuery(bin, question string, private bool) (stdout string, exitCode int, err error) {
	args := []string{"knowledge", "query"}
	if private {
		args = append(args, "--private")
	}
	args = append(args, question)
	cmd := exec.Command(bin, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	code := 0
	if exitErr, ok := runErr.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if runErr != nil {
		return "", -1, fmt.Errorf("could not run %s knowledge query: %w (stderr: %s)", bin, runErr, strings.TrimSpace(errBuf.String()))
	}
	return outBuf.String(), code, nil
}

// parseMatches recovers the ranked Match list from Query's own printed
// stdout -- the same three-line-per-match shape printKnowledgeQuery
// (main.go) emits: header, tier1, permalink, blank.
func parseMatches(stdout string) []parsedMatch {
	lines := strings.Split(stdout, "\n")
	var out []parsedMatch
	for i, line := range lines {
		m := matchHeader.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		score, _ := strconv.Atoi(m[3])
		permalink := ""
		if i+2 < len(lines) {
			permalink = strings.TrimSpace(lines[i+2])
		}
		out = append(out, parsedMatch{ID: m[1], Source: m[2], Score: score, Permalink: permalink})
	}
	return out
}

// result is one case's outcome, kept for the final report.
type result struct {
	c        goldenset.Case
	pass     bool
	exitCode int
	got      []parsedMatch
	detail   string // filled on failure or runner error
}

// isPublishableReachable reports whether source is one of the two
// ExpectedSource values `estate knowledge query`'s default (public) mode
// can ever return a match for -- agent-estate#1133's reachable set. This
// deliberately duplicates internal/knowledge/classify.go's own
// github-stars/repo-docs "safe to default public" table rather than
// importing it: cmd/goldenquery has no compile-time dependency on
// internal/knowledge (see this file's own top-of-file doc comment for
// why), so the two-source list is repeated here, not shared. If
// classify.go's table ever grows a third publishable source, this
// function goes stale silently -- there is no test tying the two
// together, so re-check classify.go directly before trusting this list.
func isPublishableReachable(source goldenset.ExpectedSource) bool {
	switch source {
	case goldenset.SourceGithubStars, goldenset.SourceRepoDocs:
		return true
	default:
		return false
	}
}

func evaluate(c goldenset.Case, stdout string, exitCode int) result {
	r := result{c: c, exitCode: exitCode}

	if c.ExpectedSource == goldenset.SourceNone {
		// #1019's absence path: pass only if the CLI's own state was
		// genuinely no_match (exit 1), never index_missing/unreadable
		// (exit 2) and never a spurious match (exit 0).
		if exitCode == 1 && strings.Contains(stdout, "no item matches") {
			r.pass = true
			return r
		}
		r.got = parseMatches(stdout)
		r.detail = fmt.Sprintf("expected no_match (exit 1), got exit %d", exitCode)
		return r
	}

	if exitCode != 0 {
		r.detail = fmt.Sprintf("expected a matched result (exit 0, status=ok), got exit %d", exitCode)
		return r
	}

	matches := parseMatches(stdout)
	r.got = matches
	top3 := matches
	if len(top3) > 3 {
		top3 = top3[:3]
	}
	for _, m := range top3 {
		if strings.HasSuffix(m.Permalink, c.ExpectedIdentifier) {
			r.pass = true
			return r
		}
	}
	r.detail = "expected identifier not in first 3 cited results"
	return r
}

// firstMatchRank returns the 1-based rank of the first returned match whose
// Permalink carries c's ExpectedIdentifier as a suffix, scanning the FULL
// returned list (up to knowledge.QueryLimit, currently 10) rather than a
// top-3 slice -- agent-estate#1073's natural-language stratum reports
// top-3 AND top-10 hit rates from the same run, so the rank has to be
// found once against the whole list and then compared against both
// cutoffs, never re-queried per cutoff. Returns 0 if no returned match
// carries the identifier.
func firstMatchRank(c goldenset.Case, matches []parsedMatch) int {
	for i, m := range matches {
		if strings.HasSuffix(m.Permalink, c.ExpectedIdentifier) {
			return i + 1
		}
	}
	return 0
}

// naturalResult is one natural-language-stratum case's outcome: its found
// rank (0 if never returned) and whether the query itself ran to
// completion at all.
type naturalResult struct {
	c    goldenset.Case
	rank int
	ran  bool
}

// scopedQuestion is agent-estate#1077's own primitive: it builds the
// question text runStratum actually sends for one of its runs over a
// fixture. Unscoped returns question unchanged; scoped prepends
// "source:<source> " -- the same source: tag filter #1071/#1069 wired
// into Query. source names which source: tag to scope on -- generalised
// in agent-estate#1111 from a hardcoded "repo-docs" so the same primitive
// serves both the repo-docs stratum (#1073) and the github-stars stratum
// (#1111) rather than being duplicated per source. Kept as its own pure
// function, not inlined into the loop, so the exact scoping behaviour is
// independently testable without shelling out to a real binary.
func scopedQuestion(question, source string, scoped bool) string {
	if !scoped {
		return question
	}
	return "source:" + source + " " + question
}

// runNaturalStratum runs agent-estate#1073's checked-in natural-language
// fixture (internal/knowledge/goldenset.LoadNatural) once per case. The
// unscoped run is always default (public) mode -- never --private, and
// never merged with cases.json's own score: #1069 settled that the two
// measure different things and must be reported separately, weaker one
// leading.
//
// agent-estate#1077 originally ran the scoped arm in public mode too
// ("source:repo-docs " prepended, still default/public query). agent-estate#1162
// found that arm measured scoping in the one mode it provably cannot
// matter: default/public mode has only two publishable sources
// (repo-docs, github-stars, agent-estate#1162's own comment), so
// source:repo-docs can only ever remove github-stars items, and for these
// twelve repo-oriented questions github-stars never entered the unscoped
// top-10 -- the scoped and unscoped public arms were measured identical on
// all twelve cases, at every ordinal rank, on c8e0b59. scoped now runs
// --private instead: private mode has seven competing sources (corpus and
// vault items genuinely outrank repo-docs on some of these questions), so
// the filter has real work to do and the line can actually fail. This is
// the "replace" side of agent-estate#1162's own undecided question -- see
// this file's own top-of-file doc comment and buildRatchets' doc comment
// for the argued cost: public-mode source: scoping is now entirely
// unguarded (harmless today, since nothing can move it; not harmless if
// github-stars ever begins competing on repo-docs questions, at which
// point this decision should be revisited with fresh evidence).
// agent-estate#1209: results is the per-case outcome of THIS call's own run
// (unscoped or scoped, whichever scoped selects) -- main needs both calls'
// raw results, not just the aggregated counts, so checkExemptions can pair
// an exempt case's unscoped rank against its own scoped rank and verify the
// divergence it claims actually holds this run, not merely once when the
// case was authored.
func runNaturalStratum(w *bufio.Writer, bin string, verbose bool, scoped bool) (top3Hits, top10Hits, total int, ranAtLeastOne bool, overlapMean float64, overlapMeasured int, results []naturalResult) {
	cases, err := goldenset.LoadNatural()
	if err != nil {
		fmt.Fprintln(w, "goldenquery: natural-language stratum:", err)
		return 0, 0, 0, false, 0, 0, nil
	}
	label := "default/public, unscoped"
	if scoped {
		label = "--private, scoped source:repo-docs prepended -- agent-estate#1162, replaces agent-estate#1077's public-mode scoped arm"
	}
	fmt.Fprintf(w, "=== natural-language stratum (%s -- agent-estate#1073) ===\n", label)
	results, ranAtLeastOne = runStratum(w, bin, verbose, scoped, scoped, cases, "repo-docs")
	// agent-estate#1209: the UNSCOPED arm's own tally excludes cases marked
	// UnscopedExempt -- see goldenset.Case.UnscopedExempt's doc comment and
	// checkExemptions below for what earns that exclusion. The SCOPED arm
	// excludes nothing: an exempt case is exempt from the unscoped ratchet
	// only, and still counts as an ordinary hit (it must, by the earned
	// claim) toward the scoped one.
	var exclude func(goldenset.Case) bool
	if !scoped {
		exclude = func(c goldenset.Case) bool { return c.UnscopedExempt }
	}
	top3Hits, top10Hits, total = tallyNatural(results, exclude)
	overlapMean, overlapMeasured, _ = meanOverlap(cases)
	return
}

// runStarStratum runs agent-estate#1111's checked-in github-stars
// natural-language fixture (internal/knowledge/goldenset.LoadStars) once
// per case, in default (public) mode only -- same discipline as
// runNaturalStratum: never --private, and never merged with any other
// stratum's own score. #1063's own comment measured that scoping "bought
// stars nothing" (7/8 -> 7/8, against repo-docs' 4/12 -> 8/12), so unlike
// the repo-docs stratum this one is run unscoped only -- a scoped run
// would print a second number this issue's own evidence says will not
// move, and goldenquery prints one line for this stratum, not four.
func runStarStratum(w *bufio.Writer, bin string, verbose bool) (top3Hits, top10Hits, total int, ranAtLeastOne bool, overlapMean float64, overlapMeasured int) {
	cases, err := goldenset.LoadStars()
	if err != nil {
		fmt.Fprintln(w, "goldenquery: github-stars stratum:", err)
		return 0, 0, 0, false, 0, 0
	}
	fmt.Fprintln(w, "=== github-stars stratum (default/public, unscoped -- agent-estate#1111) ===")
	var results []naturalResult
	results, ranAtLeastOne = runStratum(w, bin, verbose, false, false, cases, "github-stars")
	// This stratum has no exemption concept (agent-estate#1209's exemption
	// is scoped-diagnostic natural-language cases only) -- tally every case.
	top3Hits, top10Hits, total = tallyNatural(results, nil)
	overlapMean, overlapMeasured, _ = meanOverlap(cases)
	return
}

// runStratum is the shared run loop behind runNaturalStratum and
// runStarStratum (generalised in agent-estate#1111 from runNaturalStratum's
// original repo-docs-only body): it runs every case in cases once, scoping
// each question's source: tag to sourceTag when scoped is set, and returns
// every case's own naturalResult. Neither caller reuses the other's result,
// and neither result is ever averaged with cases.json's or any other
// stratum's own score.
//
// agent-estate#1162: private selects the query mode independently of
// scoped -- runStarStratum always passes false/false (unscoped, public,
// unchanged since #1111); runNaturalStratum's unscoped run passes
// false/false (still public, unchanged since #1073) and its scoped run now
// passes true/true (private, replacing #1077's public-mode scoped arm,
// which had no competing source to filter out and so could never move --
// see runNaturalStratum's own doc comment).
//
// agent-estate#1209: this used to tally and return top3Hits/top10Hits/total
// itself. It now returns the raw per-case results and leaves tallying to
// tallyNatural, called separately by each caller -- runNaturalStratum's
// unscoped call needs to exclude UnscopedExempt cases from its own tally
// while still running (and printing) them, and the exemption can only be
// EARNED by comparing this call's own results against the scoped call's,
// which requires both raw result sets to survive past this function.
func runStratum(w *bufio.Writer, bin string, verbose bool, scoped bool, private bool, cases []goldenset.Case, sourceTag string) (results []naturalResult, ranAtLeastOne bool) {
	for _, c := range cases {
		stdout, exitCode, err := runQuery(bin, scopedQuestion(c.Question, sourceTag, scoped), private)
		if err != nil {
			results = append(results, naturalResult{c: c})
			fmt.Fprintf(w, "[ERROR] %s -- %q: %s\n\n", c.ID, c.Question, err)
			continue
		}
		ranAtLeastOne = true
		matches := parseMatches(stdout)
		rank := 0
		if exitCode == 0 {
			rank = firstMatchRank(c, matches)
		}
		results = append(results, naturalResult{c: c, rank: rank, ran: true})

		hit3 := rank >= 1 && rank <= 3
		hit10 := rank >= 1 && rank <= 10
		if verbose || !hit10 {
			status := "MISS (not in top 10)"
			switch {
			case hit3:
				status = "HIT top-3"
			case hit10:
				status = fmt.Sprintf("HIT top-10 only (rank %d)", rank)
			}
			fmt.Fprintf(w, "[%s] %s -- %q\n", status, c.ID, c.Question)
			fmt.Fprintf(w, "  expected: %s (%s)\n", c.ExpectedIdentifier, c.ExpectedSource)
			// agent-estate#1115: print this case's own term overlap right
			// next to its result, not only in the stratum-level mean --
			// a reader deciding whether a HIT is evidence of ranking
			// quality or near self-retrieval needs the per-case number,
			// not just the average.
			if frac, ok := overlapFraction(c.Question, c.TargetText); ok {
				fmt.Fprintf(w, "  term overlap vs target_text: %.0f%%\n", frac*100)
			}
			if !hit10 {
				if exitCode != 0 {
					fmt.Fprintf(w, "  detail:   exit %d\n", exitCode)
				}
				for i, m := range matches {
					if i >= 10 {
						break
					}
					fmt.Fprintf(w, "  got[%d]:   [%s] %s score=%d permalink=%s\n", i, m.ID, m.Source, m.Score, m.Permalink)
				}
			}
			fmt.Fprintln(w)
		}
	}

	return results, ranAtLeastOne
}

// tallyNatural turns a raw runStratum result set into top-3/top-10 hit
// counts, optionally skipping any case for which exclude returns true --
// agent-estate#1209's mechanism for keeping an UnscopedExempt case IN the
// run (queried, printed, checked by checkExemptions below) but OUT of the
// unscoped ratchet's own numerator/denominator. exclude may be nil, in
// which case every result counts -- runStarStratum and the scoped
// natural-language call both use this stratum's true, un-exempted total.
func tallyNatural(results []naturalResult, exclude func(goldenset.Case) bool) (top3Hits, top10Hits, total int) {
	for _, r := range results {
		if exclude != nil && exclude(r.c) {
			continue
		}
		total++
		if r.rank >= 1 && r.rank <= 3 {
			top3Hits++
		}
		if r.rank >= 1 && r.rank <= 10 {
			top10Hits++
		}
	}
	return top3Hits, top10Hits, total
}

// exemptionViolation is one UnscopedExempt case whose earned-divergence
// claim did not hold this run -- see goldenset.Case.UnscopedExempt's doc
// comment for why a self-declared flag alone is not evidence.
type exemptionViolation struct {
	id     string
	reason string
}

// checkExemptions is agent-estate#1209's guardrail 1: it re-measures, every
// run, both halves of the divergence claim an UnscopedExempt case makes --
// that it MISSES top-3 on the real unscoped arm AND HITS top-3 on the real
// scoped arm -- rather than trusting the field once at authoring time. A
// case that stops meeting either half is reported as a violation, and main
// fails the whole run over it (exit 1, same severity as a ratchet failure),
// which is what makes exempting a case cost something: parking an ordinary
// miss here, or an exemption that has gone stale because the case now hits
// unscoped or has stopped hitting scoped, cannot pass silently.
func checkExemptions(unscoped, scoped []naturalResult) []exemptionViolation {
	scopedByID := make(map[string]naturalResult, len(scoped))
	for _, r := range scoped {
		scopedByID[r.c.ID] = r
	}
	var out []exemptionViolation
	for _, u := range unscoped {
		if !u.c.UnscopedExempt {
			if u.c.ExemptReason != "" {
				out = append(out, exemptionViolation{u.c.ID, "carries exempt_reason but unscoped_exempt is not set -- a reason with no exemption is dead text, either set the flag or remove the reason"})
			}
			continue
		}
		if u.c.ExemptReason == "" {
			out = append(out, exemptionViolation{u.c.ID, "unscoped_exempt is true but exempt_reason is empty -- every exemption needs its own recorded evidence"})
		}
		if u.rank >= 1 && u.rank <= 3 {
			out = append(out, exemptionViolation{u.c.ID, fmt.Sprintf("claims exemption from the unscoped ratchet but HITS top-3 unscoped (rank %d) this run -- the divergence is not earned, remove unscoped_exempt", u.rank)})
		}
		s, ok := scopedByID[u.c.ID]
		if !ok {
			out = append(out, exemptionViolation{u.c.ID, "exempt case has no matching result in the scoped run -- cannot verify the scoped half of its divergence claim"})
			continue
		}
		if !(s.rank >= 1 && s.rank <= 3) {
			out = append(out, exemptionViolation{u.c.ID, fmt.Sprintf("claims exemption from the unscoped ratchet but MISSES top-3 scoped (rank %d) this run -- the divergence is not earned, remove unscoped_exempt", s.rank)})
		}
	}
	return out
}

// countExemptResults reports how many of a natural-language run's own
// results are UnscopedExempt and their ids -- agent-estate#1209's guardrail
// 2: the exempt bucket's size must be printed every run, alongside the
// ratchet lines, so it cannot grow unnoticed the way the stale "no drift
// evidence" reason went unchecked for weeks (agent-estate#1207). Takes the
// unscoped run's own results (not a fresh goldenset.LoadNatural() call) so
// this count can never drift from the same case set the exemption actually
// applied to this run.
func countExemptResults(results []naturalResult) (count int, ids []string) {
	for _, r := range results {
		if r.c.UnscopedExempt {
			count++
			ids = append(ids, r.c.ID)
		}
	}
	return count, ids
}

// runMode runs every case once, in the single mode (private or not)
// named by label, printing per-case detail (misses always, hits only
// under -v) to w. It returns every case's own result (agent-estate#1133
// needs the per-case ExpectedSource to split the default-mode run into
// its reachable and excluded subsets -- an aggregate hits/total alone
// cannot do that split) plus whether at least one case actually ran to
// completion, so main can tell "scored low" apart from "could not run at
// all" independently per mode.
func runMode(w *bufio.Writer, bin string, cases []goldenset.Case, private bool, label string, verbose bool) (results []result, ranAtLeastOne bool) {
	fmt.Fprintf(w, "=== %s ===\n", label)
	for _, c := range cases {
		stdout, exitCode, err := runQuery(bin, c.Question, private)
		if err != nil {
			results = append(results, result{c: c, detail: err.Error()})
			continue
		}
		ranAtLeastOne = true
		r := evaluate(c, stdout, exitCode)
		results = append(results, r)
		if verbose || !r.pass {
			status := "MISS"
			if r.pass {
				status = "HIT"
			}
			fmt.Fprintf(w, "[%s] %s -- %q\n", status, c.ID, c.Question)
			if !r.pass {
				fmt.Fprintf(w, "  expected: %s (%s)\n", c.ExpectedIdentifier, c.ExpectedSource)
				if r.detail != "" {
					fmt.Fprintf(w, "  detail:   %s\n", r.detail)
				}
				if len(r.got) == 0 {
					fmt.Fprintln(w, "  got:      (no matches parsed from output)")
				}
				for i, m := range r.got {
					if i >= 3 {
						fmt.Fprintf(w, "  ...(%d more not shown)\n", len(r.got)-3)
						break
					}
					fmt.Fprintf(w, "  got[%d]:   [%s] %s score=%d permalink=%s\n", i, m.ID, m.Source, m.Score, m.Permalink)
				}
			}
			fmt.Fprintln(w)
		}
	}

	return results, ranAtLeastOne
}

// splitPublishable takes runMode's default-mode results (agent-estate#1133)
// and splits cases.json's 17 cases into the three groups the corrected
// "publishable-reachable" line needs: the reachable set this run can
// actually be scored against (github-stars, repo-docs -- see
// isPublishableReachable), the private-by-construction set default mode
// can never return a match for regardless of ranking quality, and the one
// SourceNone case, reported separately because a no_match case is neither
// a disclosure exclusion nor a reachable-set hit. reachableHits/reachableTotal
// are the corrected score's own numerator/denominator; excludedPrivate is
// the count this line's own text must disclose so the report cannot regress
// back into a bare, unqualified "5/5". noneResult is nil if cases.json ever
// stops carrying a SourceNone case.
func splitPublishable(results []result) (reachableHits, reachableTotal, excludedPrivate int, noneResult *result) {
	for i, r := range results {
		switch {
		case r.c.ExpectedSource == goldenset.SourceNone:
			noneResult = &results[i]
		case isPublishableReachable(r.c.ExpectedSource):
			reachableTotal++
			if r.pass {
				reachableHits++
			}
		default:
			excludedPrivate++
		}
	}
	return reachableHits, reachableTotal, excludedPrivate, noneResult
}

// publishableReachableLine formats agent-estate#1133's corrected
// "publishable-reachable score" line. agent-estate#1214: every number in
// it, including the two case-set sizes that used to be hardcoded to 17
// (cases.json's total at the time), is derived from this run's own counts
// -- totalCases is len(cases.json) for THIS run, not a stored literal, so
// the line cannot describe a case set larger or smaller than the one it
// just measured. noneCount is how many cases.json cases were reported
// separately as SourceNone (see splitPublishable); excludedPrivate,
// reachableHits and reachableTotal are splitPublishable's own outputs.
func publishableReachableLine(totalCases, excludedPrivate, noneCount, reachableHits, reachableTotal int) string {
	return fmt.Sprintf("publishable-reachable score (default/public; %d of %d cases excluded -- ExpectedSource is private by construction and unreachable in this mode regardless of ranking, see internal/knowledge/classify.go; %d of %d (none-01) reported separately below, not counted here): %d/%d",
		excludedPrivate, totalCases, noneCount, totalCases, reachableHits, reachableTotal)
}

// ratchet is one agent-estate#1066 regression guard: a named measurement
// already printed elsewhere in this report, the number of misses it
// tolerates against the CURRENT total, and the reason that tolerance is
// what it is. The reason is required and is printed as part of the FAIL
// line itself (see main) -- #1066's own thesis is that a cost disclosed
// only in prose or a PR body is a cost that gets rediscovered downstream;
// putting it in the assertion message is what stops that.
//
// agent-estate#1152: this used to carry a hardcoded `minimum` hit count
// compared directly against got, with total only ever printed. That let a
// stratum gaining passing cases silently loosen the guard -- #1150 added
// five passing corpus-directive cases to the private retrieval stratum,
// moving it from 17/17 to 22/22, and the floor of 16 that used to tolerate
// one miss out of 17 now tolerated six out of 22, with every line still
// printing [OK] and the reason text still reading as accurate. maxMisses
// fixes this structurally: it is compared against total-got, so a ratchet's
// tolerance stays fixed in MISSES as the fixture grows, rather than
// drifting looser every time the fixture gets bigger. floor() below derives
// the printed floor from maxMisses and the CURRENT total so the printed
// line's shape (`got/total (floor N)`) is unchanged.
type ratchet struct {
	name      string
	got       int
	total     int
	maxMisses int
	reason    string
}

// floor is the minimum got value this ratchet currently accepts, derived
// from maxMisses and this run's own total -- never a stored literal, so it
// tracks the fixture's current size automatically instead of requiring a
// human to recompute and edit it by hand every time a case is added.
func (r ratchet) floor() int {
	return r.total - r.maxMisses
}

// ok reports whether this ratchet's currently measured value still stays
// within its accepted miss budget. Comparing total-got against maxMisses,
// rather than got against a stored floor, is what makes this
// denominator-independent (agent-estate#1152): adding a passing case grows
// total and got together and leaves total-got unchanged, so it cannot loosen
// the guard the way a hardcoded hit floor did.
func (r ratchet) ok() bool {
	return r.total-r.got <= r.maxMisses
}

// buildRatchets is agent-estate#1066's own set of regression guards --
// deliberately NOT every line this runner prints, and NOT every line that
// could in principle regress. Two families are excluded on purpose, and
// this comment is the one place that exclusion and its reasoning live
// rather than being scattered per call site:
//
//   - The natural-language stratum's unscoped top-10 line drifts with the
//     live corpus alone -- no code change required. Measured moving
//     10/12->9/12 and 11/12->10/12 in one night and back, and
//     agent-estate#1112's own review reproduced the identical drift on
//     unmodified main side by side. The mechanism is structural: the
//     stratum's targets are a fixed set of repo-docs sections while the
//     index grows around them, so new competitors arrive, the answer does
//     not move, but its rank does. A ratchet on this line would fire on a
//     day nobody touched the scorer, and a check that cries wolf gets
//     disabled -- worse than no ratchet at all (#1066's own thesis). The
//     scoped top-10 line is excluded too, but for a different reason: it now
//     runs in --private mode (agent-estate#1162), a mode #1112 never
//     measured drift against, so there is no evidence yet either way -- an
//     unevidenced exclusion, not an inherited one, and worth re-examining
//     once this mode has been observed over time. The top-3 lines from the
//     SAME stratum ARE ratcheted below: #1112 measured drift only on the
//     top-10 cutoff, never top-3, in the one mode it tested.
//   - Both term-overlap lines (github-stars, natural-language) measure
//     fixture honesty -- how much of a question's own wording already
//     appears in its target -- not retrieval quality. Ratcheting either
//     would reward authoring questions that share no words with anything,
//     which is not the goal (agent-estate#1115, agent-estate#1138).
//
// Every maxMisses value below is the miss budget implied by the floor
// measured on add887e (2026-09-04), the first commit where the baselines it
// ratchets had stopped moving -- agent-estate#1137 and agent-estate#1138
// were the last two changes to move them, and both are required
// preconditions for this function to exist at all (see this issue's own
// sequencing precondition) -- EXCEPT:
//
//   - the two natural-language top-3 lines, whose miss budget reflects
//     agent-estate#1140's deliberate floor drop from 6 to 4 (the same way
//     #1138 lowered the github-stars top-10 floor): nl-09 and nl-11 were
//     re-authored from a caller's actual need instead of borrowing the
//     target section's own title wording (both were 100% term overlap,
//     rank 1), and both fell out of the top 10 entirely as a result. That
//     drop is the fixture getting better while the retriever is unchanged,
//     not a retrieval regression -- see each ratchet's own reason string,
//     never only this comment or a PR body.
//   - retrieval score (private), whose maxMisses is agent-estate#1152's own
//     fix: agent-estate#1150 added five passing corpus-directive cases
//     (camelcase-01..05) without raising the floor, so a hardcoded-hit-count
//     floor of 16 against the new total of 22 silently went from tolerating
//     1 miss to tolerating 6. maxMisses=1 restores the original tolerance
//     and keeps it fixed as the total keeps growing, instead of another
//     one-line floor edit someone has to remember to make next time.
//
// agent-estate#1162: the "natural-language stratum top-3, scoped" ratchet
// now guards a DIFFERENT measurement than the one #1140's floor was set
// against. The scoped run moved from public to --private mode (see
// runNaturalStratum's own doc comment for why: the public-mode scoped arm
// had zero degrees of freedom -- only two publishable sources exist, and
// github-stars never outranked repo-docs on any of these twelve questions,
// so the public scoped and unscoped arms were measured identical at every
// rank on c8e0b59). Re-measured fresh under --private on this commit: still
// 4/12, and the SAME two cases miss (nl-09, nl-11) for the SAME reason
// #1140 recorded -- both were re-authored from a caller's actual need
// instead of the target section's own title wording, which cost them their
// rank in every mode measured so far, not just the public one. The floor is
// carried forward at the same value on that basis, not re-derived from
// scratch; if a future measurement finds these two cases behave differently
// under --private, that divergence is itself worth reporting, not silently
// folded into a floor edit.
//
// agent-estate#1169: the natural-language stratum's own totals moved 12->21,
// nlTop3MaxMisses did NOT move. #1169's issue named two shapes for closing
// the "9 of 12 repo-docs files have zero fixture coverage" gap it measured
// -- per-file (every file gets at least one case, mechanical and checkable)
// vs per-importance (only the documents lanes are actually directed to,
// smaller but resting on a judgement call that drifts). This picked
// PER-FILE: it closes exactly the failure mode #1169 described (a document
// falling out of retrieval prints nothing and fails nothing) without
// requiring anyone to keep re-deciding which of the 12 files "matters", and
// it stays proportionate the same way the original 12-case set did -- one
// case per file, not one per section (118 sections were explicitly NOT the
// target, see #1169's own "not claiming" list). The cost accepted: nine more
// cases to maintain, and a per-file case is a shallower probe of a large
// file than #1073's original AGENTS.md cases were of AGENTS.md, since one
// case cannot exercise everything in a 16-section file. nl-13..nl-21 (one
// per previously-uncovered file: docs/director-brief.md, docs/tui/SPEC.md,
// docs/tui/PRD.md, docs/knowledge-system.md, docs/phase-plan.md,
// docs/director-loop.md, docs/spend-observation.md, docs/reviewer-value.md,
// docs/ci-rules-retired.md) were each written from a caller's own need
// against that file's actual content, never from a section's own title, and
// each was measured landing at rank 1-3 on a fresh scratch index before
// being checked in -- the same "chosen before any query ran, verified
// after" discipline #1073/#1140 used. All nine passed at top-3, so this is
// OPTION 1 from #1169's own framing (cases added that pass, miss budget
// left alone): nlTop3MaxMisses is UNCHANGED at 8, and the denominator-
// independent floor derived from it (ratchet.floor) rose automatically from
// 4-of-12 to 13-of-21 with no edit here, exactly as #1152 intended. Measured
// 13/21 on this same commit -- zero of the nine new cases missed, so the
// floor was not loosened to absorb a failure; it moved only because the
// total moved. See natural_cases.json's own nl-13..nl-21 entries for each
// case's individual rank evidence.
//
// Re-measure before trusting any of these numbers further; they are one
// observation from one checkout, not a constant.
func buildRatchets(nlTop3, nlTotal, nlScopedTop3, nlScopedTotal, privateHits, privateTotal, reachableHits, reachableTotal, starTop3, starTop10, starTotal int, noneResult *result) []ratchet {
	// agent-estate#1155: each constant below is the ONLY place its ratchet's
	// miss budget is spelled out as a number -- the reason strings format
	// this same constant with fmt.Sprintf rather than hand-typing the value
	// a second time, the way agent-estate#1121's TestRankingBasisNamesLiveFieldWeights
	// pins prose to a live field-weight constant. A reason that quoted its
	// budget as a separate hand-written numeral could drift the moment
	// someone edited the constant and not the string; formatting from the
	// constant makes that impossible by construction, and
	// TestBuildRatchetsReasonsStateTheirOwnMissBudget below still asserts it,
	// the same belt-and-suspenders shape #1121 used.
	//
	// agent-estate#1155, closing comment (audited on 0140ec1, staying open
	// until this landed): the issue's literal clause was "3 of 7 reasons do
	// not state their own floor." Audited again after this fix, 4 of 7 name
	// no numeric floor at all -- they name the miss budget instead, which is
	// the actually-enforced, actually-pinned constant (see the paragraph
	// above and TestBuildRatchetsMaxMissesArePinned). That is not the three
	// still failing the literal clause; it is a deliberate supersession this
	// comment records rather than leaving implicit: STATING THE MISS BUDGET
	// SUPERSEDES STATING THE FLOOR. A reason that names both a budget and a
	// floor has two numbers to keep in sync instead of one, which is the
	// exact defect class #1152 fixed for the enforcement itself; naming the
	// floor a second time in prose would reintroduce it in prose instead.
	//
	// The one line the audit found still short of even the four-of-seven
	// pattern was github-stars stratum top-10: its reason named `8-of-8` --
	// the value it moved FROM -- twice, and named no current number at all,
	// which is the one case where the original issue's complaint still read
	// true. Fixed here by making that line match the four budget-only lines
	// (retrieval score, publishable-reachable, github-stars top-3, none-01),
	// not by adding today's floor (7) to its prose: the printed
	// `(floor N)` on the report line already carries that number, derived,
	// never hand-typed (ratchet.floor), and a reader who wants it reads it
	// there. Declining to also duplicate it in prose is the point of this
	// decision, not an oversight -- see the ratchet's own reason string.
	const (
		nlTop3MaxMisses    = 8 // floor 4 of 12 (agent-estate#1140), now 13 of 21 -- agent-estate#1169 added nl-13..nl-21 (per-file coverage), all nine pass, this constant did not move
		retrievalMaxMisses = 1 // floor 16 of 17 pre-#1150, now 21 of 22 -- agent-estate#1152
		reachableMaxMisses = 0 // floor 5 of 5 (agent-estate#1133)
		starTop3MaxMisses  = 1 // floor 7 of 8
		starTop10MaxMisses = 1 // floor 7 of 8, post-agent-estate#1138
		noneMaxMisses      = 0 // agent-estate#1137's own fix, no room to slip
	)
	rs := []ratchet{
		{"natural-language stratum top-3, unscoped", nlTop3, nlTotal, nlTop3MaxMisses,
			fmt.Sprintf("agent-estate#1066: tolerates at most %d miss(es) out of the current total -- floor LOWERED from 6-of-12 to 4-of-12 by agent-estate#1140, nl-09 and nl-11 were re-authored from a caller's actual need instead of the section title's own wording (both were 100%% term overlap, rank 1, no headroom to detect a regression), which is expected to move them out of the top 10 entirely, not just out of the top 3. This is the fixture getting better while the retriever is unchanged, not a retrieval regression -- see agent-estate#1140's PR body. agent-estate#1169 added nl-13..nl-21, one PER-FILE case for each of the 9 repo-docs files (of 12 in the index) that previously had zero fixture coverage -- picked per-file over per-importance because it is mechanical and checkable rather than resting on a judgement about which files matter, at the cost of 9 more cases to maintain and each being a shallower probe than the original per-file AGENTS.md coverage. All nine measured rank <=3 before being checked in and this constant did not move -- the floor rose from 4-of-12 to 13-of-21 only because the denominator-independent total did, per agent-estate#1152, not because any miss budget was loosened", nlTop3MaxMisses)},
		{"natural-language stratum top-3, private scoped source:repo-docs", nlScopedTop3, nlScopedTotal, nlTop3MaxMisses,
			fmt.Sprintf("agent-estate#1162: tolerates at most %d miss(es) out of the current total -- same floor (4-of-12, now 13-of-21 after agent-estate#1169's nine per-file additions -- see the unscoped top-3 ratchet's own reason string for that decision) and same nl-09/nl-11 reasoning as the unscoped top-3 line above, carried forward from agent-estate#1140. This line moved from public to --private mode under agent-estate#1162: the public-mode arm it replaces was measured a tautology (identical to the unscoped line on all 12 cases, at every rank, on c8e0b59 -- default/public mode has only two publishable sources, so source:repo-docs could only ever remove github-stars items, which never outranked repo-docs on any of these questions). --private mode has seven competing sources and the filter has real work to do there; source: scoping does not recover nl-09 or nl-11 even so", nlTop3MaxMisses)},
		{"retrieval score (private)", privateHits, privateTotal, retrievalMaxMisses,
			fmt.Sprintf("agent-estate#1152: tolerates at most %d miss(es) out of the current total, denominator-independent -- unaffected by #1137/#1138, neither of which touched a private-mode cases.json case. agent-estate#1150's five camelcase-01..05 corpus-directive cases moved the denominator 17 -> 22 without raising the old hardcoded floor of 16, which silently grew the tolerated-miss count from 1 to 6; this ratchet is now defined by the miss budget instead of a hit floor, so the next case added here cannot loosen it the same way", retrievalMaxMisses)},
		{"publishable-reachable score", reachableHits, reachableTotal, reachableMaxMisses,
			fmt.Sprintf("agent-estate#1066: tolerates at most %d miss(es) out of the current total -- agent-estate#1133 established this as the reachable-only denominator (github-stars, repo-docs), not the raw 17", reachableMaxMisses)},
		{"github-stars stratum top-3", starTop3, starTotal, starTop3MaxMisses,
			fmt.Sprintf("agent-estate#1066: tolerates at most %d miss(es) out of the current total -- floor at the value measured on add887e", starTop3MaxMisses)},
		{"github-stars stratum top-10", starTop10, starTotal, starTop10MaxMisses,
			fmt.Sprintf("agent-estate#1066: tolerates at most %d miss(es) out of the current total -- accepted cost of agent-estate#1138's re-authoring, which traded target-description overlap for need-based questions and cost one hit against full marks; the miss budget above is the enforced, pinned constant (agent-estate#1152), and agent-estate#1155 records that stating it supersedes stating any specific historical or current floor value in this prose -- the printed report line's own (floor N) already carries that number, derived from this same constant, never hand-typed", starTop10MaxMisses)},
	}
	if noneResult != nil {
		got := 0
		if noneResult.pass {
			got = 1
		}
		rs = append(rs, ratchet{"none-01 (absence must report no_match)", got, 1, noneMaxMisses,
			fmt.Sprintf("agent-estate#1066: tolerates at most %d miss(es) -- this is agent-estate#1137's own fix, not a floor with room to slip further, none-01 failing to exit 1/no_match is the exact regression #1137 closed", noneMaxMisses)})
	}
	return rs
}

// ratchetFailures runs every ratchet and returns only the ones that did not
// hold -- kept separate from printing (main does that) so this logic is
// testable without capturing stdout.
func ratchetFailures(rs []ratchet) []ratchet {
	var failed []ratchet
	for _, r := range rs {
		if !r.ok() {
			failed = append(failed, r)
		}
	}
	return failed
}

func main() {
	bin := flag.String("bin", "estate", "path to the estate binary to exec `knowledge query` against")
	verbose := flag.Bool("v", false, "print every case, not just misses")
	flag.Parse()

	cases, err := goldenset.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "goldenquery:", err)
		os.Exit(2)
	}

	w := bufio.NewWriter(os.Stdout)
	defer w.Flush()

	// #1040: two independent passes, two independent numbers. Neither
	// mode's result is reused for the other -- a case that is private
	// is expected to miss in publishable-only mode and that is not an
	// error in this runner or a regression in `estate knowledge query`.
	privateResults, ranPrivate := runMode(w, *bin, cases, true, "retrieval score (--private)", *verbose)
	privateHits, privateTotal := 0, len(privateResults)
	for _, r := range privateResults {
		if r.pass {
			privateHits++
		}
	}

	// agent-estate#1133: default mode is still run once over every case in
	// cases.json -- splitPublishable does the reachable/excluded split
	// afterward, from this single run's own results, rather than this
	// runner querying the reachable subset a second time.
	pubResults, ranPub := runMode(w, *bin, cases, false, "publishable-only run (default/public, per-case detail below)", *verbose)
	reachableHits, reachableTotal, excludedPrivate, noneResult := splitPublishable(pubResults)

	// agent-estate#1073: the natural-language stratum is a separate
	// question from cases.json's ("will a caller who does not already
	// know the answer land on it?", not "can retrieval find a known
	// item?") and #1069 requires it be reported on its own, never
	// averaged into either number above.
	//
	// agent-estate#1077: run the SAME twelve cases twice -- once exactly
	// as written, default/public mode (unscoped) and once with
	// "source:repo-docs " prepended to every question, --private
	// (scoped, moved off public mode by agent-estate#1162 -- see
	// runNaturalStratum's own doc comment). Both lines print always;
	// neither is derived from or replaces the other.
	nlTop3, nlTop10, nlTotal, ranNaturalUnscoped, nlOverlapMean, nlOverlapMeasured, nlUnscopedResults := runNaturalStratum(w, *bin, *verbose, false)
	nlScopedTop3, nlScopedTop10, nlScopedTotal, ranNaturalScoped, _, _, nlScopedResults := runNaturalStratum(w, *bin, *verbose, true)
	ranNatural := ranNaturalUnscoped || ranNaturalScoped

	// agent-estate#1209: re-earn every UnscopedExempt case's divergence
	// claim against THIS run's own measurement -- see checkExemptions' doc
	// comment. A violation here is reported and fails the run below,
	// alongside the ratchet failures, not silently absorbed into either
	// natural-language tally.
	exemptViolations := checkExemptions(nlUnscopedResults, nlScopedResults)
	exemptCount, exemptIDs := countExemptResults(nlUnscopedResults)

	// agent-estate#1111: the github-stars stratum is the complement of
	// #1073's repo-docs-only one -- #1063's own comment measured that
	// repo-docs is 23% of the public store and github-stars the other
	// 77%, invisible to every number above until this fixture. Reported
	// on its own, never averaged with cases.json's or the repo-docs
	// stratum's own numbers (see runStarStratum's own doc comment).
	starTop3, starTop10, starTotal, ranStars, starOverlapMean, starOverlapMeasured := runStarStratum(w, *bin, *verbose)

	if !ranPrivate && !ranPub && !ranNatural && !ranStars {
		fmt.Fprintln(w, "goldenquery: could not run any case -- is the estate binary on PATH, and has `estate knowledge` been run to compile the index?")
		w.Flush()
		os.Exit(2)
	}

	fmt.Fprintln(w, "---")
	// #1069: lead with the weaker stratum, never averaged with the other
	// two -- a combined number describes the test mix, not the weakest
	// way real agents fail. #1077: the scoped and unscoped natural-language
	// lines sit together (same fixture, same commit) and are themselves
	// never averaged with each other -- see runNaturalStratum's doc comment
	// for what each answers.
	fmt.Fprintf(w, "natural-language stratum, top-3  (default/public, unscoped -- #1073; %d case(s) excluded, earned scope-diagnostic exemption -- agent-estate#1209, see exempt line below): %d/%d\n", exemptCount, nlTop3, nlTotal)
	fmt.Fprintf(w, "natural-language stratum, top-10 (default/public, unscoped -- #1073; %d case(s) excluded, same exemption as top-3 above): %d/%d\n", exemptCount, nlTop10, nlTotal)
	fmt.Fprintf(w, "natural-language stratum, top-3  (--private, scoped source:repo-docs -- #1162):  %d/%d\n", nlScopedTop3, nlScopedTotal)
	fmt.Fprintf(w, "natural-language stratum, top-10 (--private, scoped source:repo-docs -- #1162):                                    %d/%d\n", nlScopedTop10, nlScopedTotal)
	// agent-estate#1115: a stratum where every question reuses most of its
	// target's own words has no headroom left to catch a regression --
	// print the mean term-overlap alongside the hit rate so that fact is
	// visible without a bespoke measurement each time. This stratum has
	// no case wired with goldenset.Case.TargetText yet, so this reports
	// "not measured" honestly rather than a false 0%.
	if nlOverlapMeasured > 0 {
		fmt.Fprintf(w, "natural-language stratum term overlap vs target_text (agent-estate#1115): mean %.0f%% (measured %d/%d cases)\n", nlOverlapMean*100, nlOverlapMeasured, nlTotal)
	} else {
		fmt.Fprintln(w, "natural-language stratum term overlap vs target_text (agent-estate#1115): not measured -- no case carries target_text yet")
	}
	fmt.Fprintf(w, "retrieval score (private): %d/%d\n", privateHits, privateTotal)
	// agent-estate#1133: the corrected line. cases.json has totalCases
	// cases this run; excludedPrivate of them have an ExpectedSource
	// classify.go marks private (vault-fact, corpus-parameter,
	// loops-research) and cannot be found in default mode by disclosure
	// policy, not by any ranking deficiency -- they are excluded from the
	// score's own denominator, not silently dropped from the report.
	// none-01 is reported on its own line immediately below, neither
	// counted as a reachable hit nor folded into excludedPrivate: it is
	// not a disclosure exclusion.
	//
	// agent-estate#1214: totalCases and noneCount are both derived from
	// this run's own results (pubResults holds every case in cases.json;
	// splitPublishable partitions it into exactly these three groups), never
	// a stored literal -- the bug this closes was cases.json growing from
	// 17 to 22 cases while the printed line's denominator stayed a
	// hardcoded 17 in two places.
	totalCases := len(pubResults)
	noneCount := totalCases - reachableTotal - excludedPrivate
	fmt.Fprintln(w, publishableReachableLine(totalCases, excludedPrivate, noneCount, reachableHits, reachableTotal))
	if noneResult != nil {
		noneStatus := "MISS"
		if noneResult.pass {
			noneStatus = "HIT"
		}
		fmt.Fprintf(w, "none-01 (expected_source=none, excluded from the reachable score above -- a no_match case has no identifier to be reachable or private): %s (exit %d)\n", noneStatus, noneResult.exitCode)
	}
	// agent-estate#1111: the github-stars stratum's own line -- a stratum
	// this runner could not previously print at all. Never averaged with
	// the two lines above it: top-3 and top-10 are printed together on
	// one line here, unlike the repo-docs stratum's four, because #1063's
	// own comment measured that scoping does not move this stratum (see
	// runStarStratum's doc comment) and this issue adds measurement, not
	// a second scoped run to go with it.
	fmt.Fprintf(w, "github-stars stratum (default/public, unscoped -- #1111): top-3 %d/%d, top-10 %d/%d\n", starTop3, starTotal, starTop10, starTotal)
	// agent-estate#1115: the merged fixture's questions reused 67% of
	// their own target's words and landed 8/8 -- a saturated score with
	// no headroom to detect a regression. The stratum was re-authored
	// from the caller's need rather than the target's description; this
	// line is what makes that fact self-reporting instead of requiring a
	// fresh measurement by hand every time it is questioned again.
	if starOverlapMeasured > 0 {
		fmt.Fprintf(w, "github-stars stratum term overlap vs target_text (agent-estate#1115, was 67%% pre-re-authoring): mean %.0f%% (measured %d/%d cases)\n", starOverlapMean*100, starOverlapMeasured, starTotal)
	} else {
		fmt.Fprintln(w, "github-stars stratum term overlap vs target_text (agent-estate#1115): not measured -- no case carries target_text")
	}

	// agent-estate#1066: the ratchet. Everything above this line is
	// unchanged reporting; this is the only section that can change the
	// exit code away from the operational 0/2 pair the rest of this runner
	// already used. See buildRatchets' own doc comment for exactly which
	// lines are guarded here, which two are deliberately not, and why.
	fmt.Fprintln(w, "---")
	fmt.Fprintln(w, "ratchet (agent-estate#1066 -- fails the run, not just the printed number, when a guarded line drops below its accepted floor):")
	ratchets := buildRatchets(nlTop3, nlTotal, nlScopedTop3, nlScopedTotal, privateHits, privateTotal, reachableHits, reachableTotal, starTop3, starTop10, starTotal, noneResult)
	for _, r := range ratchets {
		status := "OK"
		if !r.ok() {
			status = "FAIL"
		}
		fmt.Fprintf(w, "  [%s] %s: %d/%d (floor %d) -- %s\n", status, r.name, r.got, r.total, r.floor(), r.reason)
	}
	// agent-estate#1214: "21 cases" used to be a stored literal here, naming
	// the natural-language fixture's size as it stood when agent-estate#1112
	// made this comparison. natural_cases.json has since grown past 21 (like
	// cases.json's 17->22, agent-estate#1214's own finding). agent-estate#1215's
	// fix pass: the live replacement is nlTotal (the unscoped stratum's own,
	// exemption-excluded total), never nlScopedTotal (the scoped stratum's
	// total, which excludes nothing). The sentence claims the scoped top-10
	// line is rank-identical to the unscoped top-10 line "on all N cases" --
	// the set that claim covers is the non-exempt cases, i.e. nlTotal.
	// checkExemptions only allows an exemption when a case misses unscoped
	// top-3 and hits scoped top-3, so an UnscopedExempt case is REQUIRED to
	// diverge between the two lines, not stay rank-identical -- nlScopedTotal
	// would wrongly fold those cases into a claim the exemption mechanism
	// itself proves false for exactly them.
	fmt.Fprintf(w, "  not ratcheted, known corpus-growth drift (agent-estate#1112): natural-language stratum top-10, unscoped (public); not ratcheted, rank-identical to that unscoped line on all %d cases and so inherits its corpus-growth drift wholesale (agent-estate#1112, agent-estate#1162): natural-language stratum top-10, private scoped source:repo-docs\n", nlTotal)
	fmt.Fprintln(w, "  not ratcheted, measures fixture honesty not quality (agent-estate#1066, agent-estate#1115): term overlap, github-stars and natural-language")
	// agent-estate#1209: guardrail 2 -- the exempt bucket's own size,
	// printed unconditionally alongside the ratchet lines above (even when
	// the count is 0), so a reader never has to go looking for whether the
	// unscoped ratchet's own denominator has been quietly shrunk. This is
	// not itself a ratchet: growth here is a signal to look, not a failure
	// on its own (see goldenset.Case.UnscopedExempt's doc comment for what
	// DOES fail the run when an exemption stops being earned).
	if exemptCount > 0 {
		fmt.Fprintf(w, "  exempt from the unscoped ratchet only (agent-estate#1209, earned per-case -- see checkExemptions): %d of %d natural-language cases -- %s\n", exemptCount, nlTotal+exemptCount, strings.Join(exemptIDs, ", "))
	} else {
		fmt.Fprintf(w, "  exempt from the unscoped ratchet only (agent-estate#1209): 0 of %d natural-language cases\n", nlTotal)
	}

	failed := ratchetFailures(ratchets)
	w.Flush()
	exitStatus := 0
	if len(failed) > 0 {
		fmt.Fprintf(os.Stderr, "goldenquery: %d ratchet(s) regressed below their accepted floor -- see the ratchet section above for which and why\n", len(failed))
		for _, r := range failed {
			fmt.Fprintf(os.Stderr, "  %s: %d/%d, floor %d -- %s\n", r.name, r.got, r.total, r.floor(), r.reason)
		}
		exitStatus = 1
	}
	// agent-estate#1209: an earned-exemption violation fails the run the
	// same way a ratchet regression does -- it is exactly as serious, since
	// it means either an ordinary miss was parked in the exempt bucket
	// without evidence, or a genuine exemption has gone stale (the case now
	// hits unscoped, or has stopped hitting scoped) and needs a human look,
	// not a silent pass.
	if len(exemptViolations) > 0 {
		fmt.Fprintf(os.Stderr, "goldenquery: %d unscoped-exemption claim(s) did not hold this run -- an exemption must be earned every run, not asserted once\n", len(exemptViolations))
		for _, v := range exemptViolations {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", v.id, v.reason)
		}
		exitStatus = 1
	}
	if exitStatus != 0 {
		os.Exit(exitStatus)
	}
}
