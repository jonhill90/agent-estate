// Package dispatchid mints the identity of one dispatched turn.
//
// THIRD ATTEMPT. See agent-estate#938 for the full record; this package
// exists because two prior fixes did not merge.
//
//   - The original scheme was `<issue>-<unix seconds>`. Two turns dispatched
//     in the same second minted the SAME id, internal/isolate correctly
//     refused the second one, and a parallel council silently became a
//     smaller one -- observed twice: three seats produced one id and two
//     refusals, and later two ids and one refusal.
//   - #926 (hand-authored, so it could not merge under the no-hand-authored
//     rule) fixed this with pid+nanos+seq -- no filesystem dependency. Its
//     weakness was evidential: its own test hand-constructed the "other
//     process" id rather than re-execing a real one.
//   - #933 fixed it with O_EXCL claim files under $TMPDIR, with a genuine
//     8-subprocess test. It was closed because its cross-process guarantee
//     assumed every dispatching process resolves os.TempDir() to the same
//     directory, and that assumption is false: internal/isolate.Create
//     creates the dispatch BRANCH (`dispatch/<id>`) in repoRoot, the shared
//     checkout, not under $TMPDIR. Two dispatches with different $TMPDIR
//     values get distinct worktree paths and can still collide on branch
//     name, which no $TMPDIR-scoped claim file can arbitrate.
//
// This package takes #926's mechanism -- no filesystem dependency at all --
// and proves it with #933's evidence standard: real re-execed OS processes,
// not goroutines and not a hand-constructed peer (see dispatchid_test.go's
// TestNew_ConcurrentProcesses).
//
// WHY NO FILESYSTEM DEPENDENCY WORKS HERE. Uniqueness rests on three
// independent components, each covering what the others cannot:
//
//   - the timestamp (nanosecond, not second, precision) orders ids and keeps
//     them readable -- the id names a directory a human may have to inspect
//     after a failed teardown;
//   - the PROCESS ID separates concurrent dispatches. The operating system
//     guarantees two LIVE processes never share a pid, and both racing
//     dispatches are live by definition -- this is the part that actually
//     fixes the council case, and it needs no coordination file because the
//     kernel is already the arbiter;
//   - the in-process sequence separates calls minted by one process (e.g. a
//     future caller that dispatches several turns without re-executing),
//     which the pid alone cannot.
//
// NAMED LIMIT. This guarantee holds only where a pid genuinely identifies one
// live process estate-wide. It does NOT hold inside a container/PID-namespace
// context where pid 1 is not unique across containers sharing a filesystem --
// two containers can each have their own pid 1 and collide. That is not this
// repo's deployment shape (launchd + tmux on one host, no containers), but it
// is written down here rather than left to be discovered a third time.
package dispatchid

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// seq disambiguates ids minted inside one process in the same instant. It is
// process-local by construction (a package-level atomic counter), which is
// exactly right: two SEPARATE processes each start their own seq at zero, and
// nothing needs to reconcile that, because the pid component already tells
// them apart.
var seq atomic.Uint64

// New returns the id for a turn on issue, minted at now.
//
// The issue keeps its leading "#" stripped so the id stays a single safe path
// element -- internal/isolate refuses anything else, and the id names both a
// worktree directory and a branch (see isolate.safeID).
func New(issue string, now time.Time) string {
	n := seq.Add(1)
	return fmt.Sprintf("%s-%d-%d-%d",
		strings.TrimPrefix(issue, "#"), now.UTC().UnixNano(), os.Getpid(), n)
}
