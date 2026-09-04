package memgraph

import (
	"errors"
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

// minZoom/maxZoom/zoomStep bound the scroll-to-zoom verb. The bounds are
// not taste: below minZoom every node in a real vault rounds onto the
// same handful of cells and the picture stops being readable at all,
// and above maxZoom a canvas-sized layout is entirely off screen with
// nothing to pan back to unless the user already knows where they went.
const (
	minZoom  = 0.25
	maxZoom  = 4.0
	zoomStep = 1.25
)

// errNoDetailLoader is what opening a node reports when no DetailLoader
// was ever wired in -- an honest, visible "we cannot look" rather than an
// empty Detail rendered as though the node genuinely had no content.
// Same discipline as View's own fetch-error branch (AGENTS.md's "absence
// is a typed value, never a bare zero").
var errNoDetailLoader = errors.New("no node-detail source is configured for this graph")

// cellPos is one node's position, in interior canvas cells -- the same
// integer grid view.go paints, and exactly what a mouse event's own X/Y
// already are once shell.Model has translated them into this pane's
// local coordinates (see internal/shell's own MouseMsg handling).
//
// Since zoom/pan landed this is GRAPH space, not screen space: a node's
// stored position is what the layout (or a drag) decided, and toScreen/
// toGraph below are the only two places that convert between it and the
// cell a user actually clicks. At zoom 1 with no pan the two are the
// identity, which is why every position assertion written before zoom
// existed still holds.
type cellPos struct{ x, y int }

type fetchResultMsg struct {
	graph Graph
	err   error
}

// detailResultMsg carries one opened node's own content back from the
// DetailLoader. id is checked against the node currently open before it
// is applied -- a slow load for a node the user already navigated away
// from must never overwrite what is on screen (the same stale-load guard
// internal/knowledge.Model's own factResultMsg case documents).
type detailResultMsg struct {
	id     string
	detail Detail
	err    error
}

// Model is the graph pane. It fetches the vault once (Fetcher), lays it
// out once with a force embedder, and then supports the four interaction
// verbs Jon's own app prints in its corner and jonhill90/agent-estate#1006
// asked for here:
//
//	scroll to zoom      -- MouseButtonWheelUp/Down, or [+]/[-]/[0]
//	drag to pan         -- press on empty canvas, then motion
//	click a node to open it -- press and release on one node with no
//	                       motion between them; the DetailLoader seam
//	                       resolves that ONE node's content
//	drag a node to move it  -- press on a node, then motion (agent-estate#937)
//
// Only the last of the four existed before agent-estate#1006; the press/motion/
// release state machine it introduced is unchanged in shape here, and
// every one of its original tests still passes against this Model.
type Model struct {
	fetch    Fetcher
	load     DetailLoader
	graph    Graph
	fetchErr error
	loaded   bool

	// pos holds every node's current GRAPH-space cell position -- the ONE
	// place a drag writes. order is the graph's own node order
	// (Graph.Nodes), kept alongside pos so rendering and hit-testing
	// iterate deterministically rather than over Go's randomized map
	// order.
	pos   map[string]cellPos
	order []string

	// zoom/panX/panY are the view transform, and are deliberately NOT
	// baked into pos: zooming or panning must never destroy where the
	// layout (or the user's own drag) put a node, exactly as withClamped
	// already refuses to re-layout on a resize.
	zoom       float64
	panX, panY int

	// grabbed is the node id currently held by a mouse-down, "" when
	// nothing is grabbed. dragged records whether any motion arrived
	// since that press -- it is the whole difference between "drag a node
	// to move it" and "click a node to open it", which are otherwise the
	// same two events.
	grabbed string
	dragged bool

	// focus is the index into order of the node the KEYBOARD has selected,
	// -1 for none. Click-to-open is a mouse verb, but a pane only openable
	// with a mouse is not openable over SSH (internal/sshserver), in a
	// terminal with mouse reporting off, or by any capture harness this
	// repo owns -- vhs, which is how a UI change here is evidenced, cannot
	// send a mouse event at all. [n]/[p] move this and [enter] opens it,
	// through the exact same openNode the click uses.
	focus int

	// panning/panFrom is the same press-motion-release shape for a press
	// that landed on empty canvas instead of on a node.
	panning            bool
	panFromX, panFromY int

	// open is the node id whose Detail is currently displayed, "" when
	// the graph itself is showing. openLoaded/openErr are the honest
	// three-state absence this repo requires everywhere data may not be
	// there: loading, failed (with the reason), or genuinely resolved.
	open       string
	openDetail Detail
	openLoaded bool
	openErr    error
	openScroll int

	width, height int
	theme         theme.Theme
}

// New builds a Model with fetch wired in -- not yet loaded; Init below
// fires the one read. A DetailLoader is wired separately
// (WithDetailLoader) so a caller that only wants the picture is not
// forced to supply one, and gets an honest "not configured" on open
// rather than a fabricated node body.
func New(fetch Fetcher) Model {
	return Model{
		fetch:  fetch,
		pos:    map[string]cellPos{},
		zoom:   1,
		focus:  -1,
		width:  100,
		height: 30,
		theme:  theme.Default,
	}
}

// WithDetailLoader returns a copy of m whose click-to-open verb resolves
// node content through load -- cmd/estate's own buildMemgraphDetail in
// the real binary. Left unset, clicking a node still opens it and shows
// errNoDetailLoader, which is the point: the pane says it cannot look,
// instead of showing an empty body that reads as "this node has nothing
// in it".
func (m Model) WithDetailLoader(load DetailLoader) Model {
	m.load = load
	return m
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

func doLoadDetail(load DetailLoader, id string) tea.Cmd {
	if load == nil {
		return nil
	}
	return func() tea.Msg {
		d, err := load(id)
		return detailResultMsg{id: id, detail: d, err: err}
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

// Opened reports whether a node's own content is currently displayed
// instead of the graph. internal/knowledge.Model reads this to decide
// whether an [esc] means "close this node" or "leave the graph" -- see
// its own handleKey, which must ask BEFORE forwarding the key here.
func (m Model) Opened() bool { return m.open != "" }

// OpenedID is the node id currently open, "" when the graph is showing.
func (m Model) OpenedID() string { return m.open }

// OpenedDetail is the currently open node's resolved content and the
// error, if any, from resolving it -- both exported (rather than only
// rendered) so a caller a package away can assert click-to-open actually
// reached the real loader, not just that some frame changed.
func (m Model) OpenedDetail() (Detail, error) { return m.openDetail, m.openErr }

// Zoom is the current scroll-to-zoom factor; 1 is the layout's own scale.
func (m Model) Zoom() float64 { return m.zoom }

// PanOffset is the current drag-to-pan offset, in graph cells.
func (m Model) PanOffset() (x, y int) { return m.panX, m.panY }

// PositionOf returns node id's current GRAPH-space cell position --
// exported for tests (model_test.go's own drag assertions) rather than
// reaching into the private pos map directly. This is where the node
// IS, not where it is currently drawn; ScreenPositionOf is the latter.
func (m Model) PositionOf(id string) (x, y int, ok bool) {
	p, ok := m.pos[id]
	return p.x, p.y, ok
}

// ScreenPositionOf returns the interior canvas cell node id is currently
// PAINTED in, with zoom and pan applied -- the cell a user would have to
// click to hit it. Equal to PositionOf at zoom 1 with no pan.
func (m Model) ScreenPositionOf(id string) (x, y int, ok bool) {
	p, ok := m.pos[id]
	if !ok {
		return 0, 0, false
	}
	x, y = m.toScreen(p)
	return x, y, true
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
			m.focus = -1
			m = m.withLayout()
		}
		return m, nil

	case detailResultMsg:
		if msg.id != m.open {
			// A second open (or a close) superseded this load -- drop it
			// rather than letting a slow read overwrite the node the user
			// is actually looking at now.
			return m, nil
		}
		m.openErr = msg.err
		m.openLoaded = msg.err == nil
		if msg.err == nil {
			m.openDetail = msg.detail
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		next, cmd := m.handleMouse(msg)
		return next, cmd
	}
	return m, nil
}

// handleKey owns the keyboard half of the same verbs the mouse drives --
// zoom without a wheel, and reading an opened node's body without one.
// Every key is deliberately inert while the graph is showing and the key
// is not one of this pane's own, because internal/knowledge forwards
// EVERY key here unconditionally (its Update's own doc comment) including
// keys meant for its list.
func (m Model) handleKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.open != "" {
		switch msg.String() {
		case "esc", "left", "backspace":
			return m.closeNode(), nil
		case "r":
			// Reload THIS node only -- one node's read, never the graph's.
			return m.openNode(m.open)
		case "down", "j":
			return m.scrollDetail(1), nil
		case "up", "k":
			return m.scrollDetail(-1), nil
		case "pgdown", "ctrl+d":
			return m.scrollDetail(10), nil
		case "pgup", "ctrl+u":
			return m.scrollDetail(-10), nil
		case "home", "g":
			m.openScroll = 0
			return m, nil
		case "end", "G":
			return m.scrollDetail(1 << 20), nil
		}
		return m, nil
	}

	switch msg.String() {
	case "r":
		return m, doFetch(m.fetch)
	case "n", "tab":
		return m.moveFocus(1), nil
	case "p", "shift+tab":
		return m.moveFocus(-1), nil
	case "enter":
		if m.focus >= 0 && m.focus < len(m.order) {
			return m.openNode(m.order[m.focus])
		}
		return m, nil
	case "+", "=":
		return m.withZoom(m.zoom * zoomStep), nil
	case "-", "_":
		return m.withZoom(m.zoom / zoomStep), nil
	case "0":
		m.zoom, m.panX, m.panY = 1, 0, 0
		return m, nil
	}
	return m, nil
}

// moveFocus steps the keyboard selection by delta, wrapping, and starts
// at the first node when nothing is selected yet.
func (m Model) moveFocus(delta int) Model {
	n := len(m.order)
	if n == 0 {
		m.focus = -1
		return m
	}
	if m.focus < 0 {
		if delta >= 0 {
			m.focus = 0
		} else {
			m.focus = n - 1
		}
		return m
	}
	m.focus = ((m.focus+delta)%n + n) % n
	return m
}

// FocusedID is the node id the keyboard has selected, "" for none.
func (m Model) FocusedID() string {
	if m.focus < 0 || m.focus >= len(m.order) {
		return ""
	}
	return m.order[m.focus]
}

// canvasSize is the interior drawing area -- pane width/height minus the
// box border (2) and, for height, the two chrome lines view.go always
// renders outside the box (the header count line above it, the verb hint
// below it). Floored at minCanvasW/minCanvasH so a not-yet-sized pane
// (New's own 100x30 defaults, or a genuinely tiny terminal) never divides
// layout() by a near-zero dimension.
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
// fetchResultMsg case), never on a resize, a drag, a zoom or a pan, so
// none of those invalidates a position the user (or the layout) already
// settled on.
//
// Height is doubled before halving back down in cellOf below -- terminal
// cells read roughly 2:1 tall/wide; without this the halved y range only
// ever fills the canvas's top half. Ported unchanged from
// tools/memoryvariants/grid.go's own cell() doc comment.
func (m Model) withLayout() Model {
	w, h := m.canvasSize()
	top := m.canvasTop()
	raw := layout(m.graph, float64(w), float64(h-top)*2, 200)

	pos := make(map[string]cellPos, len(m.graph.Nodes))
	order := make([]string, 0, len(m.graph.Nodes))
	for _, nd := range m.graph.Nodes {
		x, y := cellOf(raw[nd.ID], w, h-top)
		pos[nd.ID] = cellPos{x, y + top}
		order = append(order, nd.ID)
	}
	m.pos = pos
	m.order = order
	return m
}

// canvasTop is how many interior rows the legend owns outright at the
// canvas's top-left. The legend is drawn LAST (view.go) so it is always
// readable, which means anything laid out underneath it would simply be
// invisible -- reserving the rows instead is why no node is ever hidden
// by the key that is supposed to explain it. Bounded at a third of the
// canvas so a short pane is not mostly legend.
func (m Model) canvasTop() int {
	_, h := m.canvasSize()
	n := len(m.legendRows())
	if max := h / 3; n > max {
		n = max
	}
	if n < 0 {
		n = 0
	}
	return n
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
	for id, p := range m.pos {
		x, y := m.clampToCanvas(p.x, p.y)
		m.pos[id] = cellPos{x, y}
	}
	return m
}

// clampToCanvas is the one definition of where a node is allowed to sit:
// inside the interior canvas, and below the rows canvasTop reserves for
// the legend.
func (m Model) clampToCanvas(x, y int) (int, int) {
	w, h := m.canvasSize()
	top := m.canvasTop()
	if x < 0 {
		x = 0
	}
	if x >= w {
		x = w - 1
	}
	if y < top {
		y = top
	}
	if y >= h {
		y = h - 1
	}
	return x, y
}

// boxInset is the box border's own width (view.go's lipgloss.Border box,
// one cell top/left): a mouse event's X/Y arrive in this pane's own
// top-left-at-(0,0) coordinates (the exact frame View() renders, and what
// a caller must translate a real terminal's absolute mouse coordinates
// into first -- see internal/shell's own MouseMsg handling for the
// nav-sidebar-width subtraction that does this for X), but every node
// position in m.pos is in CANVAS-INTERIOR coordinates, one cell inside
// that border. Every mouse coordinate is translated by this exactly once,
// here, rather than at each of nodeAtScreen/withPos's own call sites.
//
// The header count line View() now renders ABOVE the box costs a row too,
// which is why Y is inset by headerRows+boxInset rather than boxInset
// alone.
const boxInset = 1

// headerRows is how many rows View() renders above the box -- the header
// count line. Part of the mouse Y translation for the same reason
// boxInset is.
const headerRows = 1

// CanvasOrigin is the pane-local cell that interior canvas cell (0,0) is
// painted at -- the box border inset, plus the header count line View()
// renders above the frame. Exported so a caller a package away
// (internal/shell's own mouse-routing tests, internal/knowledge's) can
// address a node's cell without hardcoding this pane's chrome: adding the
// header line moved every node down one row, and a test that spelled the
// old inset out by hand is exactly what silently stops testing the thing
// it names.
func CanvasOrigin() (x, y int) { return boxInset, boxInset + headerRows }

// toScreen converts a node's stored graph position into the interior
// canvas cell it is painted in, applying zoom about the canvas centre and
// then the pan offset. toGraph is its inverse (to integer-rounding
// precision). At zoom 1 with no pan both are the identity, which is what
// keeps every pre-zoom coordinate assertion in this package's tests true.
func (m Model) toScreen(p cellPos) (int, int) {
	w, h := m.canvasSize()
	cx, cy := w/2, h/2
	sx := cx + int(math.Round(float64(p.x+m.panX-cx)*m.zoom))
	sy := cy + int(math.Round(float64(p.y+m.panY-cy)*m.zoom))
	return sx, sy
}

func (m Model) toGraph(sx, sy int) (int, int) {
	w, h := m.canvasSize()
	cx, cy := w/2, h/2
	gx := cx + int(math.Round(float64(sx-cx)/m.zoom)) - m.panX
	gy := cy + int(math.Round(float64(sy-cy)/m.zoom)) - m.panY
	return gx, gy
}

// withZoom clamps z into [minZoom,maxZoom] and applies it. Nothing about
// m.pos changes -- see the zoom field's own comment.
func (m Model) withZoom(z float64) Model {
	if z < minZoom {
		z = minZoom
	}
	if z > maxZoom {
		z = maxZoom
	}
	m.zoom = z
	return m
}

// handleMouse is the press/motion/release state machine agent-estate#937
// shipped for drag-to-move, extended with the two verbs agent-estate#1006 added on
// the same three events: a press that lands on EMPTY canvas now begins a
// pan instead of doing nothing, and a press+release on one node with no
// motion between them is a CLICK, which opens it.
//
// The click/drag split is the `dragged` flag and nothing else. Both
// gestures start with the identical press and end with the identical
// release; only whether any motion arrived in between distinguishes
// them, so that is exactly what is recorded.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Wheel FIRST. A wheel event arrives as MouseActionPress with a wheel
	// Button, so without this it would fall into the press case below and
	// be treated as a click on whatever node happened to be under the
	// cursor -- scrolling would open nodes.
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		if m.open != "" {
			return m.scrollDetail(-3), nil
		}
		return m.withZoom(m.zoom * zoomStep), nil
	case tea.MouseButtonWheelDown:
		if m.open != "" {
			return m.scrollDetail(3), nil
		}
		return m.withZoom(m.zoom / zoomStep), nil
	}

	if m.open != "" {
		// A node's content is showing; the canvas underneath is not on
		// screen, so a click on it must not move or open anything.
		return m, nil
	}

	x, y := msg.X-boxInset, msg.Y-boxInset-headerRows
	switch msg.Action {
	case tea.MouseActionPress:
		if id, ok := m.nodeAtScreen(x, y); ok {
			m.grabbed, m.dragged = id, false
			return m, nil
		}
		m.panning = true
		m.panFromX, m.panFromY = x, y

	case tea.MouseActionMotion:
		if m.grabbed != "" {
			m.dragged = true
			gx, gy := m.toGraph(x, y)
			return m.withPos(m.grabbed, gx, gy), nil
		}
		if m.panning {
			m = m.withPanBy(x-m.panFromX, y-m.panFromY)
			m.panFromX, m.panFromY = x, y
		}

	case tea.MouseActionRelease:
		if id := m.grabbed; id != "" {
			m.grabbed = ""
			if !m.dragged {
				return m.openNode(id)
			}
			m.dragged = false
			gx, gy := m.toGraph(x, y)
			return m.withPos(id, gx, gy), nil
		}
		m.panning = false
	}
	return m, nil
}

