package connectors

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/theme"
)

// refreshInterval matches internal/skills/internal/mcpservers' identical
// reasoning: these are local config files a human edits by hand, not a
// live stream.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time
type fetchResultMsg struct {
	connections []Connection
	models      []AvailableModel
	err         error
}

// Model is S10's Bubble Tea program: "Provider connections and models" --
// mirrors web Connect's two sections in one pane rather than a tab picker,
// since this estate has at most three connections and one harness's model
// catalog to show (Load's own doc comment on why Claude/pi have none
// today). Not wired into internal/shell yet -- matches internal/stub (S5),
// internal/agents (S6), internal/skills (S8) and internal/mcpservers
// (S9)'s own precedent of shipping a standalone, driveable pane before its
// route exists. Per this item's own instruction, this package does not
// touch internal/nav or internal/shell routing at all.
type Model struct {
	fetch Fetcher

	connections []Connection
	models      []AvailableModel
	fetchErr    error

	selected int // indexes connections; models has no selection (read-only list)

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

// Connections/Models return m's current data, exported so a caller (a
// future shell wiring, or this package's own teatest) can assert on them
// without depending on View's rendered string.
func (m Model) Connections() []Connection { return m.connections }
func (m Model) Models() []AvailableModel  { return m.models }

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
		conns, models, err := fetch()
		return fetchResultMsg{connections: conns, models: models, err: err}
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
			m.connections = msg.connections
			m.models = msg.models
			if m.selected >= len(m.connections) {
				m.selected = len(m.connections) - 1
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
			if m.selected < len(m.connections)-1 {
				m.selected++
			}
			return m, nil
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}
