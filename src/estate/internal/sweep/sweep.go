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
//   - and consequently, a sweep run from INSIDE a dispatch worktree sweeps
//     nothing. The root is derived from the caller's own checkout, so a
//     nested caller computes a different isolate.Root() and refuses every
//     record the main checkout wrote. That is the fail-safe direction --
//     nothing is removed on a root it cannot vouch for -- but it means the
//     automatic sweep on the dispatch path is a no-op whenever a dispatch
//     is launched from within a worktree, and this host really does carry
//     several such roots. Draining those needs a sweep run from the shared
//     checkout; it is not something a nested run will get to.
//
// And eligibility only earns a worktree the right to be OFFERED to
// Worktree.Remove. Remove's three refusals -- uncommitted work, commits
// nothing else references, anything it could not measure -- still decide,
// unchanged and unweakened. This package can only ever cause fewer
// removals than Remove would allow, never more.
package sweep

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/isolate"
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
	// RemovalCheck reports whether one worktree would be safe to remove --
	// the exact read-only judgement isolate.Worktree.CheckRemovable
	// performs (committed-and-collected, THEN uncommitted-content), and
	// the same one isolate.Worktree.Remove itself calls before mutating
	// anything. The real implementation (main.go) is isolate.Reattach
	// followed by .CheckRemovable(); tests supply a fake, same as Remove.
	//
	// WHY REPORT MODE NEEDS ITS OWN SEAM FOR THIS (agent-estate#1247's
	// follow-up, two rounds of it): before this field existed at all,
	// report mode (Remove == nil) judged eligibility purely from ledger
	// state and announced "would remove" the moment a record's turn
	// reached a terminal state -- it never asked whether the worktree
	// actually held content only it has. A first fix pass wired this
	// field to DirtyStatus alone, closing the uncommitted-content path --
	// but Remove ALSO refuses via Committed + remoteHasCommit + the
	// Landed seam (a worktree with committed-but-unpushed work), and that
	// path stayed unconsulted: a clean-but-unpushed worktree was still
	// reported "would remove" and then refused by apply, the identical
	// disagreement reached through the other refusal. CheckRemovable is
	// the ONE place both checks now live; report mode calling it through
	// this seam and apply mode's Remove calling it directly means two
	// callers of one judgement, which cannot drift out of agreement with
	// each other -- two parallel implementations of the same rule
	// eventually will. RemovalCheck only ever reads; it never gives
	// report mode any power to mutate anything.
	//
	// nil disables the check: every eligible record reports "would
	// remove" exactly as it did before this field existed. This is the
	// zero value, so any caller (a test exercising something else, an
	// older wiring) that does not set it is unaffected.
	RemovalCheck func(rec ledger.Record) (state isolate.DirtyState, err error)
	// Max bounds how many worktrees one run will actually try to remove.
	// Removal of committed work costs a live fetch and a forge round trip
	// each, and this runs on the path to a dispatch; an unbounded sweep
	// would turn "dispatch a turn" into "wait for the network N times".
	// Eligible records beyond the bound are REPORTED as skipped rather than
	// dropped silently -- a cap nobody can see reads as "there was nothing
	// left". Zero or less means no bound.
	Max int
}

// Category classifies WHY a record landed where it did, independent of the
// free-text Reason -- so a caller can count and report each shape
// separately without parsing prose that is free to change on its own.
//
// agent-estate#1294: `estate sweep-worktrees` ended with a single "0
// removed, 613 left in place" line that silently added three unrelated
// situations together -- 462 ledger rows that were never worktrees at all
// (written before agent-estate#1000 added the field), 146-151 genuinely
// refused because they belong to a different checkout's dispatch root (the
// gap this issue names), and a handful whose directory is already gone.
// All three read as "left in place" and were indistinguishable from each
// other, and from "nothing to clean" -- it-d43a08d739bf32a8: a read that
// failed and an empty result look the same.
type Category int

