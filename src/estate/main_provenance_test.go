package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// realRepoRoot resolves the actual repository root this worktree belongs
// to, via `git rev-parse --show-toplevel` -- these tests need the REAL
// history containing the real knowledge.GuardCommit and its real
// ancestors/descendants, which no throwaway t.TempDir() fixture repo can
// contain (a fresh `git init` never has 2a6117f in its own history).
// #1191's own constraint ("do not check out or build historical commits")
// is respected: this only ever reads git metadata (`rev-parse`,
// `merge-base --is-ancestor`) against the CURRENT checkout's existing
// history, never checks out or builds anything.
func realRepoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// realCommit resolves ref to a full commit hash in the real repository --
// a read-only `git rev-parse`, never a checkout.
func realCommit(t *testing.T, repoRoot, ref string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", ref)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git -C %s rev-parse %s: %v", repoRoot, ref, err)
	}
	return strings.TrimSpace(string(out))
}

// TestKnowledgeQueryPreGuardCommitIsFlagged is the reproduction case
// agent-estate#1191's provenance backstop exists for: an index whose
// GeneratedBy.Commit is a REAL, positively-resolved ancestor of
// knowledge.GuardCommit (agent-estate#1185, 2a6117f) folds in
// CoveragePreGuardCommit, naming both commits -- FAILS BEFORE this change
// (no such state, no such fold existed at all) and PASSES AFTER.
func TestKnowledgeQueryPreGuardCommitIsFlagged(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot := realRepoRoot(t)
	preGuard := realCommit(t, repoRoot, knowledge.GuardCommit+"~20")

	idx := filepath.Join(t.TempDir(), "index.json")
	writeFixtureIndexWithGeneratedBy(t, idx, knowledge.GeneratedBy{Commit: preGuard, BuiltAt: time.Now().UTC()})

	got := runKnowledgeQueryJSONWithRepoRoot(t, bin, idx, repoRoot)

	var found bool
	for _, r := range got.Coverage.Reasons {
		if r.State == "pre_guard_commit" {
			found = true
			if !strings.Contains(r.Detail, preGuard[:12]) {
				t.Fatalf("pre_guard_commit Detail does not name the index's own commit: %q (want %q)", r.Detail, preGuard[:12])
			}
			if !strings.Contains(r.Detail, knowledge.GuardCommit[:12]) {
				t.Fatalf("pre_guard_commit Detail does not name the guard commit: %q (want %q)", r.Detail, knowledge.GuardCommit[:12])
			}
		}
	}
	if !found {
		t.Fatalf("Coverage.Reasons does not carry a pre_guard_commit reason for a commit that genuinely predates the guard: %+v", got.Coverage.Reasons)
	}
	if got.Coverage.State == "complete" {
		t.Fatalf("Coverage.State = %q -- must not read complete when the index predates the shared-write ack guard", got.Coverage.State)
	}
}

// TestKnowledgeQueryPostGuardCommitIsClean is the negative case: an index
// built by a REAL descendant of knowledge.GuardCommit (the current
// checkout's own HEAD, which necessarily descends from a commit merged long
// ago) reports no pre_guard_commit reason at all -- the "does not exist" /
// "explicitly clean" half of the brief's minimum coverage.
func TestKnowledgeQueryPostGuardCommitIsClean(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot := realRepoRoot(t)
	postGuard := realCommit(t, repoRoot, "HEAD")

	idx := filepath.Join(t.TempDir(), "index.json")
	writeFixtureIndexWithGeneratedBy(t, idx, knowledge.GeneratedBy{Commit: postGuard, BuiltAt: time.Now().UTC()})

	got := runKnowledgeQueryJSONWithRepoRoot(t, bin, idx, repoRoot)

	for _, r := range got.Coverage.Reasons {
		if r.State == "pre_guard_commit" {
			t.Fatalf("a commit that descends from the guard commit must never report pre_guard_commit: %+v", got.Coverage.Reasons)
		}
	}
}

// TestKnowledgeQueryAbsentGeneratedByCommitIsDistinctFromPreGuard covers an
// index with no GeneratedBy.Commit at all (built before that field existed,
// agent-estate#1082) -- the brief's "generated_by absent -> distinct
// message" requirement: this must never fold in pre_guard_commit (there is
// nothing positively confirmed to predate anything), and its own
// CoverageUnknownFreshness reason must say so in a way that does not read
// like the ancestry-unresolvable case below.
func TestKnowledgeQueryAbsentGeneratedByCommitIsDistinctFromPreGuard(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot := realRepoRoot(t)

	idx := filepath.Join(t.TempDir(), "index.json")
	writeFixtureIndexWithGeneratedBy(t, idx, knowledge.GeneratedBy{}) // zero value: Commit == ""

	got := runKnowledgeQueryJSONWithRepoRoot(t, bin, idx, repoRoot)

	var found bool
	for _, r := range got.Coverage.Reasons {
		if r.State == "pre_guard_commit" {
			t.Fatalf("an index with no build commit at all must never be flagged pre_guard_commit: %+v", got.Coverage.Reasons)
		}
		if r.State == "unknown" && strings.Contains(r.Detail, "no generated_by.commit at all") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Coverage.Reasons does not carry the distinct absent-provenance reason: %+v", got.Coverage.Reasons)
	}
}

// TestKnowledgeQueryUnresolvableAncestryIsNeverFalseClean covers the "could
// not be determined" case the brief requires: a GeneratedBy.Commit that is
// well-formed but does not exist anywhere in the real repository's history
// (git cannot resolve it, unlike the shallow-clone case this stands in
// for) must report CoverageUnknownFreshness, and MUST NOT report clean
// (no pre_guard_commit reason is not, by itself, proof of a look that
// happened -- this asserts the actual unknown reason fired).
func TestKnowledgeQueryUnresolvableAncestryIsNeverFalseClean(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot := realRepoRoot(t)
	unresolvable := "deadbeefcafedeadbeefcafedeadbeefcafedea" // 40 hex chars, resolves to no real object

	idx := filepath.Join(t.TempDir(), "index.json")
	writeFixtureIndexWithGeneratedBy(t, idx, knowledge.GeneratedBy{Commit: unresolvable, BuiltAt: time.Now().UTC()})

	got := runKnowledgeQueryJSONWithRepoRoot(t, bin, idx, repoRoot)

	var found bool
	for _, r := range got.Coverage.Reasons {
		if r.State == "pre_guard_commit" {
			t.Fatalf("an unresolvable commit must never be reported pre_guard_commit -- that would be a guess: %+v", got.Coverage.Reasons)
		}
		if r.State == "unknown" && strings.Contains(r.Detail, "could not be determined") {
			found = true
		}
	}
	if !found {
		t.Fatalf("Coverage.Reasons does not carry the ancestry-unresolvable reason: %+v", got.Coverage.Reasons)
	}
}
