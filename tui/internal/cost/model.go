package cost

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// refreshInterval is 5 minutes. This is a status readout, not a live meter
// (agent-tui#4's own constraint): every fetch shells out to `ccusage`
// twice (daily + blocks), each a real subprocess spawn -- measured against
// this machine's npx-cached ccusage 20.0.19, a single `ccusage daily --json
// --by-agent` call took low-single-digit seconds even scoped to one day,
// because it still has to parse every harness's local usage-log directory
// before filtering. Polling that on anything close to the rail's 2s
// lane-refresh or the board's 5s cadence would make the cost panel itself
// a meaningful, self-inflicted line item in the very spend it exists to
// surface. Today's total moves slowly enough that 5 minutes between
// updates never reads as stale for what this panel answers ("is a harness
// approaching its ceiling"), and Update()/Init() below are the only two
// places a fetch is scheduled -- see model_test.go's
// TestRefreshIntervalIsFiveMinutes, which pins this constant directly so a
// future edit can't silently tighten the poll.
const refreshInterval = 5 * time.Minute

// Fetcher retrieves the current cost snapshot. Errors are surfaced, never
// swallowed into a Snapshot that looks like real zeroed-out data -- same
// discipline internal/rail.Fetcher and internal/board.Fetcher document, and
// the specific failure mode agent-tui#4 exists to prevent: an instrument
// that cannot see must not look like a healthy, cost-free estate.
type Fetcher func() (Snapshot, error)

type refreshMsg time.Time
type fetchResultMsg struct {
	snap Snapshot
	err  error
}

// Model is the cost panel's Bubble Tea program -- a separate, deeper detail
// screen from internal/rail.Model and internal/board.Model, shown only
// behind -cost. Issue agent-tui#4 itself asks for the panel to be "glanceable,
// always there, no command to run"; that requirement is met by
// internal/rail's own compact cost line (rail.NewWithCost, wired in
// cmd/agent-tui) rendering by default with no flag, not by this screen.
// This package is the optional fuller view for whoever wants more than the
// rail's one line per harness: bars/numeric layouts, a view picker, and
// the same "unknown, never zero" honesty discipline the rail line also
// follows. cmd/agent-tui runs exactly one of rail, board, or this screen.
type Model struct {
	fetch Fetcher

	width, height int
	viewIdx       int

	snap        Snapshot
	fetchErr    error
	lastFetched time.Time
	quitting    bool

	// theme is agent-tui#27's seam -- see board.Model's identical field for
	// the full rationale. Defaults to theme.Default so every pre-agent-tui#27
	// caller renders exactly as before this field existed.
	theme       theme.Theme
	themeNotice string
}

func New(fetch Fetcher) Model {
	return Model{fetch: fetch, width: 100, height: 30, theme: theme.Default}
}

// WithTheme returns a copy of m with th (and, when non-empty, a visible
// notice about how th was resolved) wired in.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(refreshCmd(), doFetch(m.fetch))
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func doFetch(fetch Fetcher) tea.Cmd {
	return func() tea.Msg {
		snap, err := fetch()
		return fetchResultMsg{snap: snap, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			return m, doFetch(m.fetch)
		case "t":
			// agent-tui#25 scope item 3: runtime theme comparison, same
			// shape as rail.Model's "t" case. Agent-tui#51: this pane no
			// longer cycles m.theme itself -- see rail's identical case and
			// theme.CycleRequestedMsg's doc comment for why a per-pane
			// theme copy is the defect, not the fix.
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
		if n, ok := digitKey(msg.String()); ok && n >= 1 && n <= len(Views) {
			m.viewIdx = n - 1
		}
		return m, nil

	case refreshMsg:
		return m, tea.Batch(refreshCmd(), doFetch(m.fetch))

	case fetchResultMsg:
		m.fetchErr = msg.err
		if msg.err == nil {
			m.snap = msg.snap
		} else {
			// A failed fetch must not leave a previous, now-stale Snapshot
			// looking current: fold in Unknown() so View() renders
			// "unknown" for everything, not last cycle's real numbers
			// silently going stale under a live-looking display.
			m.snap = Unknown()
		}
		m.lastFetched = time.Now()
		return m, nil
	}
	return m, nil
}

func digitKey(s string) (int, bool) {
	if len(s) != 1 || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	return int(s[0] - '0'), true
}

var titleStyle = lipgloss.NewStyle().Bold(true)

// costStyles are built per-render from m.theme -- see board.boardStyles's
// doc comment for why there is no package-level var carrying a literal
// theme.Role colour any more.
type costStyles struct {
	err, dim, warn lipgloss.Style
}

func (m Model) styles() costStyles {
	th := m.theme
	return costStyles{
		err:  lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleError)),
		dim:  lipgloss.NewStyle().Faint(true),
		warn: lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleWarn)),
	}
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	width := m.width
	if width <= 0 {
		width = 100
	}
	st := m.styles()

	var out string
	out += titleStyle.Render("cost") + "\n"

	if m.themeNotice != "" {
		out += st.err.Render("! "+m.themeNotice) + "\n"
	}

	if m.fetchErr != nil {
		out += st.err.Render("! unavailable") + "\n"
		out += st.dim.Render(m.fetchErr.Error()) + "\n"
	} else if !m.snap.Known && m.lastFetched.IsZero() {
		out += st.dim.Render("(loading)") + "\n"
	}

	view := Views[m.viewIdx]
	body := view.Render(m.snap, width)
	out += colorizeWarn(body, st.warn)

	if !m.lastFetched.IsZero() {
		age := time.Since(m.lastFetched).Round(time.Second)
		out += "\n" + st.dim.Render(fmt.Sprintf("fetched %s ago -- refreshes every %s", age, refreshInterval)) + "\n"
	}
	out += st.dim.Render(fmt.Sprintf("view %d/%d: %s -- %s", m.viewIdx+1, len(Views), view.Name, view.Description)) + "\n"
	// Agent-tui#51: 't' persists now (shell.Model owns the write via
	// theme.Save) -- see rail.Model's identical footer line comment.
	out += st.dim.Render(fmt.Sprintf("theme: %s  [t] switch", m.theme.Name)) + "\n"
	out += st.dim.Render(fmt.Sprintf("[1-%d] switch view  [r] refresh  [q] quit", len(Views)))
	return out
}

// colorizeWarn highlights any line a View marked "WARN" (RenderBars and
// RenderNumeric both write that literal word for a Limit whose ccusage
// status isn't "ok") -- done here, not in view.go, for the same reason
// board.colorizeAged is: the plain-text Render functions stay usable
// outside a styled terminal (a fixture, a test) while the interactive
// Model still gets color.
func colorizeWarn(body string, warnStyle lipgloss.Style) string {
	var out string
	for _, line := range splitLinesKeepEmpty(body) {
		if containsWarn(line) {
			out += warnStyle.Render(line) + "\n"
		} else {
			out += line + "\n"
		}
	}
	if len(out) > 0 {
		out = out[:len(out)-1]
	}
	return out
}

func containsWarn(line string) bool {
	for i := 0; i+4 <= len(line); i++ {
		if line[i:i+4] == "WARN" {
			return true
		}
	}
	return false
}

func splitLinesKeepEmpty(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}
