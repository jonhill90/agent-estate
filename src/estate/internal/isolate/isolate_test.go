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

// repoWithRemote builds a repo (as repo does) plus a bare "origin" it can
// fetch from, with branch already pushed there -- what CreateOnBranch needs:
// a real remote tip to fetch and check out, not a local ref.
func repoWithRemote(t *testing.T, branch string) string {
	t.Helper()
	root := repo(t)
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	for _, args := range [][]string{
		{"remote", "add", "origin", bare},
		{"push", "-q", "origin", "HEAD:" + branch},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
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

// CreateOnBranch is what a fix pass uses to continue an EXISTING pull
// request's branch (agent-estate#940's "does not survive a fix pass"
// follow-up) instead of Create's always-a-new-branch shape.
func TestCreateOnBranchChecksOutTheFetchedTipDetached(t *testing.T) {
	root := repoWithRemote(t, "pr-branch")
	w, err := CreateOnBranch(root, "fix-1", "pr-branch")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Remove()

	if !w.Detached {
		t.Fatal("CreateOnBranch did not mark the worktree Detached")
	}
	if w.Branch != "pr-branch" {
		t.Fatalf("w.Branch = %q, want %q", w.Branch, "pr-branch")
	}
	out, err := exec.Command("git", "-C", w.Path, "symbolic-ref", "-q", "HEAD").CombinedOutput()
	if err == nil {
		t.Fatalf("HEAD is a branch (%s), not detached", strings.TrimSpace(string(out)))
	}
	head, err := w.Head()
	if err != nil {
		t.Fatal(err)
	}
	if head != w.Base {
		t.Fatalf("fresh CreateOnBranch checkout HEAD %q != recorded Base %q", head, w.Base)
	}
}

// Two dispatches continuing the SAME PR branch, one after the other (the
// exact fix-pass-after-the-original-dispatch shape), must not collide --
// the whole reason the checkout is detached rather than a same-named local
// branch (see CreateOnBranch's own doc comment).
func TestCreateOnBranchDoesNotCollideWithAnEarlierWorktreeOnTheSameBranch(t *testing.T) {
	root := repoWithRemote(t, "pr-branch")
	first, err := Create(root, "original-author")
	if err != nil {
		t.Fatal(err)
	}
	// Simulate the original dispatch's worktree still holding committed,
	// uncollected work -- Remove refuses to tear that down, so it is not
	// guaranteed gone by the time a fix pass runs. Push its branch as the PR
	// branch fix-1 will continue.
	if err := os.WriteFile(filepath.Join(first.Path, "more.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "more"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = first.Path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// first.Branch ("dispatch/original-author") is a DIFFERENT name than
	// "pr-branch" -- push its content onto pr-branch to simulate a PR that
	// really was opened from a dispatch branch.
	if out, err := exec.Command("git", "-C", first.Path, "push", "-qf", "origin", "HEAD:pr-branch").CombinedOutput(); err != nil {
		t.Fatalf("git push: %v: %s", err, out)
	}

	w, err := CreateOnBranch(root, "fix-1", "pr-branch")
	if err != nil {
		t.Fatalf("CreateOnBranch collided with the still-live original worktree: %v", err)
	}
	if err := w.Remove(); err != nil {
		t.Fatalf("Remove refused a detached, uncommitted CreateOnBranch worktree: %v", err)
	}
}

func TestCreateOnBranchRefusesRatherThanAssumeTheBranchExists(t *testing.T) {
	t.Run("branch not on origin", func(t *testing.T) {
		root := repoWithRemote(t, "some-other-branch")
		if _, err := CreateOnBranch(root, "fix-1", "does-not-exist-on-origin"); err == nil {
			t.Fatal("must refuse when origin does not have the branch")
		}
	})
	t.Run("no origin remote at all", func(t *testing.T) {
		root := repo(t)
		if _, err := CreateOnBranch(root, "fix-1", "pr-branch"); err == nil {
			t.Fatal("must refuse when there is no origin to fetch from")
		}
	})
	t.Run("empty branch", func(t *testing.T) {
		root := repoWithRemote(t, "pr-branch")
		if _, err := CreateOnBranch(root, "fix-1", "   "); err == nil {
			t.Fatal("must refuse a blank branch argument")
		}
	})
	t.Run("bad dispatch id", func(t *testing.T) {
		root := repoWithRemote(t, "pr-branch")
		if _, err := CreateOnBranch(root, "../escape", "pr-branch"); err == nil {
			t.Fatal("must refuse a dispatch id that can escape its directory, same as Create")
		}
	})
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

// Regression: an independent review found that Remove()'s "never delete
// uncollected work" guarantee did not see GITIGNORED output. `git status
// --porcelain` omits ignored paths by default, so a turn whose only output
// was e.g. a *.log or a build artifact reported clean and was deleted.
//
// The guarantee is about a HUMAN'S ability to recover a turn's output, and a
// file's gitignore status has nothing to do with whether it was worth
// keeping.
func TestRemoveRefusesWhenTheOnlyOutputIsGitignored(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "ignored-output")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, ".gitignore"), []byte("*.secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Commit the .gitignore so the worktree is otherwise clean; the ONLY
	// uncommitted thing left is the ignored file.
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "ignore rules"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = w.Path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	out := filepath.Join(w.Path, "agent-output.secret")
	if err := os.WriteFile(out, []byte("work nobody collected"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirty, err := w.Dirty()
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Error("Dirty() must see gitignored output; a turn's result is not less real for being ignored")
	}
	if err := w.Remove(); err == nil {
		t.Fatal("Remove must refuse: the worktree holds output nothing has collected")
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("Remove destroyed gitignored output: %v", err)
	}
}

// From a council seat: Remove() destroyed COMMITTED agent output. An agent
// that commits its work on the dispatch branch leaves a clean `git status`,
// so Dirty() reported nothing to collect, and Remove() then ran
// `git branch -D` -- deleting the only ref to those commits.
//
// The uncommitted case was already covered. This is the opposite one, and it
// is worse: the agent did the tidy thing and lost more for it.
func TestRemoveRefusesToDiscardCommittedWork(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "committed-work")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "result.txt"), []byte("the turn's output"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "the turn's work"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = w.Path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// git status is clean now -- that is the trap.
	dirty, err := w.Dirty()
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("precondition: a committed worktree is not dirty")
	}

	err = w.Remove()
	if err == nil {
		t.Fatal("Remove must refuse: the branch holds commits nothing else references")
	}
	if !strings.Contains(err.Error(), "commit") {
		t.Errorf("the refusal must say the work is committed; got: %v", err)
	}
	// And the work must still be there.
	if _, err := os.Stat(filepath.Join(w.Path, "result.txt")); err != nil {
		t.Fatalf("the refused Remove destroyed committed work anyway: %v", err)
	}
	out, err := exec.Command("git", "-C", root, "branch", "--list", w.Branch).Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Error("the branch holding the commits was deleted")
	}
}

// The fix for agent-estate#983: a worktree whose ONLY ignored content is the
// __pycache__ directory reference/'s own unmaintained Python leaves behind
// must proceed, because there is nothing in it to collect.
func TestRemoveProceedsWhenTheOnlyIgnoredContentIsKnownDetritus(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "only-detritus")
	if err != nil {
		t.Fatal(err)
	}
	// A tracked sibling under the same directory, plus the .gitignore rule
	// that makes __pycache__/ ignored rather than merely untracked -- both
	// committed BEFORE the branch's recorded Base, via the shared repo's
	// seed commit, so this worktree's branch never advances past Base and
	// the separate "committed work" refusal does not fire here; only Dirty
	// is under test.
	seed := []struct{ path, content string }{
		{filepath.Join("reference", "scripts", "supervisor", "core.py"), "# reference only\n"},
		{".gitignore", "__pycache__/\n"},
	}
	for _, f := range seed {
		full := filepath.Join(root, f.path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "seed reference + gitignore"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// Re-point the branch created by Create at the new tip, keeping Base at
	// the ORIGINAL seed commit -- Create already ran, so the worktree must
	// pick the seeding commit up the same way a fresh checkout continuing an
	// existing branch would.
	if out, err := exec.Command("git", "-C", w.Path, "merge", "-q", "--ff-only", "main").CombinedOutput(); err != nil {
		t.Fatalf("git merge --ff-only main: %v: %s", err, out)
	}
	head, err := w.Head()
	if err != nil {
		t.Fatal(err)
	}
	w.Base = head

	pycache := filepath.Join(w.Path, "reference", "scripts", "supervisor", "__pycache__")
	if err := os.MkdirAll(pycache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pycache, "mod.cpython-311.pyc"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirty, err := w.Dirty()
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatal("Dirty() must not count known detritus -- reference/scripts/supervisor/__pycache__/ is regenerable and holds nothing a turn produced")
	}
	if err := w.Remove(); err != nil {
		t.Fatalf("Remove must proceed when the only ignored content is known detritus: %v", err)
	}
}

// The guard must not become "ignore everything --ignored reports": a
// gitignored path NOT on the known-detritus list -- a lane's own built
// binary, here -- must still refuse teardown exactly as before this fix.
func TestRemoveStillRefusesUnlistedIgnoredContent(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "unlisted-ignored")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, ".gitignore"), []byte("/estate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "ignore rules"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = w.Path
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	bin := filepath.Join(w.Path, "estate")
	if err := os.WriteFile(bin, []byte("not really a binary, but the only copy"), 0o755); err != nil {
		t.Fatal(err)
	}

	dirty, err := w.Dirty()
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatal("Dirty() must still see an unlisted gitignored file as uncollected work")
	}
	if err := w.Remove(); err == nil {
		t.Fatal("Remove must still refuse: a gitignored binary not on the known-detritus list may be the only copy that exists")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("the refused Remove destroyed the unlisted ignored file anyway: %v", err)
	}
}

// The guard is an EXACT match on the one allowlisted entry, not a prefix
// match on its directory. A gitignored path that lives inside
// reference/scripts/supervisor/ but is not the allowlisted
// __pycache__/ entry itself -- a sibling ignored directory, here -- must
// still refuse teardown. This is the near-miss PR #986's review asked for:
// a HasPrefix(path, "reference/scripts/supervisor/") mutation of
// isKnownDetritus makes this test fail while
// TestRemoveStillRefusesUnlistedIgnoredContent (an unrelated top-level
// path) keeps passing, because that test never exercises anything under
// the allowlisted directory.
func TestRemoveStillRefusesUnlistedIgnoredContentInsideTheAllowlistedDirectory(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "near-miss-detritus")
	if err != nil {
		t.Fatal(err)
	}
	// Same seeding shape as TestRemoveProceedsWhenTheOnlyIgnoredContentIsKnownDetritus:
	// a tracked sibling under reference/scripts/supervisor/ is required for git to
	// report the ignored subdirectory on its own line. Without a tracked sibling,
	// git collapses the whole untracked "reference/" tree to one "!! reference/"
	// line regardless of what's ignored inside it, which would make this test pass
	// for the wrong reason -- confirmed directly: without the seed, this test
	// passed even under the reviewer's HasPrefix mutation, because Dirty() never
	// saw the near-miss path at all.
	buildOutput := filepath.Join("reference", "scripts", "supervisor", "build-output")
	seed := []struct{ path, content string }{
		{filepath.Join("reference", "scripts", "supervisor", "core.py"), "# reference only\n"},
		{".gitignore", "__pycache__/\n" + buildOutput + "/\n"},
	}
	for _, f := range seed {
		full := filepath.Join(root, f.path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(f.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "seed reference + gitignore"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "-C", w.Path, "merge", "-q", "--ff-only", "main").CombinedOutput(); err != nil {
		t.Fatalf("git merge --ff-only main: %v: %s", err, out)
	}
	head, err := w.Head()
	if err != nil {
		t.Fatal(err)
	}
	w.Base = head

	full := filepath.Join(w.Path, buildOutput)
	if err := os.MkdirAll(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "artifact"), []byte("not really a binary, but the only copy"), 0o644); err != nil {
		t.Fatal(err)
	}

	dirty, err := w.Dirty()
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatal("Dirty() must still see reference/scripts/supervisor/build-output/ as uncollected work -- it is not the allowlisted __pycache__/ entry, only a sibling under the same directory")
	}
	if err := w.Remove(); err == nil {
		t.Fatal("Remove must still refuse: a gitignored directory under reference/scripts/supervisor/ that is not the allowlisted entry may be the only copy that exists")
	}
	if _, err := os.Stat(filepath.Join(full, "artifact")); err != nil {
		t.Fatalf("the refused Remove destroyed the unlisted ignored file anyway: %v", err)
	}
}

// A worktree that committed nothing is still removable -- otherwise every
// dispatch leaks.
func TestRemoveStillCleansUpWhenNothingWasCommitted(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "no-commits")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Remove(); err != nil {
		t.Fatalf("a worktree with no commits and no changes must be removable: %v", err)
	}
}

// If we never learned where the branch started, we cannot tell what is new.
// That is "could not measure", and it must refuse rather than read as
// "nothing was committed".
func TestUnknownBaseRefusesRatherThanAssumingNothingWasCommitted(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "no-base")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { w.Base = ""; _ = w.Remove() }()

	w.Base = "" // as if Create had not recorded it
	if _, err := w.Committed(); err == nil {
		t.Fatal("an unrecorded base must be an error, not a confident false")
	}
	if err := w.Remove(); err == nil {
		t.Fatal("Remove must refuse when it cannot tell whether work was committed")
	}
}
