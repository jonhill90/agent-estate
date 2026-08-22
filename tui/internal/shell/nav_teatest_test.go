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

// TestEnterOnAnUnwiredRouteRendersStub confirms a nav route routeToPane
// does not map to a real Pane falls through to internal/stub.View (S5)
// rather than rendering nothing -- "Knowledge" (row 5: Home, Dashboard,
// Agents, Chat, Tasks, Knowledge), confirmed still unwired by actually
// entering it (internal/knowledge exists on main since #87 but nothing in
// routeToPane maps "knowledge" to it yet -- grep confirms, not assumed:
// `grep -rn "\"knowledge\"" internal/shell/model.go` is empty). Was
// "Dashboard" until this change gave that route a real pane
// (internal/dashboard) -- this test moved to the next still-unwired route
// rather than asserting a route this build now renders for real.
//
// The stub's own content, not merely "not built yet" appearing somewhere
// in the frame, is what's asserted: the sidebar's OWN row labels ("Library",
// "Knowledge", ...) are always on screen regardless of which pane is
// active, so a substring check against a sidebar label alone would pass
// no matter which route was actually entered -- exactly the mistake this
// test's own history made once already resolving this same rename.
func TestEnterOnAnUnwiredRouteRendersStub(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 5; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "not built yet")
	if !bytes.Contains(out, []byte("│ knowledge")) {
		t.Fatalf("stub view's own content missing the \"knowledge\" route title (stub.go renders m.nav.Active(), lowercase, inside its own bordered box):\n%s", out)
	}
}

// TestDashboardRouteShowsRealDashboardPane reaches "Dashboard," the 2nd
// top-level row (Home, Dashboard) -- it must now render the real
// internal/dashboard.Model testModel() wires in, not a stub and not home's
// own text.
func TestDashboardRouteShowsRealDashboardPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "AGENTS")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Dashboard route still rendering the S5 stub:\n%s", out)
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

// TestAgentsRouteShowsRealAgentsPane is this change's own job: "Agents"
// (row 2: Home, Dashboard, Agents) used to fall through to PaneStub
// (TestEnterOnAnUnwiredRouteRendersStub, above, before it moved to
// Dashboard) -- it must now render the real internal/agents.Model
// testModel() wires in, not a stub and not home's own text.
func TestAgentsRouteShowsRealAgentsPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "director:w1")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Agents route still rendering the S5 stub:\n%s", out)
	}
}

// TestSkillsRouteShowsRealSkillsPane reaches "Skills," Build's first
// child: expand Build (→), then ↓ once more onto Skills, then Enter.
func TestSkillsRouteShowsRealSkillsPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 8; i++ { // Home..Build header
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // expand Build
	waitFor(t, tm, "Skills")
	tm.Send(tea.KeyMsg{Type: tea.KeyDown}) // onto the Skills row itself
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "test-marker-skill")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Skills route still rendering the S5 stub:\n%s", out)
	}
}

// TestMCPServersRouteShowsRealMCPServersPane reaches "MCP Servers," Build's
// third child: expand Build, then ↓↓↓ (Skills, Workflows, MCP Servers).
func TestMCPServersRouteShowsRealMCPServersPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 8; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight})
	waitFor(t, tm, "MCP Servers")
	for i := 0; i < 3; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "test-marker-server")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("MCP Servers route still rendering the S5 stub:\n%s", out)
	}
}

// TestConnectionsRouteShowsRealConnectorsPane reaches "Connections,"
// Connect's first child -- Connect is the group header right after Build
// (whether or not Build is expanded in THIS test, which never expands it).
func TestConnectionsRouteShowsRealConnectorsPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 9; i++ { // Home..Build header..Connect header
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight})
	waitFor(t, tm, "Connections")
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// "openai" is testModel()'s fake Connection's Provider -- connectors is
	// the one pane here with no arbitrary "test-marker-*" string of its
	// own to key off; its View renders real field values (harness/
	// provider/configured/model), so a real one doubles as the marker.
	out := waitFor(t, tm, "openai")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Connections route still rendering the S5 stub:\n%s", out)
	}
}

// TestAdminServicesRouteShowsRealAdminPane reaches "Services," the Admin
// group's first child -- Admin is the last group header (Build, Connect,
// Observe, Docs, Admin).
func TestAdminServicesRouteShowsRealAdminPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 12; i++ { // Home..Build..Connect..Observe..Docs..Admin header
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight})
	waitFor(t, tm, "Services")
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "test-marker-container")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Admin route still rendering the S5 stub:\n%s", out)
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
