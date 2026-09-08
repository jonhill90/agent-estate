package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/isolate"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/reclaim"
	"github.com/jonhill90/agent-estate/estate/internal/sweep"
)

// TestSweepNamedRootFixture is agent-estate#1294's own acceptance fixture:
// two SEPARATE, real git repositories (own and foreign, each with its own
// dispatch root -- exactly the shape the issue names: "the dispatch root
// is derived per-checkout... nothing can clean anyone else's"), one
// cleanly-removable worktree in each, and one ledger carrying both
// records (the real-world shape of the defect: a ledger row naming a
// worktree that belongs to a DIFFERENT checkout's dispatch root).
//
// Fails to even COMPILE against pre-agent-estate#1294 main: resolveSweepRoot,
// sweepConfig(root, ...) taking a root directly, and isolate.ReattachAt do
// not exist there at all -- there was no way to name a root, only ever the
// caller's own derived one. The brief's own required case is the second
// subtest by name ("a named foreign root sweeps only when named"); all four
// are new, none pass on main.
func TestSweepNamedRootFixture(t *testing.T) {
	own := seedRepo(t)
	foreign := seedRepo(t)

	ownWT, err := isolate.Create(own, "own-turn")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownWT.Path, "done.txt"), []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, ownWT.Path, "add", "-A")
	git(t, ownWT.Path, "commit", "-qm", "own work")
	git(t, ownWT.Path, "push", "-q", "origin", "HEAD:"+ownWT.Branch)

	foreignWT, err := isolate.Create(foreign, "foreign-turn")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreignWT.Path, "done.txt"), []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, foreignWT.Path, "add", "-A")
	git(t, foreignWT.Path, "commit", "-qm", "foreign work")
	git(t, foreignWT.Path, "push", "-q", "origin", "HEAD:"+foreignWT.Branch)

	// One ledger, both records -- the shape a stale or hand-edited row
	// produces in reality; sweep.Run judges records purely on their own
	// content, never on which file they were read from.
	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []*isolate.Worktree{ownWT, foreignWT} {
		id := filepath.Base(w.Path)
		if err := l.Append(ledger.Record{
			ID: id, Issue: "1294", Lane: id, State: ledger.Complete,
			Worktree: w.Path, Branch: w.Branch, Base: w.Base,
		}); err != nil {
			t.Fatal(err)
		}
	}
	records, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}

	noProcess := func(int) (reclaim.ProcessInfo, error) {
		return reclaim.ProcessInfo{}, errors.New("no process in this test")
	}
	// Origin already has both pushes; the forge is never needed to
	// establish either worktree as collected, so it always says no.
	forge := func(string) (bool, error) { return false, nil }

	sweepReport := func(root string) sweepSummary {
		t.Helper()
		cfg := sweepConfig(root, forge, false) // report mode: mutates nothing
		cfg.Probe = noProcess
		return summarizeSweep(sweep.Run(records, cfg))
	}

	t.Run("own root sweeps as before", func(t *testing.T) {
		root, foreignFlag, rerr := resolveSweepRoot(own, "")
		if rerr != nil {
			t.Fatal(rerr)
		}
		if foreignFlag {
			t.Fatal("this checkout's own derived root was reported as foreign")
		}
		s := sweepReport(root)
		if s.removed != 1 {
			t.Fatalf("own-root sweep: want 1 removable record (own-turn), got %+v", s)
		}
		if s.outsideRoot != 1 {
			t.Fatalf("own-root sweep: want 1 outside-root record (foreign-turn), got %+v", s)
		}
	})

	t.Run("a named foreign root sweeps only when named", func(t *testing.T) {
		root, foreignFlag, rerr := resolveSweepRoot(own, isolate.Root(foreign))
		if rerr != nil {
			t.Fatal(rerr)
		}
		if !foreignFlag {
			t.Fatal("a genuinely different checkout's root was reported as this checkout's own")
		}
		s := sweepReport(root)
		if s.removed != 1 {
			t.Fatalf("named foreign-root sweep: want 1 removable record (foreign-turn, now IN scope), got %+v", s)
		}
		if s.outsideRoot != 1 {
			t.Fatalf("named foreign-root sweep: want 1 outside-root record (own-turn, now OUT of scope), got %+v", s)
		}
	})

	t.Run("an unnamed foreign root is still refused", func(t *testing.T) {
		// No --root at all -- an operator who names nothing gets no access
		// to another checkout's worktrees, exactly as before this feature
		// existed. Checked on the specific record, not just the aggregate
		// count, so this reads as its own guarantee rather than a restatement
		// of the first subtest's totals.
		root, _, rerr := resolveSweepRoot(own, "")
		if rerr != nil {
			t.Fatal(rerr)
		}
		for _, r := range sweep.Run(records, sweepConfig(root, forge, false)) {
			if r.Record.Worktree == foreignWT.Path && r.Category != sweep.CategoryOutsideRoot {
				t.Fatalf("foreign-turn was not refused without --root: category=%v reason=%q", r.Category, r.Reason)
			}
		}
	})

	t.Run("a named path outside the dispatch parent is refused", func(t *testing.T) {
		outside := t.TempDir() // NOT under isolate.DispatchParent()
		if _, _, rerr := resolveSweepRoot(own, outside); rerr == nil {
			t.Fatalf("resolveSweepRoot accepted %s, which does not sit under the dispatch-root parent %s", outside, isolate.DispatchParent())
		}
		// The confinement is directory depth, not merely a path prefix: a
		// path two levels under the parent (a single WORKTREE, not a root)
		// must be refused too, not silently accepted as an empty sweep.
		tooDeep := filepath.Join(isolate.Root(own), "some-worktree-id")
		if _, _, rerr := resolveSweepRoot(own, tooDeep); rerr == nil {
			t.Fatalf("resolveSweepRoot accepted %s, which is a worktree's own path, not a dispatch root", tooDeep)
		}
	})
}
