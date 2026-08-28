package connectors

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)
var sectionStyle = lipgloss.NewStyle().Bold(true).Faint(true)

// unknown is what an absent Connection.Provider/DefaultModel renders as --
// never blank, matching internal/agents.Row and internal/skills.Skill's
// own convention for the same shape of gap.
const unknown = "unknown"

func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

// View renders two sections -- CONNECTIONS (one row per harness: harness,
// provider, configured, default model) and MODELS (Codex's own catalog,
// the one this estate can read locally) -- selected row highlighted in
// the connections table only (models has no per-row action). A quitting
// Model renders nothing, the same convention every other pane in this
// module follows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("connectors") + "\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! could not read local connector config: "+m.fetchErr.Error()) + "\n")
	}

	b.WriteString(sectionStyle.Render("-- connections --") + "\n")
	if len(m.connections) == 0 {
		b.WriteString(legendStyle.Render("(no connections)") + "\n")
	} else {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-10s %-16s %-12s %s", "HARNESS", "PROVIDER", "CONFIGURED", "DEFAULT MODEL")) + "\n")
		for i, c := range m.connections {
			provider := c.Provider
			if provider == "" {
				provider = unknown
			}
			configured := "no"
			if c.Configured {
				configured = "yes"
			}
			model := unknown
			if c.DefaultModel != nil {
				model = *c.DefaultModel
			}
			line := fmt.Sprintf("%-10s %-16s %-12s %s", c.Harness, provider, configured, model)
			if i == m.selected {
				line = selectedStyle(m.theme).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("-- models --") + "\n")
	if len(m.models) == 0 {
		b.WriteString(legendStyle.Render("(no model catalog available locally)") + "\n")
	} else {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-10s %-16s %-24s %s", "HARNESS", "SLUG", "NAME", "DESCRIPTION")) + "\n")
		for _, mo := range m.models {
			b.WriteString(fmt.Sprintf("%-10s %-16s %-24s %s", mo.Harness, truncate(mo.Slug, 16), truncate(mo.DisplayName, 24), truncate(mo.Description, 40)) + "\n")
		}
	}

	b.WriteString("\n")
	b.WriteString(legendStyle.Render("[j/k] move  [r] refresh  [t] theme  [q] quit"))

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
