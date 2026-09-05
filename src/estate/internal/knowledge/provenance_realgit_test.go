package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise ResolveGuardProvenance against the REAL git binary
// (defaultGitRunner, cfg.RunGit left nil) rather than a fake RunGit --
// provenance_test.go's fakes prove the state machine is correct given a
// claimed git answer, but never prove defaultGitRunner's real exit-code
// translation (errGitExitOne for a genuine "not an ancestor") actually
// matches what real git does. Closing that gap needs a repository with a
// known, controlled ancestry relationship -- built here from scratch in
// t.TempDir() with two commits this test creates itself, never the
// ambient checkout's own history. This is deliberately hermetic: it works
// identically on a full clone, a shallow `--depth 1` clone, and a fresh CI
// runner, because it never reads or depends on any commit that isn't one
// it just made (see agent-estate#1199's review: the end-to-end test that
// used to cover this reached into the real repository's history via
// `knowledge.GuardCommit~20`, which a shallow CI checkout does not have).
//
// The real GuardCommit constant's own identity is not re-verified here --
// that was confirmed independently against the real repository (agent-
// estate#1199's review, Lens 2: diffed GuardCommit against its parent).
// This proves the ancestry MECHANISM (real git, real exit codes), treating
// one of its own two commits as playing the "guard commit" role.

// realGitAvailable skips the test outright (never a false pass) when git
// itself is not on PATH -- distinct from the production ProvenanceUnknown
// case, which these tests are not exercising.
func realGitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH -- cannot run a real-git provenance test")
	}
}

// runGit runs a real git command in dir, failing the test on any error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newTwoCommitRepo builds a fresh git repository in t.TempDir() with two
// commits it creates itself -- full control over ancestry, no dependency
// on any pre-existing history. Returns (repoRoot, olderCommit,
// newerCommit); newerCommit is a direct descendant of olderCommit.
func newTwoCommitRepo(t *testing.T) (repoRoot, older, newer string) {
	t.Helper()
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")

	writeFile(t, dir, "a.txt", "first")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "first")
	older = runGit(t, dir, "rev-parse", "HEAD")

	writeFile(t, dir, "a.txt", "second")
	runGit(t, dir, "add", "a.txt")
	runGit(t, dir, "commit", "-q", "-m", "second")
	newer = runGit(t, dir, "rev-parse", "HEAD")

	return dir, older, newer
}

// TestResolveGuardProvenanceRealGitPreGuardAncestor is the hermetic
// replacement for the removed end-to-end "PreGuardCommitIsFlagged" case:
// an indexCommit that genuinely predates the commit playing the guard
// role reports ProvenancePreGuard, using real git the whole way through
// (defaultGitRunner, no fake).
func TestResolveGuardProvenanceRealGitPreGuardAncestor(t *testing.T) {
	realGitAvailable(t)
	repoRoot, older, newer := newTwoCommitRepo(t)

	cfg := Config{RepoRoot: repoRoot}
	got := ResolveGuardProvenance(cfg, older, newer)
	if got != ProvenancePreGuard {
		t.Fatalf("ResolveGuardProvenance(older, guard=newer) = %q, want %q", got, ProvenancePreGuard)
	}
}

// TestResolveGuardProvenanceRealGitCleanDescendant is the positive-clean
// counterpart: an indexCommit that IS the guard commit's own descendant
// reports ProvenanceClean, again via real git.
func TestResolveGuardProvenanceRealGitCleanDescendant(t *testing.T) {
	realGitAvailable(t)
	repoRoot, older, newer := newTwoCommitRepo(t)

	cfg := Config{RepoRoot: repoRoot}
	got := ResolveGuardProvenance(cfg, newer, older)
	if got != ProvenanceClean {
		t.Fatalf("ResolveGuardProvenance(newer, guard=older) = %q, want %q", got, ProvenanceClean)
	}
}

// TestResolveGuardProvenanceRealGitUnresolvableCommit covers a
// syntactically valid but nonexistent commit against real git -- must
// never read as ProvenanceClean or ProvenancePreGuard, only
// ProvenanceUnknown. This is the same case main_provenance_test.go's
// TestKnowledgeQueryUnresolvableAncestryIsNeverFalseClean covers at the
// CLI level; repeated here against a repo this test fully controls so it
// does not depend on the ambient checkout having (or lacking) any
// particular commit.
func TestResolveGuardProvenanceRealGitUnresolvableCommit(t *testing.T) {
	realGitAvailable(t)
	repoRoot, _, newer := newTwoCommitRepo(t)
	unresolvable := "deadbeefcafedeadbeefcafedeadbeefcafedea"

	cfg := Config{RepoRoot: repoRoot}
	got := ResolveGuardProvenance(cfg, unresolvable, newer)
	if got != ProvenanceUnknown {
		t.Fatalf("ResolveGuardProvenance(unresolvable, guard=newer) = %q, want %q", got, ProvenanceUnknown)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
