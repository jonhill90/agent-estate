package admin

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func boolPtr(b bool) *bool { return &b }

func TestUpdate_FetchResultPopulatesSnapshotAndRendersAllFiveSections(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{snap: Snapshot{
		Services:     []Service{{Name: "mcp-vibes-server", Image: "vibes:latest", Status: "Up 3 days"}},
		Dependencies: []Dependency{{Name: "docker", Reachable: boolPtr(true)}},
		Settings:     []Setting{{Name: "theme", Value: "Signal (default)"}},
		ProfilesNote: noProfilesNote,
		UsersNote:    noUsersNote,
	}})
	m = next.(Model)

	out := m.View()
	for _, want := range []string{"Services", "mcp-vibes-server", "Dependencies", "docker", "yes", "Settings", "Signal (default)", "Profiles", noProfilesNote, "Users", noUsersNote} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q:\n%s", want, out)
		}
	}
}

func TestUpdate_FetchErrorRendersVisibly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("boom")})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "! admin snapshot unavailable") || !strings.Contains(out, "boom") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
}

func TestUpdate_PerSectionErrorRendersVisibly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{snap: Snapshot{ServicesErr: errors.New("docker daemon not running")}})
	m = next.(Model)

	out := m.View()
	if !strings.Contains(out, "docker daemon not running") {
		t.Fatalf("ServicesErr not rendered:\n%s", out)
	}
	if strings.Contains(out, "(no containers)") {
		t.Fatalf("rendered \"(no containers)\" alongside a Services error -- these must not both appear:\n%s", out)
	}
}

func TestUpdate_UnknownReachabilityRendersDistinctlyFromNo(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{snap: Snapshot{Dependencies: []Dependency{
		{Name: "found", Reachable: boolPtr(true)},
		{Name: "missing", Reachable: boolPtr(false)},
		{Name: "never-checked", Reachable: nil},
	}}})
	m = next.(Model)

	out := m.View()
	for _, want := range []string{"yes", "no", unknown} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing reachability marker %q:\n%s", want, out)
		}
	}
}

func TestUpdate_RKeyRefetches(t *testing.T) {
	calls := 0
	fetch := func() (Snapshot, error) { calls++; return Snapshot{}, nil }
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

func TestUpdate_QQuitsAndRendersNothing(t *testing.T) {
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
