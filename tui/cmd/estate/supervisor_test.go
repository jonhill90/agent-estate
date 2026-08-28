package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDiscoverSupervisorRepo_WalksUpFromCWD reproduces agent-tui#49 item 1's
// blocking defect: `estate` run bare, from a directory unrelated to any
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
	fallback := filepath.Join(home, "source", "repos", "Personal", "agent-supervisor")
	mustMkSupervisorScript(t, fallback)

	unrelated := t.TempDir()
	got := discoverSupervisorRepo(unrelated)
	if got != fallback {
		t.Errorf("got %q, want fallback %q", got, fallback)
	}
}

// TestDiscoverSupervisorRepo_FallsBackUnderHome_RenamedRepo pins
// agent-tui#168 (merge-impact-inventory-agent-estate.md row 8): the
// jonhill90/agent-supervisor -> jonhill90/agent-estate rename
// (agent-supervisor#682, Track A) is queued but has not happened, so
// "agent-supervisor" must still resolve (the prior test) -- but a checkout
// already renamed to "agent-estate" must resolve too, with no code change
// required when the rename actually lands.
func TestDiscoverSupervisorRepo_FallsBackUnderHome_RenamedRepo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	fallback := filepath.Join(home, "source", "repos", "Personal", "agent-estate")
	mustMkSupervisorScript(t, fallback)

	unrelated := t.TempDir()
	got := discoverSupervisorRepo(unrelated)
	if got != fallback {
		t.Errorf("got %q, want fallback %q", got, fallback)
	}
}

// TestDiscoverSupervisorRepo_PrefersCurrentNameOverFuture asserts ordering:
// when both the current name and the future renamed one exist under $HOME
// (e.g. mid-migration, or a stale leftover), the current name still wins --
// fallbackSupervisorRepoNames lists "agent-supervisor" before
// "agent-estate" on purpose, since the rename has not happened and this
// change must not alter today's resolution when only the old name is real.
func TestDiscoverSupervisorRepo_PrefersCurrentNameOverFuture(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	current := filepath.Join(home, "source", "repos", "Personal", "agent-supervisor")
	future := filepath.Join(home, "source", "repos", "Personal", "agent-estate")
	mustMkSupervisorScript(t, current)
	mustMkSupervisorScript(t, future)

	unrelated := t.TempDir()
	got := discoverSupervisorRepo(unrelated)
	if got != current {
		t.Errorf("got %q, want current-name fallback %q (checked before %q)", got, current, future)
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
