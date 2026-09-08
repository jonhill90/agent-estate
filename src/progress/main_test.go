package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// isTrackedDir reports whether path (repo-root relative) is a real,
// git-tracked directory in HEAD -- not merely present on local disk, which
// would pass for an untracked scratch directory a real checkout never has,
// and not inferred from a git-log commit count, which stays nonzero for a
// path years after it is deleted (agent-estate#1311: src/langguard matched 2
// historical commits after removal, so a count-based check would have
// passed anyway).
func isTrackedDir(t *testing.T, root, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "ls-tree", "-d", "--name-only", "HEAD", "--", path)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-tree %s: %v", path, err)
	}
	return strings.TrimSpace(string(out)) == path
}

// TestAppAndSupportPathsResolveOnDisk is agent-estate#1311's own required
// proof: nothing bound these two hardcoded lists to the tree, so
// src/langguard stayed listed for as long after its deletion as nobody
// happened to look. Confirmed FAILING against unmodified main first
// (src/langguard listed, not a tracked directory) before this fix removed
// it; this test would have caught it the day the directory was deleted.
func TestAppAndSupportPathsResolveOnDisk(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	all := append(append([]string{}, appPaths...), supportPaths...)
	for _, p := range all {
		if !isTrackedDir(t, root, p) {
			t.Errorf("%q is listed in appPaths/supportPaths but is not a tracked directory in HEAD", p)
		}
	}
}

// TestUnclassifiedReportsPathsInNeitherList pins the set-subtraction logic
// standalone, without a real repo: a universe entry not named in either
// classified list must come back, sorted, and nothing else should.
func TestUnclassifiedReportsPathsInNeitherList(t *testing.T) {
	universe := []string{"src/estate", "src/tui", "src/notify", "scripts", "unused"}
	app := []string{"src/estate", "src/tui"}
	sup := []string{"src/notify"}
	got := unclassified(universe, app, sup)
	want := []string{"scripts", "unused"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unclassified() = %v, want %v", got, want)
	}
}

// TestUnclassifiedEmptyWhenFullyCovered confirms the report stays silent
// (nil, not an empty-but-non-nil slice callers must special-case) when every
// universe entry is accounted for -- the common case, and the one that must
// never print a spurious "unclassified" line.
func TestUnclassifiedEmptyWhenFullyCovered(t *testing.T) {
	universe := []string{"src/estate", "docs"}
	app := []string{"src/estate"}
	sup := []string{"docs"}
	if got := unclassified(universe, app, sup); len(got) != 0 {
		t.Fatalf("unclassified() = %v, want none", got)
	}
}

// TestClassifiableUniverseExcludesSrcItselfNotItsChildren guards the mixed
// depth this whole check depends on: appPaths/supportPaths classify "src/tui"
// and "src/estate" individually, never bare "src", so the universe this
// function builds must do the same -- "src" itself must never appear (it
// would always show up as unclassified, incorrectly, since neither list
// names it), and its real children must.
func TestClassifiableUniverseExcludesSrcItselfNotItsChildren(t *testing.T) {
	universe, err := classifiableUniverse()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range universe {
		if p == "src" {
			t.Fatal("classifiableUniverse() includes bare \"src\", which neither list ever classifies by that name")
		}
	}
	var sawChild bool
	for _, p := range universe {
		if strings.HasPrefix(p, "src/") {
			sawChild = true
			break
		}
	}
	if !sawChild {
		t.Fatal("classifiableUniverse() found no src/* child -- src/ enumeration is broken")
	}
}

// newFixtureRepo creates an isolated, throwaway git repo under t.TempDir() so
// path-rebirth history can be scripted exactly, rather than depending on this
// repo's own real history staying the same shape forever.
func newFixtureRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "fixture@example.com")
	run("config", "user.name", "Fixture")
	return root
}

