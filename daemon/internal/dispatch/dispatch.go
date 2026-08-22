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
	"agent-supervisor/daemon/internal/claim"
	"agent-supervisor/daemon/internal/ledger"
	"agent-supervisor/daemon/internal/pressure"
)

// Gates are checked BEFORE a process is spawned. Budget and Pressure are
// read-only refusals, not warnings: the estate's failures came from checks
// that reported a problem and carried on anyway. Claim is different in
// kind -- taking it is a MUTATION (a GitHub issue assignee write via
// claim.sh), not a read -- so it is not part of Check() below; RunGated
// calls Claim.Take itself, after Check() passes and before any ledger
// write, and Claim.Release from both of its own terminal stamps. nil
// disables each independently.
type Gates struct {
	Budget   *budget.Tracker  // nil disables
	Pressure *pressure.Limits // nil disables
	Claim    claim.Gate       // nil disables -- see RunGated's own issue/repo params
}

// Check runs every READ-ONLY gate. First refusal wins and names itself.
// Claim is not checked here -- see Gates' own doc comment.
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

// IssueRef names the GitHub issue a task corresponds to, for Gates.Claim.
// Not every task this daemon dispatches is issue-backed (a demo/proof task
// has no issue at all) -- nil (the "no issue" case, distinct from a
// zero-value IssueRef) means RunGated skips the claim step entirely, the
// same "nil disables" convention Gates.Budget/Gates.Pressure already use.
type IssueRef struct {
	Number int
	Repo   string // OWNER/NAME; empty lets claim.sh resolve it from cwd
}

// Run creates the task, starts the agent, waits for the process to exit, and
// writes exactly one terminal stamp from what it observed.
//
// On timeout the task is deliberately left NON-terminal. A turn that has not
// come back is UNKNOWN, not failed -- the distinction #488 was filed over. A
// human or a later liveness check resolves it; this function will not guess.
func Run(ctx context.Context, l *ledger.DB, a agent.Adapter, cwd, harness, taskID, lane, brief string) Outcome {
	return RunGated(ctx, l, a, cwd, harness, taskID, lane, brief, nil, Gates{})
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
//
// `issue` is this task's own IssueRef, or nil for a task with no GitHub
// issue behind it. When both `issue` and `g.Claim` are non-nil, the claim
// is taken BEFORE any ledger write below -- a real collision gap this
// daemon had until now: it wrote lanes/tasks directly with no claim/lease
// logic at all, the SAME ledger.sqlite3 file the tmux/cli.py side manages
// WITH one (scripts/supervisor/claim.sh, built after issue #28 was
// dispatched twice, once by the Director and once by the supervisor, for
// exactly this reason -- see claim.go's own doc comment). A failed claim
// refuses the dispatch outright, the same fail-closed shape Check()'s own
// refusals already take: no ledger row, no process, no cost.
func RunGated(ctx context.Context, l *ledger.DB, a agent.Adapter, cwd, harness, taskID, lane, brief string, issue *IssueRef, g Gates) Outcome {
	start := time.Now()
	if err := g.Check(); err != nil {
		// Refused before anything was created: no ledger row, no process, no
		// cost. A refusal is not a failed task.
		return Outcome{TaskID: taskID, Err: err, Liveness: agent.LivenessBlocked}
	}
	if issue != nil && g.Claim != nil {
		if err := g.Claim.Take(ctx, issue.Number, issue.Repo, lane); err != nil {
			// Same shape as Check()'s own refusals: nothing written yet.
			return Outcome{TaskID: taskID, Err: fmt.Errorf("claim gate: %w", err), Liveness: agent.LivenessBlocked}
		}
	}
	release := func() {
		if issue != nil && g.Claim != nil {
			// Best-effort: a release failure here does not change what
			// already happened to the task (the dispatch itself already
			// has its own terminal stamp by the time this runs) -- it
			// leaves a claim outstanding for claim.sh's own stale/audit/
			// reap sweep to catch later, the same second-half cleanup its
			// own header comment already documents relying on for a
			// killed process. Not folded into Outcome.Err: this function
			// has no logger of its own, and overwriting a real dispatch
			// error with a release failure would hide the more important
			// one.
			_ = g.Claim.Release(ctx, issue.Number, issue.Repo)
		}
	}
	if err := l.EnsureLane(lane, cwd, harness); err != nil {
		release()
		return Outcome{TaskID: taskID, Err: err}
	}
	if err := l.Create(ledger.Task{
		ID: taskID, Lane: lane, Summary: firstLine(brief), WorktreePath: cwd,
	}); err != nil {
		release()
		return Outcome{TaskID: taskID, Err: err}
	}
	if err := l.MarkRunning(taskID); err != nil {
		release()
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
		// UNKNOWN, not failed. Leave the row `running` for a liveness check
		// -- and leave the claim in place too: the lane may still be
		// working, and releasing it here would let a second dispatcher
		// take the same issue out from under a task that has not actually
		// stopped. claim.sh's own stale/audit/reap sweep is what resolves
		// an abandoned claim later, same as it already does for a killed
		// tmux pane.
		if isTimeout(err) {
			return Outcome{TaskID: taskID, OK: false, Elapsed: el,
				Err: fmt.Errorf("%w -- task left non-terminal on purpose", err)}
		}
		release()
		if ferr := l.Finish(taskID, false); ferr != nil {
			return Outcome{TaskID: taskID, Elapsed: el, Err: fmt.Errorf("%v (and stamp failed: %v)", err, ferr)}
		}
		return Outcome{TaskID: taskID, OK: false, Elapsed: el, Liveness: live, Err: err}
	}

	release()
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
