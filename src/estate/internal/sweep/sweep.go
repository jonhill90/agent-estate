// Package sweep removes dispatch worktrees whose turn has reached a
// terminal state and whose output has been collected -- from a process that
// is not the one that created them.
//
// WHY THIS IS NOT A defer (agent-estate#1000). `estate dispatch` does tear
// its own worktree down when its turn ends, and that path is the common
// case. It is not sufficient, because signals skip defers: a dispatch that
// is OOM-killed, SIGKILLed, or loses its whole tmux server runs no cleanup
// whatsoever, and that is precisely how this host died on 2026-09-03 -- 176
// worktrees, one per turn, none removed. Any design where only the dying
// process can tidy up is the same defect with a longer list of cases.
//
// So teardown here is a thing a THIRD PARTY does about a CORPSE, out of the
// durable record: internal/isolate.Reattach rebuilds the Worktree value from
// the path, branch and base the ledger holds, and the identical
// Worktree.Remove refusals then apply. Nothing about the dead process needs
// to have run; nothing about it even needs to be observable beyond its pid,
// which internal/reclaim already checks.
//
// WHAT IT WILL NOT TOUCH. Eligibility is deliberately narrow, and every
// exclusion is stated rather than silent:
//
//   - `unknown` is never swept, at any age, however dead the process. That
//     is the state a timed-out turn lands in, and the estate's own rule is
//     "unknown is not failed" (ledger.State.Terminal). A worktree kept
//     forever is an annoyance; a worktree deleted out from under work
//     nobody collected is unrecoverable.
//   - `dispatched` is swept only when internal/reclaim positively observes
//     that the process cannot still be the one launched. Age is not
//     evidence, and this package does not re-derive that judgement -- it
//     asks the package that owns it.
//   - a path outside this repository's own dispatch root is refused before
//     anything looks at it, so a corrupted or hand-edited record can never
//     aim `git worktree remove` at something else on the disk.
//
// And eligibility only earns a worktree the right to be OFFERED to
// Worktree.Remove. Remove's three refusals -- uncommitted work, commits
// nothing else references, anything it could not measure -- still decide,
// unchanged and unweakened. This package can only ever cause fewer
// removals than Remove would allow, never more.
package sweep

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/reclaim"
)

// Remover tears one record's worktree down. The real implementation
// (main.go) is internal/isolate.Reattach followed by Worktree.Remove; tests
// supply a fake so the eligibility rules can be driven without a git
// worktree per case. nil means report-only: Run decides and explains, and
// removes nothing.
type Remover func(rec ledger.Record) error

// Config is everything Run needs from outside itself. Every field that
// touches the world is a seam, so the decision logic is exercised against
// fakes rather than a live host.
type Config struct {
	// Root is this repository's dispatch root
	// (internal/isolate.Root(repoRoot)). A record naming a path that is not
	// directly inside it is refused.
	Root string
	// Boot is the host's boot time, passed through to internal/reclaim. A
	// zero value disables reclaim's reboot check exactly as it does there --
	// it narrows what can be judged dead, it never asserts anything.
	Boot time.Time
	// Probe is how a recorded pid is observed. Passed straight through to
	// reclaim.Assess.
	Probe reclaim.Probe
	// Exists reports whether a recorded worktree path is still on disk. A
	// record whose worktree is already gone is reported as such, never
	// removed and never treated as a failure.
	Exists func(path string) bool
	// Remove tears an eligible record's worktree down. nil is report-only.
	Remove Remover
	// Max bounds how many worktrees one run will actually try to remove.
	// Removal of committed work costs a live fetch and a forge round trip
	// each, and this runs on the path to a dispatch; an unbounded sweep
	// would turn "dispatch a turn" into "wait for the network N times".
	// Eligible records beyond the bound are REPORTED as skipped rather than
	// dropped silently -- a cap nobody can see reads as "there was nothing
	// left". Zero or less means no bound.
	Max int
}

