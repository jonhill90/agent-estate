package gallery

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestNewStartsAtFirstState(t *testing.T) {
	m := New()
	if m.offset != 0 {
		t.Fatalf("New() offset = %d, want 0", m.offset)
	}
	if len(m.rows) == 0 {
		t.Fatal("New() has no rows")
	}
}

func TestScrollDownAdvancesOffset(t *testing.T) {
	m := New()
	model, _ := m.Update(keyMsg("j"))
	m = model.(Model)
	if m.offset != 1 {
		t.Fatalf("offset after one down-scroll = %d, want 1", m.offset)
	}
}

func TestScrollUpNeverGoesNegative(t *testing.T) {
	m := New()
	model, _ := m.Update(keyMsg("k"))
	m = model.(Model)
	if m.offset != 0 {
		t.Fatalf("offset after scrolling up from 0 = %d, want 0 (must not go negative)", m.offset)
	}
}

func TestScrollDownNeverPassesLastState(t *testing.T) {
	m := New()
	for i := 0; i < len(m.rows)+10; i++ {
		model, _ := m.Update(keyMsg("j"))
		m = model.(Model)
	}
	if m.offset != len(m.rows)-1 {
		t.Fatalf("offset after scrolling far past the end = %d, want %d (last valid index)", m.offset, len(m.rows)-1)
	}
}

func TestGoToEndAndTop(t *testing.T) {
	m := New()
	model, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	m = model.(Model)
	if m.offset != len(m.rows)-1 {
		t.Fatalf("offset after 'G' = %d, want %d", m.offset, len(m.rows)-1)
	}
	model, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = model.(Model)
	if m.offset != 0 {
		t.Fatalf("offset after 'g' = %d, want 0", m.offset)
	}
}

func TestQuitSetsQuittingAndBlanksView(t *testing.T) {
	m := New()
	m.width, m.height = 80, 30
	model, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = model.(Model)
	if !m.quitting {
		t.Fatal("ctrl+c did not set quitting")
	}
	if cmd == nil {
		t.Fatal("ctrl+c did not return tea.Quit")
	}
	if m.View() != "" {
		t.Fatalf("View() after quitting = %q, want empty", m.View())
	}
}

// TestViewFlagsUnrenderableGlyphs is agent-tui#11 acceptance item 3 at the
// Model level (rows_test.go's TestRenderFlagsUnrenderableGlyphsInOutput
// covers the pure Render path): the interactive screen Jon actually looks
// at must contain the flag, not just the underlying data function.
func TestViewFlagsUnrenderableGlyphs(t *testing.T) {
	m := New()
	m.width, m.height = 100, 60 // tall enough to include a nerd cell in the first page
	out := m.View()
	if !strings.Contains(out, "[NF]") {
		t.Fatalf("View() does not contain \"[NF]\" anywhere in a tall-enough viewport:\n%s", out)
	}
}

func TestViewShowsSignalAsFirstVariantColumn(t *testing.T) {
	m := New()
	m.width, m.height = 100, 30
	out := m.View()
	if !strings.Contains(out, "signal") {
		t.Fatalf("View() does not mention the \"signal\" variant, which should be the first column shown:\n%s", out)
	}
}
