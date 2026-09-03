package estatus

import (
	"os"
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
