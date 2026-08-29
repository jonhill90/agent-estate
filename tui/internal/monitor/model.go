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

	// fetchInFlight guards m.fetch the same way internal/rail.Model's and
	// internal/agents.Model's own fetchInFlight fields do, for the exact
	// defect agent-tui#177 measured: this pane's refreshMsg fired every
	// refreshInterval (2s) regardless of whether the previous fetch had
	// answered, so under load it kept enqueueing "sessions" calls behind
	// rail/agents/chat's own against the same single-threaded
	// mcp_server.py, widening the queue #177's own instrumentation
	// measured rather than draining it (internal/agents/model.go's
	// fetchInFlight doc comment has the fuller measurement). Set true the
	// moment a fetch is issued (New/Init and the refreshMsg/"r" cases
	// below), cleared only in fetchResultMsg -- see that case's own
	// comment for why clearing it first, unconditionally, matters.
	fetchInFlight bool

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in; nil is a valid, silent "no
// monitoring source configured" default, the same convention every other
// optional Fetcher in this module follows. fetchInFlight starts true
// whenever fetch != nil, matching internal/rail.Model.New's and
// internal/agents.New's own seed: Init() below always issues the first
// fetch unconditionally, so the guard must already reflect that before the
// first refreshMsg (refreshInterval later) can check it.
func New(fetch Fetcher) Model {
	return Model{fetch: fetch, fetchInFlight: fetch != nil, width: 100, height: 30, theme: theme.Default}
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
		// agent-tui#177: m.fetch only re-fires when the previous one has
		// already answered -- see fetchInFlight's own doc comment for why
		// an unconditional re-fire here is exactly the request pile-up
		// that issue measured.
		cmds := []tea.Cmd{refreshCmd()}
		if m.fetch != nil && !m.fetchInFlight {
			m.fetchInFlight = true
			cmds = append(cmds, doFetch(m.fetch))
		}
		return m, tea.Batch(cmds...)

	case fetchResultMsg:
		// agent-tui#177: clear the in-flight guard BEFORE anything else
		// in this case, unconditionally (success or failure) -- same
		// discipline as internal/agents' own fetchResultMsg case.
		// Leaving it set on any path (including a timeout) wedges every
		// future refreshMsg/"r" into believing one is still outstanding
		// forever, silently freezing this pane on stale data.
		m.fetchInFlight = false
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
			// agent-tui#177: same fetchInFlight guard refreshMsg uses -- a
			// manual "r" while a fetch (periodic or a previous "r") is
			// already outstanding must not add a second request behind
			// it; the one already in flight will land and repaint
			// regardless.
			if m.fetch == nil || m.fetchInFlight {
				return m, nil
			}
			m.fetchInFlight = true
			return m, doFetch(m.fetch)
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}
