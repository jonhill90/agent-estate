package external

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// View names the destination and what will happen to it. It deliberately
// does NOT say "not built yet": there is nothing to build here, and a
// reader who is told to wait for a pane that will never exist has been
// told something false.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	title := m.title
	if title == "" {
		title = "external destination"
	}
	b.WriteString(titleStyle.Render(strings.ToLower(title)) + "\n")
	b.WriteString(legendStyle.Render("external destination -- a browser page, not a pane in this application") + "\n\n")

	urlStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleNeutral))
	if m.url == "" {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! no URL recorded for this route") + "\n")
		b.WriteString(legendStyle.Render("internal/nav's tree carries the href for every KindExternal item; this one is empty") + "\n")
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}
	b.WriteString("  " + urlStyle.Render(m.url) + "\n\n")
	b.WriteString(legendStyle.Render("this route is declared external in the nav tree (nav.KindExternal), matching") + "\n")
	b.WriteString(legendStyle.Render("hill90-app's own nav-items.ts, where it is `external: true` -- so it opens in") + "\n")
	b.WriteString(legendStyle.Render("your browser rather than rendering here.") + "\n\n")

	switch {
	case m.open == nil:
		b.WriteString(legendStyle.Render("no browser opener is wired in this build -- copy the URL above by hand") + "\n")
	case m.openErr != nil:
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! could not open it: "+m.openErr.Error()) + "\n")
	case m.opened:
		okStyle := lipgloss.NewStyle().Foreground(m.theme.Color(theme.RoleDone))
		b.WriteString(okStyle.Render("handed to your browser -- check behind this terminal") + "\n")
	}

	b.WriteString("\n")
	if m.themeNotice != "" {
		b.WriteString(legendStyle.Render(m.themeNotice) + "\n")
	}
	b.WriteString(legendStyle.Render("[o] open in browser  [t] theme  [q] quit"))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}
