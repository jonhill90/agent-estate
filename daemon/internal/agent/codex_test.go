package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// --- args(): pure function, no subprocess ---------------------------------

func TestCodexArgs_FirstTurn(t *testing.T) {
	c := &Codex{Model: "gpt-5-codex"}
	got := c.args("do the thing")
	want := []string{
		"exec", "--json", "--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
		"--model", "gpt-5-codex", "do the thing",
	}
	assertArgvEqual(t, got, want)
}

func TestCodexArgs_NoModel(t *testing.T) {
	c := &Codex{}
	got := c.args("hi")
	want := []string{
		"exec", "--json", "--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox", "hi",
	}
	assertArgvEqual(t, got, want)
}

// Resume's own usage is `codex exec resume [OPTIONS] [SESSION_ID] [PROMPT]`
// (confirmed against `codex exec resume --help`, 2026-08-22) -- SESSION_ID
// must precede PROMPT and follow "resume", not "exec".
func TestCodexArgs_Resume(t *testing.T) {
	c := &Codex{Model: "gpt-5-codex", SessionID: "01a02ac4-d701-71c1-9d82-8aae367d3b68"}
	got := c.args("go on")
	want := []string{
		"exec", "resume", "--json", "--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
		"--model", "gpt-5-codex",
		"01a02ac4-d701-71c1-9d82-8aae367d3b68", "go on",
	}
	assertArgvEqual(t, got, want)
}

func assertArgvEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv length: got %d %q, want %d %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d]: got %q, want %q (full got=%q)", i, got[i], want[i], got)
		}
	}
}

// --- parseCodexEvents(): pure function, over real recorded JSONL ----------

// codexPongFixture is a byte-for-byte capture of a real `codex exec --json`
// success run, `codex-cli 0.149.0`, 2026-08-22 (see codex.go's own doc
// comment). Kept verbatim rather than hand-simplified so a future CLI
// version's shape change shows up as a test failure against real output,
// not against an idealised guess at it.
const codexPongFixture = `{"type":"thread.started","thread_id":"01a02ac4-a618-7f10-a528-c07b0cc55ee2"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"PONG"}}
{"type":"turn.completed","usage":{"input_tokens":18293,"cached_input_tokens":6912,"cache_write_input_tokens":0,"output_tokens":6,"reasoning_output_tokens":0}}
`

// codexFailFixture is a byte-for-byte capture of a real failing run (a
// nonexistent --model), same CLI/date. exit was 1; this is stdout only.
const codexFailFixture = `{"type":"thread.started","thread_id":"01a02ac5-169e-79f2-91a8-8ec3ae756bb4"}
{"type":"item.completed","item":{"id":"item_0","type":"error","message":"Model metadata for ` + "`definitely-not-a-real-model`" + ` not found. Defaulting to fallback metadata; this can degrade performance and cause issues."}}
{"type":"turn.started"}
{"type":"error","message":"{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The 'definitely-not-a-real-model' model is not supported when using Codex with a ChatGPT account.\"}}"}
{"type":"turn.failed","error":{"message":"{\"type\":\"error\",\"status\":400,\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The 'definitely-not-a-real-model' model is not supported when using Codex with a ChatGPT account.\"}}"}}
`

func TestParseCodexEvents_Success(t *testing.T) {
	r, err := parseCodexEvents(codexPongFixture)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.IsError {
		t.Fatalf("IsError = true on a success stream")
	}
	if r.SessionID != "01a02ac4-a618-7f10-a528-c07b0cc55ee2" {
		t.Fatalf("SessionID = %q", r.SessionID)
	}
	if r.Text != "PONG" {
		t.Fatalf("Text = %q, want PONG", r.Text)
	}
	if r.NumTurns != 1 {
		t.Fatalf("NumTurns = %d, want 1", r.NumTurns)
	}
	if r.CostUSD != 0 {
		t.Fatalf("CostUSD = %v, want 0 (codex has no per-turn dollar figure)", r.CostUSD)
	}
}

func TestParseCodexEvents_Failure(t *testing.T) {
	r, err := parseCodexEvents(codexFailFixture)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if !r.IsError {
		t.Fatalf("IsError = false on a turn.failed stream")
	}
	if !strings.Contains(r.Subtype, "invalid_request_error") {
		t.Fatalf("Subtype = %q, want it to carry the vendor's own error text", r.Subtype)
	}
	if r.SessionID == "" {
		t.Fatalf("SessionID empty even though thread.started was present")
	}
}

func TestParseCodexEvents_EmptyStream(t *testing.T) {
	if _, err := parseCodexEvents(""); err == nil {
		t.Fatal("expected an error for an empty stream, got nil")
	}
}

