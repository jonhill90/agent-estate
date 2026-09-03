package shell

// This file is jonhill90/agent-estate#937's own fix-pass regression test, for a defect a real
// cross-lane review demonstrated: pressing [b] (nav's own icons-only
// toggle) shrinks the sidebar from fullWidth (26) to iconWidth (4) entirely
// inside nav.Update, with no tea.WindowSizeMsg -- the only place the
// shell's own cached m.navWidth field was recomputed (resize()).
// Translating a content-pane MouseMsg by that stale cached field left
// every click/drag 22 columns short of the real content-pane origin from
// the moment [b] was pressed until the next resize. Model.Update's
// tea.MouseMsg branch now reads m.nav.Width() live at the translation site
// instead.
//
// This drives the real graph pane (internal/memgraph) through a real
// tea.Program and presses/drags the ONE real node the fixture graph
// renders, proving the press actually lands on the node under the cursor
// -- not merely that some event was accepted.

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// graphNodePosition drives testModel() to the loaded memory-graph pane
// (Home -> Knowledge -> [tab] -> [g]) and returns the ONE fixture node's
// own canvas-local position (knowledge.Model.GraphPositionOf). The force
// layout is a deterministic function of the graph and the canvas size
// (internal/memgraph's own withLayout doc comment: "called exactly once
// per successful fetch"), so this is stable across repeated runs at a
// fixed terminal size.
func graphNodePosition(t *testing.T) (x, y int) {
	t.Helper()
	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")
	for i := 0; i < 5; i++ { // Home -> Dashboard -> Agents -> Chat -> Tasks -> Knowledge
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	waitFor(t, tm, "drag to reposition")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	final := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m := final.(Model)
	x, y, ok := m.knowledge.GraphPositionOf("test-marker-fact")
	if !ok {
		t.Fatal("node \"test-marker-fact\" has no position after initial layout")
	}
	return x, y
}

// TestMouseTranslationLiveAfterIconsToggle is the regression: collapse the
// sidebar with [b] (no resize), then press-drag-release the fixture node at
// its REAL, POST-COLLAPSE screen position, and confirm it actually moved.
// Before the fix this is red: Model.Update subtracted the stale cached
// m.navWidth (still 26), landing every event 22 columns off the node's
// tolerance (internal/memgraph.nodeNear allows only 1 cell), so the press
// never grabs it and the "drag" is a no-op.
func TestMouseTranslationLiveAfterIconsToggle(t *testing.T) {
	x0, y0 := graphNodePosition(t)

	// boxInset -- internal/memgraph's own box border width (model.go's own
	// boxInset doc comment): a mouse event's pane-local coordinate is the
	// node's canvas position plus this inset, the same relationship
	// internal/memgraph/model_test.go's own TestDragSequenceMovesGrabbedNode
	// asserts within that package.
	const boxInset = 1
	// iconWidth -- internal/nav's own collapsed sidebar width
	// (nav/model.go's own fullWidth/iconWidth doc comment).
	const iconWidth = 4

	tm := run(t, testModel())
	waitFor(t, tm, "⌂ Home")
	for i := 0; i < 5; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	waitFor(t, tm, "drag to reposition")

	// Collapse the sidebar WITHOUT a resize -- [tab] back to the sidebar
	// first, since [b] is nav's own key and only takes effect while the
	// sidebar has focus (routeNavKey's own doc comment), then [tab] forward
	// again (not load-bearing for mouse routing, but matches how a real
	// user would leave the sidebar before dragging).
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	tm.Send(tea.KeyMsg{Type: tea.KeyTab})

	// The node's REAL screen column is now iconWidth + boxInset + x0 -- NOT
	// the pre-collapse fullWidth + boxInset + x0. Press exactly there.
	screenX := iconWidth + boxInset + x0
	screenY := boxInset + y0

	// Move away from the left canvas edge so the drag target stays
	// in-bounds regardless of where the single-node layout happened to
	// place it (mirrors internal/memgraph/model_test.go's own edge guard).
	dx := -3
	if x0 < 3 {
		dx = 3
	}

	tm.Send(tea.MouseMsg{X: screenX, Y: screenY, Action: tea.MouseActionPress})
	tm.Send(tea.MouseMsg{X: screenX + dx, Y: screenY, Action: tea.MouseActionMotion})
	waitFor(t, tm, "dragging test-marker-fact")
	tm.Send(tea.MouseMsg{X: screenX + dx, Y: screenY, Action: tea.MouseActionRelease})

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	final := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	m := final.(Model)
	x1, y1, ok := m.knowledge.GraphPositionOf("test-marker-fact")
	if !ok {
		t.Fatal("node \"test-marker-fact\" lost its position")
	}
	if x1 != x0+dx || y1 != y0 {
		t.Fatalf("drag after collapsing the sidebar landed at (%d,%d), want (%d,%d) -- mouse X translation used a stale sidebar width instead of the live one", x1, y1, x0+dx, y0)
	}
}
