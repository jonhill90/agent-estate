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
// Usage:
//
//	go run ./cmd/goldenquery [-bin estate] [-v]
//
// Exit code is 0 whenever the measurement itself ran to completion,
// regardless of the score -- a low hit rate is a successful measurement,
// not a runner failure (see #1023's own brief). Exit code is 2 only when
// the measurement could not be attempted at all (binary not found, every
// single case errored out before returning a real state).
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

// runQuery execs `<bin> knowledge query <question>` and returns its raw
// stdout, stderr and exit code -- never parsed logic here, just the
// subprocess boundary, so parseMatches (below) is independently testable
// against a captured string.
func runQuery(bin, question string) (stdout string, exitCode int, err error) {
	cmd := exec.Command(bin, "knowledge", "query", question)
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

	var results []result
	ranAtLeastOne := false
	for _, c := range cases {
		stdout, exitCode, err := runQuery(*bin, c.Question)
		if err != nil {
			results = append(results, result{c: c, detail: err.Error()})
			continue
		}
		ranAtLeastOne = true
		r := evaluate(c, stdout, exitCode)
		results = append(results, r)
		if *verbose || !r.pass {
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

	if !ranAtLeastOne {
		fmt.Fprintln(w, "goldenquery: could not run any case -- is the estate binary on PATH, and has `estate knowledge` been run to compile the index?")
		w.Flush()
		os.Exit(2)
	}

	hits := 0
	for _, r := range results {
		if r.pass {
			hits++
		}
	}
	fmt.Fprintf(w, "hits/total: %d/%d\n", hits, len(results))
	w.Flush()
}
