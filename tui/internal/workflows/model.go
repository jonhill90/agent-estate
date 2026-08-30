package workflows

import (
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/tui/internal/board"
	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// refreshInterval matches internal/rail/internal/board's own ledger-poll
// cadence, not internal/library's slower 30s one -- a dispatch history is
// closer in spirit to the board (both read the SAME ledger.ReadTaskRows)
// than to the corpus's own batch-loaded cadence.
const refreshInterval = 5 * time.Second

type refreshMsg time.Time
type fetchResultMsg struct {
	rows []board.TaskRow
	err  error
}

// Model is Build -> Workflows' pane: every task's own dispatched/
// delivered/accepted/completed path through the estate, newest first.
// Read-only -- this package has no write path.
type Model struct {
	fetch Fetcher

	rows     []board.TaskRow
	fetchErr error
	// unconfigured is true when New was given a nil Fetcher -- no -ledger
	// (or its auto-discovered default) was available at all, the same
	// distinct-from-"fetched zero rows" state internal/library.Model's own
	// doc comment establishes and w5c.md required.
	unconfigured bool

	fetchedOnce bool
	lastFetched time.Time

	selected int

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in.
func New(fetch Fetcher) Model {
	return Model{fetch: fetch, unconfigured: fetch == nil, width: 100, height: 30, theme: theme.Default}
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this module exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// Rows returns m's current dispatch history, newest first, exported so a
// caller (a future shell wiring, or this package's own teatest) can assert
// on the fetched data without depending on View's rendered string.
func (m Model) Rows() []board.TaskRow { return m.rows }

func (m Model) Init() tea.Cmd {
	if m.unconfigured {
		return nil
	}
	return tea.Batch(refreshCmd(), doFetch(m.fetch))
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func doFetch(fetch Fetcher) tea.Cmd {
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		rows, err := fetch()
		return fetchResultMsg{rows: rows, err: err}
	}
}

// byCreatedDesc sorts newest dispatch first -- a human reading a dispatch
// history wants to see the most recent activity without scrolling, the
// same "what happened lately" framing internal/board's own kanban columns
// give WIP.
func byCreatedDesc(rows []board.TaskRow) []board.TaskRow {
	sorted := make([]board.TaskRow, len(rows))
	copy(sorted, rows)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].CreatedAt > sorted[j].CreatedAt })
	return sorted
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case refreshMsg:
		return m, tea.Batch(refreshCmd(), doFetch(m.fetch))

	case fetchResultMsg:
		m.fetchErr = msg.err
		if msg.err == nil {
			m.rows = byCreatedDesc(msg.rows)
			m.lastFetched = time.Now()
			m.fetchedOnce = true
			if m.selected >= len(m.rows) {
				m.selected = len(m.rows) - 1
			}
			if m.selected < 0 {
				m.selected = 0
			}
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			return m, doFetch(m.fetch)
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case "down", "j":
			if m.selected < len(m.rows)-1 {
				m.selected++
			}
			return m, nil
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}
