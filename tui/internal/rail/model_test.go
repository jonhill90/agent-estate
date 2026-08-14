package rail

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/lane"
)

func TestDigitKeySelectsGlyphSet(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	if m.glyphSet != 0 {
		t.Fatalf("default glyphSet must be 0 (lane.Default) with no selection made, got %d", m.glyphSet)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("3")})
	m = updated.(Model)
	if m.glyphSet != 2 {
		t.Fatalf("pressing '3' should select lane.Variants[2], got glyphSet=%d", m.glyphSet)
	}
}

func TestDigitKeyBeyondVariantCountIsIgnored(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	tooHigh := len(lane.Variants) + 1
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune('0' + tooHigh)}})
	m2 := updated.(Model)
	if m2.glyphSet != m.glyphSet {
		t.Fatalf("a digit with no corresponding variant must not change glyphSet, got %d", m2.glyphSet)
	}
}

func TestViewNeverEmptyAcrossAllVariants(t *testing.T) {
	for i := range lane.Variants {
		m := New(func() ([]lane.Lane, error) { return nil, nil })
		m.glyphSet = i
		m.lanes = []lane.Lane{
			{Window: 1, WindowID: "@0", Name: "w0", Command: "claude", State: "stale", IdleSeconds: 5},
			{Window: 2, WindowID: "@1", Name: "w1", Command: "claude", State: "menu-blocked", IdleSeconds: 5},
			{Window: 3, WindowID: "@2", Name: "w2", Command: "claude", State: "unsent", IdleSeconds: 5},
		}
		out := m.View()
		if out == "" {
			t.Fatalf("variant %q rendered an empty view", lane.Variants[i].ID)
		}
	}
}
