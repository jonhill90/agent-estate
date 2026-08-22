// Package stub is SPEC-shell.md's S5: an honest placeholder for every nav
// destination that has no real view yet. "A visible stub beats a hidden
// screen -- the current failure is not knowing the board exists" (S5) --
// so every route the shell can navigate to renders *something* the moment
// S1-S4 land, rather than a route that silently does nothing until its own
// item is built.
//
// This package renders only. It owns no nav data of its own (S1's
// internal/nav is a different item, built separately) and no Bubble Tea
// Model -- a stub has no state to update and no keys to handle beyond what
// the shell's own routing already does, so a plain render function is the
// whole surface. The shell wires View's output into its content pane for
// any route that is not one of S4's three (Tasks, Usage, Lanes).
package stub

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

// View renders title and description plus a fixed "not built yet" line,
// styled through th so a stub looks like part of the same application
// rather than an unstyled placeholder -- theme is the seam every other
// render path in this module goes through (internal/theme's own doc
// comment), and a stub is not an exception to that.
func View(th theme.Theme, title, description string) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(th.Colors[theme.RoleNeutral])

	descStyle := lipgloss.NewStyle().
		Foreground(th.Colors[theme.RoleNeutral])

	notBuiltStyle := lipgloss.NewStyle().
		Foreground(th.Colors[theme.RoleWarn])

	body := lipgloss.JoinVertical(
		lipgloss.Left,
		titleStyle.Render(title),
		descStyle.Render(description),
		"",
		notBuiltStyle.Render("not built yet"),
	)

	return lipgloss.NewStyle().
		Border(th.Border).
		BorderForeground(th.Colors[theme.RoleBorder]).
		Padding(th.Padding).
		Render(body)
}
