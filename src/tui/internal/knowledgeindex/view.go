package knowledgeindex

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

// View renders the per-source status block plus either the item list
// (modeList) or one item's own three tiers (modeDetail). A quitting
// Model renders nothing, matching every other pane in this module.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("knowledge index (derived, never authoritative)") + "\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! "+m.fetchErr.Error()) + "\n")
		b.WriteString(legendStyle.Render("[r] refresh  [t] theme  [q] quit"))
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	if !m.fetched {
		b.WriteString(legendStyle.Render("loading…") + "\n")
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	for _, s := range m.res.Sources {
		if s.OK {
			b.WriteString(fmt.Sprintf("  ok   %-18s %d item(s)\n", s.Name, s.Count))
		} else {
			errStyle := lipgloss.NewStyle().Foreground(m.theme.Color(theme.RoleError))
			b.WriteString(errStyle.Render(fmt.Sprintf("  FAIL %-18s %s", s.Name, s.Reason)) + "\n")
		}
	}

	if m.mode == modeDetail {
		if it, ok := m.currentItem(); ok {
			b.WriteString("\n")
			b.WriteString(fmt.Sprintf("source: %s   id: %s\n", it.Source, it.ID))
			b.WriteString(fmt.Sprintf("permalink: %s\n\n", it.Permalink))
			b.WriteString("Tier 1: " + it.Tier1 + "\n")
			if it.Tier2 != "" {
				b.WriteString("\nTier 2: " + it.Tier2 + "\n")
			}
			if it.Tier3 != "" {
				b.WriteString("\nTier 3: " + it.Tier3 + "\n")
			}
			if len(it.StructuralTags) > 0 {
				b.WriteString("\nstructural tags: " + strings.Join(it.StructuralTags, " ") + "\n")
			}
			if len(it.SynapticTags) > 0 {
				b.WriteString("synaptic tags: " + strings.Join(it.SynapticTags, " ") + "\n")
			}
		}
		b.WriteString("\n" + legendStyle.Render("[esc] back  [t] theme  [q] quit"))
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	b.WriteString("\n")
	if len(m.res.Items) == 0 {
		b.WriteString(legendStyle.Render("(no items -- every source above is either empty or unreadable)") + "\n")
	} else {
		b.WriteString(m.listVP.View() + "\n")
	}
	b.WriteString(legendStyle.Render(fmt.Sprintf("generated %s -- %s", m.res.GeneratedAt.Format("2006-01-02 15:04:05Z"), m.res.StalenessRule)) + "\n")
	b.WriteString(legendStyle.Render("[enter] open  [r] refresh  [t] theme  [q] quit"))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

// renderListLines builds listVP's own content -- one line per Item's
// Tier1, the selected row highlighted.
func (m Model) renderListLines() string {
	if len(m.res.Items) == 0 {
		return ""
	}
	sel := selectedStyle(m.theme)
	var lines []string
	for i, it := range m.res.Items {
		line := fmt.Sprintf("%-20s %s", truncate(it.Source, 20), truncate(it.Tier1, 140))
		if i == m.selected {
			line = sel.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
