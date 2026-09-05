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

// agent-estate#1184: an ad-hoc isolated checkout -- e.g. a reviewer's own
// `git worktree add /tmp/...` -- is a real git worktree but not one
// isolate.Create/CreateOnBranch made, so IsDispatchWorktree correctly
// reports false for it. Nothing used to stand between that "false" and the
// shared index, so ResolveWritePath must refuse there (requiresAck=true)
// rather than let a WRITE fall through silently the way a READ safely can.
//
// This test FAILS before the fix (requiresAck was never a concept; the
// resolved path always mapped straight to the shared default with no
// signal a caller could act on) and PASSES after it.
func TestResolveWritePathRequiresAckForAnUnrecognisedCwd(t *testing.T) {
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", "")
	t.Setenv(AllowSharedWriteEnv, "")

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "index.json")

	dir := t.TempDir() // NOT under TMPDIR/estate-dispatch -- an ad-hoc checkout
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	path, requiresAck, err := ResolveWritePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != shared {
		t.Errorf("ResolveWritePath() path = %q, want the shared default %q", path, shared)
	}
	if !requiresAck {
		t.Error("ResolveWritePath() requiresAck = false for an unrecognised cwd, want true -- this is agent-estate#1184")
	}
}

// Setting AllowSharedWriteEnv is the explicit opt-in agent-estate#1184
// requires before a write is allowed to proceed to the shared path from an
// unrecognised cwd.
func TestResolveWritePathAckedViaEnvAllowsTheSharedWrite(t *testing.T) {
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", "")
	t.Setenv(AllowSharedWriteEnv, "1")

	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	_, requiresAck, err := ResolveWritePath()
	if err != nil {
		t.Fatal(err)
	}
	if requiresAck {
		t.Error("ResolveWritePath() requiresAck = true after AllowSharedWriteEnv was set, want false")
	}
}

// A real dispatch worktree must never require the acknowledgement -- only
// the unrecognised-cwd fallback does. This is the "no operator/author
// impact for the case that already worked" half of agent-estate#1184's
// fix.
func TestResolveWritePathNeverRequiresAckInsideADispatchedTurn(t *testing.T) {
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", "")
	t.Setenv(AllowSharedWriteEnv, "")

	chdirToSimulatedDispatchWorktree(t, "turn-write-path-1184")
	_, requiresAck, err := ResolveWritePath()
	if err != nil {
		t.Fatal(err)
	}
	if requiresAck {
		t.Error("ResolveWritePath() requiresAck = true inside a dispatch worktree, want false")
	}
}

// An explicit ESTATE_KNOWLEDGE_INDEX override never requires the
// acknowledgement either -- the caller already said exactly where to
// write.
func TestResolveWritePathNeverRequiresAckWithAnExplicitOverride(t *testing.T) {
	override := filepath.Join(t.TempDir(), "explicit-index.json")
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", override)
	t.Setenv(AllowSharedWriteEnv, "")

	path, requiresAck, err := ResolveWritePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != override {
		t.Errorf("ResolveWritePath() path = %q, want the explicit override %q", path, override)
	}
	if requiresAck {
		t.Error("ResolveWritePath() requiresAck = true with an explicit override, want false")
	}
}

// agent-estate#1191 hole 2: resolveOutputPath used to return
// sharedFallback=false for ANY non-empty ESTATE_KNOWLEDGE_INDEX, without
// checking whether it names the shared default -- so pointing the override
// at the shared path wrote it with no acknowledgement. This is the same
// $HOME-relative path resolveOutputPath itself would fall through to; it
// must require the acknowledgement exactly as the fallback does.
//
// This test FAILS before the fix (requiresAck = false, because the override
// branch never looked at where p pointed) and PASSES after it.
func TestResolveWritePathRequiresAckWhenOverrideNamesTheSharedPathExactly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(AllowSharedWriteEnv, "")

	shared := filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "index.json")
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", shared)

	path, requiresAck, err := ResolveWritePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != shared {
		t.Errorf("ResolveWritePath() path = %q, want %q", path, shared)
	}
	if !requiresAck {
		t.Error("ResolveWritePath() requiresAck = false for an override naming the shared path exactly, want true -- this is agent-estate#1191 hole 2")
	}
}

// Same defect, a differently-spelled path naming the identical file: `..`
// segments that lexically clean down to the shared default. samePath must
// resolve both sides to absolute, cleaned form before comparing rather than
// requiring a byte-identical string.
//
// This test FAILS before the fix for the same reason as the exact-spelling
// case above, and would also fail against a naive `p == shared` string
// comparison fix that did not clean/absolute both sides first.
func TestResolveWritePathRequiresAckWhenOverrideNamesTheSharedPathViaDotDotSegments(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(AllowSharedWriteEnv, "")

	shared := filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "index.json")
	nonCanonical := filepath.Join(home, ".local", "state", "agent-estate", "knowledge", "nested", "..", "index.json")
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", nonCanonical)

	_, requiresAck, err := ResolveWritePath()
	if err != nil {
		t.Fatal(err)
	}
	if !requiresAck {
		t.Errorf("ResolveWritePath() requiresAck = false for %q, a non-canonical spelling of the shared path %q, want true -- this is agent-estate#1191 hole 2", nonCanonical, shared)
	}
}

// A symlinked directory component is the third spelling this fix commits to
// handling: the resolved, real shared path is reached via a symlink placed
// somewhere in its ancestry (the shape a symlinked $HOME produces).
// normalizePath must resolve that symlink before comparing rather than
// treating the symlink path and its target as different files.
func TestResolveWritePathRequiresAckWhenOverrideNamesTheSharedPathViaASymlinkedDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(AllowSharedWriteEnv, "")

	sharedDir := filepath.Join(home, ".local", "state", "agent-estate", "knowledge")
	if err := os.MkdirAll(sharedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(t.TempDir(), "linked-knowledge-dir")
	if err := os.Symlink(sharedDir, link); err != nil {
		t.Skipf("symlink not supported in this environment: %v", err)
	}
	viaSymlink := filepath.Join(link, "index.json")
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", viaSymlink)

	_, requiresAck, err := ResolveWritePath()
	if err != nil {
		t.Fatal(err)
	}
	if !requiresAck {
		t.Errorf("ResolveWritePath() requiresAck = false for %q, a symlinked spelling of the shared path, want true -- this is agent-estate#1191 hole 2", viaSymlink)
	}
}

// An override pointing anywhere genuinely else -- not the shared path under
// any spelling -- must keep behaving exactly as before this fix: no
// acknowledgement required. A per-turn or scratch index depends on this;
// every dispatched lane's own ESTATE_KNOWLEDGE_INDEX must keep writing
// without prompting.
func TestResolveWritePathStillNoAckForAnOverrideElsewhere(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(AllowSharedWriteEnv, "")

	override := filepath.Join(t.TempDir(), "dispatch", "turn-123", "index.json")
	t.Setenv("ESTATE_KNOWLEDGE_INDEX", override)

	path, requiresAck, err := ResolveWritePath()
	if err != nil {
		t.Fatal(err)
	}
	if path != override {
		t.Errorf("ResolveWritePath() path = %q, want %q", path, override)
	}
	if requiresAck {
		t.Error("ResolveWritePath() requiresAck = true for an override pointing elsewhere, want false")
	}
}
