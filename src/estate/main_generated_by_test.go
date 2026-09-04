package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// generatedByCoverageJSON is the minimal shape this file reads back out of
// `estate knowledge query --json`'s Coverage -- the same narrow-decode
// pattern main_staleness_coverage_test.go's staleCoverageJSON already uses,
// so these tests fail to build meaningfully rather than merely failing to
// parse against the parent commit (which has no CoverageBinaryMismatch
// constant at all).
type generatedByCoverageJSON struct {
	IndexGeneratedBy struct {
		Commit string `json:"commit"`
	} `json:"index_generated_by"`
	Coverage struct {
		State   string `json:"state"`
		Reasons []struct {
			State  string `json:"state"`
			Source string `json:"source"`
			Detail string `json:"detail"`
		} `json:"reasons"`
	} `json:"coverage"`
}

// writeGitFixtureRepo creates a real, clean, one-commit git checkout under
// a scratch t.TempDir() (never a real project checkout) and returns
// (repoRoot, headCommit) -- the fixture ResolveBuildCommit's real git
// invocation resolves against, so these tests exercise the actual `git`
// binary this feature shells out to, not a fake seam (that seam is already
// covered directly in internal/knowledge/build_commit_test.go).
func writeGitFixtureRepo(t *testing.T) (repoRoot, headCommit string) {
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
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "AGENTS.md")
	run("commit", "-q", "-m", "fixture commit")
	head := run("rev-parse", "HEAD")
	return dir, head
}

// writeFixtureIndexWithGeneratedBy writes a minimal compiled index at idx
// carrying the given GeneratedBy -- these tests exercise the index-vs-
// binary comparison, not ranking, so the question deliberately matches
// nothing (the same no-op-match shape writeFixtureIndexAt already uses).
func writeFixtureIndexWithGeneratedBy(t *testing.T, idx string, generatedBy knowledge.GeneratedBy) {
	t.Helper()
	res := knowledge.Result{
		GeneratedAt: time.Now().UTC().Add(1 * time.Hour), // far enough ahead that source-staleness noise never fires
		GeneratedBy: generatedBy,
		Sources: []knowledge.SourceResult{
			{Name: "github-stars", OK: true, Count: 0},
		},
		Items: []knowledge.Item{
			{ID: "it-0000000000001082", Source: "vault-fact", Permalink: "/tmp/unrelated.md",
				Tier1: "an item unrelated to any question this file asks", Publishable: true,
				PublishBasis: "vault fact, always public"},
		},
	}
	if err := knowledge.Write(idx, res); err != nil {
		t.Fatalf("write fixture index: %v", err)
	}
}

// runKnowledgeQueryJSONWithRepoRoot runs `estate knowledge query --json`
// against idx with ESTATE_REPO_ROOT pinned to repoRoot -- the env var
// knowledge.DefaultConfig reads first, before falling back to walking up
// from the working directory (write.go's findRepoRoot), so this is the
// one override these tests need to control which checkout the running
// binary resolves its OWN commit against.
func runKnowledgeQueryJSONWithRepoRoot(t *testing.T, bin, idx, repoRoot string) generatedByCoverageJSON {
	t.Helper()
	cmd := exec.Command(bin, "knowledge", "query", "--json", "zzz_no_match_question_zzz")
	cmd.Env = append(os.Environ(),
		"ESTATE_KNOWLEDGE_INDEX="+idx,
		"ESTATE_LEDGER="+filepath.Join(t.TempDir(), "ledger.jsonl"),
		"AGENT_MEMORY_VAULT="+t.TempDir(),
		"ESTATE_CORPUS="+filepath.Join(t.TempDir(), "absent.sqlite3"),
		"ESTATE_REPO_ROOT="+repoRoot,
	)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		if _, ok := runErr.(*exec.ExitError); !ok {
			t.Fatalf("run estate knowledge query --json: %v\n%s", runErr, out)
		}
	}
	var got generatedByCoverageJSON
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal QueryResult JSON: %v\n%s", err, out)
	}
	return got
}

