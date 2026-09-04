package memgraph

import (
	"fmt"
	"hash/fnv"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// knownTypeColor/knownTypeGlyph/fallbackPalette/fallbackGlyphs/hashIndex/
// colorFor/glyphFor/legend are tools/memoryvariants/main.go's own lookups,
// ported unchanged: typ is an open-ended string (graph.go's own doc
// comment), and an unrecognized one gets a deterministic hash-derived
// color/glyph rather than being refused, the same way Hill90's
// KnowledgeGraph.tsx colors an unknown type by hash.
var knownTypeColor = map[string]lipgloss.Color{
	"user":      lipgloss.Color("#c026d3"),
	"feedback":  lipgloss.Color("#f1c40f"),
	"project":   lipgloss.Color("#3b82f6"),
	"reference": lipgloss.Color("#22c55e"),
}

var knownTypeGlyph = map[string]string{
	"user":      "◆",
	"feedback":  "●",
	"project":   "■",
	"reference": "▲",
}

// knownTypeOrder is the vocabulary memory-conventions.md itself names
// ("type: user | feedback | project | reference"). It is the ONLY set of
// kinds this pane will report a zero count for -- a kind named by the
// convention and absent from the vault is a real, countable zero, the
// same way Jon's own app's header reads "0 agents". Any other kind is
// reported only when it is actually present, because this pane has no
// way to know what other kinds could exist.
var knownTypeOrder = []string{"user", "feedback", "project", "reference"}

var fallbackPalette = []lipgloss.Color{
	lipgloss.Color("#f97316"), lipgloss.Color("#06b6d4"), lipgloss.Color("#a855f7"),
	lipgloss.Color("#84cc16"), lipgloss.Color("#ec4899"), lipgloss.Color("#64748b"),
}

var fallbackGlyphs = []string{"◇", "○", "□", "△", "◈", "✦"}

func hashIndex(t string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(t))
	return int(h.Sum32()) % n
}

func colorFor(t string) lipgloss.Color {
	if c, ok := knownTypeColor[t]; ok {
		return c
	}
	return fallbackPalette[hashIndex(t, len(fallbackPalette))]
}

func glyphFor(t string) string {
	if g, ok := knownTypeGlyph[t]; ok {
		return g
	}
	if t == "" {
		return "·" // uncategorized: distinct from any hashed unknown type
	}
	return fallbackGlyphs[hashIndex(t, len(fallbackGlyphs))]
}

// kindsPresent lists every non-empty Type in the current graph, known
// kinds first (in knownTypeOrder) and any others after, alphabetically --
// a deterministic order for both the legend and the header line.
func (m Model) kindsPresent() []string {
	seen := map[string]bool{}
	for _, nd := range m.graph.Nodes {
		if nd.Type != "" {
			seen[nd.Type] = true
		}
	}
	out := make([]string, 0, len(seen)+len(knownTypeOrder))
	for _, t := range knownTypeOrder {
		out = append(out, t)
	}
	var extra []string
	for t := range seen {
		if _, known := knownTypeColor[t]; !known {
			extra = append(extra, t)
		}
	}
	sort.Strings(extra)
	return append(out, extra...)
}

// countByKind tallies the CURRENT graph's own nodes by kind, plus the
// number whose kind could not be resolved at all. Nothing here is
// derived from anything but m.graph, which is whatever the real Fetcher
// returned -- there is no path in this function that can produce a
// number the index did not.
func (m Model) countByKind() (counts map[string]int, unresolved int) {
	counts = map[string]int{}
	for _, nd := range m.graph.Nodes {
		if nd.Type == "" {
			// The index has this node but its kind never resolved (in the
			// real vault: the fact's own file could not be read, see
			// cmd/estate/buildMemgraphFetch's skip branch). Counting it
			// under any kind would be inventing the answer; it gets said
			// out loud instead -- see headerLine.
			unresolved++
			continue
		}
		counts[nd.Type]++
	}
	return counts, unresolved
}

// headerLine is the pane's own count line -- the shape Jon's app prints
// ("3 collections, 7 sources, 0 agents"), over OUR kinds, from the real
// index.
//
// The one rule it must not break (agent-estate#1006, verbatim: "Never a
// fabricated count: if a kind cannot be counted, say so rather than
// printing 0"): a node whose kind did not resolve is NOT folded into
// some kind's total and NOT silently dropped from the totals. It is
// reported, separately and in words, as unresolved.
func (m Model) headerLine() string {
	counts, unresolved := m.countByKind()

	parts := []string{
		fmt.Sprintf("%d facts", len(m.graph.Nodes)),
		fmt.Sprintf("%d links", len(m.graph.Edges)),
	}
	for _, t := range m.kindsPresent() {
		parts = append(parts, fmt.Sprintf("%d %s", counts[t], t))
	}
	line := strings.Join(parts, ", ")
	if unresolved > 0 {
		line += fmt.Sprintf("  (+%d whose kind could not be read -- not counted above)", unresolved)
	}
	return line
}

