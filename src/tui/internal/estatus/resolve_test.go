package estatus

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveLeavesAbsolutePathsAlone(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "x.jsonl")
	if got := Resolve(abs); got != abs {
		t.Errorf("Resolve(%q) = %q, want it untouched", abs, got)
	}
	if got := Resolve(""); got != "" {
		t.Errorf("Resolve(\"\") = %q, want \"\"", got)
	}
}

// A relative path that exists where we are must keep meaning that -- an
// explicit flag from a shell means what the shell means.
func TestResolvePrefersTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "here.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := Resolve("here.jsonl"); got != "here.jsonl" {
		t.Errorf("a relative path that exists here must be left alone, got %q", got)
	}
}

// Nothing is invented: a path that exists nowhere comes back unchanged, so
// the reader reports honestly instead of being handed a guess.
func TestResolveInventsNothing(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	const missing = "definitely/not/here.jsonl"
	if got := Resolve(missing); got != missing {
		t.Errorf("Resolve must not invent a path; got %q", got)
	}
}

// A council seat built the binary outside any repository, ran it inside an
// UNRELATED git repo that happened to contain docs/tick-log.jsonl, and got
// that repo's fabricated content presented as the Director's record.
//
// That is worse than the bug this file was written to fix. Reporting absence
// when blind is bad; reporting someone else's data as yours is a fabrication
// with a timestamp on it.
//
// So the working-directory fallback is gone. If the binary is not inside a
// repository, there is no "the repo" to resolve against, and guessing which
// one the user meant is exactly the invention the doc comment forbids.
func TestResolveDoesNotAdoptAnUnrelatedRepository(t *testing.T) {
	// A repo that is not ours, containing a file at the same relative path.
	other := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = other
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(other, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	planted := filepath.Join(other, "docs", "tick-log.jsonl")
	if err := os.WriteFile(planted, []byte(`{"at":"2020-01-01T00:00:00Z","phase_item":"fake"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Stand in a SUBDIRECTORY, so the working-directory lookup misses and
	// the repository fallback is the code actually under test. Standing at
	// the root would short-circuit on os.Stat and prove nothing -- which is
	// exactly what the first version of this test did.
	sub := filepath.Join(other, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}

	got := ResolveFrom("docs/tick-log.jsonl", "/definitely/not/a/repo")
	if got == planted {
		t.Fatal("resolved to an unrelated repository's file -- that is a fabrication, not a fallback")
	}
	if got != "docs/tick-log.jsonl" {
		t.Errorf("with no repo of our own, the original must come back unchanged; got %q", got)
	}
}
