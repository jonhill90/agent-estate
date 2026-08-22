package shell

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Clicking a nav item must switch panes. This drives a real tea.MouseMsg at
// coordinates derived from the RENDERED frame, not from a hardcoded guess --
// a test that clicks a made-up (x,y) proves nothing about where the nav
// actually is.
func clickAt(t *testing.T, m Model, x, y int) (Model, bool) {
	t.Helper()
	msg := tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft}
	next, _, handled := m.handleMouse(msg)
	return next, handled
}

// locate finds a label and returns its coordinates from the real render.
//
// It scans from the TOP, unlike this file's own pre-rebase version (which
// scanned from the bottom to avoid matching the home pane's own footer-key
// hint text). Post-rebase the sidebar -- not the footer -- is the real nav
// (SPEC-shell.md S3), and the sidebar renders ABOVE the content area, so a
// top-down scan finds the sidebar row first; a label that also appears
// lower in the frame (e.g. home's own hint text mentioning the same word)
// is exactly the collision agent-tui's own mouse-nav history already
// flagged once, which is why this comment names the direction on purpose
// rather than leaving it to be re-discovered.
func locate(t *testing.T, m Model, label string) (x, y int) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	for i, line := range lines {
		if c := strings.Index(line, label); c >= 0 {
			return c, i
		}
	}
	t.Fatalf("label %q not found in rendered view", label)
	return 0, 0
}

// TestClickingTasksInSidebarSwitchesPane is the sidebar-row half of this
// package's own rebase note: "Tasks" (nav route id "tasks") is a top-level,
// always-visible row -- routeToPane maps it to PaneBoard, the same S4
// mapping the keyboard's Enter key already drives.
func TestClickingTasksInSidebarSwitchesPane(t *testing.T) {
	m := testModel()
	m, _ = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	_ = m.View() // Scan must run once so zones have coordinates.

	if m.active != PaneHome {
		t.Fatalf("precondition: want PaneHome, got %v", m.active)
	}
	x, y := locate(t, m, "Tasks")
	next, handled := clickAt(t, m, x, y)
	if !handled {
		t.Fatalf("click at (%d,%d) on \"Tasks\" was not handled", x, y)
	}
	if next.active != PaneBoard {
		t.Fatalf("after clicking Tasks want PaneBoard, got %v", next.active)
	}
	// Clicking a view means working in it.
	if next.focus != focusContent {
		t.Fatalf("clicking a pane must move focus to content, got %v", next.focus)
	}
	if next.nav.Active() != "tasks" {
		t.Fatalf("nav.Active() = %q after clicking Tasks, want %q", next.nav.Active(), "tasks")
	}
}

// TestClickingHomeAfterTasksNavigatesBothWays is the round-trip: two
// top-level rows, both always visible, both clicked in turn.
func TestClickingHomeAfterTasksNavigatesBothWays(t *testing.T) {
	m := testModel()
	m, _ = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	_ = m.View()

	x, y := locate(t, m, "Tasks")
	m, ok := clickAt(t, m, x, y)
	if !ok || m.active != PaneBoard {
		t.Fatalf("click tasks: handled=%v active=%v", ok, m.active)
	}
	_ = m.View()
	x, y = locate(t, m, "Home")
	m, ok = clickAt(t, m, x, y)
	if !ok || m.active != PaneHome {
		t.Fatalf("click home: handled=%v active=%v", ok, m.active)
	}
}

// TestClickingGroupHeaderExpandsThenChildRowNavigates proves the OTHER half
// of a sidebar row's own click contract: a group header's row toggles
// expansion (never picks a route -- there is no destination named "Observe"
// itself), and once expanded, its own child row ("Usage", nested under
// Observe) is clickable and routes exactly like Enter already does
// (routeToPane["usage"] == PaneCost).
func TestClickingGroupHeaderExpandsThenChildRowNavigates(t *testing.T) {
	m := testModel()
	m, _ = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	_ = m.View()

	if strings.Contains(m.View(), "Usage") {
		t.Fatal("test setup: \"Usage\" already visible before its group was expanded")
	}

	x, y := locate(t, m, "Observe")
	m, ok := clickAt(t, m, x, y)
	if !ok {
		t.Fatalf("click on \"Observe\" header was not handled")
	}
	if m.active != PaneHome {
		t.Fatalf("clicking a GROUP HEADER must not change the active pane, got %v", m.active)
	}
	out := m.View()
	if !strings.Contains(out, "Usage") {
		t.Fatalf("\"Observe\" did not expand after being clicked:\n%s", out)
	}

	x, y = locate(t, m, "Usage")
	m, ok = clickAt(t, m, x, y)
	if !ok || m.active != PaneCost {
		t.Fatalf("click usage: handled=%v active=%v", ok, m.active)
	}
	if m.nav.Active() != "usage" {
		t.Fatalf("nav.Active() = %q after clicking Usage, want %q", m.nav.Active(), "usage")
	}
}

// A click nowhere near the nav must NOT be swallowed -- it has to fall
// through to the focused pane, or panes with their own clickables go dead.
// This coordinate is deliberately well past the sidebar's own fixed width
// (nav.Model's fullWidth, 26) and low enough to avoid the footer -- the
// content pane's own body, where no zone exists at all.
func TestClickOutsideNavIsNotHandled(t *testing.T) {
	m := testModel()
	m, _ = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	_ = m.View()
	if _, handled := clickAt(t, m, 140, 10); handled {
		t.Fatal("a click in the content pane must not be handled by the nav")
	}
}

// Press without release must not act: click-and-drag-away is not a click.
func TestPressWithoutReleaseDoesNotNavigate(t *testing.T) {
	m := testModel()
	m, _ = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	_ = m.View()
	x, y := locate(t, m, "Tasks")
	msg := tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	next, _, handled := m.handleMouse(msg)
	if handled || next.active != PaneHome {
		t.Fatalf("press alone must not navigate: handled=%v active=%v", handled, next.active)
	}
}

// TestClickingGalleryInFooterStillWorks is the footer-only half of this
// rebase: gallery has no sidebar row at all (routeToPane's own doc
// comment), so its footer zone must still be clickable exactly as before.
func TestClickingGalleryInFooterStillWorks(t *testing.T) {
	m := testModel()
	m, _ = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	_ = m.View()

	x, y := locate(t, m, "[f4]gallery")
	next, handled := clickAt(t, m, x+2, y)
	if !handled {
		t.Fatalf("click at (%d,%d) on [f4]gallery was not handled", x+2, y)
	}
	if next.active != PaneGallery {
		t.Fatalf("after clicking gallery want PaneGallery, got %v", next.active)
	}
}
