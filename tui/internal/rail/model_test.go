package rail

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/cost"
	"github.com/jonhill90/keelson/internal/lane"
)

var errCostFetchFailed = errors.New("ccusage: exec: executable file not found in $PATH")

func TestDigitKeySelectsGlyphSet(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	if m.glyphSet != 0 {
		t.Fatalf("default glyphSet must be 0 (lane.Default) with no selection made, got %d", m.glyphSet)
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	m = updated.(Model)
	if m.glyphSet != 1 {
		t.Fatalf("pressing '2' should select lane.Variants[1], got glyphSet=%d", m.glyphSet)
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

// TestCostRefreshIntervalIsFiveMinutes pins the rail's cost-fetch cadence
// to internal/cost's own refreshInterval (5m). agent-tui#4 wiring the cost
// line into the rail must not poll ccusage any harder just because the
// line is now always visible -- see costRefreshInterval's doc comment.
func TestCostRefreshIntervalIsFiveMinutes(t *testing.T) {
	if costRefreshInterval != 5*time.Minute {
		t.Errorf("costRefreshInterval = %s, want 5m", costRefreshInterval)
	}
}

// TestPlainNewHasNoCostLine is New()'s backward-compatibility contract: no
// costFetch means no cost line, exactly as before NewWithCost existed.
func TestPlainNewHasNoCostLine(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	if strings.Contains(m.View(), "cost:") {
		t.Fatalf("New() (no cost fetcher) rendered a cost line:\n%s", m.View())
	}
}

// TestNewWithCostRendersLineWithNoFlag is the wiring-level check for
// agent-tui#4's "glanceable, always there, no command to run": the rail
// built the way cmd/agent-tui's default path builds it (NewWithCost) must
// show cost data with no key pressed and no flag needed.
func TestNewWithCostRendersLineWithNoFlag(t *testing.T) {
	m := NewWithCost(
		func() ([]lane.Lane, error) { return nil, nil },
		func() (cost.Snapshot, error) { return cost.Snapshot{}, nil },
	)
	model, _ := m.Update(costFetchResultMsg{
		snap: cost.Compose([]cost.Harness{{
			Name:  "codex",
			Cost:  cost.KnownFigure(8.49),
			Limit: cost.Limit{Known: true, Percent: 92, Label: "plan quota", Warn: true},
		}}, cost.Limit{}),
	})
	m = model.(Model)
	out := m.View()
	if !strings.Contains(out, "cost:") {
		t.Fatalf("NewWithCost's View() has no cost section:\n%s", out)
	}
	if !strings.Contains(out, "92%") {
		t.Fatalf("NewWithCost's View() does not show codex's limit pressure:\n%s", out)
	}
}

// TestCostFetchErrorRendersUnknownNeverStale mirrors internal/cost's own
// blindness test at the rail level: a failed cost fetch must clear any
// prior real snapshot and show "unknown", never stale-but-real numbers or
// a bare 0.
func TestCostFetchErrorRendersUnknownNeverStale(t *testing.T) {
	m := NewWithCost(
		func() ([]lane.Lane, error) { return nil, nil },
		func() (cost.Snapshot, error) { return cost.Snapshot{}, nil },
	)
	model, _ := m.Update(costFetchResultMsg{
		snap: cost.Compose([]cost.Harness{{Name: "claude", Cost: cost.KnownFigure(412.98)}}, cost.Limit{}),
	})
	m = model.(Model)
	if !strings.Contains(m.View(), "$412.98") {
		t.Fatalf("setup: expected the healthy fetch's real cost in View():\n%s", m.View())
	}

	model, _ = m.Update(costFetchResultMsg{err: errCostFetchFailed})
	m = model.(Model)
	out := m.View()
	if strings.Contains(out, "412.98") {
		t.Errorf("View() still shows the previous fetch's real cost after a failed cost fetch:\n%s", out)
	}
	if !strings.Contains(out, "unknown") {
		t.Errorf("View() does not show \"unknown\" after a failed cost fetch:\n%s", out)
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