// withPanBy shifts the view by a SCREEN-cell delta, converted into graph
// cells -- at 2x zoom the content has to move two screen cells for the
// view to have moved one graph cell, so dividing by zoom here is what
// makes a drag track the cursor at every zoom level rather than only at 1.
func (m Model) withPanBy(dsx, dsy int) Model {
	m.panX += int(math.Round(float64(dsx) / m.zoom))
	m.panY += int(math.Round(float64(dsy) / m.zoom))
	return m
}

// openNode switches the pane to id's own content and asks the
// DetailLoader for it. With no loader wired it still opens, showing
// errNoDetailLoader -- see that variable's own comment.
func (m Model) openNode(id string) (Model, tea.Cmd) {
	m.open = id
	m.openDetail = Detail{}
	m.openLoaded = false
	m.openErr = nil
	m.openScroll = 0
	if m.load == nil {
		m.openErr = errNoDetailLoader
		return m, nil
	}
	return m, doLoadDetail(m.load, id)
}

func (m Model) closeNode() Model {
	m.open = ""
	m.openDetail = Detail{}
	m.openLoaded = false
	m.openErr = nil
	m.openScroll = 0
	return m
}

// scrollDetail moves the opened node's body by delta lines, clamped so
// the last line is the furthest it can go -- the same clamp viewport
// would apply, without pulling a viewport into a pane whose only
// scrollable content is one node's body.
func (m Model) scrollDetail(delta int) Model {
	max := m.detailMaxScroll()
	m.openScroll += delta
	if m.openScroll > max {
		m.openScroll = max
	}
	if m.openScroll < 0 {
		m.openScroll = 0
	}
	return m
}

// nodeAtScreen finds the node painted closest to interior canvas cell
// (x,y), within one cell in either axis -- a real terminal mouse click is
// rarely cell-exact on a single-glyph target, so hitting one requires "on
// or adjacent to", not "exactly on".
//
// The comparison is in SCREEN space (toScreen), not graph space: once
// zoom exists, the cell a user clicks is the cell the node is drawn in,
// and a one-cell tolerance measured in graph cells would be four screen
// cells wide at 4x zoom and a quarter of one at 0.25x.
func (m Model) nodeAtScreen(x, y int) (string, bool) {
	best := ""
	bestD := 1 << 30
	for _, id := range m.order {
		p, ok := m.pos[id]
		if !ok {
			continue
		}
		sx, sy := m.toScreen(p)
		dx, dy := absInt(sx-x), absInt(sy-y)
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

// withPos sets id's GRAPH-space position, clamped to the current canvas
// bounds.
func (m Model) withPos(id string, x, y int) Model {
	cx, cy := m.clampToCanvas(x, y)
	m.pos[id] = cellPos{cx, cy}
	return m
}
