package monitor

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// refreshInterval matches internal/rail/internal/agents' 2s cadence, not
// internal/cost's 5-minute one -- host load and agent state are both
// live-changing figures a human watches move, unlike ccusage's polled
// spend totals (internal/cost's own refreshInterval doc comment).
const refreshInterval = 2 * time.Second

type refreshMsg time.Time
type fetchResultMsg struct {
	snapshot Snapshot
	err      error
}

// Model is Observe -> Monitoring's pane: one fetch's worth of Snapshot,
// host and agent health rendered independently -- see Snapshot's own doc
// comment for why one half failing must never blank the other.
type Model struct {
	fetch Fetcher

	snapshot    Snapshot
	fetchErr    error
	lastFetched time.Time
	fetchedOnce bool

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in; nil is a valid, silent "no
// monitoring source configured" default, the same convention every other
// optional Fetcher in this module follows.
func New(fetch Fetcher) Model {
	return Model{fetch: fetch, width: 100, height: 30, theme: theme.Default}
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this module exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// Snapshot returns m's current figures, exported so a caller (a future
// shell wiring, or this package's own teatest) can assert on the fetched
// data without depending on View's rendered string.
func (m Model) Snapshot() Snapshot { return m.snapshot }

func (m Model) Init() tea.Cmd {
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
		snap, err := fetch()
		return fetchResultMsg{snapshot: snap, err: err}
	}
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
			m.snapshot = msg.snapshot
			m.lastFetched = time.Now()
			m.fetchedOnce = true
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			return m, doFetch(m.fetch)
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}
