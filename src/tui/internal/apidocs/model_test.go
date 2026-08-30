package apidocs

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func fakeRef() Reference {
	return Reference{
		Title:      "Fixture API",
		Version:    "9.9.9",
		OpenAPI:    "3.0.3",
		SourcePath: "/fake/openapi.yaml",
		PathCount:  2,
		Endpoints: []Endpoint{
			{Method: "GET", Path: "/agents", Summary: "List agents", Auth: boolPtr(true)},
			{Method: "GET", Path: "/health", Summary: "Health check", Auth: boolPtr(false)},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

func fetched(m Model, ref Reference, err error) Model {
	next, _ := m.Update(fetchResultMsg{ref: ref, err: err})
	return next.(Model)
}

func key(m Model, s string) Model {
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)})
	return next.(Model)
}

func TestViewRendersEveryOperationFromTheDocument(t *testing.T) {
	m := fetched(New(func() (Reference, error) { return fakeRef(), nil }), fakeRef(), nil)
	out := m.View()
	for _, want := range []string{"Fixture API", "/fake/openapi.yaml", "/agents", "/health", "List agents", "2 operations across 2 paths"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q:\n%s", want, out)
		}
	}
}

// TestViewShowsAuthsThreeStates: the table must not report an operation as
// public when the document merely says nothing about it.
func TestViewShowsAuthsThreeStates(t *testing.T) {
	ref := fakeRef()
	ref.Endpoints = append(ref.Endpoints, Endpoint{Method: "DELETE", Path: "/silent", Summary: "no security key"})
	out := fetched(New(func() (Reference, error) { return ref, nil }), ref, nil).View()
	for _, want := range []string{"yes", "public", "default"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing auth label %q:\n%s", want, out)
		}
	}
}

// TestUnconfiguredNamesTheRealSource is the brief's own rule for a
// destination with nothing behind it: say what would back it and what is
// missing. "not built yet" would be strictly less useful, and this pane's
// unconfigured state must never regress to it.
func TestUnconfiguredNamesTheRealSource(t *testing.T) {
	out := New(nil).View()
	for _, want := range []string{"no spec configured", "-openapi", "HILL90_APP_REPO", "services/api/src/openapi/openapi.yaml"} {
		if !strings.Contains(out, want) {
			t.Errorf("unconfigured View() missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "not built yet") {
		t.Errorf("unconfigured View() reads as an unbuilt stub:\n%s", out)
	}
}

func TestUnconfiguredModelDoesNotFetch(t *testing.T) {
	if cmd := New(nil).Init(); cmd != nil {
		t.Error("Init() on an unconfigured model returned a command")
	}
}

// TestFetchErrorIsVisible: a spec that could not be read must say so, not
// render an empty table that reads as "this API has no endpoints".
func TestFetchErrorIsVisible(t *testing.T) {
	m := fetched(New(func() (Reference, error) { return Reference{}, errors.New("read /nope: no such file") }),
		Reference{}, errors.New("read /nope: no such file"))
	out := m.View()
	if !strings.Contains(out, "could not read the spec") || !strings.Contains(out, "/nope") {
		t.Errorf("View() hides the read failure:\n%s", out)
	}
}

func TestFilterNarrowsTheTable(t *testing.T) {
	m := fetched(New(func() (Reference, error) { return fakeRef(), nil }), fakeRef(), nil)
	m = key(m, "/")
	m = key(m, "health")
	if got := m.Visible(); len(got) != 1 || got[0].Path != "/health" {
		t.Fatalf("Visible() after filtering = %+v, want just /health", got)
	}
	out := m.View()
	if strings.Contains(out, "/agents") {
		t.Errorf("filtered View() still lists /agents:\n%s", out)
	}
}

func TestEscClearsTheFilter(t *testing.T) {
	m := fetched(New(func() (Reference, error) { return fakeRef(), nil }), fakeRef(), nil)
	m = key(m, "/")
	m = key(m, "health")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if len(m.Visible()) != 2 {
		t.Fatalf("esc did not clear the filter: %d visible", len(m.Visible()))
	}
}

// TestCtrlCQuitsFromFilterMode is agent-tui#22's trap: a mode that eats
// every key, quit included.
func TestCtrlCQuitsFromFilterMode(t *testing.T) {
	m := fetched(New(func() (Reference, error) { return fakeRef(), nil }), fakeRef(), nil)
	m = key(m, "/")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c in filter mode returned no command -- quit was swallowed")
	}
}
