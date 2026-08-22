package skills

// This file drives the real tea.Program via charmbracelet/x/exp/teatest --
// the same discipline internal/agents/internal/board's own
// model_teatest_test.go files use: send tea.Msg through the actual event
// loop, then read the actual rendered output.

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
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

// TestInitialFrameShowsSkillsFromTheFakeFetch drives a real Program
// against a fake Fetcher (no real filesystem read -- adapter discipline,
// AGENTS.md) and asserts the actual rendered frame lists the fetched
// skill, with last-eval/invocations visibly "unknown," never blank or
// zero.
func TestInitialFrameShowsSkillsFromTheFakeFetch(t *testing.T) {
	fetch := func() ([]Skill, error) {
		return []Skill{{Dir: "alpha", Name: "alpha", Description: "does alpha things"}}, nil
	}
	tm := run(t, New(fetch))
	out := waitFor(t, tm, "does alpha things")
	if !bytes.Contains(out, []byte("unknown")) {
		t.Fatalf("last-eval/invocations columns not rendered as \"unknown\":\n%s", out)
	}
	if !bytes.Contains(out, []byte(VerdictUnevaluated)) {
		t.Fatalf("verdict column not rendered as %q:\n%s", VerdictUnevaluated, out)
	}
}

// TestEKeyShowsEvalLoopNoticeAgainstARealProgram is the pty-level half of
// model_test.go's TestUpdate_EKeySetsANamedNoticeRatherThanSilentlyDoingNothing.
func TestEKeyShowsEvalLoopNoticeAgainstARealProgram(t *testing.T) {
	fetch := func() ([]Skill, error) {
		return []Skill{{Dir: "alpha", Name: "alpha", Description: "x"}}, nil
	}
	tm := run(t, New(fetch))
	waitFor(t, tm, "alpha")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	waitFor(t, tm, "eval harness exists (agent-evals#21) but persists no results store yet")
}

// TestQQuitsARealProgram matches every other pane's own convention.
func TestQQuitsARealProgram(t *testing.T) {
	tm := run(t, New(nil))
	waitFor(t, tm, "skills")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}
