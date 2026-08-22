package workflows

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

const unknown = "unknown"

func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

// View renders one row per dispatched task -- source, lane, status and its
// four timestamps -- newest first. Unconfigured (no ledger) renders a
// visible error, never an empty table (library.Model's own hard
// requirement, repeated here for the same reason).
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("workflows") + "\n")
	b.WriteString(legendStyle.Render("a task's own path through the estate -- dispatched, delivered, accepted, completed") + "\n\n")

	if m.unconfigured {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! unavailable") + "\n")
		b.WriteString(legendStyle.Render("no -ledger (or $AGENT_TUI_LEDGER) configured -- point it at a COPY of the ledger to see dispatch history") + "\n")
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! could not read ledger: "+m.fetchErr.Error()) + "\n")
	}

	if len(m.rows) == 0 {
		if m.fetchedOnce {
			b.WriteString(legendStyle.Render("(no dispatches recorded)") + "\n")
		} else {
			b.WriteString(legendStyle.Render("not fetched yet") + "\n")
		}
	} else {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-20s %-16s %-10s %-11s %-17s %-17s %-17s %-17s", "SOURCE", "LANE", "STATUS", "TASK", "DISPATCHED", "DELIVERED", "ACCEPTED", "COMPLETED")) + "\n")
		for i, r := range m.rows {
			line := fmt.Sprintf("%-20s %-16s %-10s %-11s %-17s %-17s %-17s %-17s",
				sourceLabel(r), laneLabel(r), statusLabel(r), truncate(r.TaskID, 11),
				formatTS(r.CreatedAt), formatOptTS(r.DeliveredAt), formatOptTS(r.AcceptedAt), formatOptTS(r.CompletedAt))
			if i == m.selected {
				line = selectedStyle(m.theme).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	if m.fetchedOnce {
		age := time.Since(m.lastFetched).Round(time.Second)
		b.WriteString(legendStyle.Render(fmt.Sprintf("fetched %s ago -- refreshes every %s", age, refreshInterval)) + "\n")
	}
	if m.themeNotice != "" {
		b.WriteString(legendStyle.Render(m.themeNotice) + "\n")
	}
	b.WriteString(legendStyle.Render("[j/k] move  [r] refresh  [t] theme  [q] quit"))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

func sourceLabel(r board.TaskRow) string {
	if r.Repo.Name == "" || r.Number == "" {
		return unknown
	}
	kind := "#"
	if r.SourceKind == "pull" {
		kind = "PR#"
	} else if r.SourceKind == "issue" {
		kind = "#"
	}
	return truncate(r.Repo.Name+" "+kind+r.Number, 20)
}

func laneLabel(r board.TaskRow) string {
	if r.Lane == "" {
		return unknown
	}
	return r.Lane
}

func statusLabel(r board.TaskRow) string {
	if r.TaskStatus != "" {
		return r.TaskStatus
	}
	if r.SourceStatus != "" {
		return r.SourceStatus
	}
	return unknown
}

func formatTS(sec int64) string {
	if sec == 0 {
		return unknown
	}
	return time.Unix(sec, 0).Format("2006-01-02 15:04")
}

func formatOptTS(sec *int64) string {
	if sec == nil || *sec == 0 {
		return "--"
	}
	return time.Unix(*sec, 0).Format("2006-01-02 15:04")
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
