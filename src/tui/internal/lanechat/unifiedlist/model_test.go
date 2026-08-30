package unifiedlist

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestNewStartsCollapsed(t *testing.T) {
	m := New()
	if m.expanded {
		t.Fatal("New() started expanded -- should start collapsed")
	}
	if len(m.rows) == 0 {
		t.Fatal("New() has no rows")
	}
}

func TestEnterTogglesExpansion(t *testing.T) {
	m := New()
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if !m.expanded {
		t.Fatal("enter did not expand the selected row")
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if m.expanded {
		t.Fatal("second enter did not collapse the row again")
	}
}

func TestComposeOnlyAvailableWhenExpanded(t *testing.T) {
	m := New()
	model, _ := m.Update(keyMsg("i"))
	m = model.(Model)
	if m.composing {
		t.Fatal("\"i\" entered compose mode on a collapsed row -- must require expansion first")
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	model, _ = m.Update(keyMsg("i"))
	m = model.(Model)
	if !m.composing {
		t.Fatal("\"i\" did not enter compose mode once the row was expanded")
	}
}

func TestComposeRefusesUnknownMention(t *testing.T) {
	m := New()
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // expand
	m = model.(Model)
	model, _ = m.Update(keyMsg("i"))
	m = model.(Model)
	for _, r := range "@ghost hello" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(Model)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if !strings.Contains(m.status, "not in this room") {
		t.Fatalf("status = %q, want a refusal naming \"not in this room\"", m.status)
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

func TestQuitSetsQuitting(t *testing.T) {
	m := New()
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = model.(Model)
	if !m.quitting || cmd == nil {
		t.Fatal("ctrl+c did not quit")
	}
}