// legendRow is one line of the colour->kind key drawn INSIDE the canvas
// at its top-left, the way Jon's own app floats its legend over the graph
// rather than parking it under the frame.
type legendRow struct {
	text  string
	color lipgloss.Color
}

func (m Model) legendRows() []legendRow {
	kinds := m.kindsPresent()
	rows := make([]legendRow, 0, len(kinds))
	for _, t := range kinds {
		rows = append(rows, legendRow{text: glyphFor(t) + " " + t, color: colorFor(t)})
	}
	return rows
}

// bresenham walks the integer grid line from (x0,y0) to (x1,y1), calling
// plot for every cell on it, endpoints included -- ported unchanged from
// tools/memoryvariants/grid.go.
func bresenham(x0, y0, x1, y1 int, plot func(x, y int)) {
	dx := absInt(x1 - x0)
	dy := -absInt(y1 - y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	x, y := x0, y0
	for {
		plot(x, y)
		if x == x1 && y == y1 {
			break
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x += sx
		}
		if e2 <= dx {
			err += dx
			y += sy
		}
	}
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// canvas is the character grid View paints into -- a rune plus a colour
// per cell, exactly the shape tools/memoryvariants/grid.go's own grid()
// built, lifted into a type only because four separate passes (edges,
// labels, nodes, legend) now write into it instead of two.
type canvas struct {
	w, h   int
	runes  [][]rune
	colors [][]lipgloss.Color
}

func newCanvas(w, h int) *canvas {
	c := &canvas{w: w, h: h, runes: make([][]rune, h), colors: make([][]lipgloss.Color, h)}
	for y := 0; y < h; y++ {
		c.runes[y] = make([]rune, w)
		c.colors[y] = make([]lipgloss.Color, w)
		for x := 0; x < w; x++ {
			c.runes[y][x] = ' '
		}
	}
	return c
}

func (c *canvas) inBounds(x, y int) bool { return x >= 0 && x < c.w && y >= 0 && y < c.h }

// set writes unconditionally -- the later pass wins. Used by the node and
// legend passes, which must always be readable.
func (c *canvas) set(x, y int, r rune, col lipgloss.Color) {
	if !c.inBounds(x, y) {
		return
	}
	c.runes[y][x] = r
	c.colors[y][x] = col
}

// setIf writes only when the cell currently holds one of allow -- used by
// the edge and label passes so neither ever paints over something more
// important that is already there.
func (c *canvas) setIf(x, y int, r rune, col lipgloss.Color, allow ...rune) bool {
	if !c.inBounds(x, y) {
		return false
	}
	cur := c.runes[y][x]
	for _, a := range allow {
		if cur == a {
			c.runes[y][x] = r
			c.colors[y][x] = col
			return true
		}
	}
	return false
}

func (c *canvas) render() string {
	rows := make([]string, 0, c.h)
	for y := 0; y < c.h; y++ {
		var line strings.Builder
		for x := 0; x < c.w; x++ {
			r := c.runes[y][x]
			if r == ' ' {
				line.WriteRune(r)
				continue
			}
			line.WriteString(lipgloss.NewStyle().Foreground(c.colors[y][x]).Render(string(r)))
		}
		rows = append(rows, line.String())
	}
	return strings.Join(rows, "\n")
}

const (
	edgeRune  = '·'
	blankRune = ' '
	// maxLabelWidth caps how much of a node's own Label is painted beside
	// it. Vault slugs and titles run long; an uncapped label on a busy
	// graph paints over most of the canvas and hides the very edges it is
	// meant to annotate.
	maxLabelWidth = 20
)

var edgeColor = lipgloss.Color("#3a3a3a")

// View renders the graph as a bordered character-grid canvas -- nodes as
// their type's glyph, edges as Bresenham-walked lines, each node's own
// Label painted beside it, and a colour->kind legend floated over the
// top-left -- with the header count line above the frame and the four
// interaction verbs named below it.
//
// The three states an absent/unreachable vault can leave this in
// (fetching, fetch error, empty graph) are each rendered honestly rather
// than falling back to a fabricated demo graph -- AGENTS.md's "absence is
// a typed value" convention, this pane's own required reading per the
// issue that built it. Opening a node (viewDetail) obeys the same rule
// for its own three states.
func (m Model) View() string {
	if m.fetch == nil {
		// No Fetcher wired in at all (New(nil), or a caller that never
		// called WithGraph) -- a distinct, honest state from "still
		// loading": nothing is in flight and nothing ever will be.
		return lipgloss.NewStyle().Faint(true).Render("(memory graph not configured)")
	}
	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		return errStyle.Render("! could not read memory vault: "+m.fetchErr.Error()) + "\n" +
			lipgloss.NewStyle().Faint(true).Render("[r] retry")
	}
	if !m.loaded {
		return lipgloss.NewStyle().Faint(true).Render("(loading memory graph…)")
	}
	if m.open != "" {
		return m.viewDetail()
	}
	if len(m.graph.Nodes) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("(no facts in the memory vault yet)")
	}

	w, h := m.canvasSize()
	c := newCanvas(w, h)

	// 1. Edges, under everything.
	for _, e := range m.graph.Edges {
		p0, ok0 := m.pos[e.From]
		p1, ok1 := m.pos[e.To]
		if !ok0 || !ok1 {
			continue
		}
		x0, y0 := m.toScreen(p0)
		x1, y1 := m.toScreen(p1)
		bresenham(x0, y0, x1, y1, func(x, y int) {
			c.setIf(x, y, edgeRune, edgeColor, blankRune)
		})
	}

	// 2. Labels, over edges but never over another node's glyph -- so the
	//    node cells are reserved first, then each label is written into
	//    blank/edge cells only.
	reserved := map[[2]int]bool{}
	for _, nd := range m.graph.Nodes {
		if p, ok := m.pos[nd.ID]; ok {
			x, y := m.toScreen(p)
			reserved[[2]int{x, y}] = true
		}
	}
	labelColor := m.theme.Color(theme.RoleNeutral)
	for _, nd := range m.graph.Nodes {
		p, ok := m.pos[nd.ID]
		if !ok {
			continue
		}
		x, y := m.toScreen(p)
		label := nd.Label
		if label == "" {
			label = nd.ID
		}
		for i, r := range []rune(truncateRunes(label, maxLabelWidth)) {
			lx := x + 2 + i
			if reserved[[2]int{lx, y}] {
				break
			}
			if !c.setIf(lx, y, r, labelColor, blankRune, edgeRune) {
				break
			}
		}
	}

	// 3. Nodes, over labels and edges -- a node's own glyph always wins.
	for _, nd := range m.graph.Nodes {
		p, ok := m.pos[nd.ID]
		if !ok {
			continue
		}
		x, y := m.toScreen(p)
		col := colorFor(nd.Type)
		if nd.ID == m.grabbed || nd.ID == m.FocusedID() {
			// A grabbed node renders bold-white so a drag in progress is
			// visually obvious, not just functionally correct; the
			// keyboard-selected node uses the same treatment for the same
			// reason -- a selection you cannot see is not a selection.
			col = lipgloss.Color("#ffffff")
		}
		c.set(x, y, []rune(glyphFor(nd.Type))[0], col)
	}

	// 4. Legend, top-left, over everything -- a key that a dense graph can
	//    paint over is not a key.
	for i, row := range m.legendRows() {
		if i >= c.h {
			break
		}
		for j, r := range []rune(row.text) {
			c.set(j, i, r, row.color)
		}
	}

	boxed := lipgloss.NewStyle().
		Border(m.theme.Border).
		BorderForeground(m.theme.Color(theme.RoleBorder)).
		Render(c.render())

	faint := lipgloss.NewStyle().Faint(true)
	header := lipgloss.NewStyle().Bold(true).Render(m.headerLine())

	// The four verbs, named on the frame the way Jon's own app prints its
	// own contract in the corner. Every one of them is real here: zoom is
	// handleMouse's wheel case, pan is its empty-canvas press, open is its
	// no-motion release, move is agent-estate#937's drag. Nothing is
	// advertised that the pane does not do -- agent-estate#937's own fix pass had to
	// delete a hint for a hover feature that did not exist, and that is
	// the mistake this line must not repeat.
	hint := m.hintLine()
	return header + "\n" + boxed + "\n" + faint.Render(hint)
}

