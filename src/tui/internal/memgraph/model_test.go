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
	// at (0,0), the border included) -- boxInset cells inside that is
	// where a node's own canvas-interior position (PositionOf) actually
	// sits. See handleMouse's own doc comment.
	next, _ := m.Update(tea.MouseMsg{X: x0 + boxInset, Y: y0 + boxInset, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.grabbed != "a" {
		t.Fatalf("press on node a's own cell: grabbed = %q, want \"a\"", m.grabbed)
	}

	w, h := m.canvasSize()
	tx, ty := w/2, h/2
	if tx == x0 && ty == y0 {
		tx++
	}
	next, _ = m.Update(tea.MouseMsg{X: tx + boxInset, Y: ty + boxInset, Action: tea.MouseActionMotion})
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
	next, _ = m.Update(tea.MouseMsg{X: rx + boxInset, Y: ry + boxInset, Action: tea.MouseActionRelease})
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

	next, _ := m.Update(tea.MouseMsg{X: x0 + boxInset + 5, Y: y0 + boxInset + 2, Action: tea.MouseActionMotion})
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

	next, _ := m.Update(tea.MouseMsg{X: fx + boxInset, Y: fy + boxInset, Action: tea.MouseActionPress})
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
