package apidocs

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/theme"
)

// refreshInterval matches internal/knowledge/internal/skills' slow poll,
// not the board's 5s one: an OpenAPI document changes when someone edits
// the API service and commits, not by the second, so a slow reload plus
// [r] for "I just regenerated it" is the right cadence -- the same
// reasoning those two packages' own refreshInterval comments give.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time
type fetchResultMsg struct {
	ref Reference
	err error
}

// Model is Docs -> API Docs' pane: the estate's own OpenAPI document as an
// operation table, filterable by path. Read-only -- this package has no
// write path and never calls the API it documents.
type Model struct {
	fetch Fetcher

	ref      Reference
	fetchErr error
	// unconfigured is true when New was given a nil Fetcher -- no spec
	// path was resolvable at all. Distinct from "read the spec and it has
	// no endpoints", the same distinction internal/workflows.Model draws
	// for a missing ledger, and the reason the view can name what to set
	// rather than printing an empty table.
	unconfigured bool

	fetchedOnce bool
	lastFetched time.Time

	// filter is a live path substring filter. An API this size (104 paths
	// in the estate's own spec) does not fit a pane, so scrolling alone
	// would make it a document nobody reads to the end of.
	filter    string
	filtering bool

	selected int
	offset   int

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in. A nil fetch is the "no spec
// configured" state, not a crash -- cmd/keelson passes nil when neither
// -openapi nor $HILL90_APP_REPO resolves to a file.
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

// Reference returns what was last loaded, exported so a caller (or this
// package's own teatest) can assert on the parsed document without
// depending on View's rendered string.
func (m Model) Reference() Reference { return m.ref }

// Visible is the endpoint list after the current filter -- View's own
// source, exported for the same reason Reference is.
func (m Model) Visible() []Endpoint {
	if m.filter == "" {
		return m.ref.Endpoints
	}
	needle := strings.ToLower(m.filter)
	var out []Endpoint
	for _, e := range m.ref.Endpoints {
		if strings.Contains(strings.ToLower(e.Path), needle) ||
			strings.Contains(strings.ToLower(e.Summary), needle) ||
			strings.Contains(strings.ToLower(e.Method), needle) {
			out = append(out, e)
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
		ref, err := fetch()
		return fetchResultMsg{ref: ref, err: err}
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
			m.ref = msg.ref
			m.lastFetched = time.Now()
			m.fetchedOnce = true
		}
		return m.clampSelection(), nil

	case tea.KeyMsg:
		if m.filtering {
			return m.filterKey(msg)
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			return m, doFetch(m.fetch)
		case "/":
			m.filtering = true
			return m, nil
		case "esc":
			m.filter = ""
			return m.clampSelection(), nil
		case "up", "k":
			if m.selected > 0 {
				m.selected--
			}
			return m.clampSelection(), nil
		case "down", "j":
			if m.selected < len(m.Visible())-1 {
				m.selected++
			}
			return m.clampSelection(), nil
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}

// filterKey is the "/" mode: printable runes extend the filter, backspace
// shortens it, enter/esc leave the mode (esc also clears it). Quit keys
// are deliberately NOT swallowed here -- agent-tui#22's trap was a mode
// that ate every key including ctrl+c.
func (m Model) filterKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "enter":
		m.filtering = false
		return m.clampSelection(), nil
	case "esc":
		m.filtering = false
		m.filter = ""
		return m.clampSelection(), nil
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
		}
		return m.clampSelection(), nil
	}
	if msg.Type == tea.KeyRunes {
		m.filter += string(msg.Runes)
	}
	return m.clampSelection(), nil
}

// clampSelection keeps the cursor and the scroll offset inside the current
// (possibly just filtered) list.
func (m Model) clampSelection() Model {
	visible := m.Visible()
	if m.selected >= len(visible) {
		m.selected = len(visible) - 1
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

// listRows is how many endpoint lines fit, after the pane's own header,
// column header and footer lines. Kept in one place so View and
// clampSelection cannot disagree about the window they are scrolling.
func (m Model) listRows() int {
	rows := m.height - 8
	if rows < 1 {
		rows = 1
	}
	return rows
}
