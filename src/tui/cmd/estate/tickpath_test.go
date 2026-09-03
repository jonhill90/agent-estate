package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
)

func TestRepoRootFromSource(t *testing.T) {
	got := repoRootFromSource(filepath.FromSlash("/x/agent-estate/src/tui/cmd/estate/main.go"))
	want := filepath.FromSlash("/x/agent-estate")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveTickLogPath_RelativeJoinsRepoRoot(t *testing.T) {
	got := resolveTickLogPath(filepath.FromSlash("/repo/root"), "docs/tick-log.jsonl")
	want := filepath.Join(filepath.FromSlash("/repo/root"), "docs", "tick-log.jsonl")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveTickLogPath_AbsoluteFlagWinsOverRepoRoot(t *testing.T) {
	abs := filepath.FromSlash("/somewhere/else/tick-log.jsonl")
	got := resolveTickLogPath(filepath.FromSlash("/repo/root"), abs)
	if got != abs {
		t.Errorf("got %q, want %q (explicit -estate-tick-log must be honoured as given)", got, abs)
	}
}

// TestTickLogSourceFileIsThisBinarysOwnPath is the load-bearing assumption
// behind resolveTickLogPath: tickLogSourceFile must actually be this
// repository's src/tui/cmd/estate/tickpath.go, not some GOPATH cache copy
// or a path stripped by a trimpath build. It is captured with
// runtime.Caller(0) in tickpath.go; this just confirms the file it names
// exists and repoRootFromSource lands on a directory that looks like this
// repo (its own src/tui/cmd/estate/main.go must be there).
func TestTickLogSourceFileIsThisBinarysOwnPath(t *testing.T) {
	if _, err := os.Stat(tickLogSourceFile); err != nil {
		t.Fatalf("tickLogSourceFile = %q does not exist: %v", tickLogSourceFile, err)
	}
	root := repoRootFromSource(tickLogSourceFile)
	mainGo := filepath.Join(root, "src", "tui", "cmd", "estate", "main.go")
	if _, err := os.Stat(mainGo); err != nil {
		t.Fatalf("repoRootFromSource(%q) = %q, but %q does not exist: %v", tickLogSourceFile, root, mainGo, err)
	}
}

// TestTickLogPath_ResolvedFromSourceNotCWD reproduces the operator-hit
// defect directly: the TUI reported the Director as not running when
// launched from any directory other than the repository root, because the
// tick log's relative default path was resolved against the process's
// working directory instead of the repo root.
//
// This test builds a synthetic repo layout in a tmp dir (its own
// src/tui/cmd/estate/main.go stand-in plus its own docs/tick-log.jsonl),
// chdirs the test process to a SECOND, unrelated tmp dir -- genuinely
// standing outside the synthetic repo, the condition none of the ~120
// other test files creates because every one of them runs from its own
// package directory inside this checkout -- and confirms the tick log is
// still found.
//
// MUTATION CHECK: reverting resolveTickLogPath to `return tickLogFlag`
// (ignoring repoRoot, i.e. the pre-fix behaviour where main.go used
// *estateTickLog bare) turns this test red: status.Ticks comes back Absent
// because the relative "docs/tick-log.jsonl" then resolves against
// `elsewhere`, which has no docs/ directory at all. Verified by hand before
// this test was kept in the suite -- see the PR body for the captured
// failure output.
func TestTickLogPath_ResolvedFromSourceNotCWD(t *testing.T) {
	repoRoot := t.TempDir()
	fakeMainGo := filepath.Join(repoRoot, "src", "tui", "cmd", "estate", "main.go")
	if err := os.MkdirAll(filepath.Dir(fakeMainGo), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fakeMainGo, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tickLog := filepath.Join(repoRoot, "docs", "tick-log.jsonl")
	if err := os.MkdirAll(filepath.Dir(tickLog), 0o755); err != nil {
		t.Fatal(err)
	}
	tick := `{"at":"2026-09-03T00:00:00Z","phase_item":"repro","src_head":"deadbeef01","artifact":"pr#1"}` + "\n"
	if err := os.WriteFile(tickLog, []byte(tick), 0o644); err != nil {
		t.Fatal(err)
	}

	elsewhere := t.TempDir() // stands outside repoRoot entirely
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(elsewhere); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldwd); err != nil {
			t.Fatal(err)
		}
	})

	root := repoRootFromSource(fakeMainGo)
	if root != repoRoot {
		t.Fatalf("repoRootFromSource(%q) = %q, want %q", fakeMainGo, root, repoRoot)
	}
	resolved := resolveTickLogPath(root, "docs/tick-log.jsonl")

	status := estatus.Read(filepath.Join(elsewhere, "no-ledger.jsonl"), resolved)
	if status.Ticks != estatus.Present {
		t.Fatalf("Ticks = %s, want %s -- process cwd was %q (outside the repo), repo root %q, resolved tick path %q",
			status.Ticks, estatus.Present, elsewhere, repoRoot, resolved)
	}
	if status.LastTick == nil || status.LastTick.SrcHead != "deadbeef01" {
		t.Fatalf("LastTick = %+v, want a tick with SrcHead=deadbeef01", status.LastTick)
	}
}
