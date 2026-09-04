package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/estate/internal/isolate"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/reclaim"
	"github.com/jonhill90/agent-estate/estate/internal/sweep"
)

// This is the whole of agent-estate#1000 driven end to end against REAL git
// and a REAL ledger file, through the same sweepConfig the `estate dispatch`
// and `estate sweep-worktrees` paths build. The only thing stubbed is the
// forge, because a test may not depend on a live network -- everything that
// actually deletes a directory is production code.
//
// Both directions run in one case on purpose: three worktrees in the same
// dispatch root, differing only in whether their work was collected, judged
// in one sweep. A test that only asserted the removal direction would pass
// against an implementation that removes everything.

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
}

// seedRepo builds a checkout with a bare origin, the shape every dispatch
// worktree here hangs off.
func seedRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q", "-b", "main")
	git(t, root, "config", "user.email", "test@example.com")
	git(t, root, "config", "user.name", "test")
	if err := os.WriteFile(filepath.Join(root, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "seed")
	bare := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	git(t, root, "remote", "add", "origin", bare)
	t.Cleanup(func() { os.RemoveAll(isolate.Root(root)) })
	return root
}

func TestSweepRemovesWhatLandedAndKeepsWhatDidNot(t *testing.T) {
	root := seedRepo(t)

	// 1. LANDED. Committed, pushed, and its pull request squash-merged --
	//    which deletes the branch from origin, so the only remaining
	//    evidence of collection is the forge's.
	landedWT, err := isolate.Create(root, "landed-turn")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(landedWT.Path, "shipped.txt"), []byte("merged"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, landedWT.Path, "add", "-A")
	git(t, landedWT.Path, "commit", "-qm", "work that landed")
	git(t, landedWT.Path, "push", "-q", "origin", "HEAD:"+landedWT.Branch)
	git(t, landedWT.Path, "push", "-q", "origin", "--delete", landedWT.Branch)
	landedHead, err := landedWT.Head()
	if err != nil {
		t.Fatal(err)
	}

	// 2. NOT LANDED. It pushed once and then committed again without
	//    pushing, so origin's copy of the branch is right there and simply
	//    does not contain the tip. That is a DEFINITE no rather than a
	//    failed fetch -- deliberately, because "the branch is missing" and
	//    "the branch is behind" are different code paths, and a version of
	//    this test where every refusal came from a failed fetch stayed green
	//    against a mutant with the uncollected-commits refusal deleted.
	strandedWT, err := isolate.Create(root, "stranded-turn")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strandedWT.Path, "pushed.txt"), []byte("collected"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, strandedWT.Path, "add", "-A")
	git(t, strandedWT.Path, "commit", "-qm", "work that was pushed")
	git(t, strandedWT.Path, "push", "-q", "origin", "HEAD:"+strandedWT.Branch)
	stranded := filepath.Join(strandedWT.Path, "never-collected.txt")
	if err := os.WriteFile(stranded, []byte("the only copy"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, strandedWT.Path, "add", "-A")
	git(t, strandedWT.Path, "commit", "-qm", "work nobody collected")

	// 3. DIRTY. Nothing committed, output sitting uncommitted in the tree --
	//    the shape three lanes died in on 2026-09-03 and survived because
	//    teardown refused.
	dirtyWT, err := isolate.Create(root, "dirty-turn")
	if err != nil {
		t.Fatal(err)
	}
	uncommitted := filepath.Join(dirtyWT.Path, "half-written.txt")
	if err := os.WriteFile(uncommitted, []byte("mid-turn output"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A real ledger file, written through the real Append, then read back
	// through the real Current() -- the same round trip the sweep does.
	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []*isolate.Worktree{landedWT, strandedWT, dirtyWT} {
		id := filepath.Base(w.Path)
		if err := l.Append(ledger.Record{
			ID: id, Issue: "1000", Lane: id, State: ledger.Complete,
			Worktree: w.Path, Branch: w.Branch, Base: w.Base,
		}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}

	// The forge, stubbed: it knows about exactly the one commit that landed.
	// Anything else is a definite "no", never an error, so a refusal below
	// is a refusal on the merits rather than on blindness.
	asked := map[string]bool{}
	forge := func(commit string) (bool, error) {
		asked[commit] = true
		return commit == landedHead, nil
	}

	cfg := sweepConfig(root, forge, true)
	cfg.Probe = func(int) (reclaim.ProcessInfo, error) {
		return reclaim.ProcessInfo{}, errors.New("no process in this test")
	}
	results := sweep.Run(records, cfg)

	byID := map[string]sweep.Result{}
	for _, r := range results {
		byID[r.Record.ID] = r
	}
	if r := byID["landed-turn"]; !r.Removed {
		t.Fatalf("work that landed was not swept: %s", r.Reason)
	}
	if _, err := os.Stat(landedWT.Path); !os.IsNotExist(err) {
		t.Fatalf("the sweep reported removing %s but it is still there", landedWT.Path)
	}
	if !asked[landedHead] {
		t.Fatal("the forge was never consulted, so the removal happened on some other evidence than the one under test")
	}

	if r := byID["stranded-turn"]; r.Removed {
		t.Fatalf("committed work nothing has collected was destroyed: %s", r.Reason)
	}
	if _, err := os.Stat(stranded); err != nil {
		t.Fatalf("the stranded turn's only copy is gone: %v", err)
	}
	if r := byID["dirty-turn"]; r.Removed {
		t.Fatalf("uncommitted work was destroyed: %s", r.Reason)
	}
	if _, err := os.Stat(uncommitted); err != nil {
		t.Fatalf("the dirty turn's uncommitted output is gone: %v", err)
	}
	if r := byID["dirty-turn"]; !strings.Contains(r.Reason, "uncommitted") {
		t.Fatalf("the dirty turn was kept, but not for the stated reason: %s", r.Reason)
	}
}

// The same three worktrees, all recorded `unknown` instead of `complete`.
// Nothing may be touched -- including the one whose work demonstrably
// landed, which is the case that would otherwise look most safe to remove.
func TestSweepTouchesNothingForAnUnknownTurnEvenWhenItsWorkLanded(t *testing.T) {
	root := seedRepo(t)
	w, err := isolate.Create(root, "timed-out-turn")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.Path, "shipped.txt"), []byte("merged"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, w.Path, "add", "-A")
	git(t, w.Path, "commit", "-qm", "work that landed")
	git(t, w.Path, "push", "-q", "origin", "HEAD:"+w.Branch)

	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "timed-out-turn", Issue: "1000", Lane: "timed-out-turn",
		State: ledger.Unknown, At: time.Now(),
		Worktree: w.Path, Branch: w.Branch, Base: w.Base,
	}); err != nil {
		t.Fatal(err)
	}
	records, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}

	cfg := sweepConfig(root, func(string) (bool, error) { return true, nil }, true)
	cfg.Probe = func(int) (reclaim.ProcessInfo, error) { return reclaim.ProcessInfo{Exists: false}, nil }
	for _, r := range sweep.Run(records, cfg) {
		if r.Removed || r.Eligible {
			t.Fatalf("an unknown turn's worktree was swept: %s", r.Reason)
		}
	}
	if _, err := os.Stat(w.Path); err != nil {
		t.Fatalf("the unknown turn's worktree is gone: %v", err)
	}
}
