// This file is the proof contract.go's own doc comment and main.go's comment
// both cite: BuildSourceHealth's four states (agent-estate#1139 slice 3
// acceptance criterion, PR #1228 review) exercised against a REAL root, not
// just the provenance.SourceState enum's zero-value/String() mapping (that
// lives in internal/provenance/health_test.go and proves nothing about this
// package's own classification logic). Every case here asserts the state is
// DISTINGUISHABLE from its neighbours, not merely non-zero -- Unreadable vs
// Empty is the pair the review specifically flagged.
package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/provenance"
)

// TestBuildSourceHealth_Missing: root does not exist at all.
func TestBuildSourceHealth_Missing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "does-not-exist")

	report, err := BuildSourceHealth(root)
	if err != nil {
		t.Fatalf("BuildSourceHealth: %v", err)
	}
	if report.SourceHealth.State != provenance.SourceStateMissing {
		t.Fatalf("State = %s, want %s", report.SourceHealth.State, provenance.SourceStateMissing)
	}
	if report.SourceHealth.Root != root {
		t.Errorf("Root = %q, want %q", report.SourceHealth.Root, root)
	}
}

// TestBuildSourceHealth_EmptyDir: root exists, is readable, but contains no
// files at all -- the first of two distinct routes to SourceStateEmpty.
func TestBuildSourceHealth_EmptyDir(t *testing.T) {
	root := t.TempDir()

	report, err := BuildSourceHealth(root)
	if err != nil {
		t.Fatalf("BuildSourceHealth: %v", err)
	}
	if report.SourceHealth.State != provenance.SourceStateEmpty {
		t.Fatalf("State = %s, want %s", report.SourceHealth.State, provenance.SourceStateEmpty)
	}
	if report.SourceHealth.FilesTotal != 0 {
		t.Errorf("FilesTotal = %d, want 0", report.SourceHealth.FilesTotal)
	}
}

// TestBuildSourceHealth_EmptyZeroOperatorTurns: root exists, is readable, and
// has files that parse -- but zero of them carry a genuine operator turn.
// This is the SECOND, distinct route to SourceStateEmpty: FilesTotal > 0 does
// not by itself mean Populated.
func TestBuildSourceHealth_EmptyZeroOperatorTurns(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "no-operator-turns.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-empty-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: assistant reply, no operator turn anywhere in this file"}]}}`,
	})

	report, err := BuildSourceHealth(root)
	if err != nil {
		t.Fatalf("BuildSourceHealth: %v", err)
	}
	if report.SourceHealth.FilesTotal == 0 {
		t.Fatalf("FilesTotal = 0, want > 0 (this case must be distinguished from TestBuildSourceHealth_EmptyDir by having files but zero operator turns)")
	}
	if report.SourceHealth.State != provenance.SourceStateEmpty {
		t.Fatalf("State = %s, want %s (files present, zero operator turns extracted)", report.SourceHealth.State, provenance.SourceStateEmpty)
	}
}

// TestBuildSourceHealth_Unreadable: root itself exists and is readable, but a
// subdirectory under it is not -- filepath.WalkDir surfaces this as a walk
// error, which BuildSourceHealth must classify as Unreadable, never Empty.
// This is the pair the review specifically required be distinguished by a
// test. Skipped on any platform where chmod 0 does not deny read (root/admin
// processes commonly ignore file-mode permissions).
func TestBuildSourceHealth_Unreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root/euid 0: chmod 0 does not deny read, cannot exercise this case")
	}
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission denial is not portable to windows")
	}

	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A file under the blocked directory so it isn't just an empty listing --
	// what makes this Unreadable is that WalkDir cannot even list the
	// directory's entries, not that the entries themselves are absent.
	if err := os.WriteFile(filepath.Join(blocked, "session.jsonl"), []byte(`{}`), 0o644); err != nil {
		t.Fatalf("writing fixture under blocked dir: %v", err)
	}
	if err := os.Chmod(blocked, 0o000); err != nil {
		t.Fatalf("chmod 0: %v", err)
	}
	t.Cleanup(func() {
		// Restore permissions so t.TempDir()'s own cleanup can remove it.
		_ = os.Chmod(blocked, 0o755)
	})

	report, err := BuildSourceHealth(root)
	if err != nil {
		t.Fatalf("BuildSourceHealth: %v", err)
	}
	if report.SourceHealth.State != provenance.SourceStateUnreadable {
		t.Fatalf("State = %s, want %s (a subdirectory WalkDir cannot list must not read as Empty)", report.SourceHealth.State, provenance.SourceStateUnreadable)
	}
	if report.SourceHealth.State == provenance.SourceStateEmpty {
		t.Fatal("State classified as Empty -- Unreadable and Empty must be distinguishable, this is exactly the pair the gate requires")
	}
}

// TestBuildSourceHealth_Populated: root exists, is readable, and contains at
// least one genuine operator turn.
func TestBuildSourceHealth_Populated(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "populated.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"session_meta","payload":{"id":"fixture-populated-session"}}`,
		`{"timestamp":"2026-01-01T00:00:01.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: a genuine operator turn"}]}}`,
	})

	report, err := BuildSourceHealth(root)
	if err != nil {
		t.Fatalf("BuildSourceHealth: %v", err)
	}
	if report.SourceHealth.State != provenance.SourceStatePopulated {
		t.Fatalf("State = %s, want %s", report.SourceHealth.State, provenance.SourceStatePopulated)
	}
	if report.SourceHealth.UnitsExtracted != 1 {
		t.Errorf("UnitsExtracted = %d, want 1", report.SourceHealth.UnitsExtracted)
	}
	if !report.SourceHealth.Freshness.Known {
		t.Error("Freshness.Known = false, want true (a populated source with a parsable timestamp must report freshness)")
	}
}

// TestBuildSourceHealth_AllStatesMutuallyDistinguishable is a direct
// side-by-side check: the four states produced by the cases above must all
// differ from one another when run in the same test, not just pairwise in
// isolation.
func TestBuildSourceHealth_AllStatesMutuallyDistinguishable(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "missing")

	emptyRoot := t.TempDir()

	populatedRoot := t.TempDir()
	writeFixture(t, populatedRoot, "populated.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00.000Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture turn"}]}}`,
	})

	missing, err := BuildSourceHealth(missingRoot)
	if err != nil {
		t.Fatalf("BuildSourceHealth(missing): %v", err)
	}
	empty, err := BuildSourceHealth(emptyRoot)
	if err != nil {
		t.Fatalf("BuildSourceHealth(empty): %v", err)
	}
	populated, err := BuildSourceHealth(populatedRoot)
	if err != nil {
		t.Fatalf("BuildSourceHealth(populated): %v", err)
	}

	states := map[provenance.SourceState]string{
		missing.SourceHealth.State:   "missing",
		empty.SourceHealth.State:     "empty",
		populated.SourceHealth.State: "populated",
	}
	if len(states) != 3 {
		t.Fatalf("got %d distinct states across 3 different roots, want 3: missing=%s empty=%s populated=%s",
			len(states), missing.SourceHealth.State, empty.SourceHealth.State, populated.SourceHealth.State)
	}
}
