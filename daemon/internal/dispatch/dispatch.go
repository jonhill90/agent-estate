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
	"agent-supervisor/daemon/internal/budget"
	"agent-supervisor/daemon/internal/ledger"
	"agent-supervisor/daemon/internal/pressure"
)

// Gates are checked BEFORE a process is spawned. Both are refusals, not
// warnings: the estate's failures came from checks that reported a problem and
// carried on anyway.
type Gates struct {
	Budget   *budget.Tracker  // nil disables
	Pressure *pressure.Limits // nil disables
}

// Check runs every gate. First refusal wins and names itself.
func (g Gates) Check() error {
	if g.Pressure != nil {
		if r := pressure.Check(*g.Pressure); !r.OK {
			return fmt.Errorf("host pressure gate: %s", r.Reason)
		}
	}
	if g.Budget != nil {
		if v := g.Budget.Check(); v.Decision == budget.Block {
			return fmt.Errorf("budget gate: %s", v)
		}
	}
	return nil
}

type Outcome struct {
	TaskID    string
	OK        bool
	SessionID string
	Turns     int
	Elapsed   time.Duration
	// Liveness is the QUALITY verdict, orthogonal to OK. "exit 0" is not
	// "did work" -- agent-supervisor#414 in one field.
	Liveness agent.Liveness
	CostUSD  float64
	Err      error
}

// Run creates the task, starts the agent, waits for the process to exit, and
// writes exactly one terminal stamp from what it observed.
//
// On timeout the task is deliberately left NON-terminal. A turn that has not
// come back is UNKNOWN, not failed -- the distinction #488 was filed over. A
// human or a later liveness check resolves it; this function will not guess.
func Run(ctx context.Context, l *ledger.DB, a agent.Adapter, cwd, harness, taskID, lane, brief string) Outcome {
	return RunGated(ctx, l, a, cwd, harness, taskID, lane, brief, Gates{})
}

// RunGated is Run with pre-spawn gates. `a` is an agent.Adapter, not a
// *agent.Claude -- this is the one place the daemon becomes multi-harness:
// whatever the caller passed (Claude, Codex, or a future adapter) is driven
// through the exact same lifecycle, gates, ledger stamps and Liveness
// classification. Nothing below this line knows or cares which vendor CLI
// `a` wraps.
//
// `cwd` and `harness` are passed explicitly rather than read off the
// adapter (a.Cwd, as the old *agent.Claude-typed signature did) because
// Adapter is deliberately narrow -- Run(ctx, prompt) only, see adapter.go's
// own doc comment -- and every caller already has both in hand from the
// same Job/flag it used to build `a`. `harness` flows straight into
// ledger.EnsureLane so the lane's own `harness` column records which
// adapter actually ran, not a hardcoded 'claude' regardless of `a`'s real
// type (see EnsureLane's own doc comment for the bug this replaces).
func RunGated(ctx context.Context, l *ledger.DB, a agent.Adapter, cwd, harness, taskID, lane, brief string, g Gates) Outcome {
	start := time.Now()
	if err := g.Check(); err != nil {
		// Refused before anything was created: no ledger row, no process, no
		// cost. A refusal is not a failed task.
		return Outcome{TaskID: taskID, Err: err, Liveness: agent.LivenessBlocked}
	}
	if err := l.EnsureLane(lane, cwd, harness); err != nil {
		return Outcome{TaskID: taskID, Err: err}
	}
	if err := l.Create(ledger.Task{
		ID: taskID, Lane: lane, Summary: firstLine(brief), WorktreePath: cwd,
	}); err != nil {
		return Outcome{TaskID: taskID, Err: err}
	}
	if err := l.MarkRunning(taskID); err != nil {
		return Outcome{TaskID: taskID, Err: err}
	}

	res, err := a.Run(ctx, brief)
	el := time.Since(start)
	live := agent.Classify(res, err)

	// Record spend from the CLI's own figure, as soon as the turn returns and
	// BEFORE the next gate check. Paperclip's own caveat applies: the cap is
	// evaluated between cost events, so one expensive call can overshoot --
	// the bound on overshoot is this granularity.
	if g.Budget != nil && res != nil && res.CostUSD > 0 {
		g.Budget.Record(res.CostUSD)
	}

	if err != nil {
		// UNKNOWN, not failed. Leave the row `running` for a liveness check.
		if isTimeout(err) {
			return Outcome{TaskID: taskID, OK: false, Elapsed: el,
				Err: fmt.Errorf("%w -- task left non-terminal on purpose", err)}
		}
		if ferr := l.Finish(taskID, false); ferr != nil {
			return Outcome{TaskID: taskID, Elapsed: el, Err: fmt.Errorf("%v (and stamp failed: %v)", err, ferr)}
		}
		return Outcome{TaskID: taskID, OK: false, Elapsed: el, Liveness: live, Err: err}
	}

	if ferr := l.Finish(taskID, true); ferr != nil {
		return Outcome{TaskID: taskID, OK: true, Elapsed: el, Err: ferr}
	}
	return Outcome{
		TaskID: taskID, OK: true, SessionID: res.SessionID,
		Turns: res.NumTurns, Elapsed: el, Liveness: live, CostUSD: res.CostUSD,
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
