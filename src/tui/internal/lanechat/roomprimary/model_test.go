package roomprimary

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestNewBuildsOneRoomPerLane(t *testing.T) {
	m := New()
	if len(m.rooms) == 0 {
		t.Fatal("New() has no rooms")
	}
	for _, r := range m.rooms {
		if r.lane.Name != r.thread.Lane && r.thread.Lane != "" {
			t.Fatalf("room lane %q does not match its own thread's Lane %q -- rooms must be 1:1", r.lane.Name, r.thread.Lane)
		}
	}
}

func TestRoomRowShowsLaneState(t *testing.T) {
	m := New()
	m.width, m.height = 100, 30
	out := m.View()
	if !strings.Contains(out, "busy") || !strings.Contains(out, "hung") {
		t.Fatalf("View() does not show lane state inline on the room row:\n%s", out)
	}
}

func TestComposeRefusesStoppedParticipant(t *testing.T) {
	m := New()
	model, _ := m.Update(keyMsg("i"))
	m = model.(Model)
	for _, r := range "@fixture-delta still there?" {
		model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = model.(Model)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = model.(Model)
	if !strings.Contains(m.status, "not running") {
		t.Fatalf("status = %q, want it to name \"not running\" (fixture-delta is dead)", m.status)
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
