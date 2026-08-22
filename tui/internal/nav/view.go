package nav

import (
	"fmt"
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
	for i, n := range m.tree.Flatten() {
		cursored := i == m.cursor
		switch {
		case n.IsGroupHeader():
			if m.iconsOnly {
				continue
			}
			disclosure := "▸ "
			if m.expanded[n.Group.ID] {
				disclosure = "▾ "
			}
			hdr := disclosure + n.Group.Label
			if cursored {
				lines = append(lines, m.cursorStyle().Render("▌"+hdr))
			} else {
				lines = append(lines, groupHeaderStyle.Render(" "+hdr))
			}
		case n.GroupID != "" && !m.expanded[n.GroupID]:
			// Collapsed group: skip its children entirely.
			continue
		default:
			lines = append(lines, m.renderItem(n, selectedStyle, cursored))
		}
	}

	// The sidebar is now the ONE always-visible column (SPEC-shell.md S3
	// replaced internal/rail with this Model in that role) -- rail's own
	// "theme: <name>" footer line (rail's Model.View) is what used to prove
	// a [t] cycle repainted the always-visible surface; with rail routed
	// behind "Lanes" instead of fixed on screen, this Model must carry that
	// same proof itself, or a [t] press from PaneHome/PaneStub/any
	// non-Lanes pane has no always-visible surface left to show it
	// happened at all. Kept icons-only-safe (rendered even then, since
	// iconsOnly only ever hid group headers, never other footer lines).
	lines = append(lines, groupHeaderStyle.Render(fmt.Sprintf("theme: %s", m.theme.Name)))

	body := strings.Join(lines, "\n")
	return lipgloss.NewStyle().Width(m.Width()).Height(m.height).Render(body)
}

// cursorStyle is the CURSOR signal, deliberately different in kind from the
// active-route highlight: bold + accent foreground + a left bar, never a
// background fill, so both can be read at a glance when they sit on
// different rows.
func (m Model) cursorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleDirector))
}

func (m Model) renderItem(n Node, selectedStyle lipgloss.Style, cursored bool) string {
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

	// CURSOR vs ACTIVE are two different things and both must be visible.
	//
	// Found by DRIVING it, not reading it: pressing Down twice left the
	// highlight sitting on Home, because the only styled state was
	// m.active. The cursor lived in the shell and was rendered nowhere, so
	// keyboard traversal was invisible -- you could not tell what Enter
	// would open. That is the whole point of a sidebar.
	//
	// active   = the route currently on screen  -> filled highlight
	// cursored = where Enter would take you     -> bar + bold + accent
	// both     = filled highlight AND the bar
	marker := " "
	if cursored {
		marker = "▌"
	}
	switch {
	case n.Item.ID == m.active && cursored:
		return selectedStyle.Bold(true).Render(marker + text)
	case n.Item.ID == m.active:
		return selectedStyle.Render(marker + text)
	case cursored:
		return m.cursorStyle().Render(marker + text)
	}
	return marker + text
}