// hintLine names the four verbs on the frame, in the widest form the
// pane can actually hold. A hint that is cut off mid-verb is worse than a
// shorter one that is whole -- so the long form is used only when it
// fits, and both forms carry the same "click a node" phrase so nothing
// downstream has to know which one is on screen.
//
// Every verb named here is real: zoom is handleMouse's wheel case, pan is
// its empty-canvas press, open is its no-motion release, move is
// agent-estate#937's drag. agent-estate#937's own fix pass had to delete
// a hint for a hover feature that did not exist; advertising something
// this pane does not do is the specific mistake this line must not repeat.
func (m Model) hintLine() string {
	if m.grabbed != "" {
		return truncateRunes(fmt.Sprintf("dragging %s -- release to drop it, or release without moving to open it", m.grabbed), m.width)
	}
	zoomNote := ""
	if m.zoom != 1 {
		zoomNote = fmt.Sprintf(" (now %.0f%%)", m.zoom*100)
	}
	sel := ""
	if id := m.FocusedID(); id != "" {
		sel = "  ·  selected: " + truncateRunes(id, 24)
	}
	full := "scroll to zoom" + zoomNote + "  ·  drag empty space to pan  ·  click a node to open it  ·  drag a node to move it  ·  [n]/[p] select  [enter] open  [0] reset" + sel
	if lipgloss.Width(full) <= m.width {
		return full
	}
	compact := "scroll: zoom" + zoomNote + " · drag: pan · click a node: open · drag a node: move · [n]/[p]+[enter] open" + sel
	if lipgloss.Width(compact) <= m.width {
		return compact
	}
	return truncateRunes(compact, m.width)
}