// Result is one record's outcome. Every record passed in produces exactly
// one Result, eligible or not: a silent "no" is exactly as unhelpful as a
// silent "yes", the same posture internal/reclaim.Assessment takes.
type Result struct {
	Record ledger.Record
	// Eligible is whether this record's worktree was offered to Remove at
	// all. It says nothing about whether removing it was safe -- that is
	// Remove's judgement, reported in Removed and Reason.
	Eligible bool
	// Removed is true only when Remove was called and returned nil.
	Removed bool
	// Reason is always populated.
	Reason string
}

// Run judges every record and, for the eligible ones, offers each worktree
// to cfg.Remove. Records are expected to be the ledger's CURRENT view (one
// record per task, its latest); passing the full history would judge the
// same worktree several times against stale states.
func Run(records []ledger.Record, cfg Config) []Result {
	out := make([]Result, 0, len(records))
	attempted := 0
	for _, rec := range records {
		r := judge(rec, cfg)
		if !r.Eligible {
			out = append(out, r)
			continue
		}
		if cfg.Max > 0 && attempted >= cfg.Max {
			r.Reason = fmt.Sprintf("eligible (%s) but this run's bound of %d removals is reached -- left for the next sweep, not skipped silently", r.Reason, cfg.Max)
			out = append(out, r)
			continue
		}
		attempted++
		if cfg.Remove == nil {
			r.Reason = "would remove: " + r.Reason + " -- report only, nothing was removed"
			out = append(out, r)
			continue
		}
		if err := cfg.Remove(rec); err != nil {
			r.Reason = "kept: " + err.Error()
			out = append(out, r)
			continue
		}
		r.Removed = true
		r.Reason = "removed: " + r.Reason
		out = append(out, r)
	}
	return out
}

// judge answers whether one record's worktree may be offered to Remove at
// all. It is pure apart from cfg's own seams, so every branch below is
// reachable from a test without a git worktree or a live process.
func judge(rec ledger.Record, cfg Config) Result {
	r := Result{Record: rec}

	if strings.TrimSpace(rec.Worktree) == "" {
		r.Reason = "no worktree path recorded -- nothing to sweep (a record written before agent-estate#1000 added the field)"
		return r
	}
	if !underRoot(cfg.Root, rec.Worktree) {
		r.Reason = fmt.Sprintf("worktree %s is not directly under the dispatch root %s -- refusing to consider it", rec.Worktree, cfg.Root)
		return r
	}
	if cfg.Exists == nil || !cfg.Exists(rec.Worktree) {
		r.Reason = fmt.Sprintf("worktree %s is already gone", rec.Worktree)
		return r
	}

	switch rec.State {
	case ledger.Complete, ledger.Failed:
		r.Eligible = true
		r.Reason = fmt.Sprintf("turn is %s, a terminal state", rec.State)
		return r
	case ledger.Unknown:
		// Never, at any age. See this package's doc comment.
		r.Reason = "turn is unknown, which is not terminal -- it may have done work nothing has collected, so its worktree is kept"
		return r
	default:
		// Still recorded in flight. Only a positive observation that the
		// process cannot be the one dispatched makes this a corpse, and
		// internal/reclaim owns that judgement.
		a := reclaim.Assess(rec, cfg.Boot, cfg.Probe)
		if !a.Reclaimable {
			r.Reason = fmt.Sprintf("turn is %s and %s -- not a corpse, so its worktree stays", rec.State, a.Reason)
			return r
		}
		r.Eligible = true
		r.Reason = fmt.Sprintf("turn is %s but %s -- the dispatch died without tearing down", rec.State, a.Reason)
		return r
	}
}

// underRoot reports whether path names a directory sitting DIRECTLY inside
// root -- not root itself, not something beside it, not something nested
// deeper. This is the same confinement internal/isolate.Reattach applies;
// it is repeated here so a report can explain the refusal without first
// building a Worktree, and so the two must both be broken for a bad path to
// reach `git worktree remove`.
func underRoot(root, path string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !strings.ContainsRune(rel, filepath.Separator)
}
