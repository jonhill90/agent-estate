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
func RunPool(ctx context.Context, l *ledger.DB, mk func(Job) *agent.Claude, jobs []Job, workers int) []Outcome {
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

			a := mk(j)
			out[i] = Run(ctx, l, a, j.TaskID, j.Lane, j.Brief)
		}(i, j)
	}
	wg.Wait()
	return out
}
