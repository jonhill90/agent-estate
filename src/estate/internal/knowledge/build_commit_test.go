package knowledge

import (
	"errors"
	"strings"
	"testing"
)

// TestResolveBuildCommitCleanTree is the reproduction case agent-estate#1082
// asks for: a clean checkout resolves to its real HEAD commit, never
// "unknown" -- this is what fails before the fix (ResolveBuildCommit did
// not exist at all; Generate never called anything like it, so
// GeneratedBy.Commit was simply absent from the index) and passes after.
func TestResolveBuildCommitCleanTree(t *testing.T) {
	var gotArgs [][]string
	fake := func(args ...string) ([]byte, error) {
		gotArgs = append(gotArgs, args)
		switch args[len(args)-1] {
		case "--porcelain":
			return []byte(""), nil // clean
		case "HEAD":
			return []byte("abc123def4567890abc123def4567890abc123d\n"), nil
		}
		return nil, errors.New("unexpected git invocation: " + strings.Join(args, " "))
	}
	cfg := Config{RepoRoot: "/repo", RunGit: fake}
	got := ResolveBuildCommit(cfg)
	if got != "abc123def4567890abc123def4567890abc123d" {
		t.Fatalf("ResolveBuildCommit() = %q, want the real HEAD commit", got)
	}
	if len(gotArgs) != 2 {
		t.Fatalf("expected a status check then a rev-parse, got %v", gotArgs)
	}
}

// TestResolveBuildCommitDirtyTreeIsUnknown is #1082's own "not a guess"
// requirement: a dirty tree has no single commit that describes what is
// actually on disk, so this must report "unknown" rather than HEAD.
func TestResolveBuildCommitDirtyTreeIsUnknown(t *testing.T) {
	fake := func(args ...string) ([]byte, error) {
		if args[len(args)-1] == "--porcelain" {
			return []byte(" M internal/knowledge/query.go\n"), nil
		}
		t.Fatalf("rev-parse HEAD must not run against a dirty tree, got %v", args)
		return nil, nil
	}
	cfg := Config{RepoRoot: "/repo", RunGit: fake}
	if got := ResolveBuildCommit(cfg); got != unknownCommit {
		t.Fatalf("ResolveBuildCommit() on a dirty tree = %q, want %q", got, unknownCommit)
	}
}

// TestResolveBuildCommitNoRepoRootIsUnknown covers a process running
// outside any git checkout -- #1048/#1082's "outside a checkout" case named
// explicitly in the issue.
func TestResolveBuildCommitNoRepoRootIsUnknown(t *testing.T) {
	cfg := Config{RepoRoot: "", RunGit: func(args ...string) ([]byte, error) {
		t.Fatalf("git must never be invoked with no repo root, got %v", args)
		return nil, nil
	}}
	if got := ResolveBuildCommit(cfg); got != unknownCommit {
		t.Fatalf("ResolveBuildCommit() with no RepoRoot = %q, want %q", got, unknownCommit)
	}
}

// TestResolveBuildCommitGitErrorIsUnknown covers git itself being
// unavailable or erroring -- never surfaced as a fabricated commit.
func TestResolveBuildCommitGitErrorIsUnknown(t *testing.T) {
	cfg := Config{RepoRoot: "/repo", RunGit: func(args ...string) ([]byte, error) {
		return nil, errors.New("exec: \"git\": executable file not found in $PATH")
	}}
	if got := ResolveBuildCommit(cfg); got != unknownCommit {
		t.Fatalf("ResolveBuildCommit() with git unavailable = %q, want %q", got, unknownCommit)
	}
}
