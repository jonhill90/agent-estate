package memgraph

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func fakeGraph() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "a", Label: "A", Type: "project"},
			{ID: "b", Label: "B", Type: "feedback"},
			{ID: "c", Label: "C", Type: "reference"},
		},
		Edges: []Edge{{From: "a", To: "b"}, {From: "b", To: "c"}},
	}
}

func loaded(t *testing.T) Model {
	t.Helper()
	m := New(func() (Graph, error) { return fakeGraph(), nil })
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = next.(Model)
	next, _ = m.Update(fetchResultMsg{graph: fakeGraph()})
	return next.(Model)
}

// TestDragSequenceMovesGrabbedNode is the evidence the PR body must name:
// press on a real node, motion while grabbed, release -- the node's own
// position must change, and must land exactly on the release coordinates
// (clamped to canvas bounds), the same three-step drive
// tools/memoryvariants/spike/main_test.go used to prove the mechanism
// live.
func TestDragSequenceMovesGrabbedNode(t *testing.T) {
	m := loaded(t)

	x0, y0, ok := m.PositionOf("a")
	if !ok {
		t.Fatalf("node a has no position after initial layout")
	}

	// Mouse events carry pane-local coordinates (this pane's own top-left
	// at (0,0), the header count line and the border included) -- the
	// cell CanvasOrigin names is where a node's own canvas-interior
	// position (PositionOf) actually sits. See handleMouse's own doc
	// comment.
	next, _ := m.Update(tea.MouseMsg{X: x0 + boxInset, Y: y0 + boxInset + headerRows, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.grabbed != "a" {
		t.Fatalf("press on node a's own cell: grabbed = %q, want \"a\"", m.grabbed)
	}

	w, h := m.canvasSize()
	tx, ty := w/2, h/2
	if tx == x0 && ty == y0 {
		tx++
	}
	next, _ = m.Update(tea.MouseMsg{X: tx + boxInset, Y: ty + boxInset + headerRows, Action: tea.MouseActionMotion})
	m = next.(Model)
	mx, my, _ := m.PositionOf("a")
	if mx != tx || my != ty {
		t.Fatalf("motion while grabbed: node a at (%d,%d), want (%d,%d)", mx, my, tx, ty)
	}
	if m.grabbed != "a" {
		t.Fatalf("still mid-drag: grabbed = %q, want \"a\"", m.grabbed)
	}

	rx, ry := tx+1, ty
	if rx >= w {
		rx = tx - 1
	}
	next, _ = m.Update(tea.MouseMsg{X: rx + boxInset, Y: ry + boxInset + headerRows, Action: tea.MouseActionRelease})
	m = next.(Model)
	fx, fy, _ := m.PositionOf("a")
	if fx != rx || fy != ry {
		t.Fatalf("release: node a at (%d,%d), want (%d,%d)", fx, fy, rx, ry)
	}
	if m.grabbed != "" {
		t.Fatalf("after release: grabbed = %q, want \"\" (nothing held)", m.grabbed)
	}

	if fx == x0 && fy == y0 {
		t.Fatalf("node a's position did not change across the drag: stayed at (%d,%d)", x0, y0)
	}
}

// TestMotionWithoutPressDoesNotGrab mirrors
// tools/memoryvariants/spike/main_test.go's own negative case: a motion
// event with no prior press over a node must not move anything.
func TestMotionWithoutPressDoesNotGrab(t *testing.T) {
	m := loaded(t)
	x0, y0, _ := m.PositionOf("a")

	next, _ := m.Update(tea.MouseMsg{X: x0 + boxInset + 5, Y: y0 + boxInset + headerRows + 2, Action: tea.MouseActionMotion})
	m = next.(Model)

	x1, y1, _ := m.PositionOf("a")
	if x1 != x0 || y1 != y0 {
		t.Fatalf("ungrabbed motion moved node a to (%d,%d), want unchanged (%d,%d)", x1, y1, x0, y0)
	}
	if m.grabbed != "" {
		t.Fatalf("grabbed = %q after motion with no prior press, want \"\"", m.grabbed)
	}
}

// TestPressOffAnyNodeGrabsNothing: a press on empty canvas space must not
// grab the nearest node regardless of distance -- only a press ON (or
// adjacent to) a node counts.
func TestPressOffAnyNodeGrabsNothing(t *testing.T) {
	m := loaded(t)
	w, h := m.canvasSize()

	// Find a cell at least 3 away (Chebyshev) from every node.
	occupied := map[[2]int]bool{}
	for _, id := range m.order {
		x, y, _ := m.PositionOf(id)
		occupied[[2]int{x, y}] = true
	}
	fx, fy := -1, -1
outer:
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			clear := true
			for oc := range occupied {
				dx, dy := oc[0]-x, oc[1]-y
				if dx < 0 {
					dx = -dx
				}
				if dy < 0 {
					dy = -dy
				}
				if dx <= 1 && dy <= 1 {
					clear = false
					break
				}
			}
			if clear {
				fx, fy = x, y
				break outer
			}
		}
	}
	if fx < 0 {
		t.Skip("canvas too small in this fixture to find empty space")
	}

	next, _ := m.Update(tea.MouseMsg{X: fx + boxInset, Y: fy + boxInset + headerRows, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.grabbed != "" {
		t.Fatalf("press on empty cell (%d,%d) grabbed %q, want nothing grabbed", fx, fy, m.grabbed)
	}
}

// TestFetchErrRendersHonestly: a Fetcher failure must be a visible error
// in View(), never a silently swapped-in demo graph -- AGENTS.md's
// "absence is a typed value" convention, which this issue's own brief
// names as a hard requirement.
func TestFetchErrRendersHonestly(t *testing.T) {
	wantErr := errors.New("$AGENT_MEMORY_VAULT is not set")
	m := New(func() (Graph, error) { return Graph{}, wantErr })
	next, _ := m.Update(fetchResultMsg{err: wantErr})
	m = next.(Model)

	if m.FetchErr() == nil {
		t.Fatalf("FetchErr() = nil after a failed fetch")
	}
	view := m.View()
	if !containsAll(view, "could not read memory vault", wantErr.Error()) {
		t.Fatalf("View() does not surface the fetch error honestly:\n%s", view)
	}
	if m.Loaded() {
		t.Fatalf("Loaded() = true after a failed fetch, want false")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------
// agent-estate#1006: the three interaction verbs that did not exist --
// scroll to zoom, drag to pan, click a node to open it. Each is driven
// through Update with synthetic messages, the same way
// TestDragSequenceMovesGrabbedNode above drives the fourth (drag to
// move), so "the verb works" means the model actually changed, never
// that a frame merely rendered.
// ---------------------------------------------------------------------

func loadedWithDetail(t *testing.T, load DetailLoader) Model {
	t.Helper()
	m := New(func() (Graph, error) { return fakeGraph(), nil }).WithDetailLoader(load)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	m = next.(Model)
	next, _ = m.Update(fetchResultMsg{graph: fakeGraph()})
	return next.(Model)
}

// wheel builds the message a real terminal sends for a scroll: bubbletea
// reports it as a PRESS carrying a wheel Button, which is exactly why
// handleMouse must claim it by Button before its press case ever runs --
// see TestWheelOverANodeDoesNotOpenIt below.
func wheel(x, y int, btn tea.MouseButton) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: btn}
}

// TestWheelZoomsAndClamps -- verb 1, scroll to zoom.
func TestWheelZoomsAndClamps(t *testing.T) {
	m := loaded(t)
	if m.Zoom() != 1 {
		t.Fatalf("initial Zoom() = %v, want 1", m.Zoom())
	}

	next, _ := m.Update(wheel(10, 10, tea.MouseButtonWheelUp))
	m = next.(Model)
	if m.Zoom() <= 1 {
		t.Fatalf("wheel up: Zoom() = %v, want > 1", m.Zoom())
	}

	next, _ = m.Update(wheel(10, 10, tea.MouseButtonWheelDown))
	m = next.(Model)
	if m.Zoom() != 1 {
		t.Fatalf("wheel down after wheel up: Zoom() = %v, want back to 1", m.Zoom())
	}

	for i := 0; i < 40; i++ {
		next, _ = m.Update(wheel(10, 10, tea.MouseButtonWheelUp))
		m = next.(Model)
	}
	if m.Zoom() != maxZoom {
		t.Fatalf("Zoom() = %v after 40 wheel-ups, want it clamped at maxZoom %v", m.Zoom(), maxZoom)
	}
	for i := 0; i < 80; i++ {
		next, _ = m.Update(wheel(10, 10, tea.MouseButtonWheelDown))
		m = next.(Model)
	}
	if m.Zoom() != minZoom {
		t.Fatalf("Zoom() = %v after 80 wheel-downs, want it clamped at minZoom %v", m.Zoom(), minZoom)
	}
}

// TestZoomMovesWhereANodeIsPaintedButNotWhereItIs is the property that
// makes zoom safe: it is a VIEW transform. The node's own stored position
// must survive it untouched, so zooming can never quietly undo a drag.
func TestZoomMovesWhereANodeIsPaintedButNotWhereItIs(t *testing.T) {
	m := loaded(t)
	// Put "a" well away from the canvas centre, which is zoom's own fixed
	// point -- a node sitting exactly on the centre would not move, and
	// the test would pass for the wrong reason.
	w, h := m.canvasSize()
	m = m.withPos("a", w-2, h-2)
	gx0, gy0, _ := m.PositionOf("a")
	sx0, sy0, _ := m.ScreenPositionOf("a")
	if sx0 != gx0 || sy0 != gy0 {
		t.Fatalf("at zoom 1 with no pan, ScreenPositionOf (%d,%d) must equal PositionOf (%d,%d)", sx0, sy0, gx0, gy0)
	}

	next, _ := m.Update(wheel(0, 0, tea.MouseButtonWheelUp))
	m = next.(Model)

	gx1, gy1, _ := m.PositionOf("a")
	if gx1 != gx0 || gy1 != gy0 {
		t.Fatalf("zoom moved node a's stored position from (%d,%d) to (%d,%d) -- zoom must not write pos", gx0, gy0, gx1, gy1)
	}
	sx1, sy1, _ := m.ScreenPositionOf("a")
	if sx1 == sx0 && sy1 == sy0 {
		t.Fatalf("zoom did not change where node a is painted: still (%d,%d)", sx0, sy0)
	}
}

// TestWheelOverANodeDoesNotOpenIt is the regression the Button-first
// ordering in handleMouse exists for: a wheel event IS a press. Handled
// in press order, scrolling with the cursor over a node would open that
// node.
func TestWheelOverANodeDoesNotOpenIt(t *testing.T) {
	m := loadedWithDetail(t, func(id string) (Detail, error) {
		return Detail{ID: id, Body: "should never be loaded by a scroll"}, nil
	})
	x, y, _ := m.ScreenPositionOf("a")

	next, cmd := m.Update(wheel(x+boxInset, y+boxInset+headerRows, tea.MouseButtonWheelUp))
	m = next.(Model)
	if m.Opened() {
		t.Fatalf("a wheel event over node a opened it (OpenedID %q) -- wheel must not be read as a click", m.OpenedID())
	}
	if cmd != nil {
		t.Fatal("a wheel event returned a command -- nothing should be loaded by scrolling")
	}
	if m.grabbed != "" {
		t.Fatalf("a wheel event grabbed %q", m.grabbed)
	}
}

// TestDragOnEmptyCanvasPansTheView -- verb 2, drag to pan. A press that
// lands on empty canvas must move the VIEW, not a node.
func TestDragOnEmptyCanvasPansTheView(t *testing.T) {
	m := loaded(t)
	ex, ey, ok := emptyCell(m)
	if !ok {
		t.Skip("canvas too small in this fixture to find empty space")
	}

	gx0, gy0, _ := m.PositionOf("a")
	sx0, sy0, _ := m.ScreenPositionOf("a")

	next, _ := m.Update(tea.MouseMsg{X: ex + boxInset, Y: ey + boxInset + headerRows, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.grabbed != "" {
		t.Fatalf("press on empty canvas grabbed %q, want a pan instead", m.grabbed)
	}
	if !m.panning {
		t.Fatal("press on empty canvas did not begin a pan")
	}

	const dx, dy = 4, 2
	next, _ = m.Update(tea.MouseMsg{X: ex + dx + boxInset, Y: ey + dy + boxInset + headerRows, Action: tea.MouseActionMotion})
	m = next.(Model)

	px, py := m.PanOffset()
	if px != dx || py != dy {
		t.Fatalf("PanOffset() = (%d,%d) after a %d,%d drag at zoom 1, want (%d,%d)", px, py, dx, dy, dx, dy)
	}

	gx1, gy1, _ := m.PositionOf("a")
	if gx1 != gx0 || gy1 != gy0 {
		t.Fatalf("panning moved node a's stored position from (%d,%d) to (%d,%d) -- a pan must not write pos", gx0, gy0, gx1, gy1)
	}
	sx1, sy1, _ := m.ScreenPositionOf("a")
	if sx1 != sx0+dx || sy1 != sy0+dy {
		t.Fatalf("after the pan node a is painted at (%d,%d), want (%d,%d)", sx1, sy1, sx0+dx, sy0+dy)
	}

	next, _ = m.Update(tea.MouseMsg{X: ex + dx + boxInset, Y: ey + dy + boxInset + headerRows, Action: tea.MouseActionRelease})
	m = next.(Model)
	if m.panning {
		t.Fatal("release did not end the pan")
	}
}

// emptyCell finds an interior cell at least 2 away (Chebyshev) from every
// node's painted position, so a press there is unambiguously off-node
// (nodeAtScreen's tolerance is 1).
func emptyCell(m Model) (int, int, bool) {
	w, h := m.canvasSize()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			clear := true
			for _, id := range m.order {
				sx, sy, _ := m.ScreenPositionOf(id)
				if absInt(sx-x) <= 1 && absInt(sy-y) <= 1 {
					clear = false
					break
				}
			}
			if clear {
				return x, y, true
			}
		}
	}
	return 0, 0, false
}

// clickNode drives the exact press+release pair a click is -- no motion
// between them, which is the ONLY thing distinguishing it from a drag.
func clickNode(t *testing.T, m Model, id string) (Model, tea.Cmd) {
	t.Helper()
	x, y, ok := m.ScreenPositionOf(id)
	if !ok {
		t.Fatalf("node %q has no position", id)
	}
	next, _ := m.Update(tea.MouseMsg{X: x + boxInset, Y: y + boxInset + headerRows, Action: tea.MouseActionPress})
	m = next.(Model)
	next, cmd := m.Update(tea.MouseMsg{X: x + boxInset, Y: y + boxInset + headerRows, Action: tea.MouseActionRelease})
	return next.(Model), cmd
}

// TestClickOnNodeOpensItFromTheDetailLoader -- verb 3, the one
// agent-estate#1006 calls the one that matters. A click must reach the
// DetailLoader seam with THAT node's id, and the content it returns must
// be what the pane then renders: the thing itself, not a summary of it.
func TestClickOnNodeOpensItFromTheDetailLoader(t *testing.T) {
	var asked []string
	m := loadedWithDetail(t, func(id string) (Detail, error) {
		asked = append(asked, id)
		return Detail{
			ID:      id,
			Label:   "Fact B",
			Type:    "feedback",
			Summary: "a one-line summary",
			Created: "2026-09-03T00:00:00Z",
			Body:    "the body of fact b, verbatim",
		}, nil
	})

	m, cmd := clickNode(t, m, "b")
	if !m.Opened() || m.OpenedID() != "b" {
		t.Fatalf("click on node b: Opened()=%v OpenedID()=%q, want true/\"b\"", m.Opened(), m.OpenedID())
	}
	if cmd == nil {
		t.Fatal("click on node b returned no command -- nothing would ever ask the DetailLoader")
	}

	msg := cmd()
	res, ok := msg.(detailResultMsg)
	if !ok {
		t.Fatalf("click command produced %T, want detailResultMsg", msg)
	}
	if len(asked) != 1 || asked[0] != "b" {
		t.Fatalf("DetailLoader was asked for %v, want exactly [b] -- one node's content, not the vault's", asked)
	}

	next, _ := m.Update(res)
	m = next.(Model)

	got, err := m.OpenedDetail()
	if err != nil {
		t.Fatalf("OpenedDetail error after a successful load: %v", err)
	}
	if got.Body != "the body of fact b, verbatim" {
		t.Fatalf("OpenedDetail().Body = %q, want the loader's own body", got.Body)
	}
	view := m.View()
	if !containsAll(view, "Fact B", "the body of fact b, verbatim", "a one-line summary", "feedback") {
		t.Fatalf("the opened node's frame does not show the thing itself:\n%s", view)
	}

	// [esc] closes the node and returns to the graph, not out of the pane.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.Opened() {
		t.Fatalf("[esc] left node %q open", m.OpenedID())
	}
	if !strings.Contains(m.View(), "click a node") {
		t.Fatalf("[esc] did not return to the graph frame:\n%s", m.View())
	}
}

// TestDragOnANodeDoesNotOpenIt is verb 3's negative and verb 4's
// guarantee: the same press and the same release, with motion in between,
// must still be a move.
func TestDragOnANodeDoesNotOpenIt(t *testing.T) {
	var asked []string
	m := loadedWithDetail(t, func(id string) (Detail, error) {
		asked = append(asked, id)
		return Detail{ID: id}, nil
	})
	x, y, _ := m.ScreenPositionOf("a")

	// Move AWAY from whichever horizontal edge the layout put node a on,
	// so the target stays in bounds and the drag is a real move rather
	// than a clamp back onto the same cell.
	w, _ := m.canvasSize()
	dx := 3
	if x+dx >= w {
		dx = -3
	}

	next, _ := m.Update(tea.MouseMsg{X: x + boxInset, Y: y + boxInset + headerRows, Action: tea.MouseActionPress})
	m = next.(Model)
	next, _ = m.Update(tea.MouseMsg{X: x + dx + boxInset, Y: y + boxInset + headerRows, Action: tea.MouseActionMotion})
	m = next.(Model)
	next, cmd := m.Update(tea.MouseMsg{X: x + dx + boxInset, Y: y + boxInset + headerRows, Action: tea.MouseActionRelease})
	m = next.(Model)

	if m.Opened() {
		t.Fatalf("a drag opened node %q -- motion must make this a move, not a click", m.OpenedID())
	}
	if cmd != nil || len(asked) != 0 {
		t.Fatalf("a drag asked the DetailLoader for %v", asked)
	}
	fx, _, _ := m.PositionOf("a")
	if fx != x+dx {
		t.Fatalf("the drag did not move node a: x = %d, want %d", fx, x+dx)
	}
}

// TestOpenWithNoDetailLoaderSaysSoRatherThanShowingNothing: with no
// loader wired, clicking must report that there is no source -- an empty
// body would read as "this node has no content", which is a different and
// false claim.
func TestOpenWithNoDetailLoaderSaysSoRatherThanShowingNothing(t *testing.T) {
	m := loaded(t) // New(fetch) with no WithDetailLoader
	m, cmd := clickNode(t, m, "a")
	if cmd != nil {
		t.Fatal("click with no DetailLoader returned a command")
	}
	if !m.Opened() {
		t.Fatal("click with no DetailLoader did not open the node at all")
	}
	_, err := m.OpenedDetail()
	if err == nil {
		t.Fatal("OpenedDetail() error = nil with no DetailLoader wired, want an honest refusal")
	}
	if !strings.Contains(m.View(), "no node-detail source is configured") {
		t.Fatalf("the frame does not say why the node cannot be shown:\n%s", m.View())
	}
}

// TestOpenReportsTheLoadersOwnError: a real read failure must surface the
// reason, never an empty body.
func TestOpenReportsTheLoadersOwnError(t *testing.T) {
	wantErr := errors.New("read agent/facts/a.md: no such file or directory")
	m := loadedWithDetail(t, func(id string) (Detail, error) { return Detail{}, wantErr })
	m, cmd := clickNode(t, m, "a")
	next, _ := m.Update(cmd())
	m = next.(Model)

	if _, err := m.OpenedDetail(); err == nil || err.Error() != wantErr.Error() {
		t.Fatalf("OpenedDetail() error = %v, want the loader's own %v", err, wantErr)
	}
	// The error is WRAPPED to the pane width rather than truncated, so
	// compare against the frame with its wrapping undone -- the reason has
	// to be present in full, not merely start correctly.
	if !containsAll(unwrap(m.View()), "could not open a", wantErr.Error()) {
		t.Fatalf("the frame does not surface the loader's own error in full:\n%s", m.View())
	}
}

// TestStaleDetailLoadIsDropped: a slow load for a node the user already
// closed must not paint itself over what is on screen now.
func TestStaleDetailLoadIsDropped(t *testing.T) {
	m := loadedWithDetail(t, func(id string) (Detail, error) { return Detail{ID: id, Body: "b body"}, nil })
	m, _ = clickNode(t, m, "b")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	next, _ = m.Update(detailResultMsg{id: "b", detail: Detail{ID: "b", Body: "b body"}})
	m = next.(Model)
	if m.Opened() {
		t.Fatalf("a detail result for a closed node re-opened it as %q", m.OpenedID())
	}
}

// TestZoomKeysAndResetMatchTheWheel: the same verb without a mouse, plus
// [0], which is the only way back from a pan a user has lost themselves
// in.
func TestZoomKeysAndResetMatchTheWheel(t *testing.T) {
	m := loaded(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	m = next.(Model)
	if m.Zoom() <= 1 {
		t.Fatalf("[+] Zoom() = %v, want > 1", m.Zoom())
	}
	m.panX, m.panY = 7, -3
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("0")})
	m = next.(Model)
	if px, py := m.PanOffset(); m.Zoom() != 1 || px != 0 || py != 0 {
		t.Fatalf("[0] left zoom %v pan (%d,%d), want 1 and (0,0)", m.Zoom(), px, py)
	}
}

