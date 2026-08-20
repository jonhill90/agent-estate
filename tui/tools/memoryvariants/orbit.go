package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

const orbitImplies = "no free-form drag at all -- one node is 'focus', its neighbours ring around it by hop distance, arrow keys re-centre. Implies: cheapest to build and to read (a hub with N spokes never overlaps), but you never see the WHOLE graph at once, only one node's neighbourhood -- this is the 'maybe grab-and-move isn't the right primitive' reading"

// orbit answers the issue's own "what does grab and move mean without
// pixels" question by not needing pixels at all: instead of moving nodes
// in a shared coordinate space, one node is FOCUS and everything else is
// positioned relative to it -- 1-hop neighbours on an inner ring, 2-hop on
// an outer ring, unreached nodes listed off to the side. "Moving" a node
// is re-centring focus on it (arrow keys + enter in the real
// interaction), which needs no sub-cell interpolation and no drag physics
// -- exactly the terminal-native alternative agent-tui#61 asks whoever
// picks this up to consider before assuming Hill90's exact interaction
// model carries over.
//
// Two frames stacked (focus=loop-never-stops, then focus=framework-build)
// so both a hub-centred and a leaf-centred view are visible in one image,
// same device railCollapsed used in tools/uivariants for its two-state
// interaction.
func orbit(g graphData, th theme.Theme) string {
	frame := func(focusID string) string {
		focus, _ := g.byID(focusID)
		deg := g.degree()
		ring1 := g.neighbors(focusID)
		ring1set := map[string]bool{focusID: true}
		for _, id := range ring1 {
			ring1set[id] = true
		}
		var ring2 []string
		seen2 := map[string]bool{}
		for _, id := range ring1 {
			for _, n2 := range g.neighbors(id) {
				if !ring1set[n2] && !seen2[n2] {
					ring2 = append(ring2, n2)
					seen2[n2] = true
				}
			}
		}
		var unreached []string
		for _, nd := range g.nodes {
			if !ring1set[nd.id] && !seen2[nd.id] {
				unreached = append(unreached, nd.id)
			}
		}

		focusStyle := lipgloss.NewStyle().Bold(true).Foreground(typeColor[focus.typ]).Padding(0, 2)
		var b []string
		b = append(b, focusStyle.Render(fmt.Sprintf("[%s %s]  focus (%d links)", glyphFor(focus.typ), focus.title, deg[focus.id])))
		b = append(b, "")
		b = append(b, lipgloss.NewStyle().Faint(true).Render("  1 hop --"))
		for _, id := range ring1 {
			nd, _ := g.byID(id)
			s := lipgloss.NewStyle().Foreground(typeColor[nd.typ])
			b = append(b, fmt.Sprintf("    %s %s  (%d links)", s.Render(glyphFor(nd.typ)), s.Render(nd.title), deg[nd.id]))
		}
		if len(ring2) > 0 {
			b = append(b, "")
			b = append(b, lipgloss.NewStyle().Faint(true).Render("  2 hops --"))
			for _, id := range ring2 {
				nd, _ := g.byID(id)
				s := lipgloss.NewStyle().Foreground(typeColor[nd.typ]).Faint(true)
				b = append(b, fmt.Sprintf("      %s %s", s.Render(glyphFor(nd.typ)), s.Render(nd.title)))
			}
		}
		if len(unreached) > 0 {
			b = append(b, "")
			b = append(b, lipgloss.NewStyle().Faint(true).Render(fmt.Sprintf("  unreached from here (%d) -- ", len(unreached))+strings.Join(titlesOf(g, unreached), ", ")))
		}
		content := lipgloss.JoinVertical(lipgloss.Left, b...)
		return lipgloss.NewStyle().Border(th.Border).BorderForeground(th.Color(theme.RoleBorder)).Padding(0, 1).Width(70).Render(content)
	}

	var out strings.Builder
	out.WriteString(lipgloss.NewStyle().Faint(true).Render("-- focus: loop-never-stops (a hub, 3 links) --") + "\n")
	out.WriteString(frame("loop-never-stops") + "\n\n")
	out.WriteString(lipgloss.NewStyle().Faint(true).Render("-- focus moved to framework-build (arrow keys + enter) --") + "\n")
	out.WriteString(frame("framework-build") + "\n")
	out.WriteString(legend(th) + "\n")
	return out.String()
}

func titlesOf(g graphData, ids []string) []string {
	var out []string
	for _, id := range ids {
		nd, _ := g.byID(id)
		out = append(out, nd.title)
	}
	return out
}