// TestKnowledgeQuerySameBinaryNoMismatch is the "same binary" evidence case
// the issue asks for: an index built by exactly the commit this invocation
// is running from must report NO binary_mismatch reason at all.
func TestKnowledgeQuerySameBinaryNoMismatch(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot, head := writeGitFixtureRepo(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	writeFixtureIndexWithGeneratedBy(t, idx, knowledge.GeneratedBy{Commit: head, BuiltAt: time.Now().UTC()})

	got := runKnowledgeQueryJSONWithRepoRoot(t, bin, idx, repoRoot)

	if got.IndexGeneratedBy.Commit != head {
		t.Fatalf("IndexGeneratedBy.Commit = %q, want %q", got.IndexGeneratedBy.Commit, head)
	}
	for _, r := range got.Coverage.Reasons {
		if r.State == "binary_mismatch" {
			t.Fatalf("same-binary query reported a binary_mismatch reason: %+v", got.Coverage.Reasons)
		}
	}
}

// TestKnowledgeQueryDifferentBinaryReportsMismatchNamingBoth is the
// "different commit" evidence case: an index built by a commit that is NOT
// the checkout's current HEAD must fold in CoverageBinaryMismatch, naming
// both commits in Detail -- FAILS BEFORE this change (no such field, no
// such state, no such fold existed at all) and PASSES AFTER.
func TestKnowledgeQueryDifferentBinaryReportsMismatchNamingBoth(t *testing.T) {
	bin := buildEstateBinary(t)
	repoRoot, head := writeGitFixtureRepo(t)
	idx := filepath.Join(t.TempDir(), "index.json")
	staleCommit := "deadbeefcafedeadbeefcafedeadbeefcafedead"
	writeFixtureIndexWithGeneratedBy(t, idx, knowledge.GeneratedBy{Commit: staleCommit, BuiltAt: time.Now().UTC()})

	got := runKnowledgeQueryJSONWithRepoRoot(t, bin, idx, repoRoot)

	var found bool
	for _, r := range got.Coverage.Reasons {
		if r.State == "binary_mismatch" {
			found = true
			if !strings.Contains(r.Detail, staleCommit[:12]) || !strings.Contains(r.Detail, head[:12]) {
				t.Fatalf("binary_mismatch Detail does not name both commits: %q (want %q and %q)", r.Detail, staleCommit[:12], head[:12])
			}
		}
	}
	if !found {
		t.Fatalf("Coverage.Reasons does not carry a binary_mismatch reason for a genuinely different commit: %+v", got.Coverage.Reasons)
	}
	if got.Coverage.State == "complete" {
		t.Fatalf("Coverage.State = %q -- must not read complete when the index was built by a different commit", got.Coverage.State)
	}
}

// TestKnowledgeQueryUnknownBuildCommitReportsNoMismatch is the "unknown"
// evidence case: ESTATE_REPO_ROOT pointed at a directory that is not a git
// checkout at all resolves to "unknown" on the running side, and #1082 is
// explicit that unknown must never be treated as a guessed match OR a
// fabricated mismatch -- no binary_mismatch reason fires.
func TestKnowledgeQueryUnknownBuildCommitReportsNoMismatch(t *testing.T) {
	bin := buildEstateBinary(t)
	notARepo := t.TempDir() // no .git here at all
	idx := filepath.Join(t.TempDir(), "index.json")
	writeFixtureIndexWithGeneratedBy(t, idx, knowledge.GeneratedBy{
		Commit: "abc123def4567890abc123def4567890abc123d", BuiltAt: time.Now().UTC(),
	})

	got := runKnowledgeQueryJSONWithRepoRoot(t, bin, idx, notARepo)

	for _, r := range got.Coverage.Reasons {
		if r.State == "binary_mismatch" {
			t.Fatalf("an unresolvable running commit must never be reported as a mismatch (that would be a guess): %+v", got.Coverage.Reasons)
		}
	}
}
