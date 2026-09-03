package memgraph

import (
	"fmt"
	"hash/fnv"
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

func legend(th theme.Theme) string {
	order := []string{"user", "feedback", "project", "reference"}
	var b []string
	for _, t := range order {
		style := lipgloss.NewStyle().Foreground(colorFor(t))
		b = append(b, style.Render(glyphFor(t)+" "+t))
	}
	return lipgloss.NewStyle().Faint(true).Render("legend: ") + lipgloss.JoinHorizontal(lipgloss.Top, joinPad(b, "   ")...)
}

func joinPad(items []string, sep string) []string {
	out := make([]string, 0, len(items)*2-1)
	for i, it := range items {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, it)
	}
	return out
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

// View renders the graph as a bordered character-grid canvas -- nodes as
// their type's glyph, edges as Bresenham-walked lines -- plus a legend and
// a drag hint below it, the same composition
// tools/memoryvariants/grid.go's own grid() produced for a static frame.
// The three states an absent/unreachable vault can leave this in
// (fetching, fetch error, empty graph) are each rendered honestly rather
// than falling back to a fabricated demo graph -- AGENTS.md's "absence is
// a typed value" convention, this pane's own required reading per the
// issue that built it.
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
	if len(m.graph.Nodes) == 0 {
		return lipgloss.NewStyle().Faint(true).Render("(no facts in the memory vault yet)")
	}

	w, h := m.canvasSize()
	canvas := make([][]rune, h)
	colors := make([][]lipgloss.Color, h)
	for i := range canvas {
		canvas[i] = make([]rune, w)
		colors[i] = make([]lipgloss.Color, w)
		for j := range canvas[i] {
			canvas[i][j] = ' '
		}
	}

	cellFor := func(id string) (int, int, bool) {
		p, ok := m.pos[id]
		return p.x, p.y, ok
	}

	edgeColor := lipgloss.Color("#3a3a3a")
	for _, e := range m.graph.Edges {
		x0, y0, ok0 := cellFor(e.From)
		x1, y1, ok1 := cellFor(e.To)
		if !ok0 || !ok1 {
			continue
		}
		bresenham(x0, y0, x1, y1, func(x, y int) {
			if canvas[y][x] == ' ' {
				canvas[y][x] = '·'
				colors[y][x] = edgeColor
			}
		})
	}

	for _, nd := range m.graph.Nodes {
		x, y, ok := cellFor(nd.ID)
		if !ok {
			continue
		}
		r := []rune(glyphFor(nd.Type))[0]
		c := colorFor(nd.Type)
		if nd.ID == m.grabbed {
			// A grabbed node renders bold-white so a drag in progress is
			// visually obvious, not just functionally correct.
			c = lipgloss.Color("#ffffff")
		}
		canvas[y][x] = r
		colors[y][x] = c
	}

	var rows []string
	for y := 0; y < h; y++ {
		var line strings.Builder
		for x := 0; x < w; x++ {
			r := canvas[y][x]
			if r == ' ' {
				line.WriteRune(r)
				continue
			}
			line.WriteString(lipgloss.NewStyle().Foreground(colors[y][x]).Render(string(r)))
		}
		rows = append(rows, line.String())
	}
	canvasBody := strings.Join(rows, "\n")
	boxed := lipgloss.NewStyle().Border(m.theme.Border).BorderForeground(m.theme.Color(theme.RoleBorder)).Render(canvasBody)

	// "hover a node for its title" was dropped from this hint in
	// jonhill90/agent-estate#937's own fix pass: Node.Label was populated
	// (cmd/estate/memgraph.go's buildMemgraphFetch) and never read anywhere
	// in this package -- no hover state exists, handleMouse's
	// MouseActionMotion case is a no-op unless a node is already grabbed
	// (TestMotionWithoutPressDoesNotGrab), and the status line below shows
	// the raw grabbed id, never Label. A review against the real binary
	// confirmed hovering shows nothing. Advertising a feature that does not
	// exist on every loaded frame was the defect; the fix here is to stop
	// claiming it rather than build it under this repo's one-fix-pass rule.
	// Drag itself is real (see TestDragSequenceMovesGrabbedNode) and stays
	// in the hint below.
	hint := "press and drag to reposition; release to drop"
	if m.grabbed != "" {
		hint = fmt.Sprintf("dragging %s -- release to drop", m.grabbed)
	}
	return boxed + "\n" + legend(m.theme) + "\n" + lipgloss.NewStyle().Faint(true).Render(hint)
}
