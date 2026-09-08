package main

import (
	"os/exec"
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
