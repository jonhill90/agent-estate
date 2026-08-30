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

// TestWithSection_NarrowsToOneSectionOnly is agent-tui#150's own
// regression test: five distinct routes must produce five distinct
// content panes, not one composite pane repeated five times with only a
// title differing.
func TestWithSection_NarrowsToOneSectionOnly(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{snap: Snapshot{
		Services:     []Service{{Name: "mcp-vibes-server", Image: "vibes:latest", Status: "Up 3 days"}},
		Dependencies: []Dependency{{Name: "docker", Reachable: boolPtr(true)}},
		Settings:     []Setting{{Name: "theme", Value: "Signal (default)"}},
		ProfilesNote: noProfilesNote,
		UsersNote:    noUsersNote,
	}})
	m = next.(Model)

	sections := map[string][]string{
		SectionServices:     {"Services", "mcp-vibes-server"},
		SectionDependencies: {"Dependencies", "docker"},
		SectionSettings:     {"Settings", "Signal (default)"},
		SectionProfiles:     {"Profiles", noProfilesNote},
		SectionUsers:        {"Users", noUsersNote},
	}
	excluded := map[string][]string{
		SectionServices:     {"mcp-vibes-server"},
		SectionDependencies: {"docker"},
		SectionSettings:     {"Signal (default)"},
		SectionProfiles:     {noProfilesNote},
		SectionUsers:        {noUsersNote},
	}

	rendered := make(map[string]string, len(sections))
	for id := range sections {
		scoped := m.WithSection(id)
		if got := scoped.Section(); got != id {
			t.Errorf("Section() after WithSection(%q) = %q", id, got)
		}
		rendered[id] = scoped.View()
		for _, want := range sections[id] {
			if !strings.Contains(rendered[id], want) {
				t.Errorf("WithSection(%q).View() missing %q:\n%s", id, want, rendered[id])
			}
		}
	}

	// Every OTHER section's own distinguishing content must be absent --
	// this is the guard the brief calls out: a title-only difference would
	// still pass the "contains its own want strings" loop above.
	for id, out := range rendered {
		for otherID, marks := range excluded {
			if otherID == id {
				continue
			}
			for _, mark := range marks {
				if strings.Contains(out, mark) {
					t.Errorf("WithSection(%q).View() leaked %q from section %q:\n%s", id, mark, otherID, out)
				}
			}
		}
	}

	// All five must actually differ from each other -- not merely from a
	// hand-picked marker.
	seen := map[string]string{}
	for id, out := range rendered {
		if prior, ok := seen[out]; ok {
			t.Errorf("WithSection(%q) and WithSection(%q) rendered byte-identical panes", id, prior)
		}
		seen[out] = id
	}
}

// TestWithSection_UnknownIDFallsBackToAllFive pins WithSection's documented
// fallback: an id this package does not recognize renders every section,
// the same as New()'s own zero value, rather than a blank pane.
func TestWithSection_UnknownIDFallsBackToAllFive(t *testing.T) {
	m := New(nil)
	next, _ := m.Update(fetchResultMsg{snap: Snapshot{ProfilesNote: noProfilesNote, UsersNote: noUsersNote}})
	m = next.(Model).WithSection("not-a-real-route")

	out := m.View()
	for _, want := range []string{"Services", "Dependencies", "Settings", "Profiles", "Users"} {
		if !strings.Contains(out, want) {
			t.Errorf("WithSection(unknown).View() missing %q:\n%s", want, out)
		}
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
