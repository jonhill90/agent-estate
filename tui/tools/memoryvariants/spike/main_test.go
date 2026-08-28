package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestDragSequenceMovesGrabbedNode drives Update() with the same
// press/motion/release sequence a real terminal's SGR mouse reporting
// would deliver, standing in for the live-tmux drive this repo's own
// convention asks for (AGENTS.md: "cite the test that drives it through
// Update, or say not verified against a live session") -- tmux itself
// cannot start a server in this sandbox (`server exited unexpectedly` on
// every `tmux new-session`, with or without the sandbox disabled, even
// for a trivial `sleep 30` pane -- not specific to this program), so this
// is the mechanism-level verification available here. What it does NOT
// verify: real terminal mouse-event rate/resolution, or how a drag feels
// over an actual SSH/tmux round trip -- those need a live session this
// environment cannot provide.
func TestDragSequenceMovesGrabbedNode(t *testing.T) {
	m := initialModel()
	a := m.nodes[0] // id "a", starts at (10, 5)
	if a.x != 10 || a.y != 5 {
		t.Fatalf("fixture assumption changed: node a at (%d,%d), want (10,5)", a.x, a.y)
	}

	press := tea.MouseMsg{X: 10, Y: 5, Action: tea.MouseActionPress}
	next, _ := m.Update(press)
	m = next.(model)
	if m.grabbed != 0 {
		t.Fatalf("press on node a: grabbed = %d, want 0", m.grabbed)
	}

	motion := tea.MouseMsg{X: 25, Y: 9, Action: tea.MouseActionMotion}
	next, _ = m.Update(motion)
	m = next.(model)
	if m.nodes[0].x != 25 || m.nodes[0].y != 9 {
		t.Fatalf("motion while grabbed: node a at (%d,%d), want (25,9)", m.nodes[0].x, m.nodes[0].y)
	}
	if m.grabbed != 0 {
		t.Fatalf("still mid-drag: grabbed = %d, want 0", m.grabbed)
	}

	release := tea.MouseMsg{X: 40, Y: 12, Action: tea.MouseActionRelease}
	next, _ = m.Update(release)
	m = next.(model)
	if m.nodes[0].x != 40 || m.nodes[0].y != 12 {
		t.Fatalf("release: node a at (%d,%d), want (40,12)", m.nodes[0].x, m.nodes[0].y)
	}
	if m.grabbed != -1 {
		t.Fatalf("after release: grabbed = %d, want -1 (nothing held)", m.grabbed)
	}
	if len(m.log) != 3 {
		t.Fatalf("drag log has %d events, want 3 (down/motion/up)", len(m.log))
	}
}

// TestMotionWithoutPressDoesNotGrab covers the negative case: a mouse
// motion event that never started with a press over a node must not move
// anything -- the panning-vs-dragging distinction Hill90's own canvas
// enforces (KnowledgeGraph.tsx's isPanning/draggingNode split).
func TestMotionWithoutPressDoesNotGrab(t *testing.T) {
	m := initialModel()
	motion := tea.MouseMsg{X: 25, Y: 9, Action: tea.MouseActionMotion}
	next, _ := m.Update(motion)
	m = next.(model)
	if m.nodes[0].x != 10 || m.nodes[0].y != 5 {
		t.Fatalf("ungrabbed motion moved node a to (%d,%d), want unchanged (10,5)", m.nodes[0].x, m.nodes[0].y)
	}
	if m.grabbed != -1 {
		t.Fatalf("grabbed = %d after motion with no prior press, want -1", m.grabbed)
	}
}
