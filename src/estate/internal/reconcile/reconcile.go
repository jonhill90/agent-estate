// Package reconcile decides whether a turn the ledger still calls in-flight
// could possibly still be running.
//
// WHY IT EXISTS. `unknown is not failed` and an unobserved turn keeps its
// slot -- that rule is right, and it is why the pressure gate refuses new
// work rather than over-committing the host. But nothing ever revisited
// those records, so a turn whose process died left a claim on a slot
// forever. Tonight the estate refused its own council with "8 lanes in
// flight, cap is 6" while only 3 processes existed.
//
// The rule this package must not break: NEVER free a slot for a turn you did
// not observe finish. So it does not guess. It frees a slot only on a
// positive observation that the turn CANNOT be running -- its isolated
// worktree is gone, so there is nowhere for it to be writing. A turn whose
// worktree still exists is left alone, however old, because a long turn and
// a dead one look identical from outside and only one of them is safe to
// reclaim.
package reconcile

import (
	"fmt"
	"os"
	"time"
)

// Candidate is one in-flight record and what can be observed about it.
type Candidate struct {
	ID    string
	Issue string
	Lane  string
	State string
	At    time.Time
	// Worktree is the path recorded at dispatch, empty if none was recorded.
	Worktree string
}

// Verdict is what reconciliation concluded about one candidate.
type Verdict struct {
	ID string
	// Reclaim is true only when the turn positively cannot be running.
	Reclaim bool
	Reason  string
}

// Judge decides each candidate. exists reports whether a path is present;
// it is injected so this package never touches a filesystem in tests.
func Judge(cands []Candidate, exists func(string) bool, now time.Time) []Verdict {
	var out []Verdict
	for _, c := range cands {
		switch {
		case c.State != "dispatched" && c.State != "unknown":
			// Not something this package judges. An unrecognised state is
			// reported, never reclaimed: a state we do not understand is not
			// evidence that a turn ended.
			out = append(out, Verdict{ID: c.ID,
				Reason: fmt.Sprintf("state %q is not a turn this can judge -- left alone", c.State)})
		case c.Worktree == "":
			// No worktree was recorded, so there is nothing to observe. That
			// is could-not-measure, and could-not-measure never frees a slot.
			out = append(out, Verdict{ID: c.ID,
				Reason: "no worktree recorded, so nothing can be observed -- left alone"})
		case exists(c.Worktree):
			out = append(out, Verdict{ID: c.ID,
				Reason: fmt.Sprintf("worktree %s still exists -- a long turn and a dead one look alike, so it keeps its slot", c.Worktree)})
		default:
			out = append(out, Verdict{ID: c.ID, Reclaim: true,
				Reason: fmt.Sprintf("worktree %s is gone, so the turn has nowhere to be writing; dispatched %s and never recorded a result",
					c.Worktree, c.At.UTC().Format(time.RFC3339))})
		}
	}
	return out
}

// Exists is the real observation Judge is given in production.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
