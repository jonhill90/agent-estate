package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
)

// pressureExecTimeout bounds a single `estate pressure` invocation, the
// same discipline internal/cost's execTimeout and internal/board's own
// timeout already apply to their own subprocesses -- a wedged estate
// binary must not hang Home's fetch forever.
var pressureExecTimeout = 15 * time.Second

// pressureRunner executes `estate pressure` and returns exactly what it
// produced: stdout, stderr, and whatever error resulted (nil for exit 0,
// *exec.ExitError for a non-zero exit, something else for a failure to even
// start the process). Kept as its own function-typed value, mirroring
// cost.Runner/board's own runner seams, so buildPressureFetch's parsing
// logic below can be tested against a fixture instead of a real binary
// (this module must still build and test with no `estate` binary
// installed -- AGENTS.md's "The TUI" section, "Adapter discipline").
type pressureRunner func() (stdout, stderr []byte, err error)

// execPressureRunner is the one real implementation, shelling `estate
// pressure` out via os/exec -- the only place in this program that knows
// the pressure reading comes from a subprocess at all. Everything under
// internal/ only ever sees the parsed estatus.PressureReading this
// produces.
func execPressureRunner(estateBin string) pressureRunner {
	return func() (stdout, stderr []byte, err error) {
		ctx, cancel := context.WithTimeout(context.Background(), pressureExecTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, estateBin, "pressure")
		// Same process-group kill as board.ExecRunner/cost.ExecRunner: a
		// context timeout must take the whole subprocess tree with it, not
		// just the direct child, or Wait() can block on a surviving
		// grandchild's held-open stdout/stderr pipe.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		runErr := cmd.Run()
		if runErr != nil && ctx.Err() != nil {
			return outBuf.Bytes(), errBuf.Bytes(), fmt.Errorf("%s pressure: timed out after %s -- process was killed rather than left to hang the fetch forever", estateBin, pressureExecTimeout)
		}
		return outBuf.Bytes(), errBuf.Bytes(), runErr
	}
}

// buildPressureFetch composes pressureRunner with estatus.ParsePressureLine/
// ParsePressureReasons (pure, tested independently) into the seam
// estatus.ReadPressure takes.
//
// src/estate/main.go's own "pressure" case (its "pressure" case, confirmed
// by reading it) has exactly three outcomes this must tell apart:
//   - exit 0: measured, within limits. Reading.OK = true, no reasons.
//   - exit 1: measured, and it refuses -- STILL a real reading (the figures
//     printed to stdout are unconditional), just OK = false with Reasons
//     from stderr. This is Present, not Unreadable: the whole reason
//     src/estate's own gate fails closed is that "we looked and it says no"
//     is actionable information, not blindness.
//   - anything else (binary missing, a non-{0,1} exit such as 2 for "ledger
//     unavailable", or output that does not match the one line format
//     src/estate prints) -- we could not take the reading at all. Returns a
//     non-nil error, which estatus.ReadPressure turns into Unreadable.
func buildPressureFetch(estateBin string) func() (estatus.PressureReading, error) {
	return buildPressureFetchFromRunner(estateBin, execPressureRunner(estateBin))
}

// buildPressureFetchFromRunner is buildPressureFetch's actual logic, split
// out so pressure_test.go can inject a fake pressureRunner directly instead
// of shelling a real `estate` binary out -- this module must still build
// and test with no daemon present (AGENTS.md's "The TUI" section).
func buildPressureFetchFromRunner(estateBin string, run pressureRunner) func() (estatus.PressureReading, error) {
	return func() (estatus.PressureReading, error) {
		stdout, stderr, runErr := run()

		exitErr, isExitErr := asExitError(runErr)
		if runErr != nil && !isExitErr {
			return estatus.PressureReading{}, fmt.Errorf("run `%s pressure`: %w", estateBin, runErr)
		}

		reading, ok := estatus.ParsePressureLine(string(stdout))
		if !ok {
			return estatus.PressureReading{}, fmt.Errorf("could not parse `%s pressure` output: %q", estateBin, string(stdout))
		}

		switch {
		case runErr == nil:
			reading.OK = true
		case isExitErr && exitErr.ExitCode() == 1:
			reading.OK = false
			reading.Reasons = estatus.ParsePressureReasons(string(stderr))
		default:
			// Exit 2 ("ledger unavailable") or any other code this program
			// does not have a defined meaning for -- a figures line may
			// still have printed, but the process itself is telling us
			// something went wrong beyond "refused." Treat as unreadable
			// rather than presenting a verdict this program cannot
			// actually vouch for.
			return estatus.PressureReading{}, fmt.Errorf("`%s pressure` exited %d: %s", estateBin, exitErr.ExitCode(), string(stderr))
		}
		return reading, nil
	}
}

func asExitError(err error) (*exec.ExitError, bool) {
	if err == nil {
		return nil, false
	}
	ee, ok := err.(*exec.ExitError)
	return ee, ok
}
