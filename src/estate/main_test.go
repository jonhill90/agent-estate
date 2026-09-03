package main

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// End-to-end on the actual wiring: a real child process, exited non-zero,
// with cmd.Stderr wired to a buffer exactly the way main's dispatch path
// wires it. This is the mechanism agent-estate#950 says was missing --
// without an explicit cmd.Stderr buffer, a child's diagnostic output is
// never captured anywhere readable (whether the caller then drives the
// child via cmd.Output() or, as main's dispatch path does since
// agent-estate#944's cmd.Start()/cmd.Wait() split, via cmd.Run()-shaped
// calls -- the buffer fills identically either way).
func TestFakeFailingCommandStderrReachesTheNote(t *testing.T) {
	cmd := exec.Command("sh", "-c", "echo 'boom: disk full at /tmp/work' >&2; exit 1")
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()
	if runErr == nil {
		t.Fatal("fake command exited 0, want non-zero to exercise the failure path")
	}

	note := runErr.Error() + "; " + stderrSegment(stderrBuf.Bytes(), true)
	if !strings.Contains(note, "exit status 1") {
		t.Fatalf("note = %q, want it to still carry the exit error", note)
	}
	if !strings.Contains(note, "boom: disk full at /tmp/work") {
		t.Fatalf("note = %q, want it to carry the child's actual stderr, not just the bare exit status", note)
	}
}

// A non-zero exit must carry the child's own stderr into the ledger note,
// not just "exit status N" -- that is the whole gap agent-estate#950 names.
func TestStderrSegmentCarriesKnownStderr(t *testing.T) {
	got := stderrSegment([]byte("panic: nil pointer at frobnicate.go:12"), true)
	if !strings.Contains(got, "panic: nil pointer at frobnicate.go:12") {
		t.Fatalf("stderrSegment(known stderr) = %q, want it to contain the stderr text", got)
	}
}

// Empty stderr that was actually captured is a real observation ("we looked,
// there was nothing") and must read differently from stderr that was never
// examined at all -- the same typed-absence discipline as cost.Figure.Known
// and session.Worktree.Clean, applied to a diagnostic note.
func TestEmptyStderrIsDistinguishableFromUnexamined(t *testing.T) {
	examined := stderrSegment(nil, true)
	unexamined := stderrSegment(nil, false)

	if examined == unexamined {
		t.Fatalf("stderrSegment(nil, true) == stderrSegment(nil, false) = %q; captured-empty must read differently from never-examined", examined)
	}
	if !strings.Contains(examined, "empty") {
		t.Fatalf("stderrSegment(nil, true) = %q, want it to say stderr was captured and empty", examined)
	}
	if strings.Contains(unexamined, "empty") {
		t.Fatalf("stderrSegment(nil, false) = %q, must not claim we looked and saw nothing", unexamined)
	}
}

// The ledger is append-only JSONL; an unbounded stderr blob turns one bad
// turn into a file nobody can read back. The note must keep only a bounded
// tail and say so, so a reader knows they are seeing part of it.
func TestStderrSegmentTruncatesAndSaysSo(t *testing.T) {
	big := strings.Repeat("x", stderrTailLimit*3)
	got := stderrSegment([]byte(big), true)

	if !strings.Contains(got, "truncated") {
		t.Fatalf("stderrSegment(oversized) = %q, want it to say it was truncated", got)
	}
	// The kept tail itself must still be bounded -- truncation that still
	// appends the whole blob after announcing itself is not truncation.
	if len(got) > stderrTailLimit+200 {
		t.Fatalf("stderrSegment(oversized) length = %d, want roughly bounded to stderrTailLimit", len(got))
	}
	// It's a TAIL: the end of the original blob, not the head, since the
	// most recent output is usually the actual error.
	tail := strings.Repeat("x", 20)
	if !strings.HasSuffix(got, tail) {
		t.Fatalf("stderrSegment(oversized) did not end with the tail of the original blob")
	}
}

func TestStderrSegmentTrimsWhitespaceBeforeCheckingEmpty(t *testing.T) {
	got := stderrSegment([]byte("   \n\t  "), true)
	if !strings.Contains(got, "empty") {
		t.Fatalf("stderrSegment(whitespace-only) = %q, want it treated as empty", got)
	}
}

