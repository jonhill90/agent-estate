package main

import (
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/theme"
)

const outlineImplies = "not spatial at all -- grouped by type, each fact's Related links nested one level under it, collapsed by default. Implies: zero layout/physics/drag code, scrolls naturally (bubbles/viewport territory, agent-tui#29's own fix), reads fastest for 'what does X relate to' -- but it is a list, not the grabbable graph Jon actually asked for, and this is the honest 'the terminal wants a list here, not a graph' answer the issue's own open question invites"

// outline is the non-spatial reading of agent-tui#61's open question
// ("does this belong in the terminal at all"): instead of drawing node
// positions, group facts by OKF type (the four in
// $AGENT_MEMORY_VAULT/agent/facts -- user/feedback/project/reference) and
// nest each fact's linked facts one level under it, same shape a
// keyboard-only Obsidian outline plugin would show. This is the variant
// to pick if grab-and-move turns out not to earn its complexity budget --
// bubbles/viewport (not in go.mod today, agent-tui#29) is the natural next
// step for this one specifically, since it is bounded-height content that
// needs scrolling, not sub-cell interaction.
func outline(g graphData, th theme.Theme) string {
	// order is this variant's own grouping choice for the OKF-shaped demo
	// data (graph.go's fakeGraph) -- not a constraint the graph model
	// (node.typ) imposes. A node with a typ outside this list (or "") just
	// wouldn't get a header here; that's a limitation of this one grouped-
	// list view, not of graphData.
	order := []string{"user", "feedback", "project", "reference"}
	var b []string
	for _, t := range order {
		var inType []node
		for _, nd := range g.nodes {
			if nd.typ == t {
				inType = append(inType, nd)
			}
		}
		if len(inType) == 0 {
			continue
		}
		sort.Slice(inType, func(i, j int) bool { return inType[i].label < inType[j].label })
		header := lipgloss.NewStyle().Bold(true).Foreground(colorFor(t)).Render(strings.ToUpper(t) + " (" + strconv.Itoa(len(inType)) + ")")
		b = append(b, header)
		for _, nd := range inType {
			style := lipgloss.NewStyle().Foreground(colorFor(nd.typ))
			neigh := g.neighbors(nd.id)
			suffix := ""
			if len(neigh) == 0 {
				suffix = lipgloss.NewStyle().Faint(true).Render("  (orphan -- no links)")
			}
			b = append(b, "  "+style.Render(glyphFor(nd.typ)+" "+nd.label)+suffix)
			for _, nid := range neigh {
				n2, _ := g.byID(nid)
				b = append(b, "    "+lipgloss.NewStyle().Faint(true).Render("-> related: ")+lipgloss.NewStyle().Foreground(colorFor(n2.typ)).Faint(true).Render(n2.label))
			}
		}
		b = append(b, "")
	}
	content := lipgloss.JoinVertical(lipgloss.Left, b...)
	body := lipgloss.NewStyle().Border(th.Border).BorderForeground(th.Color(theme.RoleBorder)).Padding(0, 1).Width(74).Render(strings.TrimRight(content, "\n"))
	return body + "\n" + legend(th) + "\n"
}
