package flow

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/board"
	"github.com/jonhill90/agent-tui/internal/theme"
)

// motionInterval drives ONLY the pipeline header's travelling marker -- a
// local animation tick, never a fetch. #64 asks for a view where "work
// VISIBLY MOVES," which a 60s-refreshed static diagram cannot give; this is
// the cheap way to give it without repeating #28's mistake (a faster
// gh/ledger poll starving agent-supervisor#144's shared budget). Model owns
// NO Fetcher of its own and runs no fetch loop at all -- see WithSnapshot
// below and this package's own doc comment: cmd/estate's board.Fetcher is
// read exactly once per tick, by board.Model, and shell.Model pushes its
// result into this pane too. A second independent fetch loop here would
// double every gh/ledger call agent-tui#28 already sized against a shared
// rate budget, for a pane that shows a projection of the SAME data.
const motionInterval = 400 * time.Millisecond

type motionMsg time.Time

// Model is the flow pane's Bubble Tea program -- a sibling of board.Model
// in internal/shell, not a mode grafted onto it (same discipline
// board.Model's own doc comment cites for agent-tui#6).
type Model struct {
	width, height int

	items       []Item
	fetchErr    error
	lastFetched time.Time

	// animFrame advances on every motionMsg, independent of any fetch --
	// arrowTrack (view.go) uses it to slide a marker along the pipeline.
	animFrame int

	// showAll toggles the body list between InFlight-only (default: what
	// is actually moving) and every stage including Queued/Done -- 'a'
	// toggles it, so a lane can still see the queue depth and recent
	// completions without them crowding the default "watch it move" view.
	showAll bool

	body     viewport.Model
	quitting bool

	theme       theme.Theme
	themeNotice string
}

func New() Model {
	return Model{
		width:  100,
		height: 30,
		body:   viewport.New(100, 20),
		theme:  theme.Default,
	}
}

// WithSnapshot re-projects snap onto this pane's Stage pipeline -- the one
// place flow's data ever changes. shell.Model calls this every time it
// updates board.Model (routeAll/resize), passing board.Model's own
// Snapshot()/LastFetched()/FetchErr() straight through: this is the
// "read the exact same already-derived board.Snapshot" contract this
// package's doc comment names, made concrete as one method instead of a
// second fetch loop.
func (m Model) WithSnapshot(snap board.Snapshot, lastFetched time.Time, err error) Model {
	m.fetchErr = err
	if err == nil {
		m.items = DeriveItems(snap.Cards)
		m.lastFetched = lastFetched
	}
	m.body.SetContent(m.bodyContent())
	return m
}

// WithTheme mirrors board.Model.WithTheme -- shell.Model's applyTheme calls
// this on every pane, flow included, so a runtime 't' cycle repaints this
// pane along with the rest (agent-tui#51's single-owner rule).
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

func (m Model) Init() tea.Cmd {
	return motionCmd()
}

func motionCmd() tea.Cmd {
	return tea.Tick(motionInterval, func(t time.Time) tea.Msg { return motionMsg(t) })
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.body.Width = m.width
		m.body.Height = m.bodyHeight()
		m.body.SetContent(m.bodyContent())
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "a":
			m.showAll = !m.showAll
			m.body.SetContent(m.bodyContent())
			m.body.GotoTop()
			return m, nil
		case "t":
			// Same shape as board.Model's own "t" case -- see
			// theme.CycleRequestedMsg's doc comment for why this asks
			// shell.Model to cycle rather than cycling m.theme itself.
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
		var cmd tea.Cmd
		m.body, cmd = m.body.Update(msg)
		return m, cmd

	case motionMsg:
		m.animFrame++
		m.body.SetContent(m.bodyContent()) // re-render so any age-driven colour stays current too
		return m, motionCmd()
	}
	return m, nil
}

