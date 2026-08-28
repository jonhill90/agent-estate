package monitor

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestView_UnknownWhenNothingFetched(t *testing.T) {
	m := New(nil)
	out := m.View()
	if !strings.Contains(out, "monitoring") {
		t.Errorf("View missing title, got:\n%s", out)
	}
	if !strings.Contains(out, "not fetched yet") {
		t.Errorf("View missing 'not fetched yet', got:\n%s", out)
	}
	if !strings.Contains(out, unknown) {
		t.Errorf("View missing %q for an unfetched snapshot, got:\n%s", unknown, out)
	}
}

func TestView_RendersKnownHostFigures(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{snapshot: Snapshot{
		Host: Host{
			Cores:           8,
			LoadAvg1:        KnownFigure(1.23),
			LoadAvg5:        KnownFigure(1.10),
			LoadAvg15:       KnownFigure(0.95),
			SwapUsedPercent: KnownFigure(12.5),
			ClaudeProcesses: KnownCount(4),
		},
		Agents: AgentHealth{Known: true, ByState: map[string]int{"free": 2, "busy": 1}, Total: 3},
	}})
	m = next.(Model)
	out := m.View()

	for _, want := range []string{"8", "1.23", "1.10", "0.95", "12.5%", "4", "3 total", "free:2", "busy:1"} {
		if !strings.Contains(out, want) {
			t.Errorf("View missing %q, got:\n%s", want, out)
		}
	}
}

func TestView_PartialFailureLeavesOtherFieldsIntact(t *testing.T) {
	// Host figures partially unknown (swap failed) must not blank agent
	// health, and vice versa -- Snapshot's own doc comment's central claim.
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{snapshot: Snapshot{
		Host: Host{
			Cores:           4,
			LoadAvg1:        KnownFigure(0.5),
			LoadAvg5:        KnownFigure(0.4),
			LoadAvg15:       KnownFigure(0.3),
			SwapUsedPercent: Figure{}, // unknown
			ClaudeProcesses: KnownCount(2),
		},
		Agents: AgentHealth{Known: false},
	}})
	m = next.(Model)
	out := m.View()

	if !strings.Contains(out, "0.50") {
		t.Errorf("View lost load average when swap was unknown, got:\n%s", out)
	}
	if !strings.Contains(out, unknown) {
		t.Errorf("View missing unknown for agents, got:\n%s", out)
	}
}

func TestView_ShowsFetchError(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("boom")})
	m = next.(Model)
	out := m.View()
	if !strings.Contains(out, "boom") {
		t.Errorf("View missing fetch error, got:\n%s", out)
	}
}

func TestView_QuittingRendersEmpty(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if out := m.View(); out != "" {
		t.Errorf("View after quit = %q, want empty", out)
	}
}
