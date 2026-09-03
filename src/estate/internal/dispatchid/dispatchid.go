// Package dispatchid mints the identity of one dispatched turn.
//
// WHY IT IS ITS OWN PACKAGE. The id was `<issue>-<unix seconds>`, so two
// turns dispatched in the same second got the SAME id. internal/isolate then
// refused the second one -- correctly, because sharing a worktree is exactly
// what isolation prevents -- and the refusal read as a mysterious "already
// exists" rather than "your ids collided".
//
// That made a council impossible: three seats launched together produced one
// id and two refusals. The review mechanism this estate depends on is
// inherently parallel, and its identity scheme forbade parallelism.
//
// The same defect was solved a few hours earlier in internal/knowledge, whose
// ids carry the operator's own "prevents agent collision" comment. Seconds
// were not enough there either.
package dispatchid

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// seq disambiguates ids minted inside one process in the same instant.
//
// It is NOT enough on its own, and the first version of this file wrongly
// said nanoseconds covered the cross-process case. Three council seats
// found the same defect: `estate dispatch` is a separate OS process per
// turn, so seq resets to zero every time and every real dispatch got seq=1.
// One seat measured this machine's clock and found it advances in ~1000ns
// steps, not true nanoseconds -- so uniqueness rested on two processes never
// starting inside the same microsecond, which is luck rather than design.
//
// The PR's own evidence showed it: all three ids ended in "-1".
var seq atomic.Uint64

// New returns the id for a turn on issue.
//
// Nanosecond precision, because second precision made concurrent dispatch
// impossible. The issue keeps its "#" stripped so the id stays a single safe
// path element -- internal/isolate refuses anything else, and the id names a
// directory.
// New returns the id for a turn on issue.
//
// Three components, each covering what the others cannot:
//
//   - the timestamp orders ids and keeps them readable, since the id names a
//     directory a human may have to inspect after a failed teardown;
//   - the PROCESS ID separates concurrent dispatches, because the operating
//     system guarantees two live processes never share one -- this is the
//     part that actually fixes the council case;
//   - the sequence separates calls inside one process, which the pid cannot.
//
// The issue keeps its "#" stripped so the id stays a single safe path
// element; internal/isolate refuses anything else, and the id names a
// directory and a branch.
func New(issue string, now time.Time) string {
	n := seq.Add(1)
	return fmt.Sprintf("%s-%d-%d-%d",
		strings.TrimPrefix(issue, "#"), now.UTC().UnixNano(), os.Getpid(), n)
}
