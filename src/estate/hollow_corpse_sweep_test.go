package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/isolate"
	"github.com/jonhill90/agent-estate/estate/internal/ledger"
	"github.com/jonhill90/agent-estate/estate/internal/reclaim"
	"github.com/jonhill90/agent-estate/estate/internal/sweep"
)

// TestSweepWorktreesReconcilesAHollowCorpseThroughTheRealCLIWiring is
// agent-estate#1337's own end-to-end fixture: a real worktree (a real
// git checkout, isolate.Create, not a fake), emptied of every file --
// .git included -- the exact shape found on the real dispatch root,
// never applied against it here (a throwaway seedRepo(t) fixture
// throughout). Exercises the actual main.go wiring (sweepConfig's
// RemovalCheck and Remove closures), not a fake Remover -- the sweep
// package's own tests already prove the categorization in isolation;
// this proves main.go's ReattachAt-then-ReconcileHollowCorpse path is
// actually wired, the same reason TestSweepNamedRootFixture exists at
// this layer rather than sweep's own.
func TestSweepWorktreesReconcilesAHollowCorpseThroughTheRealCLIWiring(t *testing.T) {
	own := seedRepo(t)

	wt, err := isolate.Create(own, "hollow-turn")
	if err != nil {
		t.Fatal(err)
	}
	path := wt.Path

	// Empty out every file the same way emptyOutWorktree does in
	// internal/isolate's own test -- .git included, directory structure
	// and a symlink left standing.
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(path, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(path, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("docs", filepath.Join(path, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}

	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "hollow-turn", Issue: "1337", Lane: "hollow-turn", State: ledger.Complete,
		Worktree: wt.Path, Branch: wt.Branch, Base: wt.Base,
	}); err != nil {
		t.Fatal(err)
	}
	records, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}

	noProcess := func(int) (reclaim.ProcessInfo, error) {
		return reclaim.ProcessInfo{}, nil
	}
	forge := func(string) (bool, error) { return false, nil }
	root := isolate.Root(own)

	// Report mode first: must name this its own category, never
	// CategoryRefused, and must mutate nothing.
	reportCfg := sweepConfig(root, forge, false)
	reportCfg.Probe = noProcess
	reportResults := sweep.Run(records, reportCfg)
	if len(reportResults) != 1 {
		t.Fatalf("got %d result(s), want 1", len(reportResults))
	}
	if reportResults[0].Category != sweep.CategoryHollow {
		t.Fatalf("report mode: got category %v, want CategoryHollow -- reason: %s", reportResults[0].Category, reportResults[0].Reason)
	}
	if reportResults[0].Removed {
		t.Fatal("report mode set Removed -- it must mutate nothing")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("report mode touched the worktree directory: %v", err)
	}

	// Apply mode: the same judgement, now actually acted on -- the empty
	// shell is removed via isolate.ReconcileHollowCorpse (main.go's own
	// wiring, not a fake Remover), nothing else changes. Reported as an
	// ordinary CategoryRemoved success: sweep.Run's Remover contract is
	// a plain error return with no side channel for "which kind of
	// success", and this codebase already has a place that distinction
	// matters most -- report mode, asserted above, where it decides
	// whether a reader trusts --apply before running it. Once actually
	// applied, "reconciled" and "removed" both mean the record no longer
	// needs attention; unlike report mode, there is no decision left to
	// inform.
	applyCfg := sweepConfig(root, forge, true)
	applyCfg.Probe = noProcess
	applyResults := sweep.Run(records, applyCfg)
	if len(applyResults) != 1 {
		t.Fatalf("got %d result(s), want 1", len(applyResults))
	}
	if !applyResults[0].Removed || applyResults[0].Category != sweep.CategoryRemoved {
		t.Fatalf("apply mode did not reconcile the hollow corpse -- Removed=%v Category=%v Reason=%s", applyResults[0].Removed, applyResults[0].Category, applyResults[0].Reason)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("hollow corpse's empty directory shell survived apply mode: %v", statErr)
	}
}

// TestSweepSummaryNamesAHollowCorpseInReportModeOnly is the same fixture
// as the CLI-wiring test above, checked at the summary-line layer
// specifically: report mode's own printed line must name the
// reconciliation, distinct from "removed" or "refused", the exact
// property main_test.go's TestSweepSummarySeparatesCategories asserts
// for a synthetic fixture -- this is the same claim against the real,
// live-judged report path.
func TestSweepSummaryNamesAHollowCorpseInReportModeOnly(t *testing.T) {
	own := seedRepo(t)
	wt, err := isolate.Create(own, "hollow-summary")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(wt.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(wt.Path, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(wt.Path, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	l, err := ledger.Open(filepath.Join(t.TempDir(), "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(ledger.Record{
		ID: "hollow-summary", Issue: "1337", Lane: "hollow-summary", State: ledger.Complete,
		Worktree: wt.Path, Branch: wt.Branch, Base: wt.Base,
	}); err != nil {
		t.Fatal(err)
	}
	records, err := l.Current()
	if err != nil {
		t.Fatal(err)
	}

	noProcess := func(int) (reclaim.ProcessInfo, error) { return reclaim.ProcessInfo{}, nil }
	forge := func(string) (bool, error) { return false, nil }
	cfg := sweepConfig(isolate.Root(own), forge, false)
	cfg.Probe = noProcess

	s := summarizeSweep(sweep.Run(records, cfg))
	if s.hollow != 1 {
		t.Fatalf("summarizeSweep did not count the hollow corpse in report mode: %+v", s)
	}
	joined := strings.Join(s.report(false), "\n")
	if !strings.Contains(joined, "1 hollow corpse(s) would reconcile") {
		t.Fatalf("report mode does not name the pending reconciliation:\n%s", joined)
	}
	if strings.Contains(joined, "1 refused") {
		t.Fatalf("hollow corpse was also counted as a genuine refusal:\n%s", joined)
	}
}
