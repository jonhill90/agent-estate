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
