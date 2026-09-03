// Command vhscapture runs one VHS tape through internal/vhscapture's
// verify-and-retry loop instead of a bare `vhs <tape>` invocation. See
// internal/vhscapture's own package doc for why: vhs's own Screenshot
// command has been shown, independently, twice (testdata/vhs/
// agents-mode.tape's header, and agent-estate#947's review) to silently
// write a stale, blank, or transitional frame regardless of tape content
// or wait condition. This wraps the whole tape run, verifies every
// Screenshot it produced, and retries the ENTIRE run when it wasn't
// settled -- exiting non-zero only if no attempt within -max-attempts
// produced a genuine capture.
//
// Usage:
//
//	go run ./cmd/vhscapture -tape testdata/vhs/agents-mode.tape
//
// Run from src/tui (the same directory every tape's own relative
// Screenshot path and go-build target already assume).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/jonhill90/agent-estate/src/tui/internal/vhscapture"
)

func main() {
	tape := flag.String("tape", "", "path to the .tape file to run (required)")
	// 1000 is agent-estate#956's own evidence -- blank frames measure 1
	// color, the partial/transitional frame that review caught measured
	// 259, and the two settled frames it measured directly
	// (agents-mode.tape) were 4393 and 5674. agent-estate#960 then measured
	// this default against every tape in the repo and found it rejects
	// over a third of them: a sparse pane's own real settled frame can
	// measure a few hundred colors, below this default and below a
	// busier tape's own partial/torn frame -- no single number is above
	// one and below the other. This flag is now the FALLBACK only, used
	// when a tape has no sidecar floor file recorded next to it (see
	// internal/vhscapture's PerTargetFloors); it no longer claims to be
	// the right number for every tape. Passing this flag explicitly
	// overrides any sidecar for every target in this run, uniformly --
	// useful to force a specific floor while debugging, not something a
	// normal run should need.
	minColors := flag.Int("min-colors", 1000, "fallback distinct-color floor for a target with no recorded sidecar (see PerTargetFloors); passing this explicitly overrides every target's sidecar floor uniformly")
	maxAttempts := flag.Int("max-attempts", 8, "how many full tape runs to try before giving up")
	flag.Parse()

	if *tape == "" {
		fmt.Fprintln(os.Stderr, "vhscapture: -tape is required")
		os.Exit(2)
	}

	content, err := os.ReadFile(*tape)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vhscapture: reading %s: %v\n", *tape, err)
		os.Exit(2)
	}

	targets := vhscapture.ScreenshotTargets(string(content))
	if len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "vhscapture: %s names no Screenshot targets -- nothing to verify\n", *tape)
		os.Exit(2)
	}

	explicitMinColors := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "min-colors" {
			explicitMinColors = true
		}
	})

	sidecarFloors, err := vhscapture.PerTargetFloors(*tape)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vhscapture: reading %s: %v\n", vhscapture.SidecarPath(*tape), err)
		os.Exit(2)
	}

	floorFor := func(target string) int {
		if !explicitMinColors {
			if v, ok := sidecarFloors[filepath.Base(target)]; ok {
				return v
			}
		}
		return *minColors
	}

	switch {
	case explicitMinColors:
		fmt.Printf("vhscapture: min-colors=%d for every target (explicit flag)\n", *minColors)
	case len(sidecarFloors) > 0:
		fmt.Printf("vhscapture: per-target floors from %s (fallback %d for any target it doesn't name)\n", vhscapture.SidecarPath(*tape), *minColors)
	default:
		fmt.Printf("vhscapture: min-colors=%d for every target (no sidecar at %s -- unmeasured, this default may reject a genuine sparse frame; see agent-estate#960)\n", *minColors, vhscapture.SidecarPath(*tape))
	}

	tapePath := *tape
	runTape := func() error {
		cmd := exec.Command("vhs", tapePath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	report, runErr := vhscapture.RunUntilSettled(targets, floorFor, *maxAttempts, runTape)
	report.Print(os.Stdout)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "vhscapture: %v\n", runErr)
		os.Exit(1)
	}
}
