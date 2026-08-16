package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverSupervisorRepo_WalksUpFromCWD reproduces agent-tui#49 item 1's
// blocking defect: `keelson` run bare, from a directory unrelated to any
// repo checkout except that a supervisor checkout happens to be an
// ancestor, must find it rather than requiring -supervisor-repo.
func TestDiscoverSupervisorRepo_WalksUpFromCWD(t *testing.T) {
	root := t.TempDir()
	mustMkSupervisorScript(t, root)
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got := discoverSupervisorRepo(deep)
	if got != root {
		t.Errorf("got %q, want %q", got, root)
	}
}

func TestDiscoverSupervisorRepo_NotFoundReturnsEmpty(t *testing.T) {
	// An isolated tree with no scripts/supervisor anywhere above it, and
	// $HOME pointed somewhere with no fallback checkout either -- the
	// "genuinely nowhere to be found" case main() must open degraded for,
	// never exit 1 over.
	t.Setenv("HOME", t.TempDir())
	unrelated := t.TempDir()

	got := discoverSupervisorRepo(unrelated)
	if got != "" {
		t.Errorf("got %q, want empty (no supervisor findable)", got)
	}
}

func TestDiscoverSupervisorRepo_FallsBackUnderHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fallback := filepath.Join(home, fallbackSupervisorRepo)
	mustMkSupervisorScript(t, fallback)

	unrelated := t.TempDir()
	got := discoverSupervisorRepo(unrelated)
	if got != fallback {
		t.Errorf("got %q, want fallback %q", got, fallback)
	}
}

func mustMkSupervisorScript(t *testing.T, repoRoot string) {
	t.Helper()
	dir := filepath.Join(repoRoot, "scripts", "supervisor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp_server.py"), []byte("# stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
