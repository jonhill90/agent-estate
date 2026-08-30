package secrets

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// refreshInterval matches internal/apidocs' own reasoning: schema.yaml
// changes when someone edits hill90-app's vault layout and commits, not
// by the second, so a slow reload plus [r] for "I just changed it" is the
// right cadence.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time
type fetchResultMsg struct {
	inv Inventory
	err error
}

// Model is Connect -> Secrets' pane: agent-tui#101's decision rendered --
// every vault path's key names and consumers (levels 1-2), age and last
// rotation where known (levels 3-4, always "unknown" today -- see
// Rotation's own doc comment), and no field anywhere capable of holding a
// value (level 5, never). Read-only -- this package has no write path and
// never calls OpenBao.
type Model struct {
	fetch Fetcher

	inv      Inventory
	fetchErr error
	// unconfigured is true when New was given a nil Fetcher -- no schema
	// path was resolvable at all, the same distinction
	// internal/apidocs.Model.unconfigured draws for a missing OpenAPI
	// spec.
	unconfigured bool

	fetchedOnce bool
	lastFetched time.Time

	selected int // indexes the flattened key list, across all paths
	offset   int

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in. A nil fetch is the "no schema
// configured" state, not a crash -- cmd/estate passes nil when neither
// -secrets-schema nor $HILL90_APP_REPO resolves to a file.
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

// Inventory returns what was last loaded, exported so a caller (or this
// package's own teatest) can assert on the parsed data without depending
// on View's rendered string.
func (m Model) Inventory() Inventory { return m.inv }

// flatRow is one line of the flattened key list View renders -- a key
// together with the vault path it belongs to, since the list scrolls
// across path boundaries.
type flatRow struct {
	vaultPath string
	key       Key
}

func (m Model) rows() []flatRow {
	var out []flatRow
	for _, p := range m.inv.Paths {
		for _, k := range p.Keys {
			out = append(out, flatRow{vaultPath: p.VaultPath, key: k})
		}
	}
	return out
}

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
		inv, err := fetch()
		return fetchResultMsg{inv: inv, err: err}
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
			m.inv = msg.inv
			m.lastFetched = time.Now()
			m.fetchedOnce = true
		}
		return m.clampSelection(), nil

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
			return m.clampSelection(), nil
		case "down", "j":
			if m.selected < len(m.rows())-1 {
				m.selected++
			}
			return m.clampSelection(), nil
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}

func (m Model) clampSelection() Model {
	n := len(m.rows())
	if m.selected >= n {
		m.selected = n - 1
	}
	if m.selected < 0 {
		m.selected = 0
	}
	rows := m.listRows()
	if m.selected < m.offset {
		m.offset = m.selected
	}
	if m.selected >= m.offset+rows {
		m.offset = m.selected - rows + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
	return m
}

// listRows is how many key rows fit, after the pane's own header, approle
// line, column header and footer lines. Kept in one place so View and
// clampSelection cannot disagree about the window they are scrolling.
func (m Model) listRows() int {
	rows := m.height - 8
	if rows < 1 {
		rows = 1
	}
	return rows
}
