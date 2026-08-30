package mcpservers

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// unknown is what Server.Reachable renders as when nil -- never "false" or
// blank, the same "unknown, not zero" discipline internal/agents.Row and
// internal/skills.Skill's own absent-metric fields already enforce.
const unknown = "unknown"

func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

// View renders a flat table -- NAME | SCOPE | TRANSPORT | COMMAND/URL |
// REACHABLE -- one row per Server, selected row highlighted. A quitting
// Model renders nothing, the same convention every other pane in this
// module follows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("mcp servers") + "\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! mcp servers unavailable: "+m.fetchErr.Error()) + "\n")
	}

	if len(m.servers) == 0 {
		b.WriteString(legendStyle.Render("(no servers configured)") + "\n")
	} else {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-20s %-8s %-10s %-40s %s", "NAME", "SCOPE", "TRANSPORT", "COMMAND/URL", "REACHABLE")) + "\n")
		for i, s := range m.servers {
			target := s.Command
			if target == "" {
				target = s.URL
			}
			reachable := unknown
			if s.Reachable != nil {
				if *s.Reachable {
					reachable = "yes"
				} else {
					reachable = "no"
				}
			}
			line := fmt.Sprintf("%-20s %-8s %-10s %-40s %s", truncate(s.Name, 20), string(s.Scope), string(s.Transport), truncate(target, 40), reachable)
			if i == m.selected {
				line = selectedStyle(m.theme).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(legendStyle.Render("[r] refresh  [t] theme  [q] quit"))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
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
