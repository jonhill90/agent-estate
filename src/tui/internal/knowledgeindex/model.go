package knowledgeindex

import (
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// refreshInterval matches internal/skills's own reasoning: `estate
// knowledge` is run by hand or on its own schedule, not by the second --
// a slow poll plus [r] for "I just regenerated it" is enough.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time
type fetchResultMsg struct {
	res Result
	err error
}

// mode is which of the two panes this Model currently shows -- the item
// list, or one item's own three tiers opened.
type mode int

const (
	modeList mode = iota
	modeDetail
)

// Model renders `estate knowledge`'s own compiled index: per-source
// status (honest OK/FAIL, never a silently smaller list) and every
// Item's Tier1 in a scrollable list, with Tier2/Tier3 reachable per item
// via [enter] -- progressive disclosure the operator's own convention
// requires, not merely this pane's own choice. Read-only, same as
// internal/knowledge: there is no write path anywhere in this package.
type Model struct {
	fetch Fetcher

	res      Result
	fetched  bool
	fetchErr error

	selected int
	mode     mode

	listVP viewport.Model

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in.
func New(fetch Fetcher) Model {
	return Model{
		fetch:  fetch,
		listVP: viewport.New(0, 0),
		width:  100,
		height: 30,
		theme:  theme.Default,
	}
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this repo exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m.sync()
}

// Items exposes the current item list -- exported so a caller (this
// package's own teatest, a future shell wiring) can assert on it without
// depending on View's rendered string.
func (m Model) Items() []Item { return m.res.Items }

// Sources exposes the current per-source status.
func (m Model) Sources() []SourceResult { return m.res.Sources }

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
		res, err := fetch()
		return fetchResultMsg{res: res, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.sync(), nil

	case refreshMsg:
		return m, tea.Batch(refreshCmd(), doFetch(m.fetch))

	case fetchResultMsg:
		m.fetched = true
		m.fetchErr = msg.err
		if msg.err == nil {
			m.res = msg.res
			if m.selected >= len(m.res.Items) {
				m.selected = len(m.res.Items) - 1
			}
			if m.selected < 0 {
				m.selected = 0
			}
		}
		return m.sync(), nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "t":
		return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
	}

	if m.mode == modeDetail {
		switch msg.String() {
		case "esc", "left":
			m.mode = modeList
			return m.sync(), nil
		}
		return m, nil
	}

	switch msg.String() {
	case "r":
		return m, doFetch(m.fetch)
	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
		return m.sync(), nil
	case "down", "j":
		if m.selected < len(m.res.Items)-1 {
			m.selected++
		}
		return m.sync(), nil
	case "pgdown", "ctrl+d":
		m.listVP.HalfPageDown()
		return m, nil
	case "pgup", "ctrl+u":
		m.listVP.HalfPageUp()
		return m, nil
	case "enter", "right":
		if m.selected >= 0 && m.selected < len(m.res.Items) {
			m.mode = modeDetail
			return m.sync(), nil
		}
		return m, nil
	}
	return m, nil
}

// metrics is the width/height split sync and View must agree on.
type metrics struct {
	listHeight int
}

func (m Model) metrics() metrics {
	height := m.height
	if height <= 0 {
		height = 30
	}
	// title + per-source status block + column header + legend line.
	fixed := 4 + len(m.res.Sources)
	listHeight := height - fixed
	if listHeight < 3 {
		listHeight = 3
	}
	return metrics{listHeight: listHeight}
}

// sync recomputes listVP's size/content and keeps the selection visible --
// the one place SetContent/Width/Height is touched, same discipline
// internal/knowledge.Model.sync documents for its own two viewports.
func (m Model) sync() Model {
	mx := m.metrics()
	width := m.width
	if width <= 0 {
		width = 100
	}
	m.listVP.Width = width
	m.listVP.Height = mx.listHeight
	m.listVP.SetContent(m.renderListLines())
	m.ensureListVisible(mx)
	return m
}

func (m *Model) ensureListVisible(mx metrics) {
	if m.listVP.Height <= 0 {
		return
	}
	if m.selected < m.listVP.YOffset {
		m.listVP.SetYOffset(m.selected)
		return
	}
	if m.selected >= m.listVP.YOffset+m.listVP.Height {
		m.listVP.SetYOffset(m.selected - m.listVP.Height + 1)
	}
}

func (m Model) currentItem() (Item, bool) {
	if m.selected < 0 || m.selected >= len(m.res.Items) {
		return Item{}, false
	}
	return m.res.Items[m.selected], true
}
