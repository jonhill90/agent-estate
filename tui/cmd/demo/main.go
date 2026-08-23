// demo renders the FULL shell -- real sidebar, every destination populated --
// against invented data, so the layout can be judged before any of it is
// wired to live sources.
//
// It is a deliberate answer to a mistake: S5 shipped stubs reading
// "not built yet", which are honest and impossible to react to. You cannot
// tell whether a layout is right from a placeholder. Every row here is fake
// and the footer says so; the shape is what is real.
package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"

	"github.com/jonhill90/agent-tui/internal/nav"
)

var (
	sideStyle   = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), false, true, false, false).BorderForeground(lipgloss.Color("240")).Padding(0, 1)
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	blurbStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Italic(true)
	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	cellStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	noteStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	footStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	curStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	groupStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("246"))
	activeStyle = lipgloss.NewStyle().Bold(true).Background(lipgloss.Color("24")).Foreground(lipgloss.Color("231"))
)

type model struct {
	tree     nav.Tree
	nodes    []nav.Node
	cursor   int
	active   string
	expanded map[string]bool
	w, h     int
	zones    *zone.Manager
}

func newModel() model {
	t := nav.Build()
	return model{
		tree: t, nodes: t.Flatten(), active: "home",
		expanded: map[string]bool{}, zones: zone.New(),
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) visible(i int) bool {
	n := m.nodes[i]
	if n.IsGroupHeader() || n.GroupID == "" {
		return true
	}
	return m.expanded[n.GroupID]
}

func (m model) step(dir int) model {
	for i := m.cursor + dir; i >= 0 && i < len(m.nodes); i += dir {
		if m.visible(i) {
			m.cursor = i
			return m
		}
	}
	return m
}

func (m model) selectNode(i int) model {
	n := m.nodes[i]
	m.cursor = i
	if n.IsGroupHeader() {
		m.expanded[n.Group.ID] = !m.expanded[n.Group.ID]
		return m
	}
	m.active = n.Item.ID
	if n.GroupID != "" {
		m.expanded[n.GroupID] = true
	}
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case tea.MouseMsg:
		if msg.Action != tea.MouseActionRelease || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		for i := range m.nodes {
			if !m.visible(i) {
				continue
			}
			if m.zones.Get(zoneID(i)).InBounds(msg) {
				return m.selectNode(i), nil
			}
		}
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			return m.step(-1), nil
		case "down", "j":
			return m.step(1), nil
		case "left", "h":
			n := m.nodes[m.cursor]
			g := n.GroupID
			if n.IsGroupHeader() {
				g = n.Group.ID
			}
			delete(m.expanded, g)
			return m, nil
		case "enter", "right", "l":
			return m.selectNode(m.cursor), nil
		}
	}
	return m, nil
}

func zoneID(i int) string { return fmt.Sprintf("nav-%d", i) }

func (m model) sidebar() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("  estate") + "\n\n")
	for i, n := range m.nodes {
		if !m.visible(i) {
			continue
		}
		var line string
		switch {
		case n.IsGroupHeader():
			mark := "▸"
			if m.expanded[n.Group.ID] {
				mark = "▾"
			}
			line = groupStyle.Render(fmt.Sprintf(" %s %s", mark, n.Group.Label))
		default:
			indent := " "
			if n.GroupID != "" {
				indent = "   "
			}
			label := fmt.Sprintf("%s%s", indent, n.Item.Label)
			switch {
			case n.Item.ID == m.active:
				line = activeStyle.Render(pad(label, 24))
			case i == m.cursor:
				line = curStyle.Render(pad(label, 24))
			default:
				line = cellStyle.Render(pad(label, 24))
			}
		}
		b.WriteString(m.zones.Mark(zoneID(i), line) + "\n")
	}
	return sideStyle.Height(m.contentH()).Render(b.String())
}

func pad(s string, n int) string {
	for lipgloss.Width(s) < n {
		s += " "
	}
	return s
}

func (m model) contentH() int {
	if m.h < 4 {
		return 20
	}
	return m.h - 2
}

func (m model) content() string {
	v, ok := views[m.active]
	if !ok {
		v = view{title: m.active, blurb: "no demo content for this route yet"}
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render(v.title) + "\n")
	b.WriteString(blurbStyle.Render(v.blurb) + "\n\n")

	if len(v.headers) > 0 {
		widths := make([]int, len(v.headers))
		for i, h := range v.headers {
			widths[i] = lipgloss.Width(h)
		}
		for _, r := range v.rows {
			for i, c := range r {
				if i < len(widths) && lipgloss.Width(c) > widths[i] {
					widths[i] = lipgloss.Width(c)
				}
			}
		}
		var hb strings.Builder
		for i, h := range v.headers {
			hb.WriteString(pad(h, widths[i]+2))
		}
		b.WriteString(headerStyle.Render(hb.String()) + "\n")
		for _, r := range v.rows {
			var rb strings.Builder
			for i, c := range r {
				w := 10
				if i < len(widths) {
					w = widths[i]
				}
				rb.WriteString(pad(c, w+2))
			}
			b.WriteString(cellStyle.Render(rb.String()) + "\n")
		}
		b.WriteString("\n")
	}
	for _, n := range v.notes {
		b.WriteString(noteStyle.Render(n) + "\n")
	}
	return lipgloss.NewStyle().Padding(0, 2).Height(m.contentH()).Render(b.String())
}

func (m model) View() string {
	body := lipgloss.JoinHorizontal(lipgloss.Top, m.sidebar(), m.content())
	foot := footStyle.Render("  ↑/↓ move   enter/→ open   ← collapse   click anything   q quit        ALL DATA ON THIS SCREEN IS FAKE — layout demo only")
	return m.zones.Scan(lipgloss.JoinVertical(lipgloss.Left, body, foot))
}

func main() {
	p := tea.NewProgram(newModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}
