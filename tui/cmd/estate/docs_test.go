package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveOpenAPISpecPrefersTheFlag: an explicit -openapi wins over the
// repo-relative default, including when it does not exist -- the pane then
// reports the path the user actually gave rather than silently reading a
// different file.
func TestResolveOpenAPISpecPrefersTheFlag(t *testing.T) {
	got := resolveOpenAPISpec("/explicit/openapi.yaml", "/some/repo")
	if got != "/explicit/openapi.yaml" {
		t.Errorf("resolveOpenAPISpec = %q, want the flag value", got)
	}
}

func TestResolveOpenAPISpecFallsBackToTheRepoLayout(t *testing.T) {
	repo := t.TempDir()
	full := filepath.Join(repo, openAPIRelPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("openapi: 3.0.3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveOpenAPISpec("", repo); got != full {
		t.Errorf("resolveOpenAPISpec = %q, want %q", got, full)
	}
}

// TestResolveOpenAPISpecEmptyWithNoRepo is the state a machine with no
// hill90-app checkout is in, and it must stay distinguishable from a wrong
// path: empty means "nothing configured", which the pane turns into a
// notice naming what to set.
func TestResolveOpenAPISpecEmptyWithNoRepo(t *testing.T) {
	if got := resolveOpenAPISpec("", ""); got != "" {
		t.Errorf("resolveOpenAPISpec = %q, want empty", got)
	}
}

// TestResolveOpenAPISpecKeepsAWrongRepoPath: $HILL90_APP_REPO set but the
// document missing is a different mistake from never setting it, and the
// pane can only say which if this returns the path it tried.
func TestResolveOpenAPISpecKeepsAWrongRepoPath(t *testing.T) {
	repo := t.TempDir()
	want := filepath.Join(repo, openAPIRelPath)
	if got := resolveOpenAPISpec("", repo); got != want {
		t.Errorf("resolveOpenAPISpec = %q, want %q so the pane can name it", got, want)
	}
}

func TestBuildAPIDocsFetchIsNilWithNoPath(t *testing.T) {
	if buildAPIDocsFetch("") != nil {
		t.Error("buildAPIDocsFetch(\"\") returned a fetcher -- unconfigured must stay unconfigured")
	}
	if buildAPIDocsFetch("/some/openapi.yaml") == nil {
		t.Error("buildAPIDocsFetch with a path returned nil")
	}
}
