// Command memoryvariants is a THROWAWAY rendering harness for
// agent-tui#61 (knowledge graph view of agent memory), following
// tools/uivariants' own pattern from agent-tui#62/agent-tui#63: hardcoded fake
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
	"hash/fnv"
	"os"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
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

// knownTypeColor/knownTypeGlyph are recognized-vocabulary lookups for the
// OKF-shaped demo data (fakeGraph, graph.go) -- NOT an exhaustive legal
// set. typ is an open-ended string (graph.go); colorFor/glyphFor below
// fall back to a deterministic hash-derived choice for anything not
// listed here, the same way Hill90's KnowledgeGraph.tsx colors an unknown
// type by hash rather than refusing to render it.
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

// fallbackPalette/fallbackGlyphs back colorFor/glyphFor for any typ string
// outside knownTypeColor/knownTypeGlyph -- picked by a stable hash of the
// type string, so the same unrecognized type always renders the same way
// across a run without needing to be added to a fixed enum first.
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
