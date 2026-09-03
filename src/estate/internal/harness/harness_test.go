package harness

import (
	"context"
	"os"
	"strings"
	"testing"
)

func args(t *testing.T, name, dir, prompt string) (*Turn, []string) {
	t.Helper()
	h, err := Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := h.Start(context.Background(), dir, prompt)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(turn.Cleanup)
	return turn, turn.Cmd.Args
}

// An unknown harness must refuse, never quietly fall back. Defaulting to
// claude would run a turn on a harness the caller did not choose.
func TestUnknownHarnessRefusesAndNamesWhatExists(t *testing.T) {
	_, err := Lookup("gpt5-imaginary")
	if err == nil {
		t.Fatal("an unknown harness must be refused, not defaulted")
	}
	for _, want := range Names() {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name the available harness %q; got: %v", want, err)
		}
	}
}

func TestEveryRegisteredHarnessRunsInTheDirectoryItIsGiven(t *testing.T) {
	dir := t.TempDir()
	for _, name := range Names() {
		t.Run(name, func(t *testing.T) {
			turn, _ := args(t, name, dir, "hello")
			if turn.Cmd.Dir != dir {
				t.Errorf("Cmd.Dir = %q, want %q -- a turn that ignores its worktree is unisolated", turn.Cmd.Dir, dir)
			}
			if turn.Cmd.Stdin == nil {
				t.Error("the prompt must reach the process; Stdin was nil")
			}
			if turn.Result == nil || turn.Cleanup == nil {
				t.Error("a Turn must always carry Result and Cleanup, even when they are no-ops")
			}
		})
	}
}

// The flags that make each harness non-interactive are the whole point of the
// adapter. If one is dropped, the turn hangs waiting for a human that is not
// there -- on a three-minute loop.
func TestClaudeRunsNonInteractivelyWithItsOwnFlags(t *testing.T) {
	_, a := args(t, "claude", t.TempDir(), "p")
	joined := strings.Join(a, " ")
	for _, want := range []string{"claude", "-p", "--output-format json", "--dangerously-skip-permissions"} {
		if !strings.Contains(joined, want) {
			t.Errorf("claude args missing %q; got: %s", want, joined)
		}
	}
}

func TestCodexRunsNonInteractivelyInsideItsOwnSandbox(t *testing.T) {
	_, a := args(t, "codex", t.TempDir(), "p")
	joined := strings.Join(a, " ")
	for _, want := range []string{"codex exec", "--sandbox workspace-write", "--output-last-message", "--skip-git-repo-check"} {
		if !strings.Contains(joined, want) {
			t.Errorf("codex args missing %q; got: %s", want, joined)
		}
	}
}

// Sandboxing genuinely differs between harnesses, and the seam must report
// that difference rather than flatten it. If this ever reads the same for
// both, someone has made a guarantee one of them does not keep.
func TestSandboxingIsReportedHonestlyPerHarness(t *testing.T) {
	c, _ := Lookup("claude")
	x, _ := Lookup("codex")
	if c.Sandboxed() {
		t.Error("claude -p --dangerously-skip-permissions is not sandboxed; saying otherwise is a false guarantee")
	}
	if !x.Sandboxed() {
		t.Error("codex exec --sandbox workspace-write is sandboxed; saying otherwise understates it")
	}
}

// Exit 0 with unreadable output is NOT a clean completion. Every adapter must
// error rather than hand back an empty string that reads as a successful
// empty turn.
func TestUnreadableOutputIsAnErrorNotAnEmptyResult(t *testing.T) {
	t.Run("claude: not JSON", func(t *testing.T) {
		if _, err := claudeResult([]byte("this is not json")); err == nil {
			t.Fatal("unparseable output must be an error")
		}
	})
	t.Run("claude: JSON without a result field", func(t *testing.T) {
		if _, err := claudeResult([]byte(`{"ok":true}`)); err == nil {
			t.Fatal("a JSON envelope with no result field must be an error")
		}
	})
	t.Run("claude: good envelope", func(t *testing.T) {
		got, err := claudeResult([]byte(`{"result":"done"}`))
		if err != nil || got != "done" {
			t.Fatalf("got %q, %v; want \"done\", nil", got, err)
		}
	})
	t.Run("codex: missing result file", func(t *testing.T) {
		turn, _ := args(t, "codex", t.TempDir(), "p")
		turn.Cleanup() // delete the file the adapter made
		if _, err := turn.Result(nil); err == nil {
			t.Fatal("a missing result file must be an error, not an empty result")
		}
	})
	t.Run("codex: empty result file", func(t *testing.T) {
		turn, a := args(t, "codex", t.TempDir(), "p")
		path := ""
		for i, v := range a {
			if v == "--output-last-message" {
				path = a[i+1]
			}
		}
		if path == "" {
			t.Fatal("could not find the result path in the args")
		}
		if err := os.WriteFile(path, []byte("   \n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Result(nil); err == nil {
			t.Fatal("an empty result file must be an error, not an empty result")
		}
	})
	t.Run("codex: good result file", func(t *testing.T) {
		turn, a := args(t, "codex", t.TempDir(), "p")
		var path string
		for i, v := range a {
			if v == "--output-last-message" {
				path = a[i+1]
			}
		}
		if err := os.WriteFile(path, []byte("  done  \n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := turn.Result(nil)
		if err != nil || got != "done" {
			t.Fatalf("got %q, %v; want \"done\", nil", got, err)
		}
	})
}

// A registered harness whose binary is not installed is a real state, and
// must be distinguishable from a harness that does not exist at all.
func TestAvailableDistinguishesNotInstalledFromNotAHarness(t *testing.T) {
	if _, err := Available("gpt5-imaginary"); err == nil {
		t.Error("a harness that does not exist must be an error, not merely unavailable")
	}
	if _, err := Available("claude"); err != nil {
		t.Errorf("a registered harness must never error from Available: %v", err)
	}
}

func TestCleanupRemovesWhatStartAllocated(t *testing.T) {
	turn, a := args(t, "codex", t.TempDir(), "p")
	var path string
	for i, v := range a {
		if v == "--output-last-message" {
			path = a[i+1]
		}
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Start should have created %s: %v", path, err)
	}
	turn.Cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Cleanup left %s behind", path)
	}
}
