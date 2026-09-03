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

	"github.com/jonhill90/agent-estate/src/tui/internal/vhscapture"
)

func main() {
	tape := flag.String("tape", "", "path to the .tape file to run (required)")
	minColors := flag.Int("min-colors", 2, "an attempt's Screenshot output must have at least this many distinct colors to count as settled; 2 catches a blank single-color frame, raise it for a tape whose known-good frame count you've already measured")
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

	tapePath := *tape
	runTape := func() error {
		cmd := exec.Command("vhs", tapePath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	report, runErr := vhscapture.RunUntilSettled(targets, *minColors, *maxAttempts, runTape)
	report.Print(os.Stdout)
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "vhscapture: %v\n", runErr)
		os.Exit(1)
	}
}
