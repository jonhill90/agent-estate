package library

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

// View renders the list (ID | KIND | WEIGHT | STATUS | RESOLVED_TO | BODY,
// scrollable via listVP, selected row highlighted) or, once an item is
// opened, that item's own scrollable reading pane (bodyVP) -- the full
// body plus its originating prompt's context. A quitting Model renders
// nothing, the same convention every other pane in this module follows.
//
// Every fixed line is truncate()'d to m.width BEFORE any style is applied
// -- internal/knowledge's own View doc comment explains why the order
// matters (an already-styled string's ANSI bytes count toward the visible-
// width budget a naive truncate-after would use).
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	if m.mode == modeReading {
		return m.viewReading()
	}
	return m.viewList()
}

func (m Model) viewList() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render(truncate("library", m.width)) + "\n")

	if m.unconfigured {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render(truncate("! no ledger configured -- point -ledger (or $AGENT_TUI_LEDGER) at a copy, or let it auto-discover the live one", m.width)) + "\n")
	} else if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render(truncate("! could not read the corpus: "+m.fetchErr.Error(), m.width)) + "\n")
	} else {
		header := fmt.Sprintf("%-18s %-10s %-10s %-12s %-30s %s", "ID", "KIND", "WEIGHT", "STATUS", "RESOLVED_TO", "BODY")
		b.WriteString(legendStyle.Render(truncate(header, m.width)) + "\n")
	}

	b.WriteString(m.listVP.View() + "\n")
	b.WriteString(legendStyle.Render(truncate(scrollIndicatorText(m.listVP), m.width)) + "\n")

	countLine := "possibility_count: unknown"
	if m.countErr == nil {
		countLine = fmt.Sprintf("possibility_count: %d hard constraints live", m.count)
	}
	b.WriteString(legendStyle.Render(truncate(countLine, m.width)) + "\n")

	legend := fmt.Sprintf(
		"view: %s (%d rows)  weight: %s  status: %s  [j/k] move  [enter] open  [v] view  [f] weight  [x] status  [r] refresh  [t] theme  [q] quit",
		m.view.Label(), len(m.rows), filterLabel(m.weight), filterLabel(m.status),
	)
	b.WriteString(legendStyle.Render(truncate(legend, m.width)))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

func (m Model) renderListLines() string {
	if len(m.rows) == 0 {
		if m.unconfigured || m.fetchErr != nil {
			// The error banner above already says the ledger could not be
			// read -- this pane must not ALSO say "(no items)" underneath
			// it, which would read as "looked, found nothing" rather than
			// "could not look" (this package's own hard requirement).
			return ""
		}
		return legendStyle.Render("(no items match this view/filter)")
	}
	sel := selectedStyle(m.theme)
	var lines []string
	for i, r := range m.rows {
		resolved := r.ResolvedTo
		if resolved == "" {
			resolved = "-"
		}
		line := fmt.Sprintf("%-18s %-10s %-10s %-12s %-30s %s",
			truncate(r.ID, 18), truncate(r.Kind, 10), truncate(r.Weight, 10),
			truncate(r.Status, 12), truncate(resolved, 30), r.BodySnippet)
		if i == m.selected {
			line = sel.Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m Model) viewReading() string {
	var id string
	if m.selected >= 0 && m.selected < len(m.rows) {
		id = m.rows[m.selected].ID
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(truncate("library: "+id, m.width)) + "\n")

	if m.readErr != "" {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render(truncate("! could not open "+id+": "+m.readErr, m.width)) + "\n")
		b.WriteString("\n")
		b.WriteString(legendStyle.Render(truncate("[esc] back  [q] quit", m.width)))
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	d, ok := m.cache[id]
	if !ok {
		b.WriteString(legendStyle.Render(truncate("(loading)", m.width)) + "\n")
		b.WriteString("\n")
		b.WriteString(legendStyle.Render(truncate("[esc] back  [q] quit", m.width)))
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	resolved := d.ResolvedTo
	if resolved == "" {
		resolved = "-"
	}
	meta := fmt.Sprintf("kind: %s   weight: %s   status: %s   resolved_to: %s", d.Kind, d.Weight, d.Status, resolved)
	b.WriteString(legendStyle.Render(truncate(meta, m.width)) + "\n")

	b.WriteString(m.bodyVP.View() + "\n")
	b.WriteString(legendStyle.Render(truncate(scrollIndicatorText(m.bodyVP), m.width)) + "\n")

	b.WriteString(legendStyle.Render(truncate("[esc] back  [up/down/pgup/pgdn] scroll  [r] reload  [t] theme  [q] quit", m.width)))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

// renderDetailBody is bodyVP's own content for an opened item -- the full
// item body, its status_reason when the item was dropped, then the
// originating prompt's own context and text, clearly separated so a reader
// never confuses the JUDGEMENT (the item) with the RECORD it was judged
// from (the prompt) -- the corpus's own layered design (prompts/items,
// this package's own doc comment).
func (m Model) renderDetailBody(d ItemDetail) string {
	var b strings.Builder
	b.WriteString(d.Body)
	b.WriteString("\n")
	if d.StatusReason != "" {
		b.WriteString("\nstatus_reason: " + d.StatusReason + "\n")
	}
	b.WriteString("\n--- from prompt " + d.PromptID)
	if d.PromptAt > 0 {
		b.WriteString(" (" + time.Unix(d.PromptAt, 0).UTC().Format("2006-01-02 15:04Z") + ")")
	}
	b.WriteString(" ---\n")
	if d.PromptContext != "" {
		b.WriteString("\ncontext: " + d.PromptContext + "\n")
	}
	if d.PromptText != "" {
		b.WriteString("\n" + d.PromptText + "\n")
	}
	return b.String()
}

func scrollIndicatorText(vp viewport.Model) string {
	if vp.TotalLineCount() <= vp.Height {
		return ""
	}
	above, below := "", ""
	if !vp.AtTop() {
		above = "▲ more above "
	}
	if !vp.AtBottom() {
		below = "▼ more below "
	}
	pct := int(vp.ScrollPercent() * 100)
	return fmt.Sprintf("%s%s(%d%%)", above, below, pct)
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
