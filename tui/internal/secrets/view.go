package secrets

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// unconfiguredNotice names the file that would back this route and how to
// point at it -- the same "not built yet tells the next person nothing,
// this does" reasoning internal/apidocs.unconfiguredNotice already
// applies to its own missing-spec case.
const unconfiguredNotice = "no -secrets-schema (or $HILL90_APP_REPO) configured -- point it at hill90-app's " +
	"platform/vault/secrets-schema.yaml, the same file its own /admin/secrets endpoint reads"

// unknown is what an absent Rotation renders as -- never blank, matching
// internal/connectors.unknown's own convention for the same shape of gap.
const unknown = "unknown"

func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

// View renders one line per secret key: its vault path, name, consuming
// services, and age/last-rotation where known (always "unknown" today --
// see Rotation's own doc comment on why). Nothing rendered here, or
// anywhere in this package, can be a secret's value -- agent-tui#101's
// level 5, never implemented, not merely hidden by this view.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("secrets") + "\n")

	if m.unconfigured {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(legendStyle.Render("vault paths, key names and consumers -- never a value (agent-tui#101)") + "\n\n")
		b.WriteString(errStyle.Render("! no schema configured") + "\n")
		b.WriteString(legendStyle.Render(unconfiguredNotice) + "\n")
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	if m.inv.SourcePath != "" {
		b.WriteString(legendStyle.Render("source: "+m.inv.SourcePath) + "\n")
	}
	if len(m.inv.ApproleServices) > 0 {
		b.WriteString(legendStyle.Render("approle services: "+strings.Join(m.inv.ApproleServices, ", ")) + "\n")
	}
	b.WriteString("\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! could not read the schema: "+m.fetchErr.Error()) + "\n")
	}

	rows := m.rows()
	switch {
	case !m.fetchedOnce && m.fetchErr == nil:
		b.WriteString(legendStyle.Render("not fetched yet") + "\n")
	case len(rows) == 0 && m.fetchErr == nil:
		b.WriteString(legendStyle.Render("(the schema declares no secrets)") + "\n")
	default:
		b.WriteString(legendStyle.Render(fmt.Sprintf("%-28s %-32s %-10s %-24s %s", "VAULT PATH", "KEY", "AGE", "LAST ROTATION", "CONSUMERS")) + "\n")
		windowRows := m.listRows()
		end := m.offset + windowRows
		if end > len(rows) {
			end = len(rows)
		}
		for i := m.offset; i < end; i++ {
			r := rows[i]
			age, lastRotation := unknown, unknown
			if r.key.Rotation.Known {
				age = fmt.Sprintf("%dd", r.key.Rotation.AgeDays)
				lastRotation = r.key.Rotation.LastRotation
			}
			line := fmt.Sprintf("%-28s %-32s %-10s %-24s %s",
				truncate(r.vaultPath, 28), truncate(r.key.Name, 32), age, lastRotation,
				strings.Join(r.key.Consumers, ","))
			if i == m.selected {
				line = selectedStyle(m.theme).Render(line)
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\n")
	if m.fetchedOnce {
		b.WriteString(legendStyle.Render(fmt.Sprintf("%d keys across %d paths -- read %s ago",
			m.inv.TotalKeys, len(m.inv.Paths), time.Since(m.lastFetched).Round(time.Second))) + "\n")
	}
	if m.themeNotice != "" {
		b.WriteString(legendStyle.Render(m.themeNotice) + "\n")
	}
	b.WriteString(legendStyle.Render("[j/k] move  [r] reload  [t] theme  [q] quit"))

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
