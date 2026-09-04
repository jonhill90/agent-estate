package shell

// This file drives SPEC-shell.md S3's own contract -- "an app shell owning
// activeRoute... ↑/↓ move, Enter/→ selects, ← collapses" -- against a real
// tea.Program, the same discipline model_teatest_test.go/theme_test.go
// already use in this package.

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/nav"
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
// of the legacy [2].
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
// rather than rendering nothing -- "Storage" (Connect's second child:
// Connections, Storage, Discord, Secrets -- "Models" was REMOVED from this
// group entirely by w5f.md, see internal/nav/tree.go's own doc comment),
// confirmed still unwired by actually entering it (`grep -rn "\"storage\""
// internal/shell/model.go` is empty). Was "Workflows" until w5f.md's own
// fix wired that route to the real internal/workflows.Model, and
// "Knowledge" before that until w3e.md's own fix did the same for
// internal/knowledge.Model -- each time this test moved to the next
// still-unwired route rather than asserting a route the build now renders
// for real (this file's own git history is the record of each move, not
// re-derived here).
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

	for i := 0; i < 9; i++ { // Home..Connect header (Build stays collapsed, its 3 children skipped)
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // expand Connect
	waitFor(t, tm, "Storage")
	for i := 0; i < 2; i++ { // onto Storage (Connections, then Storage)
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "not built yet")
	if !bytes.Contains(out, []byte("│ storage")) {
		t.Fatalf("stub view's own content missing the \"storage\" route title (stub.go renders m.nav.Active(), lowercase, inside its own bordered box):\n%s", out)
	}
}

// TestKnowledgeRouteShowsRealKnowledgePane is w3e.md's own regression test:
// "Knowledge" (row 5: Home, Dashboard, Agents, Chat, Tasks, Knowledge) used
// to fall through to PaneStub even though internal/knowledge (agent-tui#87) had been
// on main the whole time -- routeToPane simply never got a "knowledge" ->
// PaneKnowledge arm (see TestEnterOnAnUnwiredRouteRendersStub's doc comment
// for the git-history-verified root cause). It must now render the real
// internal/knowledge.Model testModel() wires in, keyed off its fake vault
// fact's own slug, not a stub and not home's own text.
func TestKnowledgeRouteShowsRealKnowledgePane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 5; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "test-marker-fact")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Knowledge route still rendering the S5 stub:\n%s", out)
	}
}

// TestKnowledgeRouteGKeyReachesTheMemoryGraphPane is agent-tui's own
// "make the knowledge graph a real, draggable pane inside the app" issue:
// the graph (internal/memgraph) must be reachable by navigating to the
// Knowledge route, exactly like a real user would (leader menu or sidebar
// down to Knowledge, [g] into the graph), not only constructible in a
// unit test that never goes through internal/shell at all.
func TestKnowledgeRouteGKeyReachesTheMemoryGraphPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 5; i++ { // Home -> Dashboard -> Agents -> Chat -> Tasks -> Knowledge
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "test-marker-fact")

	// [enter] on the sidebar only selects the route -- it does not move
	// focus into the content pane (routeNavKey's own doc comment: an
	// unrecognised key while the sidebar has focus falls through to
	// homeKey, which only knows "q"/"t"). [tab] is what actually hands
	// keys to the pane, same as a real user would press before typing a
	// pane-specific key.
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	out := waitFor(t, tm, "click a node")
	if bytes.Contains(out, []byte("[g] graph")) {
		t.Fatalf("still rendering the knowledge list's own legend after [g]:\n%s", out)
	}
}

