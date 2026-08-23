package monitor

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
var sectionStyle = lipgloss.NewStyle().Bold(true).Faint(true)
var labelStyle = lipgloss.NewStyle().Bold(true)

// unknown is what any figure renders as when its own Known bit is false --
// never "0" or blank, the same "unknown, not zero" discipline
// internal/dashboard/internal/cost/internal/agents already enforce for
// this exact shape of gap.
const unknown = "unknown"

// View renders two sections -- HOST (load average, swap, this process'
// process count) and AGENTS (estate lane state counts) -- each
// independently real or "unknown." A quitting Model renders nothing, the
// same convention every other pane in this module follows.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("monitoring") + "\n\n")

	if m.fetchErr != nil {
		errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
		b.WriteString(errStyle.Render("! monitoring unavailable: "+m.fetchErr.Error()) + "\n\n")
	}

	s := m.snapshot
	b.WriteString(sectionStyle.Render("-- host --") + "\n")
	b.WriteString(statLine("CORES", fmt.Sprintf("%d", s.Host.Cores)) + "\n")
	b.WriteString(statLine("LOAD AVG", renderLoadAvg(s.Host)) + "\n")
	b.WriteString(statLine("SWAP USED", renderPercent(s.Host.SwapUsedPercent)) + "\n")
	b.WriteString(statLine("CLAUDE PROCESSES", renderCount(s.Host.ClaudeProcesses)) + "\n")
	if s.HostErr != nil {
		b.WriteString(legendStyle.Render("host read failed: "+s.HostErr.Error()) + "\n")
	}

	b.WriteString("\n")
	b.WriteString(sectionStyle.Render("-- agents --") + "\n")
	b.WriteString(statLine("BY STATE", renderAgents(s.Agents)) + "\n")

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
	return fmt.Sprintf("%s  %s", labelStyle.Render(fmt.Sprintf("%-18s", label)), value)
}

func renderCount(c Count) string {
	if !c.Known {
		return unknown
	}
	return fmt.Sprintf("%d", c.Value)
}

func renderPercent(f Figure) string {
	if !f.Known {
		return unknown
	}
	return fmt.Sprintf("%.1f%%", f.Value)
}

func renderLoadAvg(h Host) string {
	if !h.LoadAvg1.Known || !h.LoadAvg5.Known || !h.LoadAvg15.Known {
		return unknown
	}
	return fmt.Sprintf("%.2f, %.2f, %.2f (1m, 5m, 15m)", h.LoadAvg1.Value, h.LoadAvg5.Value, h.LoadAvg15.Value)
}

// renderAgents mirrors internal/dashboard's own renderAgents exactly --
// same "unknown, not zero" gate on Known, same lane.AllStates-ordered,
// nonzero-only listing -- repeated here rather than imported because
// AgentHealth and dashboard.Stats are deliberately separate types (this
// package's own doc comment on why: two panes reading the same fetch, not
// one importing the other).
func renderAgents(a AgentHealth) string {
	if !a.Known {
		return unknown
	}
	if a.Total == 0 {
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
	for st, n := range a.ByState {
		if n > 0 {
			nonzero = append(nonzero, kv{st, n})
		}
	}
	sort.Slice(nonzero, func(i, j int) bool { return order[nonzero[i].state] < order[nonzero[j].state] })
	parts := make([]string, 0, len(nonzero))
	for _, e := range nonzero {
		parts = append(parts, fmt.Sprintf("%s:%d", e.state, e.n))
	}
	return fmt.Sprintf("%d total (%s)", a.Total, strings.Join(parts, " "))
}