// truncateRunes cuts s to at most n runes, appending an ellipsis when it
// actually cut -- rune-wise, not byte-wise, because vault titles are
// prose and may hold non-ASCII.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// detailBody is the opened node's content, wrapped to the pane's width,
// as individual lines -- the one place viewDetail and detailMaxScroll
// agree on how tall the body is, so the scroll clamp can never disagree
// with what is actually rendered.
func (m Model) detailBody() []string {
	if m.openErr != nil || !m.openLoaded {
		return nil
	}
	body := m.openDetail.Body
	if strings.TrimSpace(body) == "" {
		return []string{"(this node's source has no body text)"}
	}
	w := m.width - 2
	if w < minCanvasW {
		w = minCanvasW
	}
	wrapped := lipgloss.NewStyle().Width(w).Render(body)
	return strings.Split(wrapped, "\n")
}

// detailBodyHeight is how many rows viewDetail gives the body -- the pane
// height minus its own fixed chrome (title, kind/created line, summary,
// a blank spacer, and the footer).
func (m Model) detailBodyHeight() int {
	h := m.height - 5
	if h < 3 {
		h = 3
	}
	return h
}

func (m Model) detailMaxScroll() int {
	over := len(m.detailBody()) - m.detailBodyHeight()
	if over < 0 {
		return 0
	}
	return over
}

// viewDetail renders ONE opened node -- the thing itself, not a summary
// of it. Its three states are as separated as the graph's own: the load
// is in flight, the load failed (with the loader's own reason, including
// "no source configured"), or the node genuinely resolved.
func (m Model) viewDetail() string {
	faint := lipgloss.NewStyle().Faint(true)
	title := lipgloss.NewStyle().Bold(true)

	var b strings.Builder
	label := m.openDetail.Label
	if label == "" {
		label = m.open
	}
	b.WriteString(title.Render(truncateRunes("node: "+label, m.width)) + "\n")

	switch {
	case m.openErr != nil:
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		// WRAPPED, not truncated: the loader's own reason is the entire
		// value of this frame, and a reason cut off at the pane width
		// ("read agent/facts/...") is indistinguishable from no reason.
		b.WriteString(errStyle.Width(m.width).Render("! could not open "+m.open+": "+m.openErr.Error()) + "\n")
		b.WriteString(faint.Render("[esc] back to the graph  [r] retry"))
		return b.String()

	case !m.openLoaded:
		b.WriteString(faint.Render("(opening "+m.open+"…)") + "\n")
		b.WriteString(faint.Render("[esc] back to the graph"))
		return b.String()
	}

	kindGlyph := lipgloss.NewStyle().Foreground(colorFor(m.openDetail.Type)).Render(glyphFor(m.openDetail.Type))
	kind := m.openDetail.Type
	if kind == "" {
		kind = "(kind not recorded by the source)"
	}
	created := m.openDetail.Created
	if created == "" {
		created = "(no recorded date)"
	}
	b.WriteString(kindGlyph + " " + faint.Render(truncateRunes(kind+"   id: "+m.openDetail.ID+"   "+created, m.width)) + "\n")
	if s := m.openDetail.Summary; s != "" {
		b.WriteString(faint.Render(truncateRunes(s, m.width)) + "\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("\n")

	lines := m.detailBody()
	start := m.openScroll
	if start > len(lines) {
		start = len(lines)
	}
	end := start + m.detailBodyHeight()
	if end > len(lines) {
		end = len(lines)
	}
	b.WriteString(strings.Join(lines[start:end], "\n") + "\n")

	scrollNote := ""
	if len(lines) > m.detailBodyHeight() {
		scrollNote = fmt.Sprintf("  (line %d-%d of %d)", start+1, end, len(lines))
	}
	b.WriteString(faint.Render("[esc] back to the graph  [j/k] scroll  [r] reload" + scrollNote))
	return b.String()
}
