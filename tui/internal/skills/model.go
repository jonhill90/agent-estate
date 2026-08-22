package skills

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/theme"
)

// refreshInterval: skills.md files change by hand, not by the second --
// unlike internal/rail/internal/agents' 2s "sessions" cadence, a local
// directory scan costs nothing to repeat, so this exists mainly so [r]
// (manual refresh) is not the ONLY way this pane ever notices a new skill
// added while it's open. 30s is arbitrary but cheap; nothing downstream
// depends on this exact number the way internal/rail's costRefreshInterval
// is pinned to ccusage's own measured latency.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time

type fetchResultMsg struct {
	skills []Skill
	err    error
}

// Model is S8's Bubble Tea program: a flat, name-sorted list of skills
// (Scan's own output, via Fetcher) with last-eval-result and
// invocation-count always shown as unknown (Skill's own doc comment says
// why) and "[e]" reserved for the eval loop the S8 design note says is the
// actual missing piece, not the skills themselves. Not wired into
// internal/shell yet -- matches internal/stub (S5) and internal/agents
// (S6)'s own precedent of shipping a standalone, driveable pane before its
// route exists (SPEC-shell.md's own build order leaves that wiring to a
// later item).
type Model struct {
	fetch Fetcher

	skills   []Skill
	fetchErr error

	// notice is [e]'s own placeholder -- the design note says the eval
	// loop is what's missing, so [e] must be a visibly documented no-op
	// (S5's "a visible stub beats a hidden screen" spirit), never a
	// silently swallowed keypress.
	notice string

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
// every other package in this repo exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// Skills returns m's current skill list, sorted the same way Scan already
// sorts it (by Dir) -- exported so a caller (a future shell wiring, or
// this package's own teatest) can assert on it without depending on
// View's rendered string.
func (m Model) Skills() []Skill { return m.skills }

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
		skills, err := fetch()
		return fetchResultMsg{skills: skills, err: err}
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
			m.skills = msg.skills
			if m.selected >= len(m.skills) {
				m.selected = len(m.skills) - 1
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
			if m.selected < len(m.skills)-1 {
				m.selected++
			}
			return m, nil
		case "e":
			// S8's own design note: "the missing piece is the eval loop,
			// not the skills themselves" -- a visible, named no-op rather
			// than a keypress that silently does nothing.
			m.notice = "eval loop not built yet"
			return m, nil
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}
