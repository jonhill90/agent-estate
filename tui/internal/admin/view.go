package admin

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var sectionStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// unknown is what Dependency.Reachable renders as when nil -- never
// "false" or blank, the same "unknown, not zero" discipline
// internal/mcpservers.Server.Reachable already enforces for the identical
// check.
const unknown = "unknown"

// View renders all five S11 sections, top to bottom: Services,
// Dependencies, Settings (each real, each with its own error line if its
// own fetch failed), then Profiles and Users (always their fixed note,
// never an empty "checked, zero" list -- Snapshot's own doc comment says
// why). A quitting Model renders nothing, the same convention every
// other pane in this module follows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("admin") + "\n\n")

	errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
	if m.fetchErr != nil {
		b.WriteString(errStyle.Render("! admin snapshot unavailable: "+m.fetchErr.Error()) + "\n\n")
	}

	b.WriteString(sectionStyle.Render("Services") + "\n")
	switch {
	case m.snap.ServicesErr != nil:
		b.WriteString(errStyle.Render("! "+m.snap.ServicesErr.Error()) + "\n")
	case len(m.snap.Services) == 0:
		b.WriteString(legendStyle.Render("(no containers)") + "\n")
	default:
		for _, s := range m.snap.Services {
			b.WriteString(fmt.Sprintf("  %-24s %-24s %s\n", s.Name, s.Image, s.Status))
		}
	}
	b.WriteString("\n")

	b.WriteString(sectionStyle.Render("Dependencies") + "\n")
	switch {
	case m.snap.DependenciesErr != nil:
		b.WriteString(errStyle.Render("! "+m.snap.DependenciesErr.Error()) + "\n")
	case len(m.snap.Dependencies) == 0:
		b.WriteString(legendStyle.Render("(not checked)") + "\n")
	default:
		for _, d := range m.snap.Dependencies {
			reachable := unknown
			if d.Reachable != nil {
				if *d.Reachable {
					reachable = "yes"
				} else {
					reachable = "no"
				}
			}
			b.WriteString(fmt.Sprintf("  %-24s %s\n", d.Name, reachable))
		}
	}
	b.WriteString("\n")

	b.WriteString(sectionStyle.Render("Settings") + "\n")
	switch {
	case m.snap.SettingsErr != nil:
		b.WriteString(errStyle.Render("! "+m.snap.SettingsErr.Error()) + "\n")
	case len(m.snap.Settings) == 0:
		b.WriteString(legendStyle.Render("(none)") + "\n")
	default:
		for _, s := range m.snap.Settings {
			b.WriteString(fmt.Sprintf("  %-24s %s\n", s.Name, s.Value))
		}
	}
	b.WriteString("\n")

	b.WriteString(sectionStyle.Render("Profiles") + "\n")
	b.WriteString(legendStyle.Render("  "+m.snap.ProfilesNote) + "\n\n")

	b.WriteString(sectionStyle.Render("Users") + "\n")
	b.WriteString(legendStyle.Render("  "+m.snap.UsersNote) + "\n\n")

	b.WriteString(legendStyle.Render("[r] refresh  [t] theme  [q] quit"))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}
