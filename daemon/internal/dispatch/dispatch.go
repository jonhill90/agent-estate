// Package dispatch is the one place a task goes from "recorded" to "resolved".
//
// The entire lifecycle is in ONE function with no gap another writer can slip
// into. In the shell supervisor this lifecycle was spread across dispatch.sh,
// watchdog.sh, reconcile_lane_completions.py and a reaper, each of which could
// write a status the others had not observed. That is how a task got stamped
// `failed` while its process was alive, and how 473 of 850 "complete" tasks
// have no acceptance timestamp.
package dispatch

import (
	"context"
	"fmt"
	"time"

	"agent-supervisor/daemon/internal/agent"
	"agent-supervisor/daemon/internal/ledger"
)

type Outcome struct {
	TaskID    string
	OK        bool
	SessionID string
	Turns     int
	Elapsed   time.Duration
	Err       error
}

// Run creates the task, starts the agent, waits for the process to exit, and
// writes exactly one terminal stamp from what it observed.
//
// On timeout the task is deliberately left NON-terminal. A turn that has not
// come back is UNKNOWN, not failed -- the distinction #488 was filed over. A
// human or a later liveness check resolves it; this function will not guess.
func Run(ctx context.Context, l *ledger.DB, a *agent.Claude, taskID, lane, brief string) Outcome {
	start := time.Now()
	if err := l.EnsureLane(lane, a.Cwd); err != nil {
		return Outcome{TaskID: taskID, Err: err}
	}
	if err := l.Create(ledger.Task{
		ID: taskID, Lane: lane, Summary: firstLine(brief), WorktreePath: a.Cwd,
	}); err != nil {
		return Outcome{TaskID: taskID, Err: err}
	}
	if err := l.MarkRunning(taskID); err != nil {
		return Outcome{TaskID: taskID, Err: err}
	}

	res, err := a.Run(ctx, brief)
	el := time.Since(start)

	if err != nil {
		// UNKNOWN, not failed. Leave the row `running` for a liveness check.
		if isTimeout(err) {
			return Outcome{TaskID: taskID, OK: false, Elapsed: el,
				Err: fmt.Errorf("%w -- task left non-terminal on purpose", err)}
		}
		if ferr := l.Finish(taskID, false); ferr != nil {
			return Outcome{TaskID: taskID, Elapsed: el, Err: fmt.Errorf("%v (and stamp failed: %v)", err, ferr)}
		}
		return Outcome{TaskID: taskID, OK: false, Elapsed: el, Err: err}
	}

	if ferr := l.Finish(taskID, true); ferr != nil {
		return Outcome{TaskID: taskID, OK: true, Elapsed: el, Err: ferr}
	}
	return Outcome{
		TaskID: taskID, OK: true, SessionID: res.SessionID,
		Turns: res.NumTurns, Elapsed: el,
	}
}

func isTimeout(err error) bool {
	return err != nil && (err == agent.ErrTimeout ||
		(len(err.Error()) > 0 && contains(err.Error(), "did not complete before the deadline")))
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			s = s[:i]
			break
		}
	}
	if len(s) > 120 {
		s = s[:120]
	}
	if s == "" {
		return "(no summary)"
	}
	return s
}
