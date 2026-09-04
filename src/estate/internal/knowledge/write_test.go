package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteThenReadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "index.json")
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Sources:       []SourceResult{{Name: "github-stars", OK: true, Count: 1}},
		Items:         []Item{{ID: "20260903120000", Source: "github-stars", Tier1: "a/one"}},
	}
	if err := Write(path, res); err != nil {
		t.Fatalf("Write() error: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read() error: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "20260903120000" {
		t.Fatalf("Read() = %+v", got)
	}
	if !got.GeneratedAt.Equal(res.GeneratedAt) {
		t.Errorf("GeneratedAt = %v, want %v", got.GeneratedAt, res.GeneratedAt)
	}
}

func TestWriteFileCarriesItsOwnDerivedStatement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	res := Result{GeneratedAt: time.Now().UTC(), StalenessRule: stalenessRule, Note: derivedNote}
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Note, "derived") || !strings.Contains(got.Note, "not, and must never be treated as") {
		t.Errorf("Note does not carry the derived/never-authoritative statement: %q", got.Note)
	}
	if got.StalenessRule == "" {
		t.Error("StalenessRule is empty")
	}
}

func TestReadOfUnwrittenIndexIsAVisibleError(t *testing.T) {
	if _, err := Read(filepath.Join(t.TempDir(), "never-generated.json")); err == nil {
		t.Fatal("Read() of a path that was never written returned no error")
	}
}

// chdirToSimulatedDispatchWorktree builds a directory under
// TMPDIR/estate-dispatch/<repo>/<id> -- the exact layout
// isolate.Create/CreateOnBranch produce -- and chdirs into it for the
// duration of the test, restoring the original working directory after.
// It does not touch git at all: isolate.IsDispatchWorktree (and therefore
// DefaultOutputPath) only ever inspects the path shape, never asks git
// anything, so a real worktree is not needed to exercise this.
func chdirToSimulatedDispatchWorktree(t *testing.T, id string) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "estate-dispatch", "sim-repo-1048", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}

// agent-estate#1048: two simulated turns dispatched against the same repo
// must resolve two DIFFERENT default index paths -- the property that stops
// concurrent lanes from silently overwriting each other's compiled index
// and each other's measurements.
//
// This test FAILS before the fix (both turns resolved
// ~/.local/state/agent-estate/knowledge/index.json, identically) and PASSES
// after it.
func TestDefaultOutputPathIsolatesConcurrentDispatchedTurns(t *testing.T) {
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", "") // exercise the derived default, not an override

	chdirToSimulatedDispatchWorktree(t, "turn-a-1048")
	pathA, err := DefaultOutputPath()
	if err != nil {
		t.Fatal(err)
	}

	chdirToSimulatedDispatchWorktree(t, "turn-b-1048")
	pathB, err := DefaultOutputPath()
	if err != nil {
		t.Fatal(err)
	}

	if pathA == pathB {
		t.Fatalf("two distinct dispatched turns resolved the SAME default index path %q -- this is agent-estate#1048", pathA)
	}
	if !strings.Contains(pathA, "turn-a-1048") {
		t.Errorf("pathA %q does not carry its own dispatch id", pathA)
	}
	if !strings.Contains(pathB, "turn-b-1048") {
		t.Errorf("pathB %q does not carry its own dispatch id", pathB)
	}
}

// A caller NOT running inside a dispatched turn's worktree -- the operator
// at a terminal, the Director regenerating by hand -- must keep resolving
// the one shared default exactly as before this change.
//
// This chdirs to an ordinary t.TempDir() rather than trusting the test
// binary's own ambient cwd: this repository is itself frequently run from
// inside a dispatched turn's own worktree (agent-estate's own dispatch
// convention), so the ambient cwd is exactly the case this test must NOT
// exercise.
func TestDefaultOutputPathKeepsTheSharedDefaultOutsideADispatchedTurn(t *testing.T) {
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "index.json")

	dir := t.TempDir() // NOT under TMPDIR/estate-dispatch -- an ordinary directory
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	got, err := DefaultOutputPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("DefaultOutputPath() outside a dispatched turn = %q, want the shared default %q", got, want)
	}
}

// An explicit ESTATE_KNOWLEDGE_INDEX override still wins even from inside a
// simulated dispatched turn's worktree -- agent-estate#1048 is explicit that
// taking the override away would break measurements that already depend on
// it.
func TestDefaultOutputPathExplicitOverrideStillWinsInsideADispatchedTurn(t *testing.T) {
	override := filepath.Join(t.TempDir(), "reviewer-own-index.json")
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", override)

	chdirToSimulatedDispatchWorktree(t, "turn-with-override-1048")
	got, err := DefaultOutputPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != override {
		t.Errorf("DefaultOutputPath() = %q, want the explicit override %q", got, override)
	}
}
