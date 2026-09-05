package knowledge

// This file is agent-estate#1191's permanent backstop, taken regardless of
// whichever primary guard (an OS immutable flag on the shared index, out of
// scope here -- see the issue's design comment) is or is not ever applied.
// The `generated_by` field (agent-estate#1082) let the incident that opened
// #1191 get diagnosed in under a minute, but only because a human happened
// to look at it. This makes that check automatic: does a compiled index's
// own GeneratedBy.Commit predate the commit that introduced the
// shared-write acknowledgement guard (agent-estate#1185, 2a6117f) -- i.e.
// was this index necessarily written by a binary with no way to refuse an
// unacknowledged shared write at all?

// GuardCommit is the commit that introduced the shared-index
// acknowledgement guard -- agent-estate#1185, "require explicit ack before
// writing the shared index" (#1184). Exported so main.go's own query-path
// wiring and this package's tests compare against the exact same value,
// never a second copy that could drift. A binary built from GuardCommit
// itself or any descendant of it had the guard available to enforce; a
// binary built from anything else in its history did not.
const GuardCommit = "2a6117f12c2abfe3f3355568036bb46c20f81236"

// ProvenanceState is what ResolveGuardProvenance found about a compiled
// index's own GeneratedBy.Commit, relative to a guard-introducing commit.
// Absence is typed exactly the way the rest of this package treats it:
// "this index carries no build commit at all" (ProvenanceAbsent) and "the
// ancestry check itself could not be performed" (ProvenanceUnknown) are
// different findings carrying different information, and must never
// collapse into each other or into a false ProvenanceClean.
type ProvenanceState string

const (
	// ProvenanceClean means indexCommit was positively confirmed to BE the
	// guard commit or a descendant of it -- the binary that wrote this
	// index could have refused an unacknowledged shared write. Silent in
	// the caller's disclosure, the same "nothing to say when everything is
	// fine" shape this package's other Coverage folds already use.
	ProvenanceClean ProvenanceState = "clean"
	// ProvenancePreGuard means indexCommit was positively confirmed to
	// predate the guard commit -- the binary that wrote this index had no
	// ack guard to enforce at all. This is a disclosure, never a refusal
	// (agent-estate#1191's own scope): report it, never regenerate or
	// repair anything as a result.
	ProvenancePreGuard ProvenanceState = "pre_guard"
	// ProvenanceAbsent means the index carries no GeneratedBy.Commit at
	// all -- either built before that field existed (agent-estate#1082) or
	// built by a binary that itself could not resolve its own commit
	// (unknownCommit). Strictly LESS information than ProvenancePreGuard:
	// a caller can at least confirm the field is unpopulated, versus never
	// having been able to check a real value against the guard at all.
	// Reported as its own state, never folded into ProvenanceUnknown.
	ProvenanceAbsent ProvenanceState = "absent"
	// ProvenanceUnknown means the check could not be performed at all: no
	// repository resolved, or the ancestry query itself failed to run
	// (git unavailable, the guard commit or the index commit unresolvable
	// in this checkout's history -- e.g. a shallow clone). Never treated
	// as clean -- "could not look" must never render as "looked and found
	// nothing wrong", the same rule unknownCommit already enforces for
	// ResolveBuildCommit.
	ProvenanceUnknown ProvenanceState = "unknown"
)

// ResolveGuardProvenance determines indexCommit's ancestry relative to
// guardCommit via `git merge-base --is-ancestor guardCommit indexCommit`,
// run against cfg.RepoRoot (cfg.RunGit substitutes a fake in tests, the
// same seam ResolveBuildCommit already uses). indexCommit is a compiled
// index's own GeneratedBy.Commit read back off disk -- this never resolves
// the CURRENTLY RUNNING checkout's own commit (that is ResolveBuildCommit's
// job); it resolves whether some OTHER, already-recorded commit could have
// enforced the guard.
//
// No working-tree cleanliness check runs here (unlike ResolveBuildCommit):
// ancestry of two already-known, fixed commits does not depend on what is
// currently checked out or modified in cfg.RepoRoot's working tree.
func ResolveGuardProvenance(cfg Config, indexCommit, guardCommit string) ProvenanceState {
	if indexCommit == "" || indexCommit == unknownCommit {
		return ProvenanceAbsent
	}
	if cfg.RepoRoot == "" {
		return ProvenanceUnknown
	}
	run := cfg.RunGit
	if run == nil {
		run = defaultGitRunner
	}
	_, err := run("-C", cfg.RepoRoot, "merge-base", "--is-ancestor", guardCommit, indexCommit)
	switch {
	case err == nil:
		return ProvenanceClean
	case errGitNo(err):
		// git ran fine and gave a genuine negative answer ("not an
		// ancestor") -- see errGitExitOne's own doc comment for why this
		// is distinguished from any other failure to run at all.
		return ProvenancePreGuard
	default:
		// git itself unavailable, guardCommit or indexCommit unresolvable
		// in this checkout's history (e.g. a shallow clone missing the
		// commits being compared), or any other failure to run --
		// deliberately NOT treated as either answer.
		return ProvenanceUnknown
	}
}
