package monitor

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestUpdate_FetchResultPopulatesSnapshot(t *testing.T) {
	m := New(nil)
	want := Snapshot{Host: Host{Cores: 4}, Agents: AgentHealth{Known: true, Total: 1}}
	next, _ := m.Update(fetchResultMsg{snapshot: want})
	m = next.(Model)
	if got := m.Snapshot(); got.Host.Cores != want.Host.Cores || got.Agents.Total != want.Agents.Total {
		t.Errorf("Snapshot() = %+v, want %+v", got, want)
	}
	if !m.fetchedOnce {
		t.Error("fetchedOnce not set after a successful fetch")
	}
}

func TestUpdate_FetchErrorLeavesPriorSnapshot(t *testing.T) {
	m := New(nil)
	first := Snapshot{Host: Host{Cores: 4}}
	next, _ := m.Update(fetchResultMsg{snapshot: first})
	m = next.(Model)

	next, _ = m.Update(fetchResultMsg{err: errors.New("boom")})
	m = next.(Model)

	if got := m.Snapshot(); got.Host.Cores != first.Host.Cores {
		t.Errorf("Snapshot() = %+v, want unchanged %+v after a failed fetch", got, first)
	}
	if m.fetchErr == nil {
		t.Error("fetchErr not set after a failed fetch")
	}
}

func TestUpdate_RefreshKeyTriggersFetch(t *testing.T) {
	called := false
	m := New(func() (Snapshot, error) {
		called = true
		return Snapshot{}, nil
	})
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("expected a command from [r]")
	}
	cmd()
	if !called {
		t.Error("fetch was not called")
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

func TestNew_NilFetchDoesNotPanic(t *testing.T) {
	m := New(nil)
	if cmd := m.Init(); cmd == nil {
		t.Fatal("Init should still return the refresh-tick batch even with a nil fetch")
	}
}
