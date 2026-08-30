package knowledge

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)

// unknown is what a Row's nil Type/Created render as -- never blank,
// matching internal/agents.Row and internal/skills.Skill's own convention
// for the same shape of gap (a column with no cheap source).
const unknown = "unknown"

func selectedStyle(th theme.Theme) lipgloss.Style {
	return lipgloss.NewStyle().Background(th.Color(theme.RoleSelectedBG))
}

func sortLabel(s sortMode) string {
	if s == sortAlpha {
		return "title"
	}
	return "index"
}

// View renders the list (SLUG | TYPE | TITLE/DESCRIPTION | CREATED,
// scrollable via listVP, selected row highlighted) or, once a fact is
// opened, that fact's own scrollable reading pane (bodyVP). A quitting
// Model renders nothing, the same convention every other pane in this
// module follows.
//
// Every fixed line here (title, header, error, legend) is truncate()'d
// to m.width BEFORE any style is applied, never after -- found by driving
// this at the terminal width teatest's own default (100) uses: truncating
// an already-styled string counts the invisible ANSI escape bytes toward
// the visible-width budget, cutting the line far short of (or mangling)
// what actually fits. Every other package in this module truncates plain
// text first and styles the result (internal/skills.View's own
// line-then-style order); this package's first draft did it backwards.
// Untruncated in EITHER order, an over-width line gets word-WRAPPED by
// lipgloss rather than clipped, silently adding physical rows past what
// metrics()'s fixed 4-line chrome budget accounts for -- the same
// "content taller than its own budget" class agent-tui#29 named once
// already, just found here through a header line instead of a pane body.
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
	b.WriteString(titleStyle.Render(truncate("knowledge", m.width)) + "\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render(truncate("! could not read memory vault: "+m.fetchErr.Error(), m.width)) + "\n")
	} else {
		header := fmt.Sprintf("%-40s %-10s %-56s %s", "SLUG", "TYPE", "TITLE / DESCRIPTION", "CREATED")
		b.WriteString(legendStyle.Render(truncate(header, m.width)) + "\n")
	}

	b.WriteString(m.listVP.View() + "\n")
	b.WriteString(legendStyle.Render(truncate(scrollIndicatorText(m.listVP), m.width)) + "\n")

	legend := fmt.Sprintf("sort: %s (%d facts)  [j/k] move  [pgup/pgdn] scroll  [enter] open  [s] sort  [r] refresh  [t] theme  [q] quit", sortLabel(m.sort), len(m.entries))
	b.WriteString(legendStyle.Render(truncate(legend, m.width)))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

func (m Model) viewReading() string {
	rows := m.Rows()
	var slug string
	if m.selected >= 0 && m.selected < len(rows) {
		slug = rows[m.selected].Slug
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render(truncate("knowledge: "+slug, m.width)) + "\n")

	if m.readErr != "" {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render(truncate("! could not open "+slug+": "+m.readErr, m.width)) + "\n")
		b.WriteString("\n")
		b.WriteString(legendStyle.Render(truncate("[esc] back  [q] quit", m.width)))
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	f, ok := m.cache[slug]
	if !ok {
		b.WriteString(legendStyle.Render(truncate("(loading)", m.width)) + "\n")
		b.WriteString("\n")
		b.WriteString(legendStyle.Render(truncate("[esc] back  [q] quit", m.width)))
		return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
	}

	meta := fmt.Sprintf("type: %s   created: %s", orUnknown(f.Type), orUnknown(f.Created))
	b.WriteString(legendStyle.Render(truncate(meta, m.width)) + "\n")

	b.WriteString(m.bodyVP.View() + "\n")
	b.WriteString(legendStyle.Render(truncate(scrollIndicatorText(m.bodyVP), m.width)) + "\n")

	b.WriteString(legendStyle.Render(truncate("[esc] back  [up/down/pgup/pgdn] scroll  [r] reload  [t] theme  [q] quit", m.width)))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

func orUnknown(s string) string {
	if s == "" {
		return unknown
	}
	return s
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
