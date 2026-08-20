// Command memoryvariants is a THROWAWAY rendering harness for
// agent-tui#61 (knowledge graph view of agent memory), following
// tools/uivariants' own pattern from agent-tui#62/#63: hardcoded fake
// data, no MCP client, no vault access, no tea.NewProgram for the static
// variants below -- nothing under cmd/ or internal/ imports this package.
// Delete this directory once Jon has picked a variant (or picked none);
// whatever he picks gets rebuilt as a real internal/ package against a
// live seam over the Obsidian vault, not by promoting this file.
//
// Usage: go run ./tools/memoryvariants <variant-id>. See variantList
// below for the three static frames, and ./spike/ for the separate live
// mouse-drag interaction spike (not a frame -- see spike/main.go).
package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: memoryvariants <variant-id>")
		fmt.Fprintln(os.Stderr, "variants:")
		for _, v := range variantList {
			fmt.Fprintf(os.Stderr, "  %-20s %s\n", v.id, v.implies)
		}
		os.Exit(1)
	}
	id := os.Args[1]
	for _, v := range variantList {
		if v.id == id {
			fmt.Print(v.render())
			return
		}
	}
	fmt.Fprintf(os.Stderr, "unknown variant %q\n", id)
	os.Exit(1)
}

type variant struct {
	id      string
	implies string
	render  func() string
}

var variantList = []variant{
	{"orbit", orbitImplies, func() string { return orbit(fakeGraph(), theme.Default) }},
	{"outline", outlineImplies, func() string { return outline(fakeGraph(), theme.Default) }},
	{"grid", gridImplies, func() string { return grid(fakeGraph(), theme.Default) }},
}

var typeColor = map[nodeType]lipgloss.Color{
	typeUser:      lipgloss.Color("#c026d3"),
	typeFeedback:  lipgloss.Color("#f1c40f"),
	typeProject:   lipgloss.Color("#3b82f6"),
	typeReference: lipgloss.Color("#22c55e"),
}

func glyphFor(t nodeType) string {
	switch t {
	case typeUser:
		return "◆"
	case typeFeedback:
		return "●"
	case typeProject:
		return "■"
	default:
		return "▲"
	}
}

func legend(th theme.Theme) string {
	order := []nodeType{typeUser, typeFeedback, typeProject, typeReference}
	var b []string
	for _, t := range order {
		style := lipgloss.NewStyle().Foreground(typeColor[t])
		b = append(b, style.Render(glyphFor(t)+" "+string(t)))
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
