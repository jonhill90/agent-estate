package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"agent-supervisor/daemon/internal/agent"
)

// This file is the missing half found by r-b5b's own question 1: routing is
// wired through EVERY layer below main.go (dispatch.RunGated takes harness
// explicitly, ledger.EnsureLane records it, agent.Codex satisfies
// agent.Adapter -- all covered by internal/dispatch and internal/ledger's
// own tests), but newAdapter/setSessionID/dryArgv -- the one place a
// -harness STRING actually turns into a concrete adapter, a resumed
// session, or a printable argv -- had zero test files of its own
// (`go test ./cmd/...` reported "[no test files]" before this). A passing
// `go build`/`go vet` proves the switch compiles; it proves nothing about
// which branch a given -harness string actually reaches.

func TestNewAdapterClaudeDefault(t *testing.T) {
	for _, harness := range []string{"", "claude"} {
		a, err := newAdapter(harness, "", "sonnet", "/tmp/cwd", 5*time.Minute)
		if err != nil {
			t.Fatalf("newAdapter(%q): %v", harness, err)
		}
		c, ok := a.(*agent.Claude)
		if !ok {
			t.Fatalf("newAdapter(%q) = %T, want *agent.Claude", harness, a)
		}
		if c.Bin != "claude" {
			t.Errorf("newAdapter(%q).Bin = %q, want %q (empty -bin must default per harness)", harness, c.Bin, "claude")
		}
		if c.Model != "sonnet" || c.Cwd != "/tmp/cwd" || c.Timeout != 5*time.Minute {
			t.Errorf("newAdapter(%q) did not thread model/cwd/timeout through: %+v", harness, c)
		}
		// #494: strict by default is load-bearing, not incidental -- a
		// regression here silently reopens every MCP server for every
		// Claude dispatch.
		if !c.StrictMCP {
			t.Errorf("newAdapter(%q).StrictMCP = false, want true (#494)", harness)
		}
	}
}

func TestNewAdapterClaudeExplicitBin(t *testing.T) {
	a, err := newAdapter("claude", "claude-custom", "", "/tmp", time.Minute)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	c := a.(*agent.Claude)
	if c.Bin != "claude-custom" {
		t.Errorf("Bin = %q, want explicit -bin to override the default", c.Bin)
	}
}

func TestNewAdapterCodex(t *testing.T) {
	a, err := newAdapter("codex", "", "o1", "/tmp/cwd", 10*time.Minute)
	if err != nil {
		t.Fatalf("newAdapter(codex): %v", err)
	}
	c, ok := a.(*agent.Codex)
	if !ok {
		t.Fatalf("newAdapter(codex) = %T, want *agent.Codex", a)
	}
	if c.Bin != "codex" {
		t.Errorf("newAdapter(codex).Bin = %q, want %q (empty -bin must default to codex, not claude)", c.Bin, "codex")
	}
	if c.Model != "o1" || c.Cwd != "/tmp/cwd" || c.Timeout != 10*time.Minute {
		t.Errorf("newAdapter(codex) did not thread model/cwd/timeout through: %+v", c)
	}
}

func TestNewAdapterCodexExplicitBin(t *testing.T) {
	a, err := newAdapter("codex", "codex-nightly", "", "/tmp", time.Minute)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	c := a.(*agent.Codex)
	if c.Bin != "codex-nightly" {
		t.Errorf("Bin = %q, want explicit -bin to override the default", c.Bin)
	}
}

func TestNewAdapterUnknownHarnessRejected(t *testing.T) {
	_, err := newAdapter("copilot", "", "", "/tmp", time.Minute)
	if err == nil {
		t.Fatal("newAdapter(copilot): want error, got nil -- an unrecognised harness must not silently fall through to a default adapter")
	}
	if !strings.Contains(err.Error(), "copilot") {
		t.Errorf("error %q does not name the rejected harness -- a caller reading this needs to know what it typed, not just that something failed", err)
	}
}

func TestSetSessionIDClaude(t *testing.T) {
	a, err := newAdapter("claude", "", "", "/tmp", time.Minute)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	if err := setSessionID(a, "sess-123"); err != nil {
		t.Fatalf("setSessionID: %v", err)
	}
	if got := a.(*agent.Claude).SessionID; got != "sess-123" {
		t.Errorf("Claude.SessionID = %q, want %q", got, "sess-123")
	}
}

func TestSetSessionIDCodex(t *testing.T) {
	a, err := newAdapter("codex", "", "", "/tmp", time.Minute)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	if err := setSessionID(a, "thread-abc"); err != nil {
		t.Fatalf("setSessionID: %v", err)
	}
	if got := a.(*agent.Codex).SessionID; got != "thread-abc" {
		t.Errorf("Codex.SessionID = %q, want %q -- `supervisord send -harness codex` resumes nothing if this field is never set", got, "thread-abc")
	}
}

// A type this package does not know how to resume must fail loudly, not
// silently no-op and let `send` proceed as if it had resumed a session it
// never touched.
func TestSetSessionIDUnknownAdapterErrors(t *testing.T) {
	// failAdapter already exists in this package for exactly this shape
	// (an Adapter with no session to resume); reuse it rather than a
	// second fake.
	err := setSessionID(failAdapter{err: errors.New("boom")}, "sess-1")
	if err == nil {
		t.Fatal("setSessionID(failAdapter): want error, got nil")
	}
}

func TestDryArgvClaudeDelegatesToAdapter(t *testing.T) {
	a, _ := newAdapter("claude", "claude", "sonnet", "/tmp", time.Minute)
	got := dryArgv(a, "do the thing")
	want := a.(*agent.Claude).DryRunArgv("do the thing")
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("dryArgv(claude) = %v, want %v (must match DryRunArgv exactly, not a second hand-written guess)", got, want)
	}
}

func TestDryArgvCodexDelegatesToAdapter(t *testing.T) {
	a, _ := newAdapter("codex", "codex", "", "/tmp", time.Minute)
	got := dryArgv(a, "do the thing")
	want := a.(*agent.Codex).DryRunArgv("do the thing")
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("dryArgv(codex) = %v, want %v (must match DryRunArgv exactly, not a second hand-written guess)", got, want)
	}
	if !strings.Contains(strings.Join(got, " "), "codex") {
		t.Errorf("dryArgv(codex) = %v, does not even name the codex binary", got)
	}
}

func TestDryArgvUnknownAdapterDoesNotPanic(t *testing.T) {
	got := dryArgv(failAdapter{err: errors.New("boom")}, "prompt")
	if len(got) == 0 || !strings.Contains(got[0], "no DryRunArgv") {
		t.Errorf("dryArgv(unknown adapter) = %v, want the explicit placeholder rather than a panic or silent empty result", got)
	}
}
