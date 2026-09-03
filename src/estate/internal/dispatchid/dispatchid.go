// Package dispatchid mints the id `estate dispatch` gives one turn.
//
// WHY THIS PACKAGE EXISTS. The id is not decoration: it names both the
// turn's worktree directory and its git branch (see internal/isolate), and
// isolate.Create refuses anything that is not a single safe path element.
// Two turns that receive the same id do not get two worktrees -- the second
// one is refused outright, silently shrinking whatever fan-out dispatched
// them. That happened: three review seats dispatched at once produced two
// identical ids and one legitimate refusal, twice in one night.
//
// WHY A SECOND-RESOLUTION TIMESTAMP WAS NOT ENOUGH. The previous id was
// issue + time.Now().UTC().Unix() -- one-second resolution. Any two
// `estate dispatch` invocations launched by a human or a script within the
// same wall-clock second collided outright.
//
// WHY A NANOSECOND TIMESTAMP, OR A COUNTER, IS STILL NOT A GUARANTEE.
// Narrowing to UnixNano shrinks the collision window but does not close it:
// it is a probability, not a proof, and clock resolution on some platforms
// is coarser than a nanosecond in practice. A counter is worse, not better,
// for the failure mode this package exists to fix: `estate dispatch` is one
// process per turn, so a counter held in package state -- however carefully
// guarded by a mutex inside that process -- starts at its initial value
// again in the very next process. Two dispatches launched as two separate
// `estate` invocations each see their own counter start from zero; it
// contributes nothing to uniqueness ACROSS processes, which is exactly the
// case three concurrent dispatch invocations are.
//
// THE FIX. Mint a candidate id, then CLAIM it by creating a file for it with
// O_EXCL in a directory every dispatch on this machine shares, regardless of
// which process it is. O_EXCL is atomic at the filesystem layer -- the
// kernel guarantees exactly one of any number of concurrent creators of the
// same path succeeds -- which neither an in-memory counter nor a timestamp
// comparison can promise across process boundaries. A losing attempt is not
// a failure: it mutates the candidate with a random suffix and retries,
// because the caller asked for a unique id, not for permission to give up.
//
// WHY SHARING $TMPDIR ACROSS DISPATCH PROCESSES IS NOT A NEW ASSUMPTION, NOT
// AN UNCHECKED ONE. This package's cross-process guarantee only holds between
// processes that resolve os.TempDir() to the same directory. Reviewed twice
// (PR #933) as a possible gap: internal/isolate.Root already derives a turn's
// WORKTREE ROOT from os.TempDir() -- not just this package's claim directory.
// Two processes with different effective $TMPDIR build different worktree
// roots, so they could never have shared a worktree path to collide on in
// the first place; the bug this package exists to fix (isolate.Create
// refusing a reused id and silently dropping a seat) requires the SAME root,
// which requires the SAME $TMPDIR precondition isolate already depends on.
// This package rides an assumption isolate.Root makes, not a new one.
// TestClaimDirSharesIsolateRoot asserts this structurally rather than
// leaving it as narrative. See agent-estate#936 for the reviewed record of
// this decision, including what was checked and ruled out.
package dispatchid

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxAttempts bounds the retry loop. Each retry appends 32 random bits, so
// reaching this bound without success would require repeated collisions
// against an unpredictable value -- treated as an environment failure
// (a claim directory that cannot be written to), not a real exhaustion of
// the id space.
const maxAttempts = 100

// claimDirEnv overrides claimDir. It exists for tests only: without it,
// every `go test` run claims ids in the exact directory real `estate
// dispatch` invocations use, which is how a review of this package (#936)
// found 247 stale claim files after an hour of ordinary test runs on one
// machine. Tests set this to a t.TempDir(); nothing else should set it.
const claimDirEnv = "ESTATE_DISPATCHID_CLAIM_DIR"

// claimDir is where minted ids register themselves to guarantee cross-process
// uniqueness. It lives outside any repository, next to isolate.Root's own
// worktree root -- the same reasoning applies: a runaway process must not
// write into a shared checkout, and a claim file is exactly that kind of
// incidental write.
func claimDir() string {
	if dir := os.Getenv(claimDirEnv); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "estate-dispatch-ids")
}

// New mints an id for a dispatched turn against issue, and guarantees that no
// other process -- concurrently, right now, on this machine -- receives the
// same one. The returned id is always a single safe path element: it is
// built only from the issue's digits, ASCII hex digits, and '-'.
func New(issue string) (string, error) {
	dir := claimDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("dispatchid: cannot create claim directory %s: %w", dir, err)
	}
	pruneStale(dir)

	issue = strings.TrimPrefix(strings.TrimSpace(issue), "#")
	if issue == "" {
		issue = "issue"
	}
	base := fmt.Sprintf("%s-%d", issue, time.Now().UTC().UnixNano())

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		candidate := base
		if attempt > 0 {
			suffix, err := randomSuffix()
			if err != nil {
				return "", fmt.Errorf("dispatchid: cannot generate a distinguishing suffix: %w", err)
			}
			candidate = base + "-" + suffix
		}

		claim := filepath.Join(dir, candidate)
		f, err := os.OpenFile(claim, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return candidate, nil
		}
		if !os.IsExist(err) {
			return "", fmt.Errorf("dispatchid: cannot claim %s: %w", candidate, err)
		}
		// Someone else claimed this exact candidate at this exact instant.
		// Retry with a random suffix rather than reporting failure.
		lastErr = err
	}
	return "", fmt.Errorf("dispatchid: could not mint a unique id for issue %q after %d attempts: %w", issue, maxAttempts, lastErr)
}

// staleAge is how long a claim file may sit before a sweep removes it. A
// claim file's only job is winning a race between mints landing in the same
// instant (see the package doc); once New returns, nothing ever reads that
// file again, so a file older than this was never going to be consulted --
// keeping it is pure disk cost, not a safety margin. Chosen generously
// (well past any plausible dispatch cadence) so a sweep can never remove a
// claim another in-flight New call still needs. See #936.
const staleAge = 7 * 24 * time.Hour

// pruneStale removes claim files older than staleAge from dir. It is
// deliberately best-effort: a failed sweep is dropped, never surfaced, and
// never blocks minting -- New's own claim attempt below is what has to fail
// closed, not this hygiene pass. Runs once per New call; ReadDir on a
// directory of stale zero-byte files is cheap relative to the rest of a
// dispatch.
func pruneStale(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-staleAge)
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, entry.Name()))
	}
}

func randomSuffix() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
