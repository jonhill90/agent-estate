// Command spike is the LIVE mouse-drag feasibility spike for
// agent-tui#61, run separately from ../main.go's static frames because it
// needs a real tea.NewProgram with mouse motion enabled -- the exact
// thing tools/uivariants (and ../ here) deliberately avoid, per their own
// package docs. It exists to answer one question empirically, not to look
// good: does Bubble Tea deliver mouse-down/motion/up events over tmux at
// a resolution and rate that make "grab and move" usable on a character
// grid?
//
// Throwaway, like its parent package -- not imported by cmd/ or
// internal/, deleted alongside tools/memoryvariants/ once #61 is decided.
//
// Usage: go run ./tools/memoryvariants/spike
// Keys: q or ctrl+c to quit and print the drag event log to stderr.
// Mouse: press on a node, drag, release.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type node struct {
	id    string
	x, y  int
	glyph string
	color lipgloss.Color
}

type dragEvent struct {
	kind string // down, motion, up
	x, y int
	t    time.Duration
}

type model struct {
	nodes   []node
	grabbed int // index into nodes, -1 if nothing grabbed
	log     []dragEvent
	start   time.Time
	w, h    int
}

func initialModel() model {
	return model{
		grabbed: -1,
		start:   time.Now(),
		nodes: []node{
			{id: "a", x: 10, y: 5, glyph: "●", color: "#f1c40f"},
			{id: "b", x: 30, y: 8, glyph: "■", color: "#3b82f6"},
			{id: "c", x: 50, y: 4, glyph: "▲", color: "#22c55e"},
		},
		w: 80, h: 20,
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) nodeAt(x, y int) int {
	for i, n := range m.nodes {
		if n.x == x && n.y == y {
			return i
		}
	}
	return -1
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.MouseMsg:
		me := tea.MouseEvent(msg)
		elapsed := time.Since(m.start)
		switch me.Action {
		case tea.MouseActionPress:
			if i := m.nodeAt(me.X, me.Y); i >= 0 {
				m.grabbed = i
			}
			m.log = append(m.log, dragEvent{"down", me.X, me.Y, elapsed})
		case tea.MouseActionMotion, tea.MouseActionRelease:
			kind := "motion"
			if me.Action == tea.MouseActionRelease {
				kind = "up"
			}
			m.log = append(m.log, dragEvent{kind, me.X, me.Y, elapsed})
			if m.grabbed >= 0 {
				m.nodes[m.grabbed].x = me.X
				m.nodes[m.grabbed].y = me.Y
			}
			if kind == "up" {
				m.grabbed = -1
			}
		}
	}
	return m, nil
}

func (m model) View() string {
	canvas := make([][]rune, m.h)
	colors := make([][]lipgloss.Color, m.h)
	for i := range canvas {
		canvas[i] = make([]rune, m.w)
		colors[i] = make([]lipgloss.Color, m.w)
		for j := range canvas[i] {
			canvas[i][j] = ' '
		}
	}
	for _, n := range m.nodes {
		if n.y >= 0 && n.y < m.h && n.x >= 0 && n.x < m.w {
			canvas[n.y][n.x] = []rune(n.glyph)[0]
			colors[n.y][n.x] = n.color
		}
	}
	var rows []string
	for y := 0; y < m.h; y++ {
		var line strings.Builder
		for x := 0; x < m.w; x++ {
			r := canvas[y][x]
			if r == ' ' {
				line.WriteRune(r)
				continue
			}
			line.WriteString(lipgloss.NewStyle().Foreground(colors[y][x]).Render(string(r)))
		}
		rows = append(rows, line.String())
	}
	status := fmt.Sprintf("grabbed=%d  events=%d  (q to quit)", m.grabbed, len(m.log))
	return strings.Join(rows, "\n") + "\n" + lipgloss.NewStyle().Faint(true).Render(status)
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseAllMotion())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	m := final.(model)
	fmt.Fprintf(os.Stderr, "-- drag event log (%d events) --\n", len(m.log))
	for _, e := range m.log {
		fmt.Fprintf(os.Stderr, "%s\tx=%d y=%d\tt=%s\n", e.kind, e.x, e.y, e.t.Round(time.Millisecond))
	}
	for _, n := range m.nodes {
		fmt.Fprintf(os.Stderr, "final position: %s -> (%d,%d)\n", n.id, n.x, n.y)
	}
}