const (
	// CategoryNoWorktreePath: the record has no Worktree path at all -- a
	// row written before agent-estate#1000 added the field. There was
	// never a worktree here; no sweep, of any kind, can ever act on it.
	// Distinct from CategoryAlreadyGone below, where a worktree genuinely
	// existed once and its directory is what's missing now.
	CategoryNoWorktreePath Category = iota
	// CategoryOutsideRoot: the recorded path is not directly under THIS
	// checkout's own dispatch root -- refused before anything on disk is
	// even looked at. This is the genuine, growing gap agent-estate#1294
	// exists to name: another checkout made this worktree, and this
	// checkout structurally cannot act on it (see underRoot's own doc
	// comment).
	CategoryOutsideRoot
	// CategoryAlreadyGone: the recorded path IS directly under this
	// checkout's own root, but nothing is there any more. The directory is
	// gone; only the ledger row remains.
	CategoryAlreadyGone
	// CategoryKeptByPolicy: judge() declined eligibility for a reason that
	// has nothing to do with the path -- the turn is not terminal, is
	// unknown (never swept, at any age), or is still recorded in flight
	// and not yet positively observed as a corpse. A real worktree,
	// correctly left alone.
	CategoryKeptByPolicy
	// CategoryBoundReached: eligible and not yet judged unsafe, but this
	// run's removal bound (Config.Max) was already spent by earlier
	// records. Left for the next sweep, not skipped silently.
	CategoryBoundReached
	// CategoryRefused: eligible, offered to Remove (apply mode) or
	// RemovalCheck (report mode), and refused -- uncommitted unique
	// content, commits not yet landed anywhere origin can vouch for, or
	// something Remove could not measure.
	CategoryRefused
	// CategoryRemoved: actually removed (apply mode, Remove returned nil)
	// or judged safe to remove (report mode, RemovalCheck found nothing
	// that refuses it, or -- the pre-agent-estate#1247 fallback when
	// RemovalCheck is not wired at all -- reported "would remove"
	// unconditionally, the same as it always has).
	CategoryRemoved
	// CategoryHollow: the worktree directory still exists but has been
	// emptied of every file, .git included -- isolate.HollowCorpse's own
	// positive confirmation (agent-estate#1337). Distinct from
	// CategoryRefused on purpose: nothing here needs collecting, because
	// nothing here still exists to collect. Lumping it in with a genuine
	// refusal is exactly the defect this category closes -- it reads
	// identically to real uncommitted work, and unlike a genuine refusal
	// (which can resolve once the turn commits, or origin gains the
	// content), this shape never resolves on its own, so it would sit in
	// CategoryRefused's bucket forever.
	CategoryHollow
)

func (c Category) String() string {
	switch c {
	case CategoryNoWorktreePath:
		return "no worktree recorded"
	case CategoryOutsideRoot:
		return "outside this checkout's dispatch root"
	case CategoryAlreadyGone:
		return "already gone"
	case CategoryKeptByPolicy:
		return "kept by policy"
	case CategoryBoundReached:
		return "bound reached"
	case CategoryRefused:
		return "refused"
	case CategoryRemoved:
		return "removed"
	case CategoryHollow:
		return "hollow corpse -- content already gone, .git missing"
	default:
		return "unknown category"
	}
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
	// Category is Reason's closed-set classification -- see Category's
	// doc comment. Always populated; a caller sums this, never Reason's
	// text, to count how many of each shape a run produced.
	Category Category
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
			r.Category = CategoryBoundReached
			r.Reason = fmt.Sprintf("eligible (%s) but this run's bound of %d removals is reached -- left for the next sweep, not skipped silently", r.Reason, cfg.Max)
			out = append(out, r)
			continue
		}
		if cfg.Remove == nil {
			attempted++
			out = append(out, reportJudged(r, rec, cfg))
			continue
		}
		if err := cfg.Remove(rec); err != nil {
			// A refusal costs nothing toward this run's bound. The bound
			// exists to cap network round trips a REMOVAL costs (see
			// Config.Max's doc comment) -- a refusal is a local git status
			// plus one bounded fetch/compare, not the thing the bound was
			// sized to limit. Charging it anyway is agent-estate#1247: the
			// same handful of eligible-but-refused records sort first every
			// run, so they alone exhausted the bound and nothing after them
			// was ever tried, forever.
			r.Category = CategoryRefused
			r.Reason = "kept: " + err.Error()
			out = append(out, r)
			continue
		}
		attempted++
		r.Removed = true
		r.Category = CategoryRemoved
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
		r.Category = CategoryNoWorktreePath
		r.Reason = "no worktree path recorded -- nothing to sweep (a record written before agent-estate#1000 added the field)"
		return r
	}
	if !underRoot(cfg.Root, rec.Worktree) {
		r.Category = CategoryOutsideRoot
		r.Reason = fmt.Sprintf("worktree %s is not directly under the dispatch root %s -- refusing to consider it", rec.Worktree, cfg.Root)
		return r
	}
	if cfg.Exists == nil || !cfg.Exists(rec.Worktree) {
		r.Category = CategoryAlreadyGone
		r.Reason = fmt.Sprintf("worktree %s is already gone", rec.Worktree)
		return r
	}

	switch rec.State {
	case ledger.Complete, ledger.Failed:
		r.Eligible = true
		// Category is finalized in Run() once this record's fate (removed,
		// refused, or bound-starved) is known -- judge() alone cannot say
		// which yet.
		r.Reason = fmt.Sprintf("turn is %s, a terminal state", rec.State)
		return r
	case ledger.Unknown:
		// Never, at any age. See this package's doc comment.
		r.Category = CategoryKeptByPolicy
		r.Reason = "turn is unknown, which is not terminal -- it may have done work nothing has collected, so its worktree is kept"
		return r
	default:
		// Still recorded in flight. Only a positive observation that the
		// process cannot be the one dispatched makes this a corpse, and
		// internal/reclaim owns that judgement.
		a := reclaim.Assess(rec, cfg.Boot, cfg.Probe)
		if !a.Reclaimable {
			r.Category = CategoryKeptByPolicy
			r.Reason = fmt.Sprintf("turn is %s and %s -- not a corpse, so its worktree stays", rec.State, a.Reason)
			return r
		}
		r.Eligible = true
		// Category finalized in Run(), same as the terminal-state branch
		// above.
		r.Reason = fmt.Sprintf("turn is %s but %s -- the dispatch died without tearing down", rec.State, a.Reason)
		return r
	}
}