// headerHeight/footerHeight are fixed line counts View() reserves outside
// the viewport. headerHeight counts m.header()'s ACTUAL rendered lines
// (rather than a hand-kept number that could drift from it -- agent-tui#29's
// own lesson: pipeline()'s blocked-loop line makes header() a variable
// number of lines, and a hardcoded count silently undercounted it by
// exactly one, caught by this package's own TestViewNeverExceedsHeightBudget
// before it ever reached a live pane). footerHeight is a constant instead:
// footer()'s own scrollIndicator line depends on the viewport's
// dimensions, which this budget is used to SET, so counting footer()'s
// actual lines here would be circular -- footer() always returns exactly
// footerHeight lines (scrollIndicator's own doc comment covers the "empty
// string, not a missing line" convention that keeps this true).
const footerHeight = 3

func (m Model) headerHeight() int {
	return strings.Count(joinLines(m.header()), "\n") + 1
}

func (m Model) bodyHeight() int {
	h := m.height - m.headerHeight() - footerHeight
	if h < 1 {
		h = 1
	}
	return h
}

var titleStyle = lipgloss.NewStyle().Bold(true)

type flowStyles struct {
	err, dim, warn lipgloss.Style
	stage          [stageCount]lipgloss.Style
}

func (m Model) styles() flowStyles {
	th := m.theme
	return flowStyles{
		err:  lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleError)),
		dim:  lipgloss.NewStyle().Faint(true),
		warn: lipgloss.NewStyle().Foreground(th.Color(theme.RoleWarn)),
		stage: [stageCount]lipgloss.Style{
			StageQueued:  lipgloss.NewStyle().Foreground(th.Color(theme.RoleBacklog)),
			StageWorking: lipgloss.NewStyle().Foreground(th.Color(theme.RoleInProgress)),
			StageReview:  lipgloss.NewStyle().Foreground(th.Color(theme.RoleInReview)),
			StageBlocked: lipgloss.NewStyle().Foreground(th.Color(theme.RoleBlocked)),
			StageDone:    lipgloss.NewStyle().Foreground(th.Color(theme.RoleDone)),
		},
	}
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	var out []string
	out = append(out, m.header()...)
	out = append(out, m.body.View())
	out = append(out, m.footer()...)
	return joinLines(out)
}

func joinLines(lines []string) string {
	s := ""
	for i, l := range lines {
		if i > 0 {
			s += "\n"
		}
		s += l
	}
	return s
}

func (m Model) footer() []string {
	st := m.styles()
	var lines []string
	if !m.lastFetched.IsZero() {
		age := time.Since(m.lastFetched).Round(time.Second)
		lines = append(lines, st.dim.Render(fmt.Sprintf("fetched %s ago", age)))
	} else {
		lines = append(lines, st.dim.Render("(loading)"))
	}
	mode := "in-flight only"
	if m.showAll {
		mode = "every stage"
	}
	lines = append(lines, st.dim.Render(fmt.Sprintf(
		"[a] toggle %s  [j/k up/down  pgup/pgdn] scroll  [t] theme  [q] quit  (refreshes with the board pane)", mode,
	)))
	// scrollIndicator is #64's own "confirm content taller than the pane is
	// reachable and that the user can tell something is hidden"
	// requirement (AGENTS.md's verification section) -- bubbles/viewport,
	// unlike board.Model's hand-rolled scrollBody, renders no such line on
	// its own. Always appended (blank when nothing is hidden) so footer()
	// stays exactly footerHeight lines -- see footerHeight's own doc
	// comment for why that fixed count, not this line's presence, is what
	// bodyHeight budgets against.
	lines = append(lines, st.dim.Render(m.scrollIndicator()))
	return lines
}

func (m Model) scrollIndicator() string {
	if m.body.AtTop() && m.body.AtBottom() {
		return ""
	}
	total, visible, offset := m.body.TotalLineCount(), m.body.VisibleLineCount(), m.body.YOffset
	above := offset
	below := total - offset - visible
	if below < 0 {
		below = 0
	}
	return fmt.Sprintf("-- showing lines %d-%d of %d (%d more above, %d more below) --", offset+1, offset+visible, total, above, below)
}
