package nav

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestNewDefaultsActiveToFirstTopLevelItem(t *testing.T) {
	m := New()
	if got, want := m.Active(), "home"; got != want {
		t.Errorf("Active() = %q, want %q", got, want)
	}
}

// TestWithActiveHighlightsAndAutoExpands covers SPEC-shell.md S2's own
// line: "the group containing the active route auto-expands." Before
// WithActive, Build's children are collapsed (Skills is not visible);
// after, Skills is visible and Home no longer carries the highlight.
func TestWithActiveHighlightsAndAutoExpands(t *testing.T) {
	m := New()
	before := m.View()
	if strings.Contains(before, "Skills") {
		t.Fatalf("Build group rendered expanded before any active route pointed into it:\n%s", before)
	}

	m = m.WithActive("skills")
	after := m.View()
	if !strings.Contains(after, "Skills") {
		t.Fatalf("Build group did not auto-expand for active=skills:\n%s", after)
	}
	if m.Active() != "skills" {
		t.Errorf("Active() = %q, want %q", m.Active(), "skills")
	}
}

// TestWithActiveDoesNotReCollapseAPreviouslyOpenedGroup matches hill90's
// own Sidebar.tsx: moving the active route OUT of a group must not slam
// that group shut if a user had it open.
func TestWithActiveDoesNotReCollapseAPreviouslyOpenedGroup(t *testing.T) {
	m := New().WithActive("skills")
	m = m.WithActive("home")
	if !strings.Contains(m.View(), "Skills") {
		t.Fatalf("Build group collapsed after active route moved away from it")
	}
}

// TestBKeyTogglesIconsOnly drives Update directly (not via teatest) as a
// second, cheaper proof alongside model_teatest_test.go's pty-level one:
// [b] must both narrow Width() and drop labels from View().
func TestBKeyTogglesIconsOnly(t *testing.T) {
	m := New()
	if m.Width() != fullWidth {
		t.Fatalf("Width() = %d before any [b] press, want fullWidth (%d)", m.Width(), fullWidth)
	}
	if !strings.Contains(m.View(), "Home") {
		t.Fatalf("expected label \"Home\" in full-width view:\n%s", m.View())
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = next.(Model)

	if m.Width() != iconWidth {
		t.Fatalf("Width() = %d after [b], want iconWidth (%d)", m.Width(), iconWidth)
	}
	if strings.Contains(m.View(), "Home") {
		t.Fatalf("label \"Home\" still present in icons-only view:\n%s", m.View())
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = next.(Model)
	if m.Width() != fullWidth {
		t.Fatalf("Width() = %d after second [b], want fullWidth (%d)", m.Width(), fullWidth)
	}
}