// reportJudged finishes report mode's judgement for one eligible record:
// r already carries judge's ledger-state reason and Eligible=true. This
// consults cfg.RemovalCheck -- the SAME read-only judgement
// isolate.Worktree.CheckRemovable performs, and the one Remove itself calls
// before mutating anything -- so report mode's "would remove" / "would
// keep" agrees with what apply mode would actually do, line for line
// (agent-estate#1247's follow-up, two rounds of it: report mode used to
// judge on ledger state alone; a first fix pass consulted DirtyStatus but
// not Committed/remoteHasCommit/Landed, so a clean-but-unpushed worktree
// still disagreed with apply through that other refusal path).
//
// Eligible stays true either way, matching apply mode's own convention
// (Result.Eligible says nothing about whether removal was judged safe --
// see that field's doc comment); only Reason, and never Removed, changes
// here. This never mutates anything -- RemovalCheck only ever reads.
func reportJudged(r Result, rec ledger.Record, cfg Config) Result {
	if cfg.RemovalCheck == nil {
		// Old behavior, unweakened: a caller that has not wired the check
		// (an older wiring, or a test exercising something else) reports
		// exactly as report mode always did before this field existed --
		// which, having made no refusal judgement at all, counts as
		// CategoryRemoved: it is reporting "would remove" unconditionally,
		// same as it always has.
		r.Category = CategoryRemoved
		r.Reason = "would remove: " + r.Reason + " -- report only, nothing was removed"
		return r
	}
	state, err := cfg.RemovalCheck(rec)
	if err != nil {
		// agent-estate#1337: a hollow corpse is not a genuine refusal --
		// isolate.HollowCorpse already positively confirmed nothing here
		// needs collecting, so this must not read the same as "cannot
		// tell" or "real content, not yet safe". CategoryHollow keeps it
		// distinct rather than lumping it into the same bucket, which is
		// exactly the miscategorization this issue is about: this shape
		// never resolves the way a genuine refusal can (the turn
		// committing, origin gaining the content), so it would sit in
		// CategoryRefused forever otherwise.
		var hollow *isolate.ErrHollowCorpse
		if errors.As(err, &hollow) {
			r.Category = CategoryHollow
			r.Reason = fmt.Sprintf("would reconcile: %s -- %s", r.Reason, err)
			return r
		}
		// CheckRemovable's error text already names the specific refusal
		// -- it has several distinct shapes now (uncommitted-unique
		// content, committed-but-not-on-origin-and-not-landed, cannot
		// even tell) -- so it is reported verbatim rather than
		// reconstructed here, where reconstructing it would drift out of
		// sync with whichever shape actually fired. Remove itself fails
		// closed on this exact error; report mode must agree, not
		// optimistically call it removable.
		r.Category = CategoryRefused
		r.Reason = fmt.Sprintf("would keep: %s -- %s", r.Reason, err)
		return r
	}
	// DirtyStateClean and DirtyStateSuperseded: CheckRemovable found
	// nothing that refuses removal (committed-and-collected, or clean --
	// see its doc comment), so Remove would proceed (the latter with
	// --force, since git itself refuses "modified or untracked files"
	// without regard for DirtyStatus's own byte-for-byte proof -- see
	// Remove's comment). Named explicitly, not collapsed, so the typed
	// states stay distinguishable in "would remove" output too, not only
	// in refusals.
	r.Category = CategoryRemoved
	r.Reason = fmt.Sprintf("would remove: %s (%s) -- report only, nothing was removed", r.Reason, state)
	return r
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
