package cost

import (
	"sync"
	"time"
)

// Cached wraps fetch so that the underlying `ccusage` (and quota.sh) calls
// it makes run at most once per ttl, no matter how many independent
// callers ask -- and collapses any callers that ask while a fetch is
// already running into that same in-flight call, rather than starting a
// second one.
//
// Why this exists: agent-tui#145 measured three real `ccusage daily`
// subprocess trees inside a 30-second window against a Fetcher documented
// (both here and in internal/rail) to run once every five minutes. The
// cause was not a bug inside any one caller's own refresh logic -- each of
// railModel, costModel and dashboardModel (cmd/estate/main.go) is hooked
// to the exact same costFetch closure ("one Fetcher, three consumers",
// main.go's own comment), but each is its own tea.Model with its own
// Init()-time fetch and its own independently-armed refresh ticker. Two of
// the three fire in the same second because railModel's and costModel's
// Init() both run in the same tea.Batch at shell startup
// (internal/shell.Model.Init); the third arrives a few seconds later
// because dashboardModel's fetch (cmd/estate/dashboard.go's
// buildDashboardFetch) only reaches its own costFetch() call after its
// sessions/gh work ahead of it finishes. None of the three Models is
// wrong on its own -- costFetch itself was simply never shared state, so
// "one Fetcher" only meant one closure, not one call. Cached is what makes
// it one call: wire it around costFetch exactly once in cmd/estate/main.go
// before handing it to any consumer, and every consumer's own Init-time
// fetch plus every consumer's own 5-minute ticker still fire on their own
// schedules, but only the first to actually ask inside each ttl window
// pays for a real subprocess -- the rest read the cached Snapshot (or
// join the in-flight call if one is already running).
//
// now is injected so tests can control ttl expiry without a real sleep,
// the same seam buildCostFetch already takes for the same reason.
func Cached(fetch Fetcher, ttl time.Duration, now func() time.Time) Fetcher {
	var (
		mu       sync.Mutex
		have     bool
		lastAt   time.Time
		lastSnap Snapshot
		lastErr  error
		inFlight bool
		waiters  []chan struct{}
	)

	return func() (Snapshot, error) {
		mu.Lock()
		if have && now().Sub(lastAt) < ttl {
			snap, err := lastSnap, lastErr
			mu.Unlock()
			return snap, err
		}
		if inFlight {
			ch := make(chan struct{})
			waiters = append(waiters, ch)
			mu.Unlock()
			<-ch
			mu.Lock()
			snap, err := lastSnap, lastErr
			mu.Unlock()
			return snap, err
		}
		inFlight = true
		mu.Unlock()

		snap, err := fetch()

		mu.Lock()
		lastSnap, lastErr, lastAt, have, inFlight = snap, err, now(), true, false
		woken := waiters
		waiters = nil
		mu.Unlock()

		for _, ch := range woken {
			close(ch)
		}
		return snap, err
	}
}