// TestLibraryRouteShowsRealLibraryPane closes a gap found while checking
// the SAME class of bug the Knowledge regression was: routeToPane already
// maps "library" -> PaneLibrary (added alongside Knowledge's own nav row in
// agent-tui#93), but nothing in this package had ever driven the route through a
// real Program to prove it -- `grep -rln "PaneLibrary\|library\."
// internal/shell/*_test.go` returned nothing before this test. "Library" is
// row 6 (Home, Dashboard, Agents, Chat, Tasks, Knowledge, Library).
func TestLibraryRouteShowsRealLibraryPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 6; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// "it-deadbeef" is testModel()'s fake ItemRow's own ID -- its
	// BodySnippet ("test-marker-item") is truncated off-screen by the list
	// view's fixed column widths at this terminal size, so the ID is the
	// marker that survives truncation, the same reasoning
	// TestConnectionsRouteShowsRealConnectorsPane gives for using a real
	// field value instead of an arbitrary "test-marker-*" string.
	out := waitFor(t, tm, "it-deadbeef")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Library route still rendering the S5 stub:\n%s", out)
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

// TestWorkflowsRouteShowsRealWorkflowsPane reaches "Workflows," Build's
// second child (Skills, Workflows, MCP Servers) -- w5f.md's own pane
// (internal/workflows), wired here for the first time: `grep -rln
// "PaneWorkflows\|workflows\." internal/shell/*_test.go` returned nothing
// before this test, the same gap TestLibraryRouteShowsRealLibraryPane's own
// doc comment found and closed for Library.
func TestWorkflowsRouteShowsRealWorkflowsPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 8; i++ { // Home..Build header
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // expand Build
	waitFor(t, tm, "Workflows")
	for i := 0; i < 2; i++ { // onto Workflows (Skills, then Workflows)
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	// "test-marker-lane" (the LANE column) rather than the TaskID marker --
	// view.go's fixed-width TASK column truncates the 17-char TaskID to
	// "test-marke…" at this terminal size, the same truncation
	// TestLibraryRouteShowsRealLibraryPane's own doc comment already
	// documents for BodySnippet, so LANE (not column-width-limited here) is
	// the marker that survives.
	out := waitFor(t, tm, "test-marker-lane")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Workflows route still rendering the S5 stub:\n%s", out)
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

// TestSecretsRouteShowsRealSecretsPane reaches "Secrets," the Connect
// group's fourth and last child (Connections, Storage, Discord, Secrets)
// -- agent-tui#101's decision, wired here for the first time. "test-
// marker-key" is testModel()'s fake secrets.Inventory's own key name; a
// pane still rendering internal/stub's generic fallback would show
// neither it nor a "not built yet" string this test also checks for.
func TestSecretsRouteShowsRealSecretsPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 9; i++ { // Home..Build header..Connect header
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // expand Connect
	waitFor(t, tm, "Secrets")
	for i := 0; i < 4; i++ { // onto Secrets (Connections, Storage, Discord, then Secrets)
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "test-marker-key")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Secrets route still rendering the S5 stub:\n%s", out)
	}
}

// TestMonitoringRouteShowsRealMonitorPane reaches "Monitoring," Observe's
// second child (Usage, Monitoring) -- w5f.md's own pane (internal/monitor),
// wired here for the first time (same gap TestWorkflowsRouteShowsRealWorkflowsPane's
// own doc comment closes for Workflows). Connect's own 4 children (Models
// was REMOVED by w5f.md -- internal/nav/tree.go's own doc comment) stay
// collapsed the whole time, skipped by Down the same way visitable()
// (internal/shell/model.go's routeNavKey) skips any collapsed group's
// children.
func TestMonitoringRouteShowsRealMonitorPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 10; i++ { // Home..Build header..Connect header..Observe header
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // expand Observe
	waitFor(t, tm, "Monitoring")
	for i := 0; i < 2; i++ { // onto Monitoring (Usage, then Monitoring)
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "CLAUDE PROCESSES")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Monitoring route still rendering the S5 stub:\n%s", out)
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

// TestAPIDocsRouteShowsRealAPIDocsPane reaches "API Docs," the Docs
// group's first child. Docs is the fourth group header (Build, Connect,
// Observe, Docs), all collapsed, so ↓ from Home passes each header the
// same way TestMonitoringRouteShowsRealMonitorPane's own doc comment
// describes.
//
// Both this test and the Platform Docs one below assert the ABSENCE of
// "not built yet" as well as the presence of real content: agent-tui#94's
// walk recorded rows 18 and 19 as STUB, and a route that renders a real
// pane while some other path still falls back to the stub would satisfy
// only half of that.
func TestAPIDocsRouteShowsRealAPIDocsPane(t *testing.T) {
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 11; i++ { // Home..Build..Connect..Observe..Docs header
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // expand Docs
	waitFor(t, tm, "API Docs")
	tm.Send(tea.KeyMsg{Type: tea.KeyDown}) // onto API Docs itself
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, "test-marker-endpoint")
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("API Docs route still rendering the S5 stub:\n%s", out)
	}
}

// TestPlatformDocsRouteShowsExternalDestination reaches "Platform Docs,"
// the Docs group's second child, and asserts the pane names the real
// destination from internal/nav's tree rather than a stub. The URL is not
// hardcoded here: it is read back out of nav.Build() so this test cannot
// pass against a tree that lost or changed the href.
func TestPlatformDocsRouteShowsExternalDestination(t *testing.T) {
	item, ok := nav.Build().ItemByID("platform-docs")
	if !ok {
		t.Fatal("nav.Build() has no platform-docs item")
	}
	if item.Kind != nav.KindExternal || item.URL == "" {
		t.Fatalf("platform-docs = %+v, want KindExternal with a URL", item)
	}

	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")

	for i := 0; i < 11; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // expand Docs
	waitFor(t, tm, "Platform Docs")
	for i := 0; i < 2; i++ { // onto Platform Docs (API Docs, then Platform Docs)
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	out := waitFor(t, tm, item.URL)
	if bytes.Contains(out, []byte("not built yet")) {
		t.Fatalf("Platform Docs route still rendering the S5 stub:\n%s", out)
	}
}

// TestConnectCredentialStubsShowTheirOwnSpecificDescription is a
// regression guard for a real, live bug agent-b3.md's own brief found by
// actually driving the Discord stub, not by reading source: stub.View's
// title/description come from stub.Descriptions, looked up by
// `m.nav.Active()` (a route id, e.g. "discord") -- but stub.Descriptions
// used to be keyed by Label ("Discord"), so the lookup always missed and
// every stub silently fell back to the generic
// "not built yet -- no description recorded for this route." regardless
// of what specific text lived in the map. Fixed by re-keying
// stub.Descriptions to route ids. This test presses Enter on Storage and
// Discord in turn and asserts each one's own specific, route-id-keyed
// description text is on screen -- not just "not built yet" (which the
// generic fallback ALSO renders, so a weaker assertion would pass against
// the very bug this test exists to catch). Secrets used to be a third
// case here; agent-tui#101 decided it and it is a real pane now
// (TestSecretsRouteShowsRealSecretsPane, below), not a stub, so it is
// gone from this table.
func TestConnectCredentialStubsShowTheirOwnSpecificDescription(t *testing.T) {
	cases := []struct {
		route string
		downs int // additional downs after expanding Connect, onto this route
		want  string
	}{
		{"Storage", 2, "hill90-app"},
		{"Discord", 3, "notify.sh listing"},
	}
	for _, c := range cases {
		t.Run(c.route, func(t *testing.T) {
			tm := run(t, testModel())
			waitFor(t, tm, "⌂ Home")

			for i := 0; i < 9; i++ { // Home..Build header..Connect header
				tm.Send(tea.KeyMsg{Type: tea.KeyDown})
			}
			tm.Send(tea.KeyMsg{Type: tea.KeyRight}) // expand Connect
			waitFor(t, tm, c.route)
			for i := 0; i < c.downs; i++ {
				tm.Send(tea.KeyMsg{Type: tea.KeyDown})
			}
			tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
			out := waitFor(t, tm, "not built yet")
			if bytes.Contains(out, []byte("no description recorded for this route")) {
				t.Fatalf("%s route fell through to the GENERIC stub fallback -- stub.Descriptions lookup missed (the Label/id bug this test exists to catch):\n%s", c.route, out)
			}
			if !bytes.Contains(out, []byte(c.want)) {
				t.Fatalf("%s route missing its own specific description text %q:\n%s", c.route, c.want, out)
			}
		})
	}
}