// fixtureCommit writes path with content (or removes it, when content is
// "\x00delete\x00") and commits at the given date, so --since windows and
// commit ordering are fully deterministic rather than racing wall-clock time.
func fixtureCommit(t *testing.T, root, path, content, date, message string) string {
	t.Helper()
	full := filepath.Join(root, path)
	if content == deleteMarker {
		if err := os.RemoveAll(filepath.Dir(full)); err != nil {
			t.Fatalf("remove %s: %v", path, err)
		}
		run(t, root, "add", "-A")
	} else {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", path, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		run(t, root, "add", path)
	}
	cmd := exec.Command("git", "commit", "-q", "-m", message)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date,
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit %q: %v\n%s", message, err, out)
	}
	out, err := exec.Command("git", "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

const deleteMarker = "\x00delete\x00"

func run(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestCommitsForPathExcludesPriorIncarnation is agent-estate#1316's review,
// reproduced as a fixture: scripts/ (the shell/Python supervisor) is created,
// touched, and deleted, then an unrelated scripts/ (docs tooling) is created
// and touched. A path name is not a stable directory identity across
// history -- naive `git log -- scripts` counts all five commits touching
// that string; the fix must count only the two commits belonging to the
// directory that actually exists now.
func TestCommitsForPathExcludesPriorIncarnation(t *testing.T) {
	root := newFixtureRepo(t)
	fixtureCommit(t, root, "scripts/supervisor.sh", "old era A", "2020-01-01T00:00:00", "add old scripts/")
	fixtureCommit(t, root, "scripts/supervisor.sh", "old era B", "2020-01-02T00:00:00", "touch old scripts/")
	fixtureCommit(t, root, "scripts/supervisor.sh", deleteMarker, "2020-01-03T00:00:00", "delete scripts/ -- the shell supervisor")
	fixtureCommit(t, root, "scripts/docs-lint.sh", "new era A", "2020-01-10T00:00:00", "add unrelated new scripts/")
	fixtureCommit(t, root, "scripts/docs-lint.sh", "new era B", "2020-01-11T00:00:00", "touch new scripts/")

	got, err := commitsForPath(root, "2019-01-01", "scripts")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("commitsForPath() = %d commits, want 2 (only the current scripts/ incarnation); a naive count would find 5", len(got))
	}
}

// TestCommitsForPathUnchangedWithoutRebirth is the regression guard: a path
// that has only ever existed once must count exactly as it always has --
// this fix must not shrink or drop commits for the common case, only exclude
// commits from a genuinely different, deleted directory.
func TestCommitsForPathUnchangedWithoutRebirth(t *testing.T) {
	root := newFixtureRepo(t)
	fixtureCommit(t, root, "docs/a.md", "one", "2020-01-01T00:00:00", "add docs")
	fixtureCommit(t, root, "docs/a.md", "two", "2020-01-02T00:00:00", "touch docs")
	fixtureCommit(t, root, "docs/b.md", "three", "2020-01-03T00:00:00", "add more docs")

	got, err := commitsForPath(root, "2019-01-01", "docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("commitsForPath() = %d commits, want 3 (no rebirth, nothing should be excluded)", len(got))
	}
}

// TestCountAtDeduplicatesAcrossPaths guards the property the original
// single-batched-git-log implementation had for free: a commit touching two
// paths in the same group must be counted once, not twice, now that each
// path is walked separately to find its own rebirth boundary.
func TestCountAtDeduplicatesAcrossPaths(t *testing.T) {
	root := newFixtureRepo(t)
	full := filepath.Join(root, "docs")
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notify.go"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, root, "add", "-A")
	cmd := exec.Command("git", "commit", "-q", "-m", "touch both docs and notify in one commit")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2020-01-01T00:00:00", "GIT_COMMITTER_DATE=2020-01-01T00:00:00",
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	got, err := countAt(root, "2019-01-01", []string{"docs", "notify.go"})
	if err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("countAt() = %d, want 1 (one commit touched both paths)", got)
	}
}
