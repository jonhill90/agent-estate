// Package vhscapture is agent-estate#947's answer: vhs's own Screenshot
// command silently writes a stale, blank, or transitional frame some
// fraction of the time, independent of tape content and independent of
// which wait condition gates it -- testdata/vhs/agents-mode.tape's own
// header comment documented this first (an isolated tape containing
// nothing but a printf marker + Wait+Screen + Screenshot still produced
// 5/8 blank captures), and agent-estate#947's review reproduced the same class of
// failure against a real pane: 2/5 runs were not the settled frame,
// including one entirely blank image.
//
// No tape-level wait fixes this. A previous attempt already tried the
// obvious next step -- wait on the LAST thing View() composes, plus a
// trailing settle Sleep -- and the failure rate was unchanged, because
// the race is inside vhs's own tty-to-PNG rasterization, not in how long
// the tape waits before asking for a screenshot.
//
// This package does not try to make vhs's Screenshot deterministic --
// nothing running inside the tape DSL can, since the flake reproduces
// even with zero application content involved. Instead it treats a
// vhs run as a flaky external call: run the whole tape, verify every
// Screenshot it was supposed to produce is a real settled frame (present
// on disk, above a distinct-color floor -- the same cheap proxy agent-estate#947's
// own review used to name "1 distinct color, blank" and "259 colors,
// partial" as not-a-real-capture), and retry the ENTIRE run when it
// isn't. That is an assertion against the actual captured artifact, not
// a longer guess about timing.
package vhscapture

import (
	"fmt"
	"image"
	_ "image/png" // registers the PNG decoder image.Decode needs
	"io"
	"os"
	"regexp"
)

// screenshotPattern matches a tape's own `Screenshot <path>` command line,
// the same shape every tape in testdata/vhs uses.
var screenshotPattern = regexp.MustCompile(`(?m)^Screenshot\s+(\S+)\s*$`)

// ScreenshotTargets returns every path a .tape file's own `Screenshot`
// command writes to, in the order they appear in tapeContent.
func ScreenshotTargets(tapeContent string) []string {
	var out []string
	for _, m := range screenshotPattern.FindAllStringSubmatch(tapeContent, -1) {
		out = append(out, m[1])
	}
	return out
}

// CountColors decodes the PNG at path and returns the number of distinct
// RGBA colors it contains -- the same measure agent-estate#947's own review used
// (5674 colors for a correct full frame, 1 for a blank capture, 259 for a
// partial/transitional one) to tell a settled frame from one that isn't.
func CountColors(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return 0, err
	}
	seen := map[uint32]struct{}{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bch, a := img.At(x, y).RGBA()
			key := uint32(r>>8)<<24 | uint32(g>>8)<<16 | uint32(bch>>8)<<8 | uint32(a>>8)
			seen[key] = struct{}{}
		}
	}
	return len(seen), nil
}

// TargetResult is one Screenshot target's verification outcome within a
// single attempt.
type TargetResult struct {
	Path   string
	Exists bool
	Colors int
	Err    error
	Pass   bool
}

// Attempt is one full tape run's verification, across every Screenshot
// target the tape names. RanErr is set when runTape itself returned an
// error (vhs exited non-zero) -- distinct from a verification failure,
// so a report can tell "vhs failed to run" from "vhs ran but the frame
// wasn't settled."
type Attempt struct {
	N       int
	RanErr  error
	Results []TargetResult
	Passed  bool
}

// verifyAttempt checks every target against minColors and reports
// whether ALL of them passed -- one bad frame in a multi-screenshot tape
// fails the whole attempt, matching how RunUntilSettled retries the
// entire vhs invocation rather than one screenshot at a time.
func verifyAttempt(n int, targets []string, minColors int) Attempt {
	a := Attempt{N: n}
	allPass := true
	for _, t := range targets {
		r := TargetResult{Path: t}
		info, err := os.Stat(t)
		if err != nil || info.Size() == 0 {
			if err == nil {
				err = fmt.Errorf("empty file")
			}
			r.Err = err
			allPass = false
			a.Results = append(a.Results, r)
			continue
		}
		r.Exists = true
		colors, err := CountColors(t)
		r.Colors = colors
		if err != nil {
			r.Err = err
			allPass = false
		} else if colors < minColors {
			allPass = false
		} else {
			r.Pass = true
		}
		a.Results = append(a.Results, r)
	}
	a.Passed = allPass
	return a
}

// Report is RunUntilSettled's full record -- every attempt it made, not
// just the last one, so a caller can print the same kind of run-by-run
// evidence agent-estate#947 itself demands rather than a single verdict.
type Report struct {
	Attempts []Attempt
	Settled  bool
}

// RunUntilSettled runs runTape up to maxAttempts times. Before each run
// it removes any stale file at every target (so a leftover PNG from a
// previous attempt can never be mistaken for this attempt's own
// capture), then verifies every target against minColors. It stops and
// reports success on the first attempt where every target passes, and
// returns an error if none of maxAttempts did.
func RunUntilSettled(targets []string, minColors, maxAttempts int, runTape func() error) (Report, error) {
	var report Report
	for n := 1; n <= maxAttempts; n++ {
		for _, t := range targets {
			_ = os.Remove(t)
		}
		if err := runTape(); err != nil {
			report.Attempts = append(report.Attempts, Attempt{N: n, RanErr: err})
			continue
		}
		a := verifyAttempt(n, targets, minColors)
		report.Attempts = append(report.Attempts, a)
		if a.Passed {
			report.Settled = true
			return report, nil
		}
	}
	return report, fmt.Errorf("no attempt out of %d produced a settled capture for all %d target(s)", maxAttempts, len(targets))
}

// Print writes a human-readable, run-by-run account of report to w --
// every attempt, not a summary, per agent-estate#947's own "one clean run proves
// nothing" standard.
func (r Report) Print(w io.Writer) {
	fmt.Fprintf(w, "vhscapture: %d attempt(s)\n", len(r.Attempts))
	for _, a := range r.Attempts {
		if a.RanErr != nil {
			fmt.Fprintf(w, "  attempt %d: vhs run FAILED: %v\n", a.N, a.RanErr)
			continue
		}
		status := "FAIL"
		if a.Passed {
			status = "PASS"
		}
		fmt.Fprintf(w, "  attempt %d: %s\n", a.N, status)
		for _, res := range a.Results {
			switch {
			case res.Err != nil && !res.Exists:
				fmt.Fprintf(w, "    %s: MISSING (%v)\n", res.Path, res.Err)
			case !res.Pass:
				fmt.Fprintf(w, "    %s: %d distinct colors (below floor)\n", res.Path, res.Colors)
			default:
				fmt.Fprintf(w, "    %s: %d distinct colors\n", res.Path, res.Colors)
			}
		}
	}
	if r.Settled {
		fmt.Fprintln(w, "settled: yes")
	} else {
		fmt.Fprintln(w, "settled: no")
	}
}
