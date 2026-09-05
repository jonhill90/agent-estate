package harness

import (
	"context"
	"os"
	"slices"
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
			if turn.Spend == nil {
				t.Error("a Turn must always carry Spend, even when the harness reports nothing usable")
			}
			if turn.SessionID == nil {
				t.Error("a Turn must always carry SessionID, even when the harness reports nothing usable")
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
	for _, want := range []string{"codex exec", "--json", "--sandbox workspace-write", "--output-last-message", "--skip-git-repo-check"} {
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
		if _, err := ClaudeResult([]byte("this is not json")); err == nil {
			t.Fatal("unparseable output must be an error")
		}
	})
	t.Run("claude: JSON without a result field", func(t *testing.T) {
		if _, err := ClaudeResult([]byte(`{"ok":true}`)); err == nil {
			t.Fatal("a JSON envelope with no result field must be an error")
		}
	})
	t.Run("claude: good envelope", func(t *testing.T) {
		got, err := ClaudeResult([]byte(`{"result":"done"}`))
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

// realClaudeEnvelope is claude -p --output-format json's real stdout,
// captured live 2026-09-03 against a real turn ("reply pong") -- see
// docs/spend-observation.md for the full payload and how it was taken.
// Trimmed to the fields claudeSpend actually reads plus enough surrounding
// shape to prove trailing fields don't break parsing.
const realClaudeEnvelope = `{"is_error":false,"duration_api_ms":2220,"num_turns":1,
"session_id":"d01a8703-42b9-4006-a3d0-83062d7d4339","total_cost_usd":0.1883826,
"usage":{"input_tokens":2,"cache_creation_input_tokens":30393,
"cache_read_input_tokens":17892,"output_tokens":4},"result":"pong"}`

func TestClaudeSpendReadsTheRealEnvelope(t *testing.T) {
	s, err := ClaudeSpend([]byte(realClaudeEnvelope))
	if err != nil {
		t.Fatal(err)
	}
	if s.CostUSD == nil {
		t.Fatal("CostUSD must be populated -- claude's own envelope carries total_cost_usd")
	}
	if *s.CostUSD != 0.1883826 {
		t.Errorf("CostUSD = %v, want 0.1883826", *s.CostUSD)
	}
	for name, got := range map[string]*int64{
		"InputTokens": s.InputTokens, "OutputTokens": s.OutputTokens,
		"CacheReadTokens": s.CacheReadTokens, "CacheCreationTokens": s.CacheCreationTokens,
	} {
		if got == nil {
			t.Errorf("%s must be populated from the real envelope's usage block", name)
		}
	}
	if *s.InputTokens != 2 || *s.OutputTokens != 4 || *s.CacheReadTokens != 17892 || *s.CacheCreationTokens != 30393 {
		t.Errorf("token fields = %+v, want the real captured values", s)
	}
}

func TestClaudeSpendOnUnparseableOutputIsAnError(t *testing.T) {
	if _, err := ClaudeSpend([]byte("not json")); err == nil {
		t.Fatal("unparseable stdout must be an error, not a zero Spend")
	}
}

// realClaudeEnvelope has no modelUsage block at all -- an envelope this
// shape must leave ByModel nil, not an empty-but-non-nil map (ModelSpend's
// own doc comment says an empty map is not a state this package produces).
func TestClaudeSpendWithNoModelUsageLeavesByModelNil(t *testing.T) {
	s, err := ClaudeSpend([]byte(realClaudeEnvelope))
	if err != nil {
		t.Fatal(err)
	}
	if s.ByModel != nil {
		t.Errorf("ByModel = %+v, want nil -- this envelope carries no modelUsage block", s.ByModel)
	}
}

// realClaudeEnvelopeWithModelUsage is the two-model payload from
// docs/spend-observation.md: Claude Code dispatched a haiku sub-agent
// inside a sonnet turn, and modelUsage carries each model's own share.
const realClaudeEnvelopeWithModelUsage = `{"is_error":false,"total_cost_usd":0.1883826,
"usage":{"input_tokens":2,"cache_creation_input_tokens":30393,
"cache_read_input_tokens":17892,"output_tokens":4},
"modelUsage":{"claude-haiku-4-5-20251001":{"inputTokens":1,"outputTokens":1,
"cacheReadInputTokens":0,"cacheCreationInputTokens":0,"costUSD":0.000591},
"claude-sonnet-5":{"inputTokens":1,"outputTokens":3,
"cacheReadInputTokens":17892,"cacheCreationInputTokens":30393,"costUSD":0.1877916}},
"result":"pong"}`

func TestClaudeSpendParsesModelUsageIntoByModel(t *testing.T) {
	s, err := ClaudeSpend([]byte(realClaudeEnvelopeWithModelUsage))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ByModel) != 2 {
		t.Fatalf("ByModel = %+v, want 2 entries", s.ByModel)
	}
	haiku, ok := s.ByModel["claude-haiku-4-5-20251001"]
	if !ok || haiku.CostUSD == nil || *haiku.CostUSD != 0.000591 {
		t.Errorf("haiku entry = %+v, want costUSD 0.000591", haiku)
	}
	sonnet, ok := s.ByModel["claude-sonnet-5"]
	if !ok || sonnet.CostUSD == nil || *sonnet.CostUSD != 0.1877916 {
		t.Errorf("sonnet entry = %+v, want costUSD 0.1877916", sonnet)
	}
	// The reconciliation check docs/spend-observation.md's #981 section
	// states: the two model costs sum exactly to the turn's total_cost_usd.
	sum := *haiku.CostUSD + *sonnet.CostUSD
	if s.CostUSD == nil || sum != *s.CostUSD {
		t.Errorf("haiku + sonnet costUSD = %v, want it to equal total_cost_usd %v", sum, s.CostUSD)
	}
}

// realCodexTurnCompleted is codex exec --json's real "turn.completed" line,
// captured live 2026-09-03 -- see docs/spend-observation.md.
const realCodexJSONL = `{"type":"thread.started","thread_id":"01a06826-ac92-7b62-8418-247dee57b779"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"pong"}}
{"type":"turn.completed","usage":{"input_tokens":27131,"cached_input_tokens":11008,"cache_write_input_tokens":0,"output_tokens":5,"reasoning_output_tokens":0}}`

func TestCodexSpendReadsTheRealTurnCompletedEventAndNeverInventsACost(t *testing.T) {
	s, err := codexSpend([]byte(realCodexJSONL))
	if err != nil {
		t.Fatal(err)
	}
	if s.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil -- codex reports no dollar figure, and this package must not estimate one", *s.CostUSD)
	}
	if s.InputTokens == nil || *s.InputTokens != 27131 {
		t.Errorf("InputTokens = %v, want 27131", s.InputTokens)
	}
	if s.OutputTokens == nil || *s.OutputTokens != 5 {
		t.Errorf("OutputTokens = %v, want 5", s.OutputTokens)
	}
	if s.CacheReadTokens == nil || *s.CacheReadTokens != 11008 {
		t.Errorf("CacheReadTokens = %v, want 11008", s.CacheReadTokens)
	}
}

func TestCodexSpendWithNoTurnCompletedEventIsAnError(t *testing.T) {
	if _, err := codexSpend([]byte(`{"type":"thread.started","thread_id":"x"}`)); err == nil {
		t.Fatal("a stream with no turn.completed usage must be an error, not a zero Spend")
	}
}

// TestClaudeSessionIDReadsTheRealEnvelope proves the handle agent-estate#990
// exists to record can actually be read out of a real captured claude -p
// envelope -- the same session_id docs/spend-observation.md pasted next to
// its own total_cost_usd.
func TestClaudeSessionIDReadsTheRealEnvelope(t *testing.T) {
	got, err := ClaudeSessionID([]byte(realClaudeEnvelope))
	if err != nil {
		t.Fatal(err)
	}
	if got != "d01a8703-42b9-4006-a3d0-83062d7d4339" {
		t.Errorf("ClaudeSessionID = %q, want the real captured session_id", got)
	}
}

func TestClaudeSessionIDMissingIsAnErrorNotEmptyString(t *testing.T) {
	t.Run("no session_id field", func(t *testing.T) {
		if _, err := ClaudeSessionID([]byte(`{"result":"pong"}`)); err == nil {
			t.Fatal("an envelope with no session_id must be an error, not an empty string")
		}
	})
	t.Run("not JSON", func(t *testing.T) {
		if _, err := ClaudeSessionID([]byte("not json")); err == nil {
			t.Fatal("unparseable stdout must be an error")
		}
	})
	t.Run("real mid-response cut, captured live", func(t *testing.T) {
		// realTruncatedClaudeEnvelope is a REAL claude -p --output-format json
		// stdout capture, cut at 60 bytes (`| head -c 60`) 2026-09-03 -- this is
		// the exact shape of the "API Error: Connection closed mid-response"
		// failure this issue exists because of (see the issue body): the
		// process exits with an incomplete JSON object, session_id never
		// reached. main.go's dispatch loop calls this unconditionally on
		// whatever stdout a dead turn produced, so this must error, not
		// silently return "".
		const realTruncatedClaudeEnvelope = `{"is_error":false,"duration_api_ms":6325,"num_turns":1,"stop`
		if _, err := ClaudeSessionID([]byte(realTruncatedClaudeEnvelope)); err == nil {
			t.Fatal("a real mid-response truncation must be an error, not an empty string -- this is the exact failure agent-estate#990 exists to make evaluable")
		}
	})
}

// TestCodexSessionIDReadsTheRealStream proves codex's own equivalent handle
// (thread_id, from thread.started -- codex has no session_id at all) can be
// read from the real captured --json stream.
func TestCodexSessionIDReadsTheRealStream(t *testing.T) {
	got, err := codexSessionID([]byte(realCodexJSONL))
	if err != nil {
		t.Fatal(err)
	}
	if got != "01a06826-ac92-7b62-8418-247dee57b779" {
		t.Errorf("codexSessionID = %q, want the real captured thread_id", got)
	}
}

func TestCodexSessionIDWithNoThreadStartedEventIsAnError(t *testing.T) {
	if _, err := codexSessionID([]byte(`{"type":"turn.completed","usage":{"input_tokens":1}}`)); err == nil {
		t.Fatal("a stream with no thread.started event must be an error, not an empty string")
	}
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

// The flag whose ABSENCE cost $108. Tested in both directions: the default
// must be sonnet, and an override must be honoured -- a test that only
// checked "some model is passed" would have passed against the broken code
// too, since the bug was the flag not existing at all.
func TestClaudeDispatchAlwaysPinsAModel(t *testing.T) {
	t.Setenv("ESTATE_WORKER_MODEL", "")
	got := claudeArgs()
	if !slices.Contains(got, "--model") {
		t.Fatalf("dispatch passes no --model; a worker inherits the dispatching session's model (opus). args=%v", got)
	}
	i := slices.Index(got, "--model")
	if i+1 >= len(got) || got[i+1] != "claude-sonnet-5" {
		t.Fatalf("default worker model must be sonnet per worker_model=sonnet; got %v", got)
	}
	t.Setenv("ESTATE_WORKER_MODEL", "claude-haiku-4-5")
	got = claudeArgs()
	i = slices.Index(got, "--model")
	if got[i+1] != "claude-haiku-4-5" {
		t.Fatalf("ESTATE_WORKER_MODEL must override the default; got %v", got)
	}
}
