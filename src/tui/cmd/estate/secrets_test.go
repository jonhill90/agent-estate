package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveSecretsSchemaPrefersTheFlag: an explicit -secrets-schema wins
// over the repo-relative default, including when it does not exist -- the
// pane then reports the path the user actually gave rather than silently
// reading a different file, the same rule resolveOpenAPISpec follows.
func TestResolveSecretsSchemaPrefersTheFlag(t *testing.T) {
	got := resolveSecretsSchema("/explicit/secrets-schema.yaml", "/some/repo")
	if got != "/explicit/secrets-schema.yaml" {
		t.Errorf("resolveSecretsSchema = %q, want the flag value", got)
	}
}

func TestResolveSecretsSchemaFallsBackToTheRepoLayout(t *testing.T) {
	repo := t.TempDir()
	full := filepath.Join(repo, secretsSchemaRelPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("runtime_secrets: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := resolveSecretsSchema("", repo); got != full {
		t.Errorf("resolveSecretsSchema = %q, want %q", got, full)
	}
}

// TestResolveSecretsSchemaEmptyWithNoRepo is the state a machine with no
// hill90-app checkout is in, and it must stay distinguishable from a wrong
// path: empty means "nothing configured", which the pane turns into a
// notice naming what to set.
func TestResolveSecretsSchemaEmptyWithNoRepo(t *testing.T) {
	if got := resolveSecretsSchema("", ""); got != "" {
		t.Errorf("resolveSecretsSchema = %q, want empty", got)
	}
}

// TestResolveSecretsSchemaKeepsAWrongRepoPath: $HILL90_APP_REPO set but the
// file missing is a different mistake from never setting it, and the pane
// can only say which if this returns the path it tried.
func TestResolveSecretsSchemaKeepsAWrongRepoPath(t *testing.T) {
	repo := t.TempDir()
	want := filepath.Join(repo, secretsSchemaRelPath)
	if got := resolveSecretsSchema("", repo); got != want {
		t.Errorf("resolveSecretsSchema = %q, want %q so the pane can name it", got, want)
	}
}

func TestBuildSecretsFetchIsNilWithNoPath(t *testing.T) {
	if buildSecretsFetch("") != nil {
		t.Error("buildSecretsFetch(\"\") returned a fetcher -- unconfigured must stay unconfigured")
	}
	if buildSecretsFetch("/some/secrets-schema.yaml") == nil {
		t.Error("buildSecretsFetch with a path returned nil")
	}
}
