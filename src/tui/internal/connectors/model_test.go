package connectors

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFetchResultPopulatesConnectionsAndModels drives Update directly
// (cheaper than a full teatest.Program, the same two-tier discipline
// internal/mcpservers/internal/skills' own test suites use).
func TestFetchResultPopulatesConnectionsAndModels(t *testing.T) {
	m := New(nil)
	model := "gpt-5.6-terra"
	next, _ := m.Update(fetchResultMsg{
		connections: []Connection{{Harness: HarnessCodex, Provider: "openai", Configured: true, DefaultModel: &model}},
		models:      []AvailableModel{{Harness: HarnessCodex, Slug: "gpt-5.6-sol", DisplayName: "GPT-5.6-Sol"}},
	})
	m = next.(Model)

	if len(m.Connections()) != 1 || len(m.Models()) != 1 {
		t.Fatalf("Connections()=%+v Models()=%+v, want one of each", m.Connections(), m.Models())
	}
	out := m.View()
	if !strings.Contains(out, "codex") || !strings.Contains(out, "gpt-5.6-terra") {
		t.Fatalf("View() missing the fetched connection:\n%s", out)
	}
	if !strings.Contains(out, "gpt-5.6-sol") {
		t.Fatalf("View() missing the fetched model:\n%s", out)
	}
}

// TestUnknownProviderAndModelRenderAsUnknown covers Connection's own
// absence discipline made visible.
func TestUnknownProviderAndModelRenderAsUnknown(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{
		connections: []Connection{{Harness: HarnessClaude, Provider: "anthropic", Configured: true, DefaultModel: nil}},
	})
	m = next.(Model)
	out := m.View()
	if !strings.Contains(out, unknown) {
		t.Fatalf("View() does not render %q for the missing default model:\n%s", unknown, out)
	}
}

// TestFetchErrorRendersVisibly matches internal/agents/internal/skills/
// internal/mcpservers' identical convention -- blind, not quiet.
func TestFetchErrorRendersVisibly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("permission denied")})
	m = next.(Model)
	out := m.View()
	if !strings.Contains(out, "permission denied") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
}

func TestJKMoveSelectionAndClampAtEnds(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{connections: []Connection{{Harness: "a"}, {Harness: "b"}, {Harness: "c"}}})
	m = next.(Model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = next.(Model)
	if m.selected != 0 {
		t.Fatalf("\"k\" from row 0 moved to %d, want clamped at 0", m.selected)
	}

	for i := 0; i < 5; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = next.(Model)
	}
	if m.selected != 2 {
		t.Fatalf("\"j\" x5 from a 3-row list landed on %d, want clamped at 2", m.selected)
	}
}

func TestRKeyRefetches(t *testing.T) {
	calls := 0
	fetch := func() ([]Connection, []AvailableModel, error) { calls++; return nil, nil, nil }
	m := New(fetch)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("[r] returned a nil cmd, want a fetch")
	}
	cmd()
	if calls != 1 {
		t.Fatalf("fetch called %d times after [r], want 1", calls)
	}
}

func TestQuittingRendersNothing(t *testing.T) {
	m := New(nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("\"q\" did not return tea.Quit")
	}
	if m.View() != "" {
		t.Fatalf("View() after quitting = %q, want empty", m.View())
	}
}
