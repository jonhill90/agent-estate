package agents

// This file drives the real tea.Program via charmbracelet/x/exp/teatest --
// the same discipline internal/board/internal/shell's own
// model_teatest_test.go files use (see their doc comments): send tea.Msg
// through the actual event loop, then read the actual rendered output.

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/jonhill90/agent-estate/tui/internal/lane"
)

func run(t *testing.T, m Model) *teatest.TestModel {
	t.Helper()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 24))
	t.Cleanup(func() { _ = tm.Quit() })
	return tm
}

func waitFor(t *testing.T, tm *teatest.TestModel, want string) []byte {
	t.Helper()
	var b bytes.Buffer
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		buf := make([]byte, 65536)
		n, _ := tm.Output().Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if bytes.Contains(b.Bytes(), []byte(want)) {
			return b.Bytes()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor %q: not seen after 8s. Output so far:\n%s", want, b.String())
	return nil
}

// TestInitialFrameShowsAgentsFromTheFakeSessionsFetch drives a real Program
// against a fake Fetcher (no real MCP subprocess -- adapter discipline,
// AGENTS.md) and asserts the actual rendered frame lists the fetched
// agent, with model/cost visibly "unknown," never blank or zero.
func TestInitialFrameShowsAgentsFromTheFakeSessionsFetch(t *testing.T) {
	fetch := func() ([]lane.Session, error) {
		return []lane.Session{
			{Name: "director", Lanes: []lane.Lane{{Name: "w1", State: "busy", Command: "claude"}}},
		}, nil
	}
	tm := run(t, New(fetch))
	out := waitFor(t, tm, "director:w1")
	if !bytes.Contains(out, []byte("unknown")) {
		t.Fatalf("model/cost columns not rendered as \"unknown\":\n%s", out)
	}
}

// TestNKeyShowsReadOnlyNoticeAgainstARealProgram is the pty-level half of
// model_test.go's TestNKeySetsANamedNoticeRatherThanSilentlyDoingNothing.
func TestNKeyShowsReadOnlyNoticeAgainstARealProgram(t *testing.T) {
	fetch := func() ([]lane.Session, error) {
		return []lane.Session{{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy"}}}}, nil
	}
	tm := run(t, New(fetch))
	waitFor(t, tm, "s:w1")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	waitFor(t, tm, "not built yet (S7)")
}

// TestQQuitsARealProgram matches every other pane's own convention.
func TestQQuitsARealProgram(t *testing.T) {
	tm := run(t, New(nil))
	waitFor(t, tm, "agents")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}