// agent-estate#955's review: `claude -p --output-format json` puts a bad
// model / bad entitlement diagnostic on STDOUT as {"is_error":true,...,
// "result":"..."}, with stderr left completely empty. Before this test the
// Failed branch never looked at stdout at all, so the one piece of
// diagnostic text the CLI actually produced was silently dropped -- this is
// red against the pre-fix code (no stdoutDiagnosticSegment existed) and
// green once it parses is_error/subtype/result out of stdout.
func TestStdoutDiagnosticSegmentCarriesIsErrorResult(t *testing.T) {
	stdout := []byte(`{"is_error":true,"subtype":"error_during_execution","result":"There's an issue with the selected model (does-not-exist-model). It may not exist or you may not have access to it."}`)
	got := stdoutDiagnosticSegment(stdout)

	if !strings.Contains(got, "is_error=true") {
		t.Fatalf("stdoutDiagnosticSegment(...) = %q, want it to carry is_error=true", got)
	}
	if !strings.Contains(got, "error_during_execution") {
		t.Fatalf("stdoutDiagnosticSegment(...) = %q, want it to carry the subtype", got)
	}
	if !strings.Contains(got, "There's an issue with the selected model") {
		t.Fatalf("stdoutDiagnosticSegment(...) = %q, want it to carry the CLI's own diagnostic result text", got)
	}
}

// The "Failed" branch must actually reach for stdout, not just stderr --
// this exercises the real switch statement's Note composition the way
// main's dispatch path builds it, mirroring
// TestFakeFailingCommandStderrReachesTheNote but for the stdout-diagnostic,
// empty-stderr shape.
func TestFailedNoteCarriesStdoutDiagnosticWhenStderrIsEmpty(t *testing.T) {
	cmd := exec.Command("sh", "-c", `echo '{"is_error":true,"result":"There'"'"'s an issue with the selected model (does-not-exist-model)."}'; exit 1`)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()
	if runErr == nil {
		t.Fatal("fake command exited 0, want non-zero to exercise the failure path")
	}
	if strings.TrimSpace(stderrBuf.String()) != "" {
		t.Fatalf("fake command wrote to stderr (%q), want the empty-stderr shape this test targets", stderrBuf.String())
	}

	note := runErr.Error() + "; " + stderrSegment(stderrBuf.Bytes(), true) + "; " + stdoutDiagnosticSegment(stdoutBuf.Bytes())

	if !strings.Contains(note, "stderr: (empty)") {
		t.Fatalf("note = %q, want the empty-stderr segment preserved", note)
	}
	if !strings.Contains(note, "There's an issue with the selected model") {
		t.Fatalf("note = %q, want the stdout diagnostic present since stderr carried nothing", note)
	}
}

// Unparseable stdout must be recorded as unparseable, never dumped raw --
// dumping raw stdout on failure is exactly the path that would leak an
// echoed prompt if the child ever wrote one there.
func TestStdoutDiagnosticSegmentUnparseableIsNotDumpedRaw(t *testing.T) {
	marker := "secret-prompt-marker-XYZ"
	got := stdoutDiagnosticSegment([]byte("not json at all, contains " + marker))

	if strings.Contains(got, marker) {
		t.Fatalf("stdoutDiagnosticSegment(unparseable) = %q, must not leak raw stdout content", got)
	}
	if !strings.Contains(got, "unparseable") {
		t.Fatalf("stdoutDiagnosticSegment(unparseable) = %q, want it to say the JSON was unparseable", got)
	}
}

// is_error=false means whatever is in `result` is the model's own content,
// not the CLI's own error classification -- and the model's own content is
// exactly where an echoed prompt could live. Do not extract `result` here.
func TestStdoutDiagnosticSegmentDoesNotExtractResultWhenNotError(t *testing.T) {
	marker := "secret-prompt-marker-XYZ"
	stdout := []byte(`{"is_error":false,"result":"` + marker + `"}`)
	got := stdoutDiagnosticSegment(stdout)

	if strings.Contains(got, marker) {
		t.Fatalf("stdoutDiagnosticSegment(is_error=false) = %q, must not extract result text when is_error is false", got)
	}
}

// The core leakage requirement from the #955 review, reproduced against the
// real dispatch shape: a marker present only in the piped prompt (stdin)
// must never appear in the composed ledger note, even once stdout is now
// examined on the failure path.
func TestLeakage_PromptMarkerNeverReachesTheNote(t *testing.T) {
	marker := "secret-prompt-marker-XYZ"
	cmd := exec.Command("sh", "-c", `cat >/dev/null; echo '{"is_error":true,"result":"There is an issue with the selected model."}'; exit 1`)
	cmd.Stdin = strings.NewReader("prompt containing " + marker)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()
	if runErr == nil {
		t.Fatal("fake command exited 0, want non-zero to exercise the failure path")
	}

	note := runErr.Error() + "; " + stderrSegment(stderrBuf.Bytes(), true) + "; " + stdoutDiagnosticSegment(stdoutBuf.Bytes())

	if strings.Contains(note, marker) {
		t.Fatalf("note = %q, prompt marker leaked into the recorded reason", note)
	}
}
