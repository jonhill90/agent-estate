package agents

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/lane"
)

// TestFetchResultPopulatesRows drives Update directly (cheaper than a full
// teatest.Program, same two-tier discipline internal/nav's own test suite
// uses) to confirm a fetchResultMsg actually reaches View() through Rows().
func TestFetchResultPopulatesRows(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{sessions: []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy"}}},
	}})
	m = next.(Model)

	rows := m.Rows()
	if len(rows) != 1 || rows[0].ID != "s:w1" {
		t.Fatalf("Rows() = %+v, want one row \"s:w1\"", rows)
	}
	if !strings.Contains(m.View(), "s:w1") {
		t.Fatalf("View() missing the fetched row:\n%s", m.View())
	}
}

// TestViewRendersModeColumnAsLocalNeverUnknown is SPEC-shell.md S12's own
// distinction from Model/Cost, made visible: the MODE column must show
// "local," never this package's "unknown" placeholder.
func TestViewRendersModeColumnAsLocalNeverUnknown(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{sessions: []lane.Session{
		{Name: "s", Lanes: []lane.Lane{{Name: "w1", State: "busy"}}},
	}})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "local") {
		t.Fatalf("View() does not render \"local\" in the MODE column:\n%s", out)
	}
}

// TestFetchErrorRendersVisibly is this pane's own "blind, not quiet" case
// (AGENTS.md: never look like a healthy, empty estate when the read
// failed) -- a fetchResultMsg carrying an error must show it, not render
// "(no agents)" as if there genuinely were none.
func TestFetchErrorRendersVisibly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("mcp: no supervisor")})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "! sessions unavailable") || !strings.Contains(out, "no supervisor") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
}

// TestNKeySetsANamedNoticeRatherThanSilentlyDoingNothing is SPEC-shell.md
// S6's "read-only until S7" line, made concrete: [n] must be visibly a
// documented no-op.
func TestNKeySetsANamedNoticeRatherThanSilentlyDoingNothing(t *testing.T) {
	m := New(nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("[n] returned a non-nil cmd, want nil (read-only until S7)")
	}
	if !strings.Contains(m.View(), "not built yet (S7)") {
		t.Fatalf("[n] did not render a visible notice:\n%s", m.View())
	}
}

// TestRKeyRefetches confirms manual refresh actually asks Fetcher again --
// board/rail/cost's own "[r] refresh" convention.
func TestRKeyRefetches(t *testing.T) {
	calls := 0
	fetch := func() ([]lane.Session, error) { calls++; return nil, nil }
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

// TestQuittingRendersNothing matches internal/cost.Model's own convention.
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
