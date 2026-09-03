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
// locate finds a label IN THE SIDEBAR and returns its coordinates from the
// real render.
//
// It takes the LEFTMOST match, not the first line-wise. The sidebar occupies
// the left columns; the content pane repeats several of the same words
// ("Tasks", "Home", "Chat") much further right. A top-down scan returned
// column 83 -- inside the content pane, where no zone exists -- so the click
// correctly went unhandled and the test correctly failed. The test's idea of
// where the sidebar was, was wrong; the nav was not.
//
// This is the second time this exact mistake has been made in this file, in the
// opposite direction (an earlier version searched bottom-up for the footer and
// found the home pane's own key hints). Hence the rule, written down rather
// than left to be rediscovered a third time: match on POSITION, never on
// first-occurrence.
// sidebarWidth bounds what counts as "in the sidebar" for these tests. The
// nav column is narrow; anything past this is the content pane.
const sidebarWidth = 40

// sidebarHas reports whether a label appears in the SIDEBAR columns, which is
// a different question from whether it appears on screen.
func sidebarHas(m Model, label string) bool {
	for _, line := range strings.Split(m.View(), "\n") {
		if c := strings.Index(line, label); c >= 0 && c < sidebarWidth {
			return true
		}
	}
	return false
}

func locate(t *testing.T, m Model, label string) (x, y int) {
	t.Helper()
	lines := strings.Split(m.View(), "\n")
	bestX, bestY := -1, -1
	for i, line := range lines {
		c := strings.Index(line, label)
		if c < 0 {
			continue
		}
		if bestX == -1 || c < bestX {
			bestX, bestY = c, i
		}
	}
	if bestX == -1 {
		t.Fatalf("label %q not found in rendered view", label)
	}
	return bestX, bestY
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

	// Check the SIDEBAR for "Usage", not the whole frame. The content pane
	// carries the word too, so a whole-view Contains asserts something this
	// test does not mean and fails for the wrong reason. Same lesson as
	// locate(): match on position, not on presence anywhere on screen.
	if sidebarHas(m, "Usage") {
		t.Fatal("test setup: \"Usage\" already visible in the sidebar before its group was expanded")
	}

	x, y := locate(t, m, "Observe")
	m, ok := clickAt(t, m, x, y)
	if !ok {
		t.Fatalf("click on \"Observe\" header was not handled")
	}
	if m.active != PaneHome {
		t.Fatalf("clicking a GROUP HEADER must not change the active pane, got %v", m.active)
	}
	if !sidebarHas(m, "Usage") {
		t.Fatalf("\"Observe\" did not expand after being clicked:\n%s", m.View())
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

// TestClickingEachAdminChildScopesTheSharedAdminPane is agent-tui#150's own
// regression test at the shell-integration layer (internal/admin's own
// model_test.go covers the rendering half): all five Admin children map to
// the SAME PaneAdmin (routeToPane's own doc comment), so this proves the
// content pane actually distinguishes which one was clicked rather than
// showing the same composite pane five times over.
func TestClickingEachAdminChildScopesTheSharedAdminPane(t *testing.T) {
	m := testModel()
	m, _ = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	_ = m.View()

	x, y := locate(t, m, "Admin")
	m, ok := clickAt(t, m, x, y)
	if !ok {
		t.Fatalf("click on \"Admin\" header was not handled")
	}
	if !sidebarHas(m, "Services") {
		t.Fatalf("\"Admin\" did not expand after being clicked:\n%s", m.View())
	}

	children := []string{"Services", "Profiles", "Users", "Dependencies", "Settings"}
	views := make(map[string]string, len(children))
	for _, label := range children {
		_ = m.View() // re-scan zones: content changed, so labels may have moved.
		x, y := locate(t, m, label)
		next, ok := clickAt(t, m, x, y)
		if !ok || next.active != PaneAdmin {
			t.Fatalf("click %s: handled=%v active=%v", label, ok, next.active)
		}
		m = next
		views[label] = m.View()
	}

	seen := map[string]string{}
	for label, out := range views {
		if prior, ok := seen[out]; ok {
			t.Fatalf("clicking %q and %q rendered byte-identical content panes:\n%s", label, prior, out)
		}
		seen[out] = label
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

// TestClickingTheLeaderHintOpensTheMenu replaces a test that clicked the
// footer's own [f4]gallery / [f5]flow zones. Those zones are gone: the footer
// no longer lists panes, because a legend that only fits six entries cannot
// show twenty.
//
// Every pane must still be reachable by MOUSE, so the hint itself is the
// zone, and clicking it opens the which-key menu immediately -- a mouse user
// has already expressed intent and should not wait out the keyboard timeout.
// Without this, removing the f-keys would have quietly removed mouse
// navigation with them.
func TestClickingTheLeaderHintOpensTheMenu(t *testing.T) {
	m := testModel()
	m, _ = m.resize(tea.WindowSizeMsg{Width: 160, Height: 40})
	_ = m.View()

	x, y := locate(t, m, "[space] menu")
	next, handled := clickAt(t, m, x+2, y)
	if !handled {
		t.Fatalf("click at (%d,%d) on the leader hint was not handled", x+2, y)
	}
	if !next.leaderMenu {
		t.Fatal("clicking the leader hint must open the which-key menu")
	}
	// And the menu must actually name the panes, or it is not a menu.
	view := next.View()
	for _, want := range []string{"gallery", "tasks", "knowledge"} {
		if !strings.Contains(view, want) {
			t.Errorf("the open menu must list %q; it did not", want)
		}
	}
}
