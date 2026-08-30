package library

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testRows() []ItemRow {
	return []ItemRow{
		{ID: "it-aaaa", Kind: "parameter", Weight: "hard", Status: "acknowledged", ResolvedTo: "scheduler=x", BodySnippet: "never do X"},
		{ID: "it-bbbb", Kind: "question", Weight: "hard", Status: "open", BodySnippet: "what about Y?"},
	}
}

func fakeFetch(rows []ItemRow, err error) Fetcher {
	return func(View, string, string) ([]ItemRow, error) { return rows, err }
}

// TestFetchResultPopulatesRows drives Update directly (cheaper than a full
// teatest.Program, the same two-tier discipline every other package in
// this module uses).
func TestFetchResultPopulatesRows(t *testing.T) {
	m := New(nil, nil, nil)
	next, _ := m.Update(fetchResultMsg{rows: testRows()})
	m = next.(Model)

	if len(m.rows) != 2 || m.rows[0].ID != "it-aaaa" {
		t.Fatalf("rows = %+v, want testRows() unchanged", m.rows)
	}
}

// TestFetchErrorRendersVisibly is this pane's own "blind, not quiet" case
// -- the hard requirement w5c.md states explicitly: a missing/unreadable
// ledger must be a VISIBLE error, never an empty list.
func TestFetchErrorRendersVisibly(t *testing.T) {
	m := New(fakeFetch(nil, errors.New("no ledger found")), nil, nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("no ledger found")})
	m = next.(Model)
	out := m.View()
	if !strings.Contains(out, "no ledger found") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
	if strings.Contains(out, "(no items") {
		t.Fatalf("an unreadable ledger must never render as an empty list:\n%s", out)
	}
}

// TestNilFetchRendersVisibleConfigError is w5c.md's own hard requirement,
// pinned at the Model level rather than just cmd/estate's: a Model built
// with fetch == nil (no -ledger, ever) must render a visible error on
// screen from the FIRST frame, before any fetchResultMsg could possibly
// arrive -- Init() never issues a fetch for a nil Fetcher, so m.fetchErr
// alone would stay nil forever and silently read as "(no items)".
func TestNilFetchRendersVisibleConfigError(t *testing.T) {
	m := New(nil, nil, nil)
	out := m.View()
	if !strings.Contains(out, "no ledger configured") {
		t.Fatalf("View() with a nil Fetcher did not render a config error:\n%s", out)
	}
	if strings.Contains(out, "(no items") {
		t.Fatalf("an unconfigured ledger must never render as an empty list:\n%s", out)
	}
}

// TestEmptyResultWithNoErrorRendersEmptyNotError is the contrast: a real,
// successful fetch that genuinely found zero rows (a narrow filter with no
// matches) must read as "no rows", not as a blindness error -- the two
// must stay visibly distinguishable.
func TestEmptyResultWithNoErrorRendersEmptyNotError(t *testing.T) {
	m := New(fakeFetch(nil, nil), nil, nil)
	next, _ := m.Update(fetchResultMsg{rows: nil, err: nil})
	m = next.(Model)
	out := m.View()
	if !strings.Contains(out, "no items match") {
		t.Fatalf("empty-but-successful fetch did not render the empty-list message:\n%s", out)
	}
	if strings.Contains(out, "could not read") {
		t.Fatalf("a real empty result must not render as a read error:\n%s", out)
	}
}

// TestVKeyCyclesView is the [v] key's own contract: it changes m.view AND
// triggers a re-fetch, never just flips a label nothing re-queries.
func TestVKeyCyclesView(t *testing.T) {
	calls := 0
	var gotView View
	fetch := func(v View, weight, status string) ([]ItemRow, error) {
		calls++
		gotView = v
		return nil, nil
	}
	m := New(fetch, nil, nil)
	if m.view != ViewLiveParameters {
		t.Fatalf("default view = %q, want live_parameters", m.view)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("v")})
	m = next.(Model)
	if m.view != ViewOpenQuestions {
		t.Fatalf("after [v], view = %q, want open_questions", m.view)
	}
	if cmd == nil {
		t.Fatal("[v] did not return a re-fetch command")
	}
	cmd()
	if calls != 1 || gotView != ViewOpenQuestions {
		t.Fatalf("re-fetch did not use the new view: calls=%d gotView=%q", calls, gotView)
	}
}

