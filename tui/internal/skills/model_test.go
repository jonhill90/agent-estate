package skills

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFetchResultPopulatesSkills drives Update directly (cheaper than a
// full teatest.Program, the same two-tier discipline internal/agents' own
// test suite uses) to confirm a fetchResultMsg actually reaches View().
func TestFetchResultPopulatesSkills(t *testing.T) {
	m := New(nil)
	name := "adopt-or-build"
	next, _ := m.Update(fetchResultMsg{skills: []Skill{{Dir: name, Name: name, Description: "decide."}}})
	m = next.(Model)

	if len(m.Skills()) != 1 {
		t.Fatalf("Skills() = %+v, want one skill", m.Skills())
	}
	if !strings.Contains(m.View(), name) {
		t.Fatalf("View() missing the fetched skill:\n%s", m.View())
	}
	if !strings.Contains(m.View(), unknown) {
		t.Fatalf("View() does not render the unknown eval/invocation columns:\n%s", m.View())
	}
}

// TestFetchErrorRendersVisibly is this pane's own "blind, not quiet" case,
// same as internal/agents' identical test: a failed scan must not render
// "(no skills)" as if the directory were genuinely empty.
func TestFetchErrorRendersVisibly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("permission denied")})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "! skills unavailable") || !strings.Contains(out, "permission denied") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
}

// TestEKeySetsANamedNoticeRatherThanSilentlyDoingNothing is S8's own
// design note, made concrete: "[e]" must be visibly a documented no-op.
func TestEKeySetsANamedNoticeRatherThanSilentlyDoingNothing(t *testing.T) {
	m := New(nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("[e] returned a non-nil cmd, want nil (no eval harness to call)")
	}
	if !strings.Contains(m.View(), "eval loop not built yet") {
		t.Fatalf("[e] did not render a visible notice:\n%s", m.View())
	}
}

// TestJKMoveSelectionAndClampAtEnds covers the list-navigation half of
// this pane -- clamped, not wrapping, matching the model.go implementation
// (unlike internal/agents/internal/chat's own wrap-around moveSelection).
func TestJKMoveSelectionAndClampAtEnds(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{skills: []Skill{{Dir: "a"}, {Dir: "b"}, {Dir: "c"}}})
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

// TestRKeyRefetches matches internal/agents' identical convention.
func TestRKeyRefetches(t *testing.T) {
	calls := 0
	fetch := func() ([]Skill, error) { calls++; return nil, nil }
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

// TestQuittingRendersNothing matches every other pane's own convention.
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

// TestSelectionResetsWhenSkillsShrink guards against a stale selection
// index surviving a refetch that returns fewer rows (e.g. a skill deleted
// between polls).
func TestSelectionResetsWhenSkillsShrink(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{skills: []Skill{{Dir: "a"}, {Dir: "b"}, {Dir: "c"}}})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	if m.selected != 2 {
		t.Fatalf("test setup: selected = %d, want 2", m.selected)
	}

	next, _ = m.Update(fetchResultMsg{skills: []Skill{{Dir: "a"}}})
	m = next.(Model)
	if m.selected != 0 {
		t.Fatalf("selected = %d after the list shrank to 1 row, want 0", m.selected)
	}
}
