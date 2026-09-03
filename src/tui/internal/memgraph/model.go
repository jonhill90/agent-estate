package memgraph

import (
	"math"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// minCanvasW/minCanvasH floor the interior drawing area so a very small
// (or not-yet-sized) pane still has somewhere to place nodes rather than
// dividing by a near-zero width inside layout().
const (
	minCanvasW = 20
	minCanvasH = 6
)

// cellPos is one node's position, in interior canvas cells -- the same
// integer grid view.go paints, and exactly what a mouse event's own X/Y
// already are once shell.Model has translated them into this pane's
// local coordinates (see internal/shell's own MouseMsg handling).
type cellPos struct{ x, y int }

type fetchResultMsg struct {
	graph Graph
	err   error
}

// Model is the graph pane: fetch the vault once (Fetcher), lay it out
// once with the same force layout tools/memoryvariants/layout.go used,
// then let the user grab any node with the mouse and drag it -- Update
// below is what tools/memoryvariants ONLY spiked (spike/main.go, never
// wired into the app); this is that spike's mechanism, ported, wired
// into a pane a real Program can host.
type Model struct {
	fetch    Fetcher
	graph    Graph
	fetchErr error
	loaded   bool

	// pos holds every node's current cell position -- the ONE place a
	// drag writes. order is the graph's own node order (Graph.Nodes),
	// kept alongside pos so rendering and hit-testing iterate
	// deterministically rather than over Go's randomized map order.
	pos   map[string]cellPos
	order []string

	// grabbed is the node id currently held by a mouse-down, "" when
	// nothing is grabbed -- the same press/motion/release state machine
	// tools/memoryvariants/spike/main.go proved live.
	grabbed string

	width, height int
	theme         theme.Theme
}

// New builds a Model with fetch wired in -- not yet loaded; Init below
// fires the one read.
func New(fetch Fetcher) Model {
	return Model{
		fetch:  fetch,
		pos:    map[string]cellPos{},
		width:  100,
		height: 30,
		theme:  theme.Default,
	}
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this repo exposes.
func (m Model) WithTheme(th theme.Theme) Model {
	m.theme = th
	return m
}

func (m Model) Init() tea.Cmd {
	return doFetch(m.fetch)
}

func doFetch(fetch Fetcher) tea.Cmd {
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		g, err := fetch()
		return fetchResultMsg{graph: g, err: err}
	}
}

// FetchErr is the last fetch's own error, if any -- exported so a host
// pane (internal/knowledge) can decide how to frame it without reaching
// into private state.
func (m Model) FetchErr() error { return m.fetchErr }

// Loaded reports whether a graph has ever been fetched successfully.
func (m Model) Loaded() bool { return m.loaded }

// NodeCount is the current graph's own node count -- used by tests and by
// a host pane's own summary line.
func (m Model) NodeCount() int { return len(m.graph.Nodes) }

// PositionOf returns node id's current cell position -- exported for
// tests (model_test.go's own drag assertions) rather than reaching into
// the private pos map directly.
func (m Model) PositionOf(id string) (x, y int, ok bool) {
	p, ok := m.pos[id]
	return p.x, p.y, ok
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.withClamped(), nil

	case fetchResultMsg:
		m.fetchErr = msg.err
		if msg.err == nil {
			m.graph = msg.graph
			m.loaded = true
			m = m.withLayout()
		}
		return m, nil

	case tea.KeyMsg:
		if msg.String() == "r" {
			return m, doFetch(m.fetch)
		}
		return m, nil

	case tea.MouseMsg:
		return m.handleMouse(msg), nil
	}
	return m, nil
}

// canvasSize is the interior drawing area -- pane width/height minus the
// box border (2) and, for height, the two trailing chrome lines (legend,
// hint) view.go always renders below the box. Floored at
// minCanvasW/minCanvasH so a not-yet-sized pane (New's own 100x30
// defaults, or a genuinely tiny terminal) never divides layout() by a
// near-zero dimension.
func (m Model) canvasSize() (w, h int) {
	w = m.width - 2
	h = m.height - 4
	if w < minCanvasW {
		w = minCanvasW
	}
	if h < minCanvasH {
		h = minCanvasH
	}
	return w, h
}

