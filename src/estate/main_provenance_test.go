package main

import (
	"os"
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

// TestKnowledgeQueryPreGuardCommitIsFlagged used to live here as an
// end-to-end reproduction (real repository, knowledge.GuardCommit~20) of
// agent-estate#1191's provenance backstop. It was removed on review
// (agent-estate#1199): resolving GuardCommit~20 needs 20 generations of
// real history behind that commit, and CI's actions/checkout@v5 defaults
// to fetch-depth 1, so that history -- and, confirmed by hand, GuardCommit
// itself as an actual object, not merely its syntactically-valid hex
// string -- is absent on the runner. `git merge-base --is-ancestor
// <GuardCommit> HEAD` in a real depth-1 clone of this repo fails with
// "fatal: Not a valid commit name", which the production code correctly
// folds into ProvenanceUnknown -- but that means no automated test running
// in that environment can ever observe a genuine ProvenancePreGuard or
// ProvenanceClean answer against the real GuardCommit constant; only a
// full clone (which CI deliberately does not do) can. The positive
// pre-guard and clean cases are now covered hermetically, with real git
// and full control over ancestry, by
// internal/knowledge/provenance_realgit_test.go
// (TestResolveGuardProvenanceRealGitPreGuardAncestor and
// TestResolveGuardProvenanceRealGitCleanDescendant), which build their own
// two-commit repository from scratch instead of reaching into this
// repository's real history. GuardCommit's own identity was independently
// confirmed against the real repository during review (diffed against its
// parent) rather than re-proven by a test here.

// hermeticPreGuardRepo builds a fresh, self-contained git repository in
// t.TempDir() with one commit of its own, then makes real git report that
// commit as genuinely NOT a descendant of knowledge.GuardCommit -- without
// ever fetching, checking out, or otherwise depending on this actual
// repository's history, refs, objects, config or working directory (the
// exact dependency #1199's review found and required removed).
//
// The trick is `git replace`: writing refs/replace/<GuardCommit> pointing
// at some real, local commit makes git resolve GuardCommit's hex name to a
// genuine, locally-present commit object -- `git cat-file -t` on it
// answers "commit", not "fatal: Not a valid object name" -- so `git
// merge-base --is-ancestor GuardCommit indexCommit` can run to completion
// instead of failing closed with exit 128 (which provenance.go folds into
// ProvenanceUnknown, not the PreGuard case this test exists to catch).
// Ancestry itself is walked over the REAL parent pointers recorded in each
// commit object, never over a replace ref -- nothing in this repository's
// one commit points at GuardCommit's hash as a parent, so the walk can
// never reach it, and `--is-ancestor` genuinely (not by construction of a
// false positive) answers "no": exit 1, "ran fine, real negative", which
// provenance.go's errGitNo case reads as ProvenancePreGuard. This is
// confirmed empirically, not just argued: see this file's header comment
// for the manual `git replace` + `--is-ancestor` trace this test's shape
// was verified against before being committed.
func hermeticPreGuardRepo(t *testing.T) (repoRoot, indexCommit string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
			"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hermetic pre-guard fixture"), 0o644); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	run("add", "f.txt")
	run("commit", "-q", "-m", "hermetic pre-guard fixture commit")
	indexCommit = run("rev-parse", "HEAD")

	// Make GuardCommit resolve to SOMETHING real and local, without it ever
	// becoming an ancestor of indexCommit -- see the doc comment above for
	// why replacement content is irrelevant to the ancestry walk; reusing
	// indexCommit itself as the replacement target keeps this repo to a
	// single real commit.
	run("update-ref", "refs/replace/"+knowledge.GuardCommit, indexCommit)

	return dir, indexCommit
}

// TestKnowledgeQueryPreGuardCommitIsFlaggedHermetically is the positive
// (flagged) CLI-level case #1199's review found missing: an index whose
// generated_by.commit genuinely predates knowledge.GuardCommit, queried
// through the actual built binary and main.go's real
// foldProvenanceIntoCoverage switch, must surface the ProvenancePreGuard
// disclosure. Built entirely from hermeticPreGuardRepo above -- no ambient
// history, refs, objects, config or working directory involved -- so this
// passes identically on a full clone, a `--depth 1` clone, and CI's actual
// shallow checkout, the exact property the removed non-hermetic
// GuardCommit~20 test lacked.
func TestKnowledgeQueryPreGuardCommitIsFlaggedHermetically(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot, indexCommit := hermeticPreGuardRepo(t)

	idx := filepath.Join(t.TempDir(), "index.json")
	writeFixtureIndexWithGeneratedBy(t, idx, knowledge.GeneratedBy{Commit: indexCommit, BuiltAt: time.Now().UTC()})

	got := runKnowledgeQueryJSONWithRepoRoot(t, bin, idx, repoRoot)

	var found bool
	for _, r := range got.Coverage.Reasons {
		if r.State == "pre_guard_commit" {
			found = true
			short := indexCommit[:12]
			if !strings.Contains(r.Detail, short) {
				t.Fatalf("pre_guard_commit Detail does not name the index's own build commit: %q (want %q)", r.Detail, short)
			}
		}
	}
	if !found {
		t.Fatalf("Coverage.Reasons does not carry a pre_guard_commit reason for a commit that genuinely predates GuardCommit: %+v", got.Coverage.Reasons)
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
