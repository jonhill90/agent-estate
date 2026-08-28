package mcpservers

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFetchResultPopulatesServers drives Update directly (cheaper than a
// full teatest.Program, the same two-tier discipline internal/agents' own
// test suite uses) to confirm a fetchResultMsg actually reaches View().
func TestFetchResultPopulatesServers(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{servers: []Server{
		{Name: "supervisor", Scope: ScopeGlobal, Transport: TransportStdio, Command: "python3"},
	}})
	m = next.(Model)

	if len(m.Servers()) != 1 {
		t.Fatalf("Servers() = %+v, want one server", m.Servers())
	}
	if !strings.Contains(m.View(), "supervisor") {
		t.Fatalf("View() missing the fetched server:\n%s", m.View())
	}
	if !strings.Contains(m.View(), unknown) {
		t.Fatalf("View() does not render the unknown reachability column:\n%s", m.View())
	}
}

// TestFetchErrorRendersVisibly is this pane's own "blind, not quiet" case
// -- the same discipline internal/agents/internal/skills' identical tests
// already enforce: a failed config read must not render "(no servers
// configured)" as if there genuinely were none.
func TestFetchErrorRendersVisibly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("no such file")})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "no such file") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
}

// TestReachableAndUnreachableRenderDistinctly covers all three states in
// one render -- yes/no/unknown must each read as a different word, never
// collapsed to the same rendering.
func TestReachableAndUnreachableRenderDistinctly(t *testing.T) {
	reachableTrue, reachableFalse := true, false
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{servers: []Server{
		{Name: "found", Reachable: &reachableTrue},
		{Name: "missing", Reachable: &reachableFalse},
		{Name: "web", Reachable: nil},
	}})
	m = next.(Model)

	out := m.View()
	for _, want := range []string{"yes", "no", unknown} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing reachability marker %q:\n%s", want, out)
		}
	}
}

// TestJKMoveSelectionAndClampAtEnds matches internal/skills' identical
// convention: clamped, not wrapping.
func TestJKMoveSelectionAndClampAtEnds(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{servers: []Server{{Name: "a"}, {Name: "b"}, {Name: "c"}}})
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
	fetch := func() ([]Server, error) { calls++; return nil, nil }
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

func TestSelectionResetsWhenServersShrink(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{servers: []Server{{Name: "a"}, {Name: "b"}, {Name: "c"}}})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	if m.selected != 2 {
		t.Fatalf("test setup: selected = %d, want 2", m.selected)
	}

	next, _ = m.Update(fetchResultMsg{servers: []Server{{Name: "a"}}})
	m = next.(Model)
	if m.selected != 0 {
		t.Fatalf("selected = %d after the list shrank to 1 row, want 0", m.selected)
	}
}