// ---------------------------------------------------------------------
// The header count line and the legend.
// ---------------------------------------------------------------------

// TestHeaderCountsRealKindsAndRefusesToInventOne is the count line's own
// hard rule (agent-estate#1006: "Never a fabricated count: if a kind
// cannot be counted, say so rather than printing 0"). A node whose kind
// never resolved is neither folded into a kind's total nor dropped: it is
// reported in words.
func TestHeaderCountsRealKindsAndRefusesToInventOne(t *testing.T) {
	g := Graph{
		Nodes: []Node{
			{ID: "a", Type: "project"},
			{ID: "b", Type: "project"},
			{ID: "c", Type: "user"},
			{ID: "d", Type: ""}, // its source could not be read
		},
		Edges: []Edge{{From: "a", To: "b"}},
	}
	m := New(func() (Graph, error) { return g, nil })
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = next.(Model)
	next, _ = m.Update(fetchResultMsg{graph: g})
	m = next.(Model)

	line := m.headerLine()
	if !containsAll(line, "4 facts", "1 links", "2 project", "1 user", "0 feedback", "0 reference") {
		t.Fatalf("header line does not count the real index: %q", line)
	}
	if !strings.Contains(line, "+1 whose kind could not be read") {
		t.Fatalf("header line silently absorbed the unresolved node instead of saying so: %q", line)
	}
	if strings.Contains(line, "1 ,") {
		t.Fatalf("header line rendered an empty kind name: %q", line)
	}
	if !strings.Contains(m.View(), line) {
		t.Fatalf("the header line is not on the rendered frame:\n%s", m.View())
	}
}

