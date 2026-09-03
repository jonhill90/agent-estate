package main

import (
	"os"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/mirror"
	"github.com/jonhill90/agent-estate/estate/internal/pressure"
)

// A mechanism nothing calls is a documentation rule with a binary attached --
// this repo has been bitten by exactly that (count-agents.sh existed, was
// tested, and for a time nothing invoked it). internal/mirror is only worth
// anything if dispatch actually tees the turn's streams into it, so the
// wiring itself is checked here rather than assumed.
//
// These read main.go's source because the alternative -- running a real
// dispatch -- needs a harness, a corpus, a worktree and real spend, which no
// unit test should require. A source check is weaker than an execution check
// and is not claimed to be more: it catches the mirror being UNWIRED, not the
// mirror being wired wrongly. The live end-to-end evidence for the latter is
// in the pull request.

// mainSource is main.go with its // comments removed, so these guards read
// the code rather than the prose about it -- the mirror block's own comments
// name several of the verbs the guards below ban, on purpose.
func mainSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	var out strings.Builder
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if i := strings.Index(line, "// "); i >= 0 {
			line = line[:i]
		}
		out.WriteString(line)
		out.WriteString("\n")
	}
	return out.String()
}

func TestDispatchTeesBothStreamsIntoTheMirror(t *testing.T) {
	src := mainSource(t)
	for _, want := range []string{
		"cmd.Stdout = io.MultiWriter(&stdout, mir.Stdout())",
		"cmd.Stderr = io.MultiWriter(&stderrBuf, mir.Stderr())",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("dispatch no longer tees a stream into the mirror; expected %q in main.go", want)
		}
	}
	// The transport's own capture must survive the tee: Result, Spend and
	// SessionID are all read from the stdout buffer, and a mirror that
	// replaced rather than duplicated it would silently blind all three.
	if !strings.Contains(src, "&stdout,") || !strings.Contains(src, "&stderrBuf,") {
		t.Fatalf("the mirror replaced the transport's own capture instead of duplicating it")
	}
}

func TestDispatchStampsATerminalStateIntoTheTranscript(t *testing.T) {
	src := mainSource(t)
	if !strings.Contains(src, `mir.Close(string(rec.State), rec.Note)`) {
		t.Fatalf("dispatch does not stamp the recorded state into the transcript; a dead turn's pane would trail off mid-output")
	}
	// Every path that leaves dispatch between opening the mirror and the
	// ledger append must close it. os.Exit skips defers, so a defer alone is
	// not enough and the two exiting paths close explicitly.
	for _, want := range []string{
		`mir.Close("failed", "the turn never started: "`,
		`mir.Close("unknown", "the turn is running but the ledger could not record it: "`,
		`defer mir.Close("unknown", "dispatch exited before recording a terminal state")`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("an exit path leaves the transcript unstamped; expected %q in main.go", want)
		}
	}
}

// The pane bound and the concurrency bound must be the SAME number, not two
// numbers that happen to agree today. A pane per concurrent turn is bounded;
// a pane per historical turn is not, and 208 panes is the 176-worktree
// OOM defect wearing a different hat.
func TestThePaneBoundIsTheInFlightCap(t *testing.T) {
	cap := pressure.Default().MaxInFlight
	if got := mirror.Default(cap).Max; got != cap {
		t.Fatalf("mirror bound %d does not track the in-flight cap %d", got, cap)
	}
	if !strings.Contains(mainSource(t), "mirror.Default(pressure.Default().MaxInFlight)") {
		t.Fatalf("dispatch no longer derives the pane bound from the in-flight cap; the two can now drift")
	}
}

// Visibility must never gate work. A mirror that cannot be opened -- no tmux,
// or a bound full of live turns -- is reported and the dispatch proceeds.
func TestAMirrorFailureDoesNotStopTheDispatch(t *testing.T) {
	src := mainSource(t)
	i := strings.Index(src, "mir, merr := mirror.Open(")
	if i < 0 {
		t.Fatalf("dispatch no longer opens a mirror at all")
	}
	// Look at the refusal branch only.
	tail := src[i:]
	end := strings.Index(tail, "ctx, cancel := context.WithTimeout")
	if end < 0 {
		t.Fatalf("could not find the end of the mirror block in main.go")
	}
	block := tail[:end]
	if strings.Contains(block, "os.Exit") {
		t.Fatalf("a mirror failure exits the dispatch; visibility must never gate work:\n%s", block)
	}
}

// The estate must never type into a mirror pane. internal/mirror enforces
// this on its own source; this is the caller-side half -- dispatch must not
// reach around the package to tmux either.
func TestDispatchHasNoTmuxControlPath(t *testing.T) {
	src := mainSource(t)
	for _, banned := range []string{"send-keys", "kill-server", "kill-session", "\"tmux\""} {
		if strings.Contains(src, banned) {
			t.Fatalf("main.go references %q -- tmux is reached only through internal/mirror, and only as a viewer", banned)
		}
	}
}
