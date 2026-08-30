package agents

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// unknown is what Row.Model/Row.Cost render as -- never "0" or blank, the
// same "unknown, not zero" discipline internal/cost's Figure.Known already
// enforces for the harness-wide totals this package's own Cost field
// cannot attribute per lane (Row's own doc comment).
const unknown = "unknown"

// View renders a flat table -- ID | STATE | MODE | MODEL | TASK | COST --
// one row per Row (Derive's output, via m.Rows()). MODE (SPEC-shell.md
// S12) reads like MODEL and COST now: "unknown" for a nil Row.Mode,
// never a guessed value -- see Row.Mode's and modeFor's own doc comments
// for exactly when that is. A quitting Model renders nothing, the same
// convention internal/cost.Model.View follows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("agents") + "\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! sessions unavailable: "+m.fetchErr.Error()) + "\n")
		// agent-tui#175: a failed refresh no longer clears m.sessions (see
		// Update's fetchResultMsg case), so rows below may be the last GOOD
		// fetch rather than current data -- say so explicitly rather than
		// let a reader mistake an unlabelled table for a fresh one.
		if !m.lastFetched.IsZero() {
			age := time.Since(m.lastFetched).Round(time.Second)
			b.WriteString(legendStyle.Render(fmt.Sprintf("(showing last good data, age: %s)", age)) + "\n")
		}
	}

	rows := m.Rows()
	if len(rows) == 0 {
		b.WriteString(legendStyle.Render("(no agents)") + "\n")
	} else {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-28s %-10s %-9s %-10s %-16s %s", "ID", "STATE", "MODE", "MODEL", "TASK", "COST")) + "\n")
		for _, r := range rows {
			mode := unknown
			if r.Mode != nil {
				mode = string(*r.Mode)
			}
			model := unknown
			if r.Model != nil {
				model = *r.Model
			}
			cost := unknown
			if r.Cost != nil {
				cost = *r.Cost
			}
			b.WriteString(fmt.Sprintf("%-28s %-10s %-9s %-10s %-16s %s", truncate(r.ID, 28), truncate(r.State, 10), truncate(mode, 9), truncate(model, 10), truncate(r.Task, 16), cost) + "\n")
		}
	}

	b.WriteString("\n")
	if m.notice != "" {
		b.WriteString(legendStyle.Render(m.notice) + "\n")
	}
	b.WriteString(legendStyle.Render("[n] thread (S7)  [r] refresh  [t] theme  [q] quit"))

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