// withLayout recomputes every node's position from scratch via the force
// layout -- called exactly once per successful fetch (Update's
// fetchResultMsg case), never on a resize or a drag, so neither
// invalidates a position the user (or the layout) already settled on.
//
// Height is doubled before halving back down in cellOf below -- terminal
// cells read roughly 2:1 tall/wide; without this the halved y range only
// ever fills the canvas's top half. Ported unchanged from
// tools/memoryvariants/grid.go's own cell() doc comment.
func (m Model) withLayout() Model {
	w, h := m.canvasSize()
	raw := layout(m.graph, float64(w), float64(h)*2, 200)

	pos := make(map[string]cellPos, len(m.graph.Nodes))
	order := make([]string, 0, len(m.graph.Nodes))
	for _, nd := range m.graph.Nodes {
		x, y := cellOf(raw[nd.ID], w, h)
		pos[nd.ID] = cellPos{x, y}
		order = append(order, nd.ID)
	}
	m.pos = pos
	m.order = order
	return m
}

// cellOf maps a continuous layout position into an interior cell,
// clamped to [0,w) x [0,h).
func cellOf(p point, w, h int) (int, int) {
	x := int(math.Round(p.x))
	y := int(math.Round(p.y * 0.5))
	if x < 0 {
		x = 0
	}
	if x >= w {
		x = w - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= h {
		y = h - 1
	}
	return x, y
}

// withClamped re-clamps every EXISTING position into the current canvas
// bounds after a resize -- deliberately not a re-layout: a resize while
// the user has already dragged nodes around must not throw that work
// away, only keep it on screen.
func (m Model) withClamped() Model {
	w, h := m.canvasSize()
	for id, p := range m.pos {
		if p.x >= w {
			p.x = w - 1
		}
		if p.y >= h {
			p.y = h - 1
		}
		if p.x < 0 {
			p.x = 0
		}
		if p.y < 0 {
			p.y = 0
		}
		m.pos[id] = p
	}
	return m
}

// boxInset is the box border's own width (view.go's lipgloss.Border box,
// one cell top/left): a mouse event's X/Y arrive in this pane's own
// top-left-at-(0,0) coordinates (the exact frame View() renders, and what
// a caller must translate a real terminal's absolute mouse coordinates
// into first -- see internal/shell's own MouseMsg handling for the
// nav-sidebar-width subtraction that does this for X), but every node
// position in m.pos is in CANVAS-INTERIOR coordinates, one cell inside
// that border. Every mouse coordinate is translated by this exactly once,
// here, rather than at each of nodeNear/withPos's own call sites.
const boxInset = 1

// handleMouse is the press/motion/release state machine
// tools/memoryvariants/spike/main.go proved live over a real Program
// (spike/main_test.go's TestDragSequenceMovesGrabbedNode) -- ported here
// unchanged in shape: a press only grabs when it lands ON a node (within
// one cell, nodeNear below); motion/release only move something while a
// node is actually grabbed (spike's own TestMotionWithoutPressDoesNotGrab
// negative case, mirrored by this package's own model_test.go).
func (m Model) handleMouse(msg tea.MouseMsg) Model {
	x, y := msg.X-boxInset, msg.Y-boxInset
	switch msg.Action {
	case tea.MouseActionPress:
		if id, ok := m.nodeNear(x, y); ok {
			m.grabbed = id
		}
	case tea.MouseActionMotion:
		if m.grabbed != "" {
			m = m.withPos(m.grabbed, x, y)
		}
	case tea.MouseActionRelease:
		if m.grabbed != "" {
			m = m.withPos(m.grabbed, x, y)
		}
		m.grabbed = ""
	}
	return m
}

// nodeNear finds the node closest to (x,y), within one cell in either
// axis -- a real terminal mouse click is rarely pixel/cell-exact on a
// single-glyph target, so grabbing requires "on or adjacent to", not
// "exactly on".
func (m Model) nodeNear(x, y int) (string, bool) {
	best := ""
	bestD := 1 << 30
	for _, id := range m.order {
		p, ok := m.pos[id]
		if !ok {
			continue
		}
		dx, dy := p.x-x, p.y-y
		if dx < 0 {
			dx = -dx
		}
		if dy < 0 {
			dy = -dy
		}
		if dx > 1 || dy > 1 {
			continue
		}
		d := dx*dx + dy*dy
		if best == "" || d < bestD {
			best, bestD = id, d
		}
	}
	return best, best != ""
}

// withPos sets id's position, clamped to the current canvas bounds.
func (m Model) withPos(id string, x, y int) Model {
	w, h := m.canvasSize()
	if x < 0 {
		x = 0
	}
	if x >= w {
		x = w - 1
	}
	if y < 0 {
		y = 0
	}
	if y >= h {
		y = h - 1
	}
	m.pos[id] = cellPos{x, y}
	return m
}
