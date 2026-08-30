package connectors

// This file drives the real tea.Program via charmbracelet/x/exp/teatest --
// the same discipline internal/mcpservers/internal/skills' own
// model_teatest_test.go files use.

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

// TestInitialFrameShowsConnectionsAndModelsFromTheFakeFetch drives a real
// Program against a fake Fetcher (no real filesystem read -- adapter
// discipline, AGENTS.md).
func TestInitialFrameShowsConnectionsAndModelsFromTheFakeFetch(t *testing.T) {
	model := "gpt-5.6-terra"
	fetch := func() ([]Connection, []AvailableModel, error) {
		return []Connection{{Harness: HarnessCodex, Provider: "openai", Configured: true, DefaultModel: &model}},
			[]AvailableModel{{Harness: HarnessCodex, Slug: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol"}}, nil
	}
	tm := run(t, New(fetch))
	out := waitFor(t, tm, "gpt-5.6-terra")
	if !bytes.Contains(out, []byte("gpt-5.6-sol")) {
		t.Fatalf("models section missing from the real render:\n%s", out)
	}
}

// TestQQuitsARealProgram matches every other pane's own convention.
func TestQQuitsARealProgram(t *testing.T) {
	tm := run(t, New(nil))
	waitFor(t, tm, "connectors")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}
