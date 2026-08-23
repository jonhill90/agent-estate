package admin

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// refreshInterval: docker container state and $PATH contents change by
// human action, not by the second -- the same reasoning
// internal/skills/internal/mcpservers' own refreshInterval doc comments
// give for a local read with no polling cost. 30s matches both.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time

type fetchResultMsg struct {
	snap Snapshot
	err  error
}

// Model is S11's Bubble Tea program: a read-only rendering of Snapshot's
// five named sections. "Read-only first" (S11's own spec text) is
// reflected in Update below having no write path at all -- no keys beyond
// navigation, refresh, theme and quit, matching S6's own "[n] read-only
// until S7" precedent but with no equivalent write item planned here to
// defer to. Not wired into internal/shell yet -- matches every other
// pane this build order has shipped standalone (S5/S6/S8/S9).
type Model struct {
	fetch Fetcher

	snap     Snapshot
	fetchErr error

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in.
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

// Snapshot returns m's current Snapshot -- exported so a caller (a future
// shell wiring, or this package's own teatest) can assert on it without
// depending on View's rendered string.
func (m Model) Snapshot() Snapshot { return m.snap }

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
		return fetchResultMsg{snap: snap, err: err}
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
			m.snap = msg.snap
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