// A line this adapter's schema does not recognise must not blank the whole
// read -- see parseCodexEvents' own doc comment.
func TestParseCodexEvents_SkipsUnrecognisedLines(t *testing.T) {
	raw := `not json at all
{"type":"thread.started","thread_id":"tid-1"}
{"type":"some.future.event","stuff":{"nested":true}}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"ok"}}
{"type":"turn.completed","usage":{"input_tokens":1}}
`
	r, err := parseCodexEvents(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.SessionID != "tid-1" || r.Text != "ok" || r.NumTurns != 1 {
		t.Fatalf("got %+v", r)
	}
}

// --- Run(): a fake `codex` binary on disk, driven exactly like the real one ---

// fakeCodex writes an executable shell script standing in for the codex CLI:
// it ignores its arguments and prints `stdout` verbatim, then exits with
// `exitCode`. sleepFor, if nonzero, blocks first -- used by the timeout test.
func fakeCodex(t *testing.T, stdout string, exitCode int, sleepFor time.Duration) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake binary is a POSIX shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-codex")
	var sleep string
	if sleepFor > 0 {
		sleep = "sleep " + sleepFor.String() + "\n"
	}
	// Heredoc keeps the fixture's embedded quotes/backslashes literal --
	// this is exactly the shape codexFailFixture has and printf/echo would
	// mangle it.
	script := "#!/bin/sh\n" + sleep + "cat <<'CODEXEOF'\n" + stdout + "CODEXEOF\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex: %v", err)
	}
	return path
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func TestCodexRun_Success(t *testing.T) {
	c := &Codex{Bin: fakeCodex(t, codexPongFixture, 0, 0), Timeout: 5 * time.Second}
	r, err := c.Run(context.Background(), "say PONG")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Text != "PONG" || r.SessionID == "" {
		t.Fatalf("got %+v", r)
	}
}

func TestCodexRun_Failure(t *testing.T) {
	c := &Codex{Bin: fakeCodex(t, codexFailFixture, 1, 0), Timeout: 5 * time.Second}
	r, err := c.Run(context.Background(), "use a bad model")
	if err == nil {
		t.Fatal("expected an error for a turn.failed stream, got nil")
	}
	if r == nil || !r.IsError {
		t.Fatalf("got r=%+v err=%v, want a Result with IsError=true", r, err)
	}
}

func TestCodexRun_EmptyStdout(t *testing.T) {
	c := &Codex{Bin: fakeCodex(t, "", 0, 0), Timeout: 5 * time.Second}
	_, err := c.Run(context.Background(), "hi")
	if !errors.Is(err, ErrNoJSON) {
		t.Fatalf("got %v, want ErrNoJSON", err)
	}
}

func TestCodexRun_Timeout(t *testing.T) {
	c := &Codex{Bin: fakeCodex(t, codexPongFixture, 0, 2*time.Second), Timeout: 50 * time.Millisecond}
	_, err := c.Run(context.Background(), "hi")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("got %v, want ErrTimeout", err)
	}
}

// MUTATION-CHECK, direction 1: the guard fires. A stream with recognised,
// well-formed events but NO thread.started must not be treated as a usable
// result -- there is no session id a caller could resume. Delete the
// `r.SessionID == ""` guard in Run and this test fails (Run would instead
// return a Result with SessionID == "" and a nil error, since the stream
// otherwise parses and reports success).
func TestCodexRun_NoThreadStarted_GuardFires(t *testing.T) {
	noThread := `{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"hi there"}}
{"type":"turn.completed","usage":{"input_tokens":1}}
`
	c := &Codex{Bin: fakeCodex(t, noThread, 0, 0), Timeout: 5 * time.Second}
	_, err := c.Run(context.Background(), "hi")
	if !errors.Is(err, ErrNoJSON) {
		t.Fatalf("got %v, want ErrNoJSON (no thread.started)", err)
	}
	if !strings.Contains(err.Error(), "thread.started") {
		t.Fatalf("error %q does not name the missing event -- weakens the guard's own diagnostics", err.Error())
	}
}

// MUTATION-CHECK, direction 2: the guard does not misfire. The exact same
// event shapes MINUS the missing thread.started -- i.e. TestCodexRun_Success
// above -- already proves a stream that DOES carry thread.started passes
// clean. This test is the explicit contrast: same well-formed body, only
// the presence of thread.started differs, and only its absence trips the
// guard.
func TestCodexRun_ThreadStartedPresent_GuardDoesNotMisfire(t *testing.T) {
	withThread := `{"type":"thread.started","thread_id":"tid-guard-check"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"hi there"}}
{"type":"turn.completed","usage":{"input_tokens":1}}
`
	c := &Codex{Bin: fakeCodex(t, withThread, 0, 0), Timeout: 5 * time.Second}
	r, err := c.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.SessionID != "tid-guard-check" {
		t.Fatalf("SessionID = %q", r.SessionID)
	}
}
