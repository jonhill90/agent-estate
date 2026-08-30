package dashboard

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// Fetcher retrieves the current Stats -- the one adapter seam this package
// exposes (AGENTS.md's discipline). cmd/estate composes the real
// implementation out of already-existing sources (agents' sessions fetch,
// board's gh runner, cost's ccusage fetch, knowledge's vault index); every
// test in this package builds a fake instead. The returned error is for a
// fetch that could not even attempt its sub-reads (e.g. a nil dependency
// panicking before Stats could be built) -- a normal partial failure (gh
// rate-limited, no vault configured) is not that error, it is one Stats
// field left Known false, exactly like internal/cost.Snapshot's own
// per-harness failure handling.
type Fetcher func() (Stats, error)

// refreshInterval matches internal/cost's own 5-minute cadence, not
// internal/agents'/internal/rail's 2s one -- two of this package's five
// figures are `gh` reads (OpenPRs, MergedToday), and gh's own rate limit is
// exactly why internal/cost already treats its ccusage-backed figures as a
// polled status readout rather than a live meter (that package's own
// refreshInterval doc comment). This package inherits the more
// conservative of the two cadences already established in this module
// rather than picking a third number.
const refreshInterval = 5 * time.Minute

type refreshMsg time.Time
type fetchResultMsg struct {
	stats Stats
	err   error
}

// Model is the dashboard pane: one fetch's worth of Stats, rendered as a
// handful of stat lines, each independently "unknown" when its own source
// has nothing to report -- see Stats' own doc comment for why there is no
// single all-or-nothing failure mode here.
type Model struct {
	fetch Fetcher

	stats       Stats
	fetchErr    error
	lastFetched time.Time
	fetchedOnce bool

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in; nil is a valid, silent "no
// dashboard source configured" default, same as every other optional
// Fetcher in this module.
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

// Stats returns m's current figures -- exported so a caller (a future
// shell wiring, or this package's own teatest) can assert on the fetched
// data without depending on View's rendered string.
func (m Model) Stats() Stats { return m.stats }

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
		stats, err := fetch()
		return fetchResultMsg{stats: stats, err: err}
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
			m.stats = msg.stats
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
