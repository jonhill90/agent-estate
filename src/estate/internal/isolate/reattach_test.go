package isolate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The whole point of Reattach: a worktree whose creating process is GONE --
// killed outright, so no defer of its ran -- can still be torn down, by a
// different process, from the facts the ledger recorded. Nothing of the
// original *Worktree value survives here; it is rebuilt from three strings.
func TestReattachRebuildsAWorktreeFromRecordedFactsAlone(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "corpse-clean")
	if err != nil {
		t.Fatal(err)
	}
	path, branch, base := w.Path, w.Branch, w.Base
	w = nil // the process that knew how to remove this is dead

	again, err := Reattach(root, path, branch, base)
	if err != nil {
		t.Fatalf("Reattach refused a worktree it created itself: %v", err)
	}
	if again.Detached {
		t.Fatal("Reattach reported a branch checkout as detached")
	}
	if err := again.Remove(); err != nil {
		t.Fatalf("the reattached worktree could not be removed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Remove reported success but %s is still there", path)
	}
	// The local branch goes with it -- the same teardown, not a lesser one.
	out, _ := exec.Command("git", "-C", root, "branch", "--list", branch).Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the reattached teardown left branch %s behind", branch)
	}
}

// Reattach must not hand a lesser teardown to a corpse than the live path
// gets. A reattached worktree holding uncommitted work refuses exactly as
// the original would have.
func TestAReattachedWorktreeRefusesUncollectedWorkTheSameWay(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "corpse-dirty")
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(w.Path, "the-only-copy.txt")
	if err := os.WriteFile(output, []byte("what the dead turn produced"), 0o644); err != nil {
		t.Fatal(err)
	}

	again, err := Reattach(root, w.Path, w.Branch, w.Base)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Remove(); err == nil {
		t.Fatal("a reattached worktree deleted uncommitted work the live path would have refused to touch")
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("the refused Remove destroyed the dead turn's output anyway: %v", err)
	}
}

// Detached is read off the worktree, never assumed from the branch name --
// a fix pass (CreateOnBranch) is a detached checkout of an existing branch,
// and Remove must not then try to delete a local branch that was never
// created here.
func TestReattachObservesDetachedRatherThanBelievingTheRecord(t *testing.T) {
	root := repoWithRemote(t, "feature")
	w, err := CreateOnBranch(root, "corpse-fixpass", "feature")
	if err != nil {
		t.Fatal(err)
	}
	if !w.Detached {
		t.Fatal("setup is wrong: CreateOnBranch is supposed to detach")
	}
	again, err := Reattach(root, w.Path, w.Branch, w.Base)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Detached {
		t.Fatal("Reattach did not observe that the worktree's HEAD is detached")
	}
	if err := again.Remove(); err != nil {
		t.Fatalf("the reattached fix-pass worktree could not be removed: %v", err)
	}
}

// Every refusal below is a case where a recorded fact is missing or points
// somewhere it should not. A recorded path is a string out of a file, and
// what it feeds is `git worktree remove` -- so the confinement is checked
// before anything is touched, not after.
func TestReattachRefusesRatherThanGuessing(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "confinement")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Remove() })

	// Every path below is a REAL git working tree, deliberately: a case that
	// is refused for being missing, or for not being a repository, would
	// prove nothing about the confinement -- and a first version of this
	// test did exactly that, staying green against a mutant with the
	// confinement removed entirely. These are the paths where removing the
	// confinement actually destroys something.
	nested := filepath.Join(w.Path, "src")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	sibling := repo(t) // another checkout entirely, with its own dispatch root
	other, err := Create(sibling, "someone-elses-turn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Remove() })

	cases := []struct {
		name               string
		path, branch, base string
		why                string
	}{
		{"empty path", "", w.Branch, w.Base, "a record with no worktree path"},
		{"no base recorded", w.Path, w.Branch, "", "without a base commit nothing can tell what the turn committed"},
		{"empty branch", w.Path, "", w.Base, "a branch name that is not a ref"},
		{"flag-shaped branch", w.Path, "--force", w.Base, "a branch name git could read as a flag"},
		{"the shared checkout itself", root, "main", w.Base, "the one directory every other piece of this estate reads from"},
		{"another repository's worktree", other.Path, other.Branch, other.Base, "a live worktree belonging to a different checkout"},
		{"nested inside a worktree", nested, w.Branch, w.Base, "a subdirectory of a worktree rather than the worktree"},
		{"a path that does not exist", filepath.Join(Root(root), "never-created"), w.Branch, w.Base, "a worktree that is not there"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Reattach(root, c.path, c.branch, c.base); err == nil {
				t.Fatalf("Reattach accepted %s (%s)", c.name, c.why)
			}
		})
	}
	// And the confinement refusal must not have been a blanket refusal: the
	// legitimate case still works, or the test above proves nothing.
	if _, err := Reattach(root, w.Path, w.Branch, w.Base); err != nil {
		t.Fatalf("Reattach refused the legitimate case too, so the refusals above prove nothing: %v", err)
	}
}

// A directory that sits in the dispatch root but is not a git worktree --
// leftover junk, a half-created directory -- is refused rather than handed
// to git to interpret.
func TestReattachRefusesADirectoryThatIsNotAWorktree(t *testing.T) {
	root := repo(t)
	junk := filepath.Join(Root(root), "not-a-worktree")
	if err := os.MkdirAll(junk, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Reattach(root, junk, "dispatch/not-a-worktree", "deadbeef"); err == nil {
		t.Fatal("Reattach accepted a plain directory as a worktree")
	}
}
