package knowledgeindex

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelShowsFetchErrorHonestlyNotAsEmptyList(t *testing.T) {
	fetch := func() (Result, error) { return Result{}, errors.New("index unreadable") }
	m := New(fetch)
	next, _ := m.Update(fetchResultMsg{err: errors.New("index unreadable")})
	m = next.(Model)
	if m.fetchErr == nil {
		t.Fatal("Model dropped a fetch error")
	}
	if got := m.View(); !containsStr(got, "index unreadable") {
		t.Fatalf("View() does not surface the fetch error:\n%s", got)
	}
}

func TestModelUpdateFromFetchPopulatesItemsAndSources(t *testing.T) {
	res := Result{
		Sources: []SourceResult{{Name: "github-stars", OK: true, Count: 2}},
		Items: []Item{
			{ID: "1", Source: "github-stars", Tier1: "a/one"},
			{ID: "2", Source: "github-stars", Tier1: "a/two"},
		},
	}
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{res: res})
	m = next.(Model)
	if len(m.Items()) != 2 {
		t.Fatalf("Items() = %d, want 2", len(m.Items()))
	}
	if len(m.Sources()) != 1 || !m.Sources()[0].OK {
		t.Fatalf("Sources() = %+v", m.Sources())
	}
}

func TestModelEnterOpensDetailAndEscReturnsToList(t *testing.T) {
	res := Result{Items: []Item{{ID: "1", Source: "github-stars", Tier1: "a/one", Tier2: "desc", Tier3: "more"}}}
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{res: res})
	m = next.(Model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.mode != modeDetail {
		t.Fatal("[enter] did not open detail mode")
	}
	if got := m.View(); !containsStr(got, "Tier 2: desc") {
		t.Fatalf("detail view does not show Tier2:\n%s", got)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("[esc] did not return to list mode")
	}
}

func TestModelEmptyIndexRendersHonestEmptyState(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{res: Result{}})
	m = next.(Model)
	if got := m.View(); !containsStr(got, "no items") {
		t.Fatalf("View() does not name the empty state honestly:\n%s", got)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
