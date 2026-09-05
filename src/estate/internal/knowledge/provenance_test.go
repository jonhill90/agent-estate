package knowledge

import (
	"errors"
	"strings"
	"testing"
)

// guardFixture is an arbitrary stand-in for the real GuardCommit constant --
// these tests exercise ResolveGuardProvenance's own logic against a fake
// RunGit, so the exact commit string passed in never has to be a real,
// resolvable commit (unlike the end-to-end tests in src/estate, which use
// the real repository and the real GuardCommit).
const guardFixture = "guardcommit0000000000000000000000000000"

// TestResolveGuardProvenanceCleanDescendant is the reproduction case: an
// index built by a commit that IS a descendant of (or equal to) the guard
// commit reports ProvenanceClean -- FAILS BEFORE this change (no such
// function existed at all) and PASSES AFTER.
func TestResolveGuardProvenanceCleanDescendant(t *testing.T) {
	var gotArgs []string
	fake := func(args ...string) ([]byte, error) {
		gotArgs = args
		return []byte(""), nil // `merge-base --is-ancestor` exit 0: yes
	}
	cfg := Config{RepoRoot: "/repo", RunGit: fake}
	got := ResolveGuardProvenance(cfg, "descendant0000000000000000000000000000", guardFixture)
	if got != ProvenanceClean {
		t.Fatalf("ResolveGuardProvenance() = %q, want %q", got, ProvenanceClean)
	}
	want := []string{"-C", "/repo", "merge-base", "--is-ancestor", guardFixture, "descendant0000000000000000000000000000"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("git invoked with %v, want %v", gotArgs, want)
	}
}

// TestResolveGuardProvenancePreGuardAncestorCommit is the flagged case: a
// commit that predates the guard reports ProvenancePreGuard, simulated via
// the errGitExitOne sentinel (git ran fine, genuinely said "not an
// ancestor").
func TestResolveGuardProvenancePreGuardAncestorCommit(t *testing.T) {
	fake := func(args ...string) ([]byte, error) {
		return nil, errGitExitOne
	}
	cfg := Config{RepoRoot: "/repo", RunGit: fake}
	got := ResolveGuardProvenance(cfg, "predatesguard000000000000000000000000", guardFixture)
	if got != ProvenancePreGuard {
		t.Fatalf("ResolveGuardProvenance() = %q, want %q", got, ProvenancePreGuard)
	}
}

// TestResolveGuardProvenanceAbsentCommitNeverInvokesGit covers an index
// with no GeneratedBy.Commit at all (built before that field existed) --
// reported as ProvenanceAbsent without ever shelling out, since there is
// nothing to compare.
func TestResolveGuardProvenanceAbsentCommitNeverInvokesGit(t *testing.T) {
	cfg := Config{RepoRoot: "/repo", RunGit: func(args ...string) ([]byte, error) {
		t.Fatalf("git must never be invoked when the index carries no build commit, got %v", args)
		return nil, nil
	}}
	if got := ResolveGuardProvenance(cfg, "", guardFixture); got != ProvenanceAbsent {
		t.Fatalf("ResolveGuardProvenance() with empty commit = %q, want %q", got, ProvenanceAbsent)
	}
	if got := ResolveGuardProvenance(cfg, unknownCommit, guardFixture); got != ProvenanceAbsent {
		t.Fatalf("ResolveGuardProvenance() with unknownCommit = %q, want %q", got, ProvenanceAbsent)
	}
}

// TestResolveGuardProvenanceUnknownNoRepoRoot covers a process running with
// no repository resolved at all -- reported as ProvenanceUnknown, never a
// false ProvenanceClean, and git is never invoked.
func TestResolveGuardProvenanceUnknownNoRepoRoot(t *testing.T) {
	cfg := Config{RepoRoot: "", RunGit: func(args ...string) ([]byte, error) {
		t.Fatalf("git must never be invoked with no repo root, got %v", args)
		return nil, nil
	}}
	if got := ResolveGuardProvenance(cfg, "somecommit00000000000000000000000000", guardFixture); got != ProvenanceUnknown {
		t.Fatalf("ResolveGuardProvenance() with no RepoRoot = %q, want %q", got, ProvenanceUnknown)
	}
}

// TestResolveGuardProvenanceUnknownGitError covers git itself failing to
// answer at all (unavailable, or either commit unresolvable in this
// checkout's history -- e.g. a shallow clone) -- never surfaced as
// ProvenanceClean or ProvenancePreGuard, only ProvenanceUnknown.
func TestResolveGuardProvenanceUnknownGitError(t *testing.T) {
	cfg := Config{RepoRoot: "/repo", RunGit: func(args ...string) ([]byte, error) {
		return nil, errors.New("fatal: not a valid object name guardcommit0000000000000000000000000000")
	}}
	if got := ResolveGuardProvenance(cfg, "somecommit00000000000000000000000000", guardFixture); got != ProvenanceUnknown {
		t.Fatalf("ResolveGuardProvenance() with a git error = %q, want %q", got, ProvenanceUnknown)
	}
}
