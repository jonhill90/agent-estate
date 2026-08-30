package laneprimary

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestNewSelectsFirstLaneByDefault(t *testing.T) {
	m := New()
	if len(m.lanes) == 0 {
		t.Fatal("New() has no lanes")
	}
	if m.selected != 0 {
		t.Fatalf("selected = %d, want 0", m.selected)
	}
}

func TestViewMarksFixtureDataVisibly(t *testing.T) {
	m := New()
	m.width, m.height = 100, 30
	out := m.View()
	if !strings.Contains(strings.ToUpper(out), "FIXTURE") {
		t.Fatalf("View() does not visibly mark this as fixture data:\n%s", out)
	}
}

func TestComposeRefusesUnknownMention(t *testing.T) {
	m := New()
	model, _ := m.Update(keyMsg("i"))
	m = model.(Model)
	if !m.composing {
		t.Fatal("\"i\" did not enter compose mode")
	}
	for _, r := range "@ghost hello" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(Model)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if !strings.Contains(m.status, "not in this room") {
		t.Fatalf("status after refused mention = %q, want it to name \"not in this room\"", m.status)
	}
	if !m.composing {
		t.Fatal("composer closed on a refused send -- it must stay open so the draft is not lost")
	}
}

func TestComposeAcceptsRunningParticipant(t *testing.T) {
	m := New()
	model, _ := m.Update(keyMsg("i"))
	m = model.(Model)
	for _, r := range "@fixture-atlas hi" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(Model)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if strings.HasPrefix(m.status, "!") {
		t.Fatalf("status after a valid mention = %q, want no refusal", m.status)
	}
	if m.composing {
		t.Fatal("composer stayed open after a valid send")
	}
}

func TestQuitSetsQuitting(t *testing.T) {
	m := New()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = model.(Model)
	if !m.quitting || cmd == nil {
		t.Fatal("ctrl+c did not quit")
	}
	if m.View() != "" {
		t.Fatalf("View() after quitting = %q, want empty", m.View())
	}
}
