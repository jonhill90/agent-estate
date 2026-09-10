package isolate

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// emptyOutWorktree simulates the real shape found on the dispatch root,
// agent-estate#1337: every regular file removed, directory structure and
// a symlink left standing -- consistent with macOS's own per-user
// $TMPDIR file reaper (find's own -type f never matches a symlink, so a
// file-only sweep leaves one behind exactly like this). w.Path itself
// must survive as a directory; only its contents are hollowed out.
func emptyOutWorktree(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(path, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	// Leave a directory and a symlink standing, matching what was actually
	// found on the real dispatch root -- not just an empty directory, the
	// specific shape a file-only reaper produces.
	if err := os.Mkdir(filepath.Join(path, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docs", filepath.Join(path, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
}

// TestHollowCorpseDetectsAnEmptiedWorktree is agent-estate#1337's own
// reproduction: a real worktree (Create, a genuine git checkout with a
// real .git file) whose entire content -- .git included -- is then
// stripped the way the real dispatch root's seven were found, leaving
// only directory structure and a symlink. HollowCorpse must positively
// confirm this shape.
func TestHollowCorpseDetectsAnEmptiedWorktree(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "hollow")
	if err != nil {
		t.Fatal(err)
	}
	emptyOutWorktree(t, w.Path)

	ok, n, err := HollowCorpse(w.Path)
	if err != nil {
		t.Fatalf("HollowCorpse: %v", err)
	}
	if !ok {
		t.Fatalf("HollowCorpse = false, %d; want true, 0 -- .git is gone and no regular file remains", n)
	}
	if n != 0 {
		t.Fatalf("fileCount = %d; want 0", n)
	}
}

// TestHollowCorpseRefusesWhenRealContentSurvives is the safety property
// this function exists to provide: a worktree whose .git is ALSO gone
// but which still holds a real, uncommitted file is a fundamentally
// different, more dangerous shape -- content that might still need
// collecting -- and must never be confused with the emptied-out corpse
// above. Deleting .git alone, by hand, is not the same event as the
// OS reaping every file; HollowCorpse must tell them apart.
func TestHollowCorpseRefusesWhenRealContentSurvives(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "still-real")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(w.Path, ".git")); err != nil {
		t.Fatal(err)
	}
	// seed.txt (from repo(t)'s own commit) is still there, checked out --
	// real content survives even though .git does not.

	ok, n, err := HollowCorpse(w.Path)
	if err != nil {
		t.Fatalf("HollowCorpse: %v", err)
	}
	if ok {
		t.Fatalf("HollowCorpse = true, %d; want false -- real content (seed.txt) still exists and must not be waved through", n)
	}
	if n == 0 {
		t.Fatal("fileCount = 0; want at least 1 (seed.txt survives)")
	}
}

// TestHollowCorpseIsFalseForAnOrdinaryLiveWorktree is the control: an
// untouched, real worktree (.git present, real files present) must never
// match this shape.
func TestHollowCorpseIsFalseForAnOrdinaryLiveWorktree(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "live")
	if err != nil {
		t.Fatal(err)
	}
	ok, _, err := HollowCorpse(w.Path)
	if err != nil {
		t.Fatalf("HollowCorpse: %v", err)
	}
	if ok {
		t.Fatal("HollowCorpse = true for an ordinary, untouched worktree")
	}
}

// TestReattachAtReturnsErrHollowCorpseForAnEmptiedWorktree is the
// integration point agent-estate#1337 is actually about: main.go and
// internal/sweep only ever see ReattachAt's return, never HollowCorpse
// directly, so this is what must carry the distinction through.
func TestReattachAtReturnsErrHollowCorpseForAnEmptiedWorktree(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "hollow-reattach")
	if err != nil {
		t.Fatal(err)
	}
	path, branch := w.Path, w.Branch
	emptyOutWorktree(t, path)

	_, rerr := ReattachAt(Root(root), path, branch, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if rerr == nil {
		t.Fatal("ReattachAt succeeded against an emptied worktree")
	}
	var hollow *ErrHollowCorpse
	if !errors.As(rerr, &hollow) {
		t.Fatalf("ReattachAt's error is not an *ErrHollowCorpse -- got: %v", rerr)
	}
	if hollow.FileCount != 0 {
		t.Fatalf("ErrHollowCorpse.FileCount = %d; want 0", hollow.FileCount)
	}
}

// TestReattachAtStillRefusesGenericallyWhenRealContentRemains is the
// direction that must NOT change: a worktree missing .git but still
// holding real content is exactly as unsafe as before this change --
// ReattachAt must keep refusing it with the ordinary "not a git
// worktree" error, never ErrHollowCorpse, so nothing downstream
// mistakes it for a safe reconciliation.
func TestReattachAtStillRefusesGenericallyWhenRealContentRemains(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "still-dirty")
	if err != nil {
		t.Fatal(err)
	}
	path, branch := w.Path, w.Branch
	if err := os.RemoveAll(filepath.Join(path, ".git")); err != nil {
		t.Fatal(err)
	}

	_, rerr := ReattachAt(Root(root), path, branch, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if rerr == nil {
		t.Fatal("ReattachAt succeeded against a worktree with no .git")
	}
	var hollow *ErrHollowCorpse
	if errors.As(rerr, &hollow) {
		t.Fatalf("ReattachAt returned ErrHollowCorpse even though real content (seed.txt) survives -- this must stay a generic refusal: %v", rerr)
	}
}

// TestReconcileHollowCorpseRemovesTheEmptyShell is the apply-mode action:
// once HollowCorpse has confirmed nothing survives to lose, the empty
// directory shell itself may be removed.
func TestReconcileHollowCorpseRemovesTheEmptyShell(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "reconcile")
	if err != nil {
		t.Fatal(err)
	}
	emptyOutWorktree(t, w.Path)

	if err := ReconcileHollowCorpse(w.Path); err != nil {
		t.Fatalf("ReconcileHollowCorpse: %v", err)
	}
	if _, err := os.Stat(w.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree directory survived ReconcileHollowCorpse: %v", err)
	}
}

// TestReconcileHollowCorpseRefusesWhenNoLongerHollow is the re-
// verification this function's own doc comment promises: never trust a
// judgement made moments earlier before a destructive call. If real
// content has appeared since the last check, refuse rather than delete.
func TestReconcileHollowCorpseRefusesWhenNoLongerHollow(t *testing.T) {
	root := repo(t)
	w, err := Create(root, "changed-since")
	if err != nil {
		t.Fatal(err)
	}
	emptyOutWorktree(t, w.Path)
	// Something wrote real content back between judgement and action --
	// the exact race ReconcileHollowCorpse's re-check exists to catch.
	if err := os.WriteFile(filepath.Join(w.Path, "docs", "surprise.txt"), []byte("real work\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err = ReconcileHollowCorpse(w.Path)
	if err == nil {
		t.Fatal("ReconcileHollowCorpse removed a directory that now holds real content")
	}
	if _, statErr := os.Stat(filepath.Join(w.Path, "docs", "surprise.txt")); statErr != nil {
		t.Fatalf("the refused reconcile destroyed the new content anyway: %v", statErr)
	}
}
