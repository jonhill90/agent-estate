package agents

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/lane"
	"github.com/jonhill90/keelson/internal/theme"
)

// refreshInterval matches internal/rail's own 2s cadence for the exact
// same "sessions" MCP read (internal/rail/model.go's refreshInterval) --
// this pane reads no faster than the pane that already owns that budget.
const refreshInterval = 2 * time.Second

// Fetcher retrieves lane state grouped by every tmux session --
// internal/rail.SessionsFetcher's own signature, repeated here rather than
// imported: this package's seam is its own (AGENTS.md's adapter
// discipline), even though today's only real implementation
// (cmd/keelson's "sessions" MCP call) happens to be shared.
type Fetcher func() ([]lane.Session, error)

// TaskFetcher retrieves every ledger task row -- internal/rail.TaskFetcher's
// own signature and reason for existing (see that type's doc comment);
// nil is a valid, silent "no -ledger configured" the same way there.
type TaskFetcher func() ([]board.TaskRow, error)

type refreshMsg time.Time
type fetchResultMsg struct {
	sessions []lane.Session
	err      error
}
type taskFetchResultMsg struct {
	rows []board.TaskRow
	err  error
}

// Model is S6's Bubble Tea program: a flat, estate-wide list of agents
// (Row, one per tmux lane across every session) with model/cost always
// shown as unknown (Row's own doc comment says why) and "[n]" reserved for
// S7's not-yet-built thread creation -- SPEC-shell.md S6: "Read-only until
// S7." Not wired into internal/shell yet: SPEC-shell.md's own build order
// makes that a later item's job (S3 already exists and could route to
// this the same way it routes to board/cost/rail; wiring is left to
// whichever item does that, matching internal/stub (S5)'s own precedent
// of shipping a standalone, driveable pane before its route exists).
type Model struct {
	fetch     Fetcher
	taskFetch TaskFetcher

	sessions    []lane.Session
	tasks       []board.TaskRow
	fetchErr    error
	lastFetched time.Time

	// notice is [n]'s own placeholder: SPEC-shell.md S6 says threads are
	// "read-only until S7," so [n] must be visibly a documented no-op
	// (AGENTS.md's own "a visible stub beats a hidden screen" spirit, S5),
	// never a silently swallowed keypress.
	notice string

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model with fetch wired in; taskFetch is nil until
// WithTasks is called, the same "no -ledger, no task column" degradation
// internal/rail.WithTasks documents (Row.Task then reads "(no task)" for
// every row, never a fetch error).
func New(fetch Fetcher) Model {
	return Model{fetch: fetch, width: 100, height: 30, theme: theme.Default}
}

// WithTasks wires in a ledger read for the Task column -- mirrors
// internal/rail.Model.WithTasks exactly; see that method's own doc
// comment for why nil is a safe, silent default.
func (m Model) WithTasks(fetch TaskFetcher) Model {
	m.taskFetch = fetch
	return m
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this repo exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// Rows returns Derive's own output over m's current sessions/tasks --
// exported so a caller (a future shell wiring, or this package's own
// teatest) can assert on the derived list without depending on View's
// rendered string.
func (m Model) Rows() []Row { return Derive(m.sessions, m.tasks) }

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{refreshCmd(), doFetch(m.fetch)}
	if m.taskFetch != nil {
		cmds = append(cmds, doTaskFetch(m.taskFetch))
	}
	return tea.Batch(cmds...)
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func doFetch(fetch Fetcher) tea.Cmd {
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		sessions, err := fetch()
		return fetchResultMsg{sessions: sessions, err: err}
	}
}

func doTaskFetch(fetch TaskFetcher) tea.Cmd {
	return func() tea.Msg {
		rows, err := fetch()
		return taskFetchResultMsg{rows: rows, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case refreshMsg:
		cmds := []tea.Cmd{refreshCmd(), doFetch(m.fetch)}
		if m.taskFetch != nil {
			cmds = append(cmds, doTaskFetch(m.taskFetch))
		}
		return m, tea.Batch(cmds...)

	case fetchResultMsg:
		m.sessions = msg.sessions
		m.fetchErr = msg.err
		if msg.err == nil {
			m.lastFetched = time.Now()
		}
		return m, nil

	case taskFetchResultMsg:
		if msg.err == nil {
			m.tasks = msg.rows
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			return m, doFetch(m.fetch)
		case "n":
			// SPEC-shell.md S6: "Read-only until S7" -- a visible, named
			// no-op rather than a keypress that silently does nothing.
			m.notice = "threads not built yet (S7)"
			return m, nil
		case "t":
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		}
	}
	return m, nil
}
