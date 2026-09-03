package main

import (
	"os"
	"path/filepath"
	"strings"
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
	got, err := resolveTickLogPath(filepath.FromSlash("/repo/root"), "docs/tick-log.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := filepath.Join(filepath.FromSlash("/repo/root"), "docs", "tick-log.jsonl")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveTickLogPath_AbsoluteFlagWinsOverRepoRoot(t *testing.T) {
	abs := filepath.FromSlash("/somewhere/else/tick-log.jsonl")
	got, err := resolveTickLogPath(filepath.FromSlash("/repo/root"), abs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != abs {
		t.Errorf("got %q, want %q (explicit -estate-tick-log must be honoured as given)", got, abs)
	}
}

// TestResolveTickLogPath_NonAbsoluteRepoRootReturnsError reproduces the
// -trimpath case: runtime.Caller(0) (and so repoRootFromSource) can return a
// module-relative path instead of an absolute one. resolveTickLogPath must
// refuse to guess -- it must not silently join a relative repoRoot with the
// relative tickLogFlag, which would produce a relative result that os.Open
// then resolves against the process's cwd, reintroducing this file's own
// defect one level down. It reports that refusal as a non-nil error, not as
// a path built to fail later.
func TestResolveTickLogPath_NonAbsoluteRepoRootReturnsError(t *testing.T) {
	relativeRepoRoot := filepath.FromSlash("github.com/jonhill90/agent-estate")
	got, err := resolveTickLogPath(relativeRepoRoot, "docs/tick-log.jsonl")

	if err == nil {
		t.Fatalf("resolveTickLogPath(%q, ...) = (%q, nil), want a non-nil error -- a non-absolute repoRoot must be refused, not resolved into a path", relativeRepoRoot, got)
	}
	if got != "" {
		t.Errorf("resolveTickLogPath(%q, ...) path = %q, want empty when err is non-nil", relativeRepoRoot, got)
	}
}

// TestResolveTickLogPath_NonAbsoluteRepoRootDegradesToUnreadableNotAbsent is
// agent-estate#935's own regression test: when resolveTickLogPath's error is
// carried through estatus.ReadWithTickErr, the result must be Unreadable
// ("the instrument couldn't look"), never Absent ("the Director isn't
// running") -- the two render different sentences to the operator, and only
// one of them is true here.
//
// MUTATION CHECK: before this PR, resolveTickLogPath returned a single
// deliberately nonexistent path instead of (path, error), and main.go fed
// it to plain estatus.Read. Reproducing that here -- replacing the
// estatus.ReadWithTickErr call below with
// estatus.Read(filepath.Join(elsewhere, "no-ledger.jsonl"),
// filepath.Join(string(filepath.Separator), "estate-repo-root-not-absolute",
// "docs/tick-log.jsonl")) -- makes this test fail with Ticks == Absent, not
// Unreadable. Verified by hand before this test was kept in the suite; see
// the PR body for the captured failure output.
func TestResolveTickLogPath_NonAbsoluteRepoRootDegradesToUnreadableNotAbsent(t *testing.T) {
	relativeRepoRoot := filepath.FromSlash("github.com/jonhill90/agent-estate")
	got, err := resolveTickLogPath(relativeRepoRoot, "docs/tick-log.jsonl")
	if err == nil {
		t.Fatalf("resolveTickLogPath(%q, ...) returned a nil error; the rest of this test requires the trimpath-degradation case", relativeRepoRoot)
	}

	elsewhere := t.TempDir()
	status := estatus.ReadWithTickErr(filepath.Join(elsewhere, "no-ledger.jsonl"), got, err)

	if status.Ticks != estatus.Unreadable {
		t.Fatalf("Ticks = %s, want %s -- a non-absolute repoRoot is a failure of THIS INSTRUMENT (it could not even work out where to look), not evidence the Director isn't running", status.Ticks, estatus.Unreadable)
	}
	if status.TickErr == nil || status.TickErr.Error() != err.Error() {
		t.Fatalf("TickErr = %v, want the resolveTickLogPath error %v carried through unchanged", status.TickErr, err)
	}
}

// TestLines_NonAbsoluteRepoRootRendersUnreadableNotNotRunning pins the
// user-visible sentence for the trimpath-degradation path end to end
// (resolveTickLogPath -> estatus.ReadWithTickErr -> estatus.Lines), the
// same discipline estatus.TestLinesDirectorStatesAreDistinct uses for the
// other three Director states. It must never render "not running".
func TestLines_NonAbsoluteRepoRootRendersUnreadableNotNotRunning(t *testing.T) {
	relativeRepoRoot := filepath.FromSlash("github.com/jonhill90/agent-estate")
	got, err := resolveTickLogPath(relativeRepoRoot, "docs/tick-log.jsonl")
	if err == nil {
		t.Fatalf("resolveTickLogPath(%q, ...) returned a nil error; the rest of this test requires the trimpath-degradation case", relativeRepoRoot)
	}

	elsewhere := t.TempDir()
	status := estatus.ReadWithTickErr(filepath.Join(elsewhere, "no-ledger.jsonl"), got, err)
	out := strings.Join(estatus.Lines(status), "\n")

	if !strings.Contains(out, "Director: tick log UNREADABLE") {
		t.Fatalf("rendered output must state the tick log is UNREADABLE; got:\n%s", out)
	}
	if strings.Contains(out, "not running") {
		t.Fatalf("rendered output must never claim the Director is not running when the failure is this instrument's own path resolution; got:\n%s", out)
	}
}

// TestEndToEnd_TrimpathNeverRendersNotRunning exercises main.go's exact
// wiring (repoRootFromSource(tickLogSourceFile) -> resolveTickLogPath ->
// estatus.ReadWithTickErr -> estatus.Lines) using the REAL tickLogSourceFile
// this binary was built with, rather than a hand-supplied relative string
// like the tests above. Under a normal build tickLogSourceFile is absolute
// and this is a no-op assertion; run with `go test -trimpath`, it
// reproduces agent-estate#935 for real: runtime.Caller(0) then yields a
// module-relative path, exactly as `go build -trimpath` would for the
// shipped binary. Verified by hand:
//
//	go test -trimpath -run TestEndToEnd_TrimpathNeverRendersNotRunning -v ./cmd/estate
//
// logs the resolved (non-absolute) root and confirms the rendered output
// says UNREADABLE and never "not running".
func TestEndToEnd_TrimpathNeverRendersNotRunning(t *testing.T) {
	root := repoRootFromSource(tickLogSourceFile)
	ticks, err := resolveTickLogPath(root, "docs/tick-log.jsonl")
	status := estatus.ReadWithTickErr(filepath.Join(t.TempDir(), "no-ledger.jsonl"), ticks, err)
	out := strings.Join(estatus.Lines(status), "\n")
	t.Logf("repoRoot=%q (absolute=%v) resolveErr=%v\n%s", root, filepath.IsAbs(root), err, out)

	if err == nil {
		return // normal build: repoRoot was absolute, nothing to prove here
	}
	if status.Ticks != estatus.Unreadable {
		t.Fatalf("Ticks = %s, want %s under a non-absolute repoRoot", status.Ticks, estatus.Unreadable)
	}
	if !strings.Contains(out, "Director: tick log UNREADABLE") {
		t.Fatalf("rendered output must state UNREADABLE; got:\n%s", out)
	}
	if strings.Contains(out, "not running") {
		t.Fatalf("rendered output must never claim the Director is not running; got:\n%s", out)
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
	resolved, err := resolveTickLogPath(root, "docs/tick-log.jsonl")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := estatus.Read(filepath.Join(elsewhere, "no-ledger.jsonl"), resolved)
	if status.Ticks != estatus.Present {
		t.Fatalf("Ticks = %s, want %s -- process cwd was %q (outside the repo), repo root %q, resolved tick path %q",
			status.Ticks, estatus.Present, elsewhere, repoRoot, resolved)
	}
	if status.LastTick == nil || status.LastTick.SrcHead != "deadbeef01" {
		t.Fatalf("LastTick = %+v, want a tick with SrcHead=deadbeef01", status.LastTick)
	}
}
