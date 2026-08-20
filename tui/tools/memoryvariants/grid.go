package main

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

const gridImplies = "the literal Hill90 reading -- every node placed by a real force layout on a shared character grid, edges drawn as lines, a node 'grabbed' and dragged to a new CELL. Implies: closest to what Jon actually asked for and the only one where the whole graph is visible at once, but cell-snapped movement is coarse compared to Hill90's sub-pixel canvas (see spike/ for the live mouse-drag feasibility finding) and edges/labels overlap fast past ~15 nodes on a normal terminal width"

const (
	canvasW = 92
	canvasH = 24
)

// grid is the literal "grab and move" reading: a force-directed layout
// (layout.go) computed once and snapped onto a canvasW x canvasH character
// grid, nodes drawn as their type glyph, edges as straight lines walked
// cell-by-cell (Bresenham) using light box-drawing shades so they read as
// connective tissue without competing with node glyphs. This is the
// variant spike/ actually drives live with mouse events -- this file only
// produces the settled static frame for the side-by-side picker image.
func grid(g graphData, th theme.Theme) string {
	// Layout height is doubled before halving back down in cell() below --
	// terminal cells read roughly 2:1 tall/wide, so without this the halved
	// y range only ever fills the canvas's top half.
	pos := layout(g, float64(canvasW-2), float64((canvasH-2)*2), 200)
	canvas := make([][]rune, canvasH)
	colors := make([][]lipgloss.Color, canvasH)
	for i := range canvas {
		canvas[i] = make([]rune, canvasW)
		colors[i] = make([]lipgloss.Color, canvasW)
		for j := range canvas[i] {
			canvas[i][j] = ' '
		}
	}

	cell := func(id string) (int, int) {
		p := pos[id]
		x := int(math.Round(p.x))
		y := int(math.Round(p.y * 0.5)) // halved: terminal cells are ~2:1 tall, keeps the layout from reading squashed
		if x < 0 {
			x = 0
		}
		if x >= canvasW {
			x = canvasW - 1
		}
		if y < 0 {
			y = 0
		}
		if y >= canvasH {
			y = canvasH - 1
		}
		return x, y
	}

	edgeColor := lipgloss.Color("#3a3a3a")
	for _, e := range g.edges {
		x0, y0 := cell(e.from)
		x1, y1 := cell(e.to)
		bresenham(x0, y0, x1, y1, func(x, y int) {
			if canvas[y][x] == ' ' {
				canvas[y][x] = '·'
				colors[y][x] = edgeColor
			}
		})
	}

	for _, nd := range g.nodes {
		x, y := cell(nd.id)
		canvas[y][x] = []rune(glyphFor(nd.typ))[0]
		colors[y][x] = typeColor[nd.typ]
	}

	var rows []string
	for y := 0; y < canvasH; y++ {
		var line strings.Builder
		for x := 0; x < canvasW; x++ {
			r := canvas[y][x]
			if r == ' ' {
				line.WriteRune(r)
				continue
			}
			c := colors[y][x]
			line.WriteString(lipgloss.NewStyle().Foreground(c).Render(string(r)))
		}
		rows = append(rows, line.String())
	}
	canvasBody := strings.Join(rows, "\n")
	boxed := lipgloss.NewStyle().Border(th.Border).BorderForeground(th.Color(theme.RoleBorder)).Render(canvasBody)
	return boxed + "\n" + legend(th) + "\n" + lipgloss.NewStyle().Faint(true).Render("hover a node for its title -- grab (mouse down) and drag to reposition; drop to release")
}

// bresenham walks the integer grid line from (x0,y0) to (x1,y1), calling
// plot for every cell on it, endpoints included -- standard Bresenham,
// used here instead of a float sampling loop so lines have no gaps/dupes
// regardless of slope.
func bresenham(x0, y0, x1, y1 int, plot func(x, y int)) {
	dx := abs(x1 - x0)
	dy := -abs(y1 - y0)
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

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