// TestLegendAndLabelsAreOnTheFrame: the two remaining pieces
// agent-estate#1006 named as missing -- a colour->kind key top-left, and
// each node's own text label beside it.
func TestLegendAndLabelsAreOnTheFrame(t *testing.T) {
	m := loaded(t)
	view := m.View()

	for _, kind := range knownTypeOrder {
		if !strings.Contains(view, kind) {
			t.Fatalf("legend does not name kind %q:\n%s", kind, view)
		}
	}
	// The legend is drawn INSIDE the canvas at its top-left, so the first
	// kind must appear on the frame's first canvas row, not below the box.
	lines := strings.Split(view, "\n")
	if len(lines) < 3 || !strings.Contains(lines[2], knownTypeOrder[0]) {
		t.Fatalf("legend is not at the canvas top-left (row 2 of the frame):\n%s", view)
	}

	for _, want := range []string{"A", "B", "C"} {
		if !strings.Contains(view, want) {
			t.Fatalf("node label %q is not painted on the frame:\n%s", want, view)
		}
	}
}

// unwrap collapses a rendered frame's line breaks and padding into single
// spaces, so an assertion can ask "is this whole sentence on screen"
// without depending on where lipgloss chose to wrap it.
func unwrap(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

// TestKeyboardSelectAndOpenReachesTheSameLoaderAsAClick: [n]/[p]+[enter]
// must go through openNode itself, not a parallel path -- a second way in
// that resolves content differently is how a UI ends up with one verb
// that works and one that lies. Also the only way this verb is reachable
// at all over SSH, with mouse reporting off, or from vhs (which cannot
// send a mouse event), which is why it exists.
func TestKeyboardSelectAndOpenReachesTheSameLoaderAsAClick(t *testing.T) {
	var asked []string
	m := loadedWithDetail(t, func(id string) (Detail, error) {
		asked = append(asked, id)
		return Detail{ID: id, Label: "opened " + id, Body: "body of " + id}, nil
	})

	if m.FocusedID() != "" {
		t.Fatalf("FocusedID() = %q before any selection, want \"\"", m.FocusedID())
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if m.FocusedID() != "a" {
		t.Fatalf("[n] from no selection focused %q, want the first node \"a\"", m.FocusedID())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if m.FocusedID() != "b" {
		t.Fatalf("second [n] focused %q, want \"b\"", m.FocusedID())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = next.(Model)
	if m.FocusedID() != "a" {
		t.Fatalf("[p] focused %q, want back to \"a\"", m.FocusedID())
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.Opened() || m.OpenedID() != "a" {
		t.Fatalf("[enter] Opened()=%v OpenedID()=%q, want true/\"a\"", m.Opened(), m.OpenedID())
	}
	if cmd == nil {
		t.Fatal("[enter] returned no command -- the DetailLoader is never asked")
	}
	next, _ = m.Update(cmd())
	m = next.(Model)

	if len(asked) != 1 || asked[0] != "a" {
		t.Fatalf("DetailLoader was asked for %v, want exactly [a]", asked)
	}
	if !strings.Contains(m.View(), "body of a") {
		t.Fatalf("[enter] did not render the opened node's own content:\n%s", m.View())
	}
}

// TestFocusWrapsAndSurvivesNothingSelected guards moveFocus's arithmetic
// at both ends, including the negative-modulo case a plain % gets wrong.
func TestFocusWrapsAndSurvivesNothingSelected(t *testing.T) {
	m := loaded(t) // fakeGraph: a, b, c
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
	m = next.(Model)
	if m.FocusedID() != "c" {
		t.Fatalf("[p] from no selection focused %q, want the last node \"c\"", m.FocusedID())
	}
	for i := 0; i < 3; i++ {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("p")})
		m = next.(Model)
	}
	if m.FocusedID() != "c" {
		t.Fatalf("three more [p] over a 3-node graph focused %q, want back to \"c\"", m.FocusedID())
	}

	empty := New(func() (Graph, error) { return Graph{}, nil })
	next, _ = empty.Update(fetchResultMsg{graph: Graph{}})
	e := next.(Model)
	next, cmd := e.Update(tea.KeyMsg{Type: tea.KeyEnter})
	e = next.(Model)
	if e.Opened() || cmd != nil {
		t.Fatal("[enter] on an empty graph opened something")
	}
}
