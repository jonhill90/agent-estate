package dashboard

// This file drives the real tea.Program via charmbracelet/x/exp/teatest --
// the same discipline internal/agents/internal/skills' own
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

// TestInitialFrameShowsRealFetchedStats drives a real Program against a
// fake Fetcher (no real filesystem/gh/mcp read -- adapter discipline,
// AGENTS.md) and asserts the actual rendered frame carries the fetched
// figures.
func TestInitialFrameShowsRealFetchedStats(t *testing.T) {
	fetch := func() (Stats, error) {
		return Stats{
			AgentsByState: map[string]int{"busy": 1},
			AgentsKnown:   true,
			OpenPRs:       KnownCount(5),
			MergedToday:   KnownCount(2),
			SpendToday:    KnownUSD(3.5),
			VaultFacts:    KnownCount(9),
		}, nil
	}
	tm := run(t, New(fetch))
	out := waitFor(t, tm, "busy:1")
	if !bytes.Contains(out, []byte("$3.50")) {
		t.Fatalf("spend column not rendered:\n%s", out)
	}
}

// TestQQuitsARealProgram matches every other pane's own convention.
func TestQQuitsARealProgram(t *testing.T) {
	tm := run(t, New(nil))
	waitFor(t, tm, "dashboard")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}
