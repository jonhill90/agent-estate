package workflows

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/board"
)

func TestNew_NilFetchIsUnconfigured(t *testing.T) {
	m := New(nil)
	if !m.unconfigured {
		t.Error("unconfigured should be true for a nil Fetcher")
	}
	if cmd := m.Init(); cmd != nil {
		t.Error("Init should return nil for an unconfigured Model -- no fetch to schedule")
	}
}

func TestUpdate_FetchResultSortsNewestFirst(t *testing.T) {
	m := New(nil)
	rows := []board.TaskRow{
		{TaskID: "old", CreatedAt: 100},
		{TaskID: "new", CreatedAt: 300},
		{TaskID: "mid", CreatedAt: 200},
	}
	next, _ := m.Update(fetchResultMsg{rows: rows})
	m = next.(Model)

	got := m.Rows()
	if len(got) != 3 {
		t.Fatalf("len(Rows()) = %d, want 3", len(got))
	}
	want := []string{"new", "mid", "old"}
	for i, id := range want {
		if got[i].TaskID != id {
			t.Errorf("Rows()[%d].TaskID = %q, want %q", i, got[i].TaskID, id)
		}
	}
	if !m.fetchedOnce {
		t.Error("fetchedOnce not set")
	}
}

func TestUpdate_FetchErrorLeavesRowsIntact(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{rows: []board.TaskRow{{TaskID: "a", CreatedAt: 1}}})
	m = next.(Model)

	next, _ = m.Update(fetchResultMsg{err: errors.New("boom")})
	m = next.(Model)

	if len(m.Rows()) != 1 {
		t.Errorf("Rows() changed after a failed fetch: %v", m.Rows())
	}
	if m.fetchErr == nil {
		t.Error("fetchErr not set after a failed fetch")
	}
}

func TestUpdate_SelectionClampsToRowCount(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{rows: []board.TaskRow{{TaskID: "a", CreatedAt: 1}, {TaskID: "b", CreatedAt: 2}}})
	m = next.(Model)

	for i := 0; i < 5; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		m = next.(Model)
	}
	if m.selected != 1 {
		t.Errorf("selected = %d, want clamped to 1 (last row)", m.selected)
	}

	// A fetch that returns fewer rows must reclamp, not leave selected
	// pointing past the end (byCreatedDesc's own caller in Update does
	// this) -- exercised via a second fetchResultMsg with one row.
	next, _ = m.Update(fetchResultMsg{rows: []board.TaskRow{{TaskID: "only", CreatedAt: 1}}})
	m = next.(Model)
	if m.selected != 0 {
		t.Errorf("selected = %d, want reclamped to 0", m.selected)
	}
}

func TestUpdate_QuitKeyQuits(t *testing.T) {
	m := New(nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if !m.quitting {
		t.Error("quitting not set after [q]")
	}
	if cmd == nil {
		t.Fatal("expected tea.Quit command")
	}
}
