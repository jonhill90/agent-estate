package dashboard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/lane"
	"github.com/jonhill90/agent-tui/internal/theme"
)

var titleStyle = lipgloss.NewStyle().Bold(true)
var legendStyle = lipgloss.NewStyle().Faint(true)
var labelStyle = lipgloss.NewStyle().Bold(true)

// unknown is what any Stats field renders as when its own Known bit is
// false -- never "0" or blank, the same "unknown, not zero" discipline
// internal/cost.Figure/internal/agents.Row/internal/skills.Skill already
// enforce for this exact shape of gap.
const unknown = "unknown"

// View renders five stat lines, each independently real or "unknown," plus
// a staleness readout matching internal/cost's own "fetched Ns ago"
// convention. A quitting Model renders nothing, the same convention every
// other pane in this module follows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("dashboard") + "\n\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! dashboard unavailable: "+m.fetchErr.Error()) + "\n\n")
	}

	s := m.stats
	b.WriteString(statLine("AGENTS", renderAgents(s)) + "\n")
	b.WriteString(statLine("OPEN PRS", renderCount(s.OpenPRs)) + "\n")
	b.WriteString(statLine("MERGED TODAY", renderCount(s.MergedToday)) + "\n")
	b.WriteString(statLine("SPEND TODAY", renderUSD(s.SpendToday)) + "\n")
	b.WriteString(statLine("VAULT FACTS", renderCount(s.VaultFacts)) + "\n")

	b.WriteString("\n")
	if m.fetchedOnce {
		age := time.Since(m.lastFetched).Round(time.Second)
		b.WriteString(legendStyle.Render(fmt.Sprintf("fetched %s ago -- refreshes every %s", age, refreshInterval)) + "\n")
	} else {
		b.WriteString(legendStyle.Render("not fetched yet") + "\n")
	}
	if m.themeNotice != "" {
		b.WriteString(legendStyle.Render(m.themeNotice) + "\n")
	}
	b.WriteString(legendStyle.Render("[r] refresh  [t] theme  [q] quit"))

	return lipgloss.NewStyle().Width(m.width).Height(m.height).Render(b.String())
}

func statLine(label, value string) string {
	return fmt.Sprintf("%s  %s", labelStyle.Render(fmt.Sprintf("%-14s", label)), value)
}

func renderCount(c Count) string {
	if !c.Known {
		return unknown
	}
	return fmt.Sprintf("%d", c.Value)
}

func renderUSD(u USD) string {
	if !u.Known {
		return unknown
	}
	return fmt.Sprintf("$%.2f", u.Value)
}

// renderAgents shows a total plus every state with at least one agent in
// it, in internal/lane.AllStates' own order -- stable across runs, and
// never a wall of thirteen "state: 0" lines drowning the one or two states
// actually worth looking at. AgentsKnown false (the sessions fetch itself
// failed) renders "unknown," never "0 total" -- a real answer and a
// missing one must never look the same.
func renderAgents(s Stats) string {
	if !s.AgentsKnown {
		return unknown
	}
	total := 0
	for _, n := range s.AgentsByState {
		total += n
	}
	if total == 0 {
		return "0"
	}
	order := make(map[string]int, len(lane.AllStates))
	for i, st := range lane.AllStates {
		order[st] = i
	}
	type kv struct {
		state string
		n     int
	}
	var nonzero []kv
	for st, n := range s.AgentsByState {
		if n > 0 {
			nonzero = append(nonzero, kv{st, n})
		}
	}
	sort.Slice(nonzero, func(i, j int) bool { return order[nonzero[i].state] < order[nonzero[j].state] })
	parts := make([]string, 0, len(nonzero))
	for _, e := range nonzero {
		parts = append(parts, fmt.Sprintf("%s:%d", e.state, e.n))
	}
	return fmt.Sprintf("%d total (%s)", total, strings.Join(parts, " "))
}
