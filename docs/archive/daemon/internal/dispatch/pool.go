package dispatch

import (
	"context"
	"runtime"
	"sync"

	"agent-supervisor/daemon/internal/agent"
	"agent-supervisor/daemon/internal/ledger"
)

// Job is one unit of concurrent work.
type Job struct {
	TaskID string
	Lane   string
	Brief  string
	Cwd    string
	// Harness names which agent.Adapter this job dispatches through --
	// "claude", "codex", or empty (caller's mk decides the default). This is
	// the field that makes adapter selection PER JOB rather than per batch:
	// a mk func keyed on Job.Harness can hand back a *agent.Claude for one
	// row of a batch file and a *agent.Codex for the next, so one -file can
	// mix harnesses across a genuinely multi-harness estate.
	Harness string
	// Issue is this job's own GitHub issue, or nil for a task with no issue
	// behind it (a demo/proof task, e.g.) -- passed straight to RunGated;
	// see IssueRef's own doc comment for why nil is a real, distinct state
	// here, not a zero value standing in for "issue 0".
	Issue *IssueRef
}

// DefaultConcurrency leaves headroom rather than saturating the machine.
//
// This is not arbitrary. On 2026-08-21 the shell supervisor ran ~26 concurrent
// agents, drove the 1-minute load to 27 and swap to 8.7GB of 10.2GB, and made
// the Mac unresponsive to typing -- twice, forcing a restart. A supervisor that
// makes its operator's machine unusable has failed regardless of what it
// merged. Cores-2, floored at 2 and capped at 8.
func DefaultConcurrency() int {
	n := runtime.NumCPU() - 2
	if n < 2 {
		n = 2
	}
	if n > 8 {
		n = 8
	}
	return n
}

// RunPool dispatches jobs concurrently, at most `workers` at once, and returns
// every outcome. It does NOT stop on the first failure: one agent failing tells
// you nothing about the others, and a partial result set is exactly the
// "could not measure" state that has to stay distinguishable from success.
//
// Ordering of the returned slice matches the input, so a caller can pair
// outcomes to jobs without threading an index through.
func RunPool(ctx context.Context, l *ledger.DB, mk func(Job) (agent.Adapter, string), jobs []Job, workers int) []Outcome {
	return RunPoolGated(ctx, l, mk, jobs, workers, Gates{})
}

// RunPoolGated is RunPool with pre-spawn gates evaluated PER JOB, not once for
// the batch. Spend accumulates while the batch runs, so a cap checked only at
// the start is a cap that does not bind.
//
// `mk` returns (agent.Adapter, harness) rather than just an adapter -- this
// is what makes per-job adapter selection possible AND keeps the ledger
// honest: mk reads j.Harness (or applies its own default when a job leaves
// it empty) and hands back both the concrete adapter it built and the
// harness NAME it actually resolved to, so RunGated's EnsureLane call
// records what really ran rather than trusting j.Harness verbatim -- a job
// that left Harness empty and got mk's default adapter must not record an
// empty/wrong harness column just because the Job itself never named one.
// RunPoolGated itself never branches on harness; it drives whatever mk
// handed back through the same RunGated call every other job uses.
func RunPoolGated(ctx context.Context, l *ledger.DB, mk func(Job) (agent.Adapter, string), jobs []Job, workers int, g Gates) []Outcome {
	if workers <= 0 {
		workers = DefaultConcurrency()
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}
	out := make([]Outcome, len(jobs))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j Job) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				out[i] = Outcome{TaskID: j.TaskID, Err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			a, resolvedHarness := mk(j)
			out[i] = RunGated(ctx, l, a, j.Cwd, resolvedHarness, j.TaskID, j.Lane, j.Brief, j.Issue, g)
		}(i, j)
	}
	wg.Wait()
	return out
}