// TestFKeyCyclesWeightFilterAndReFetches mirrors TestVKeyCyclesView for
// the weight filter.
func TestFKeyCyclesWeightFilterAndReFetches(t *testing.T) {
	var gotWeight string
	fetch := func(v View, weight, status string) ([]ItemRow, error) {
		gotWeight = weight
		return nil, nil
	}
	m := New(fetch, nil, nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	m = next.(Model)
	if m.weight != "hard" {
		t.Fatalf("after [f], weight = %q, want hard", m.weight)
	}
	cmd()
	if gotWeight != "hard" {
		t.Fatalf("re-fetch weight arg = %q, want hard", gotWeight)
	}
}

// TestXKeyCyclesStatusFilterAndReFetches mirrors the above for status.
func TestXKeyCyclesStatusFilterAndReFetches(t *testing.T) {
	var gotStatus string
	fetch := func(v View, weight, status string) ([]ItemRow, error) {
		gotStatus = status
		return nil, nil
	}
	m := New(fetch, nil, nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	if m.status != "open" {
		t.Fatalf("after [x], status = %q, want open", m.status)
	}
	cmd()
	if gotStatus != "open" {
		t.Fatalf("re-fetch status arg = %q, want open", gotStatus)
	}
}

// TestEnterOpensAnItemAndCachesIt is the progressive-disclosure contract
// at the Model level: [enter] triggers exactly one DetailLoader call for
// the selected row, and a second [enter] on the SAME row (after it is
// cached) does not call it again -- internal/knowledge's own test, same
// shape.
func TestEnterOpensAnItemAndCachesIt(t *testing.T) {
	calls := 0
	loadDetail := func(id string) (ItemDetail, error) {
		calls++
		return ItemDetail{ID: id, Body: "full body text"}, nil
	}
	m := New(fakeFetch(testRows(), nil), loadDetail, nil)
	next, _ := m.Update(fetchResultMsg{rows: testRows()})
	m = next.(Model)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	if m.mode != modeReading {
		t.Fatal("[enter] did not switch to reading mode")
	}
	if cmd == nil {
		t.Fatal("[enter] on an unopened row returned a nil cmd, want a detail load")
	}
	msg := cmd()
	next, _ = m.Update(msg)
	m = next.(Model)
	if calls != 1 {
		t.Fatalf("DetailLoader called %d times, want 1", calls)
	}
	if !strings.Contains(m.View(), "full body text") {
		t.Fatalf("View() missing the loaded body:\n%s", m.View())
	}

	// Second [enter] on the same (still-selected) row: no second load.
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("esc")})
	m = next.(Model)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("[enter] on an already-cached row returned a non-nil cmd, want no re-read")
	}
	if calls != 1 {
		t.Fatalf("DetailLoader called %d times after re-opening a cached row, want still 1", calls)
	}
}

// TestCountResultPopulatesLegend proves possibility_count reaches the
// screen, independent of whatever View is active.
func TestCountResultPopulatesLegend(t *testing.T) {
	m := New(nil, nil, nil)
	next, _ := m.Update(countResultMsg{count: 931})
	m = next.(Model)
	if !strings.Contains(m.View(), "931") {
		t.Fatalf("View() missing possibility_count:\n%s", m.View())
	}
}

// TestStaleDetailLoadIsDropped is internal/knowledge's own guard, applied
// here: a detailResultMsg for an id that is no longer m.opening (a second
// open, or a filter change, superseded it) must never overwrite what is
// actually on screen.
func TestStaleDetailLoadIsDropped(t *testing.T) {
	m := New(fakeFetch(testRows(), nil), nil, nil)
	next, _ := m.Update(fetchResultMsg{rows: testRows()})
	m = next.(Model)
	m.opening = "it-current"

	next, _ = m.Update(detailResultMsg{id: "it-stale", detail: ItemDetail{ID: "it-stale", Body: "STALE"}})
	m = next.(Model)
	if _, cached := m.cache["it-stale"]; cached {
		t.Fatal("a stale detailResultMsg was cached -- it should have been dropped")
	}
}
