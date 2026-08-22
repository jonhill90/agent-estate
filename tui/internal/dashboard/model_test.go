package dashboard

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestFetchResultPopulatesStats drives Update directly (cheaper than a full
// teatest.Program, the same two-tier discipline internal/agents/internal/
// skills already use) to confirm a fetchResultMsg actually reaches View().
func TestFetchResultPopulatesStats(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{stats: Stats{
		AgentsByState: map[string]int{"busy": 2, "free": 1},
		AgentsKnown:   true,
		OpenPRs:       KnownCount(7),
		MergedToday:   KnownCount(3),
		SpendToday:    KnownUSD(12.34),
		VaultFacts:    KnownCount(142),
	}})
	m = next.(Model)

	out := m.View()
	for _, want := range []string{"3 total", "busy:2", "free:1", "7", "3", "$12.34", "142"} {
		if !strings.Contains(out, want) {
			t.Fatalf("View() missing %q:\n%s", want, out)
		}
	}
}

// TestUnknownFieldsRenderUnknownIndependently is Stats' own doc comment
// made concrete: one source failing must not blank the others, and must
// never render as "0."
func TestUnknownFieldsRenderUnknownIndependently(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{stats: Stats{
		AgentsKnown: false,
		OpenPRs:     KnownCount(0), // a REAL zero -- must render "0", not "unknown"
		// MergedToday/SpendToday/VaultFacts left zero-value -- unknown
	}})
	m = next.(Model)

	out := m.View()
	if strings.Count(out, unknown) != 4 {
		t.Fatalf("want exactly 4 unknown fields (agents, merged, spend, vault), got:\n%s", out)
	}
	if !strings.Contains(out, "OPEN PRS") || strings.Contains(out, "OPEN PRS      unknown") {
		t.Fatalf("a real zero (OpenPRs) must render \"0\", not \"unknown\":\n%s", out)
	}
}

// TestFetchErrorRendersVisibly matches every other pane's own "blind, not
// quiet" case: a total fetch failure must not silently show stale/zero
// figures as if they were current.
func TestFetchErrorRendersVisibly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("boom")})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "! dashboard unavailable") || !strings.Contains(out, "boom") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
}

// TestRKeyRefetches matches internal/agents/internal/skills' identical
// convention.
func TestRKeyRefetches(t *testing.T) {
	calls := 0
	fetch := func() (Stats, error) { calls++; return Stats{}, nil }
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

// TestNotFetchedYetRendersHonestly guards the first frame, before Init's
// own fetch has resolved -- must not claim a staleness age it does not
// have.
func TestNotFetchedYetRendersHonestly(t *testing.T) {
	m := New(nil)
	if !strings.Contains(m.View(), "not fetched yet") {
		t.Fatalf("initial View() does not say \"not fetched yet\":\n%s", m.View())
	}
}
