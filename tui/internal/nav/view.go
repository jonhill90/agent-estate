package nav

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

// iconGlyphs maps each lucide-react component name nav-items.ts imports
// (Item.Icon, tree.go) to a single-column glyph this sidebar can render in
// a terminal. These are NOT lucide icons -- a terminal cannot render an
// SVG -- they are this package's own approximation, chosen for width and
// visual distinctness, not for resemblance to the source glyph.
// glyphFor's "?" fallback is what a future nav-items.ts addition renders
// as until this table is extended for it.
var iconGlyphs = map[string]string{
	"Home":            "⌂",
	"LayoutDashboard": "▦",
	"Bot":             "◈",
	"MessageSquare":   "✉",
	"CheckSquare":     "☑",
	"BookOpen":        "▥",
	"Library":         "▤",
	"Layers":          "≡", // Lanes -- this repo's own icon, no lucide source
	"Hammer":          "⚒",
	"Wrench":          "⚙",
	"Zap":             "⚡",
	"Server":          "▣",
	"Link2":           "⛓",
	"Plug":            "⏻",
	"Cpu":             "◫",
	"HardDrive":       "▨",
	"Shield":          "⛨",
	"Eye":             "◉",
	"BarChart3":       "▊",
	"Activity":        "∿",
	"Book":            "▥",
	"FileText":        "▤",
	"ExternalLink":    "↗",
	"Settings":        "⚙",
	"Box":             "▢",
	"Users":           "☺",
	"Package":         "▣",
}

func glyphFor(icon string) string {
	if g, ok := iconGlyphs[icon]; ok {
		return g
	}
	return "?"
}

// Width returns the sidebar's current fixed column width -- fullWidth
// normally, iconWidth once [b] has collapsed it.
func (m Model) Width() int {
	if m.iconsOnly {
		return iconWidth
	}
	return fullWidth
}

var (
	groupHeaderStyle = lipgloss.NewStyle().Bold(true).Faint(true)
	childIndent      = "  "
)

// View renders the tree in Flatten's order: every top-level Item, then
// each Group header followed by its Children when that group is expanded
// (m.expanded[group.ID]) -- collapsed groups render their header only, the
// same show-header-hide-children behaviour hill90's own Sidebar.tsx has.
// m.active is highlighted with theme.RoleSelectedBG, the same role
// internal/rail already uses for its own selection highlight.
func (m Model) View() string {
	selectedStyle := lipgloss.NewStyle().Background(m.theme.Color(theme.RoleSelectedBG))

	var lines []string
	for _, n := range m.tree.Flatten() {
		switch {
		case n.IsGroupHeader():
			if m.iconsOnly {
				continue
			}
			lines = append(lines, groupHeaderStyle.Render(n.Group.Label))
		case n.GroupID != "" && !m.expanded[n.GroupID]:
			// Collapsed group: skip its children entirely.
			continue
		default:
			lines = append(lines, m.renderItem(n, selectedStyle))
		}
	}

	body := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(m.Width()).Height(m.height).Render(body)
}

func (m Model) renderItem(n Node, selectedStyle lipgloss.Style) string {
	glyph := glyphFor(n.Item.Icon)

	var text string
	if m.iconsOnly {
		text = glyph
	} else {
		indent := ""
		if n.GroupID != "" {
			indent = childIndent
		}
		text = indent + glyph + " " + n.Item.Label
	}

	if n.Item.ID == m.active {
		return selectedStyle.Render(text)
	}
	return text
}
