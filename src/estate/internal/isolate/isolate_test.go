package isolate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo builds a throwaway git repo with one commit and returns its root.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "seed"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// Worktrees holding uncommitted work survive Remove by design, so a test
	// that leaves one behind would poison the next run through the shared
	// dispatch root. Clear this repo's root unconditionally.
	t.Cleanup(func() { os.RemoveAll(Root(root)) })
	return root
}

// Root must distinguish two repositories that share a base name, or they
// collide in the dispatch root and refuse each other's ids.
func TestRootIsUniquePerRepositoryNotPerBaseName(t *testing.T) {
	a := filepath.Join(t.TempDir(), "checkout")
	b := filepath.Join(t.TempDir(), "checkout")
	if Root(a) == Root(b) {
		t.Fatalf("two repositories named %q share dispatch root %s", filepath.Base(a), Root(a))
	}
}

func TestCreateGivesADirectoryThatIsNotTheSharedCheckout(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "907-1756000000")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Remove()

	if w.Path == root {
		t.Fatal("the dispatch worktree must not be the shared checkout itself")
	}
	if strings.HasPrefix(w.Path, root+string(os.PathSeparator)) {
		t.Errorf("worktree %s is inside the shared checkout; a runaway agent would still be writing into it", w.Path)
	}
	if fi, err := os.Stat(w.Path); err != nil || !fi.IsDir() {
		t.Fatalf("worktree path is not a usable directory: %v", err)
	}
	// It must be a real checkout, not an empty directory that merely exists.
	if _, err := os.Stat(filepath.Join(w.Path, "seed.txt")); err != nil {
		t.Errorf("worktree does not contain the repo's content: %v", err)
	}
}

// The property the whole phase exists for: work done in the worktree does not
// appear in the shared checkout's working tree.
func TestWritesInTheWorktreeDoNotTouchTheSharedCheckout(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "isolation-proof")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Remove()

	if err := os.WriteFile(filepath.Join(w.Path, "agent-wrote-this.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "agent-wrote-this.txt")); !os.IsNotExist(err) {
		t.Fatal("a file written in the dispatch worktree appeared in the shared checkout")
	}
	// And the shared checkout's own HEAD must not have moved.
	head := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	head.Dir = root
	out, err := head.Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "main" {
		t.Fatalf("shared checkout moved off main to %q", strings.TrimSpace(string(out)))
	}
}

func TestCreateUsesItsOwnBranchSoTwoDispatchesCannotCollide(t *testing.T) {
	root := repo(t)
	a, err := Create(root, "task-a")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Remove()
	b, err := Create(root, "task-b")
	if err != nil {
		t.Fatal(err)
	}
	defer b.Remove()

	if a.Branch == b.Branch {
		t.Fatalf("two dispatches share branch %q", a.Branch)
	}
	if a.Path == b.Path {
		t.Fatalf("two dispatches share path %q", a.Path)
	}
}

// Fails closed. Every one of these means "we could not isolate", and an
// un-isolated dispatch is a full-permission unattended agent in the shared
// checkout -- the thing this package exists to prevent.
func TestCreateRefusesRatherThanRunUnisolated(t *testing.T) {
	t.Run("not a git repository", func(t *testing.T) {
		if _, err := Create(t.TempDir(), "id"); err == nil {
			t.Fatal("must refuse when the root is not a git repository")
		}
	})
	t.Run("empty id", func(t *testing.T) {
		if _, err := Create(repo(t), ""); err == nil {
			t.Fatal("must refuse an empty dispatch id")
		}
	})
	t.Run("id escaping its directory", func(t *testing.T) {
		// "." and "..." are the ones that slip every separator and ".." check
		// while still resolving to the dispatch root itself.
		for _, bad := range []string{"../escape", "a/b", "..", "with space/x", ".", "...", "./"} {
			if _, err := Create(repo(t), bad); err == nil {
				t.Errorf("must refuse dispatch id %q -- it can leave the worktree root", bad)
			}
		}
	})
	// These contain no path separator, so the traversal check above does not
	// see them; only the character allowlist does. Without a case like this
	// the allowlist can be deleted with every test still green.
	t.Run("id with characters a shell or refname would misread", func(t *testing.T) {
		for _, bad := range []string{"with space", "semi;colon", "dollar$sign", "quote'mark", "tilde~x", "caret^y", "colon:z", "star*"} {
			if _, err := Create(repo(t), bad); err == nil {
				t.Errorf("must refuse dispatch id %q", bad)
			}
		}
	})
	// An EMPTY directory at the target is the discriminating case: `git
	// worktree add` accepts one, so if this refusal is ours it must fire
	// before git is reached. Without this, the check can be deleted and git's
	// own non-empty-directory error keeps the suite green.
	t.Run("target directory already exists but is empty", func(t *testing.T) {
		root := repo(t)
		if err := os.MkdirAll(filepath.Join(Root(root), "squatter"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := Create(root, "squatter")
		if err == nil {
			t.Fatal("must refuse when the worktree path already exists, even empty")
		}
		if !strings.Contains(err.Error(), "already exists") {
			t.Errorf("the refusal must be ours, not git's; got: %v", err)
		}
	})
	t.Run("same id twice", func(t *testing.T) {
		root := repo(t)
		w, err := Create(root, "dup")
		if err != nil {
			t.Fatal(err)
		}
		defer w.Remove()
		if _, err := Create(root, "dup"); err == nil {
			t.Fatal("must refuse to reuse a live dispatch's worktree rather than share it")
		}
	})
}

func TestRemoveCleansUpBothWorktreeAndBranch(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "cleanup")
	if err != nil {
		t.Fatal(err)
	}
	path, branch := w.Path, w.Branch
	if err := w.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree directory survived Remove: %v", err)
	}
	list := exec.Command("git", "branch", "--list", branch)
	list.Dir = root
	out, _ := list.Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("branch %s survived Remove: %q", branch, out)
	}
}

// Remove must not destroy work: a worktree with uncommitted changes is a
// dispatch whose output has not been collected yet.
func TestRemoveRefusesToDiscardUncommittedWork(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "has-work")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "unsaved.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = w.Remove()
	if err == nil {
		t.Fatal("Remove must refuse a worktree holding uncommitted work rather than delete it")
	}
	// git would also refuse here, for its own reasons. Assert the refusal is
	// OURS -- otherwise deleting the dirty check leaves this test green and
	// the guarantee unowned.
	if !strings.Contains(err.Error(), "collect or commit it first") {
		t.Errorf("the refusal must come from our dirty check, not git's; got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(w.Path, "unsaved.txt")); err != nil {
		t.Fatalf("the refused Remove destroyed the work anyway: %v", err)
	}
	// Untracked-but-added and committed-nothing states are the same absence
	// of collection; Dirty must see the file directly too.
	if dirty, err := w.Dirty(); err != nil || !dirty {
		t.Errorf("Dirty() = %v, %v; want true, nil", dirty, err)
	}
}
