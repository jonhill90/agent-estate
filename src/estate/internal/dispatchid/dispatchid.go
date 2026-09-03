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
	"strings"
	"sync/atomic"
	"time"
)

// seq disambiguates ids minted inside one process in the same nanosecond.
// Nanoseconds are enough across processes; this covers a tight loop.
var seq atomic.Uint64

// New returns the id for a turn on issue.
//
// Nanosecond precision, because second precision made concurrent dispatch
// impossible. The issue keeps its "#" stripped so the id stays a single safe
// path element -- internal/isolate refuses anything else, and the id names a
// directory.
func New(issue string, now time.Time) string {
	n := seq.Add(1)
	return fmt.Sprintf("%s-%d-%d", strings.TrimPrefix(issue, "#"), now.UTC().UnixNano(), n)
}
