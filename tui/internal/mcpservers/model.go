package mcpservers

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// refreshInterval: configuration files change by hand, not by the second --
// the same reasoning internal/skills' own refreshInterval doc comment
// gives for a local read with no polling cost. 30s matches it.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time

type fetchResultMsg struct {
	servers []Server
	err     error
}

// Model is S9's Bubble Tea program: a flat list of configured MCP
// servers -- name, scope, transport, and reachability (stdio only --
// WithReachability's own doc comment says why http/sse are never
// live-probed). Not wired into internal/shell yet -- matches
// internal/stub (S5), internal/agents (S6) and internal/skills (S8)'s own
// precedent of shipping a standalone, driveable pane before its route
// exists (SPEC-shell.md's own build order leaves that wiring to a later
// item).
type Model struct {
	fetch Fetcher

	servers  []Server
	fetchErr error

	selected int

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

// Servers returns m's current server list -- exported so a caller (a
// future shell wiring, or this package's own teatest) can assert on it
// without depending on View's rendered string.
func (m Model) Servers() []Server { return m.servers }

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
		servers, err := fetch()
		return fetchResultMsg{servers: servers, err: err}
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
			m.servers = msg.servers
			if m.selected >= len(m.servers) {
				m.selected = len(m.servers) - 1
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
			if m.selected < len(m.servers)-1 {
				m.selected++
			}
			return m, nil
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}
