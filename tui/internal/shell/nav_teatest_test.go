package shell

// This file drives SPEC-shell.md S3's own contract -- "an app shell owning
// activeRoute... ↑/↓ move, Enter/→ selects, ← collapses" -- against a real
// tea.Program, the same discipline model_teatest_test.go/theme_test.go
// already use in this package.

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestInitialFrameShowsSidebarNotRail is S3's own headline change: the nav
// sidebar (internal/nav), not internal/rail, occupies the left column by
// default -- rail's own "(no lanes)" marker only appears once "Lanes" is
// actually selected (TestDownArrowsToLanesShowsRailContent, below).
func TestInitialFrameShowsSidebarNotRail(t *testing.T) {
	tm := run(t, testModel())
	out := waitFor(t, tm, "⌂ Home")
	if bytes.Contains(out, []byte("(no lanes)")) {
		t.Fatalf("rail content visible on the initial frame -- it should only render behind the Lanes route:\n%s", out)
	}
}

// TestDownArrowEnterNavigatesToTasksRoute presses ↓ six times (Home ->
// Dashboard -> Agents -> Chat -> Tasks) then Enter, and asserts the REAL
// board pane's own content actually replaces home's -- SPEC-shell.md S4's
// "Tasks -> internal/board" mapping, driven through the keyboard instead
// of the legacy [f2].
func TestDownArrowEnterNavigatesToTasksRoute(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 4; i++ { // Home -> Dashboard -> Agents -> Chat -> Tasks
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "#7 ")
}

// TestEnterOnAnUnwiredRouteRendersStub confirms a nav route S3/S4 do not
// map to a real Pane (routeToPane) falls through to internal/stub.View
// (S5) rather than rendering nothing -- "Agents" (row 2: Home, Dashboard,
// Agents) with its own S5 description as the marker.
func TestEnterOnAnUnwiredRouteRendersStub(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "not built yet")
	if !bytes.Contains(out, []byte("Agents")) {
		t.Fatalf("stub view missing the \"Agents\" title:\n%s", out)
	}
}

// TestDownArrowsToLanesShowsRailContent is SPEC-shell.md S4's third
// mapping ("Lanes -> internal/rail") reached the same way: Lanes is the
// 8th top-level row (Home, Dashboard, Agents, Chat, Tasks, Knowledge,
// Library, Lanes).
func TestDownArrowsToLanesShowsRailContent(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 7; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "(no lanes)")
}

// TestRightArrowOnGroupHeaderExpandsThenLeftCollapses covers the group
// case of Enter/→ (toggle, not navigate) and ← (collapse, moving cursor
// to the header) against the real Program, not just internal/nav's own
// unit tests -- "Skills" is Build's own first child.
func TestRightArrowOnGroupHeaderExpandsThenLeftCollapses(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	// Home, Dashboard, Agents, Chat, Tasks, Knowledge, Library, Lanes, Build
	for i := 0; i < 8; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight})
	waitFor(t, tm, "Skills")

	tm.Send(tea.KeyMsg{Type: tea.KeyLeft})
	out := waitFor(t, tm, "▸ Build")
	if bytes.Contains(out, []byte("Skills")) {
		t.Fatalf("Skills still visible after ← collapsed its group:\n%s", out)
	}
}

// TestWithStartPaneBoardHighlightsTasksInSidebar is the "-board and -cost
// keep working" acceptance line's other half: not just that the OLD flag
// still opens the right pane (TestWithStartOpensOnTheChosenPane already
// covers that), but that the NEW sidebar does not disagree with it by
// still showing Home highlighted.
func TestWithStartPaneBoardHighlightsTasksInSidebar(t *testing.T) {
	m := testModel().WithStart(PaneBoard)
	if m.nav.Active() != "tasks" {
		t.Fatalf("nav.Active() = %q after WithStart(PaneBoard), want %q", m.nav.Active(), "tasks")
	}
}
