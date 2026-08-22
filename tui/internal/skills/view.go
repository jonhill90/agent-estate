package skills

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// unknown is what Skill.LastEval/InvocationCount render as -- never "0" or
// blank, the same "unknown, not zero" discipline internal/agents.Row's
// Model/Cost fields already enforce for this exact shape of gap.
const unknown = "unknown"

// selectedStyle marks m.selected the same way internal/board and
// internal/rail mark a cursor row -- via theme.RoleSelectedBG, never a
// hardcoded literal (theme's own seam, AGENTS.md's "every look-and-feel
// literal moves here").
func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

// View renders a flat table -- NAME | DESCRIPTION | LAST EVAL | VERDICT |
// INVOCATIONS -- one row per Skill, selected row highlighted. A quitting
// Model renders nothing, the same convention every other pane in this
// module follows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("skills") + "\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! skills unavailable: "+m.fetchErr.Error()) + "\n")
	}

	if len(m.skills) == 0 {
		b.WriteString(legendStyle.Render("(no skills)") + "\n")
	} else {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-28s %-40s %-10s %-11s %s", "NAME", "DESCRIPTION", "LAST EVAL", "VERDICT", "INVOCATIONS")) + "\n")
		for i, s := range m.skills {
			name := s.Name
			if s.ParseErr != "" {
				name = s.Dir + " (!)"
			}
			lastEval := unknown
			if s.LastEval != nil {
				lastEval = *s.LastEval
			}
			verdict := s.Verdict
			if verdict == "" {
				verdict = VerdictUnevaluated
			}
			invocations := unknown
			if s.InvocationCount != nil {
				invocations = fmt.Sprintf("%d", *s.InvocationCount)
			}
			line := fmt.Sprintf("%-28s %-40s %-10s %-11s %s", truncate(name, 28), truncate(s.Description, 40), truncate(lastEval, 10), truncate(verdict, 11), invocations)
			if i == m.selected {
				line = selectedStyle(m.theme).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	if m.notice != "" {
		b.WriteString(legendStyle.Render(m.notice) + "\n")
	}
	b.WriteString(legendStyle.Render("[e] eval  [r] refresh  [t] theme  [q] quit"))

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
