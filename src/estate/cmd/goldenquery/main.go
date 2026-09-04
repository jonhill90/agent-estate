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
// LANDS on it -- a different property, run in default (public, unscoped)
// mode only, reporting both top-3 and top-10 hit rates. #1069 settled that
// this number must never be averaged with cases.json's two: report all
// three separately, leading with the weakest.
//
// Usage:
//
//	go run ./cmd/goldenquery [-bin estate] [-v]
//
// Exit code is 0 whenever the measurement itself ran to completion,
// regardless of the score -- a low hit rate is a successful measurement,
// not a runner failure (see #1023's own brief). Exit code is 2 only when
// the measurement could not be attempted at all (binary not found, every
// single case errored out before returning a real state, in EITHER mode).
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

// runNaturalStratum runs agent-estate#1073's checked-in natural-language
// fixture (internal/knowledge/goldenset.LoadNatural) once per case, in
// default (public, unscoped) mode only -- matching the baseline #1069
// measured by hand on a7d413c (top-3 5/12, top-10 10/12). This stratum is
// never run in --private mode and never merged with cases.json's own
// score: #1069 settled that the two measure different things and must be
// reported separately, weaker one leading.
func runNaturalStratum(w *bufio.Writer, bin string, verbose bool) (top3Hits, top10Hits, total int, ranAtLeastOne bool) {
	cases, err := goldenset.LoadNatural()
	if err != nil {
		fmt.Fprintln(w, "goldenquery: natural-language stratum:", err)
		return 0, 0, 0, false
	}

	fmt.Fprintln(w, "=== natural-language stratum (default/public, unscoped -- agent-estate#1073) ===")
	var results []naturalResult
	for _, c := range cases {
		stdout, exitCode, err := runQuery(bin, c.Question, false)
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

	for _, r := range results {
		if r.rank >= 1 && r.rank <= 3 {
			top3Hits++
		}
		if r.rank >= 1 && r.rank <= 10 {
			top10Hits++
		}
	}
	total = len(results)
	return top3Hits, top10Hits, total, ranAtLeastOne
}

// runMode runs every case once, in the single mode (private or not)
// named by label, printing per-case detail (misses always, hits only
// under -v) to w. It returns the resulting hits/total and whether at
// least one case actually ran to completion, so main can tell "scored
// low" apart from "could not run at all" independently per mode.
func runMode(w *bufio.Writer, bin string, cases []goldenset.Case, private bool, label string, verbose bool) (hits, total int, ranAtLeastOne bool) {
	fmt.Fprintf(w, "=== %s ===\n", label)
	var results []result
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

	for _, r := range results {
		if r.pass {
			hits++
		}
	}
	total = len(results)
	return hits, total, ranAtLeastOne
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
	privateHits, privateTotal, ranPrivate := runMode(w, *bin, cases, true, "retrieval score (--private)", *verbose)
	pubHits, pubTotal, ranPub := runMode(w, *bin, cases, false, "publishable-only score (default)", *verbose)

	// agent-estate#1073: the natural-language stratum is a separate
	// question from cases.json's ("will a caller who does not already
	// know the answer land on it?", not "can retrieval find a known
	// item?") and #1069 requires it be reported on its own, never
	// averaged into either number above.
	nlTop3, nlTop10, nlTotal, ranNatural := runNaturalStratum(w, *bin, *verbose)

	if !ranPrivate && !ranPub && !ranNatural {
		fmt.Fprintln(w, "goldenquery: could not run any case -- is the estate binary on PATH, and has `estate knowledge` been run to compile the index?")
		w.Flush()
		os.Exit(2)
	}

	fmt.Fprintln(w, "---")
	// #1069: lead with the weaker stratum, never averaged with the other
	// two -- a combined number describes the test mix, not the weakest
	// way real agents fail.
	fmt.Fprintf(w, "natural-language stratum, top-3  (default/public, unscoped -- #1073, baseline 5/12 on a7d413c):  %d/%d\n", nlTop3, nlTotal)
	fmt.Fprintf(w, "natural-language stratum, top-10 (default/public, unscoped -- #1073, baseline 10/12 on a7d413c): %d/%d\n", nlTop10, nlTotal)
	fmt.Fprintf(w, "retrieval score (private, comparable to the 12/15 baseline on 6f28626): %d/%d\n", privateHits, privateTotal)
	fmt.Fprintf(w, "publishable-only score (default, what a caller who may not see private material can find): %d/%d\n", pubHits, pubTotal)
	w.Flush()
}
