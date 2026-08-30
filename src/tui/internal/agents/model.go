package agents

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/board"
	"github.com/jonhill90/agent-estate/src/tui/internal/cost"
	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// refreshInterval matches internal/rail's own 2s cadence for the exact
// same "sessions" MCP read (internal/rail/model.go's refreshInterval) --
// this pane reads no faster than the pane that already owns that budget.
// It drives the sessions fetch and the ledger/task fetch only -- NOT the
// cost fetch, which has its own, much slower cadence below
// (costRefreshInterval). Before agent-tui#139 this one interval also fired
// m.costFetch, which is buildAgentCostFetch's (cmd/estate/agents.go) join
// of a sqlite read AND a full `ccusage session --json` -- neither cheap,
// and with no guard against a slow run still being in flight when the next
// tick fired, re-firing every 2s let the process count climb (measured:
// respawning to 7 concurrent, one at 551% CPU). See costRefreshInterval's
// doc comment for the fix, modelled on internal/rail's own split.
const refreshInterval = 2 * time.Second

// costRefreshInterval matches internal/rail's own costRefreshInterval
// (internal/rail/model.go) -- the cost fetch here is the SAME kind of read
// rail's is (a real subprocess call, not a cheap local one), so it gets the
// same slow, separate cadence rail already pins with
// TestCostRefreshIntervalIsFiveMinutes. See TestAgentsCostRefreshIntervalIsFiveMinutes
// in this package for the equivalent pin here, and costFetchInFlight for
// the single-flight guard that keeps a slow run from being joined by a
// second one when this ticker fires again -- the part an interval alone
// does not fix (agent-tui#139).
const costRefreshInterval = 5 * time.Minute

// Fetcher retrieves lane state grouped by every tmux session --
// internal/rail.SessionsFetcher's own signature, repeated here rather than
// imported: this package's seam is its own (AGENTS.md's adapter
// discipline), even though today's only real implementation
// (cmd/estate's "sessions" MCP call) happens to be shared.
type Fetcher func() ([]lane.Session, error)

// TaskFetcher retrieves every ledger task row -- internal/rail.TaskFetcher's
// own signature and reason for existing (see that type's doc comment);
// nil is a valid, silent "no -ledger configured" the same way there.
type TaskFetcher func() ([]board.TaskRow, error)

// CostFetcher retrieves per-lane cost, pre-joined by the caller (see
// cmd/estate/agents.go's buildAgentCostFetch) from the ledger's own
// lanes.harness_session_id column and `ccusage session --json`'s
// per-session totals -- Row.go's own package doc comment explains the
// join. Keyed by the ledger's "<session>:<window-index>" lane string, the
// same key board.TaskRow.Lane already uses -- NOT by Row.ID. nil is a
// valid, silent "no -ledger configured" the same way TaskFetcher's is;
// Derive already treats a missing map entry as "unknown" for a lane it did
// find, so a nil map from a nil fetch degrades identically.
type CostFetcher func() (map[string]cost.Figure, error)

type refreshMsg time.Time
type costRefreshMsg time.Time
type fetchResultMsg struct {
	sessions []lane.Session
	err      error
}
type taskFetchResultMsg struct {
	rows []board.TaskRow
	err  error
}
type costFetchResultMsg struct {
	costs map[string]cost.Figure
	err   error
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
	costFetch CostFetcher

	sessions    []lane.Session
	tasks       []board.TaskRow
	costs       map[string]cost.Figure
	fetchErr    error
	lastFetched time.Time

	// fetchInFlight guards m.fetch the same way internal/rail.Model's own
	// fetchInFlight guards its "sessions"/"lanes" fetch, and for the
	// identical reason (agent-tui#175, the same defect shape as
	// agent-tui#55, never ported here when this package picked up its own
	// "sessions" fetch): refreshMsg fires every refreshInterval (2s)
	// regardless of whether the PREVIOUS fetch has answered, and
	// mcp_server.py answers exactly one tools/call at a time (a plain "for
	// line in stdin" loop, no concurrency of its own -- see that file's own
	// doc comment). Before this guard, this pane's own 2s ticker kept
	// issuing a new "sessions" call whether or not the last one had
	// returned -- including while a DIFFERENT pane (e.g. internal/rail,
	// visible or not, per shell.Model.routeAll's "every pane stays live"
	// policy) was doing the exact same thing against the exact same
	// single-threaded server over the exact same mcp.Client. That is a
	// self-inflicted request pile-up, not a slow server: measured directly
	// against a live mcp_server.py, five consecutive "sessions" calls on
	// one long-lived process answered in ~1s each, so a queue fed faster
	// than it drains is the only way a "no reply within 10s" surfaces here.
	// Set true the moment a fetch is issued (New/Init and the refreshMsg/"r"
	// cases below all do), cleared only when fetchResultMsg handles that
	// fetch's result (success or failure) -- see fetchResultMsg's own
	// comment for why clearing it unconditionally, first, matters.
	fetchInFlight bool

	// costFetchInFlight single-flights m.costFetch (agent-tui#139): set the
	// moment a cost fetch is issued (Init and the costRefreshMsg case both
	// do), cleared only when its result (success or failure) is handled in
	// costFetchResultMsg. costRefreshCmd fires every costRefreshInterval
	// regardless of whether the previous call has answered yet -- without
	// this guard a slow ccusage run gets joined by a second one issued on
	// the next tick, which is exactly how the process count climbed rather
	// than staying at one (internal/rail.Model.fetchInFlight's own doc
	// comment records the identical shape of bug for the sessions/lanes
	// fetch there).
	costFetchInFlight bool

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
	return Model{fetch: fetch, fetchInFlight: fetch != nil, width: 100, height: 30, theme: theme.Default}
}

// WithTasks wires in a ledger read for the Task column -- mirrors
// internal/rail.Model.WithTasks exactly; see that method's own doc
// comment for why nil is a safe, silent default.
func (m Model) WithTasks(fetch TaskFetcher) Model {
	m.taskFetch = fetch
	return m
}

// WithCosts wires in the ledger+ccusage join for the Cost column -- see
// CostFetcher's own doc comment; nil is a safe, silent default, same as
// WithTasks(nil). costFetchInFlight starts true whenever fetch != nil,
// matching internal/rail.Model.New's own fetchInFlight seed (see that
// field's doc comment): Init() below always issues the first cost fetch
// unconditionally, so the guard must already reflect that before the first
// costRefreshMsg (costRefreshInterval later) can check it.
func (m Model) WithCosts(fetch CostFetcher) Model {
	m.costFetch = fetch
	m.costFetchInFlight = fetch != nil
	return m
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this repo exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// Rows returns Derive's own output over m's current sessions/tasks/costs --
// exported so a caller (a future shell wiring, or this package's own
// teatest) can assert on the derived list without depending on View's
// rendered string.
func (m Model) Rows() []Row { return Derive(m.sessions, m.tasks, m.costs) }

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{refreshCmd(), doFetch(m.fetch)}
	if m.taskFetch != nil {
		cmds = append(cmds, doTaskFetch(m.taskFetch))
	}
	if m.costFetch != nil {
		cmds = append(cmds, costRefreshCmd(), doCostFetch(m.costFetch))
	}
	return tea.Batch(cmds...)
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func costRefreshCmd() tea.Cmd {
	return tea.Tick(costRefreshInterval, func(t time.Time) tea.Msg { return costRefreshMsg(t) })
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

func doCostFetch(fetch CostFetcher) tea.Cmd {
	return func() tea.Msg {
		costs, err := fetch()
		return costFetchResultMsg{costs: costs, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case refreshMsg:
		// agent-tui#139: costFetch no longer rides this ticker -- see
		// costRefreshMsg below, and refreshInterval's doc comment for why.
		// agent-tui#175: m.fetch only re-fires when the previous one has
		// already answered -- see fetchInFlight's own doc comment for why
		// an unconditional re-fire here is exactly the request pile-up that
		// issue was.
		cmds := []tea.Cmd{refreshCmd()}
		if m.fetch != nil && !m.fetchInFlight {
			m.fetchInFlight = true
			cmds = append(cmds, doFetch(m.fetch))
		}
		if m.taskFetch != nil {
			cmds = append(cmds, doTaskFetch(m.taskFetch))
		}
		return m, tea.Batch(cmds...)

	case costRefreshMsg:
		// Single-flight (agent-tui#139): if the previous cost fetch has not
		// answered yet, re-arm the ticker but do NOT issue a second
		// doCostFetch -- see costFetchInFlight's doc comment for exactly
		// the pile-up this prevents.
		if m.costFetch == nil || m.costFetchInFlight {
			return m, costRefreshCmd()
		}
		m.costFetchInFlight = true
		return m, tea.Batch(costRefreshCmd(), doCostFetch(m.costFetch))

	case fetchResultMsg:
		// agent-tui#175: clear the in-flight guard BEFORE anything else in
		// this case, unconditionally (success or failure) -- same discipline
		// as costFetchResultMsg below and internal/rail's own
		// fetchResultMsg/sessionsFetchResultMsg (fetchInFlight's own doc
		// comment). Leaving it set on any path wedges every future
		// refreshMsg/"r" into believing one is still outstanding forever.
		m.fetchInFlight = false
		m.fetchErr = msg.err
		// agent-tui#175's second, independent defect: a failed refresh used
		// to overwrite m.sessions with msg.sessions's zero value even on
		// error, collapsing a populated view to "(no agents)" -- which reads
		// exactly like a genuinely empty estate. Only replace m.sessions on
		// success now; on error the prior (possibly stale) rows stay on
		// screen, same discipline internal/rail.Model's fetchResultMsg and
		// sessionsFetchResultMsg already apply for the identical "sessions"
		// read. m.fetchErr above still renders the "! sessions unavailable"
		// banner, and View()'s "age:" line (fed by lastFetched, unchanged
		// below) grows on every failed cycle -- together they mark the rows
		// as stale rather than pretending they are current.
		if msg.err == nil {
			m.sessions = msg.sessions
			m.lastFetched = time.Now()
		}
		return m, nil

	case taskFetchResultMsg:
		if msg.err == nil {
			m.tasks = msg.rows
		}
		return m, nil

	case costFetchResultMsg:
		// agent-tui#139: clear the in-flight guard BEFORE anything else in
		// this case, unconditionally (success or failure) -- same discipline
		// as internal/rail's fetchResultMsg case (see fetchInFlight's doc
		// comment there). This is the one place a fetch issued via
		// m.costFetch ever completes; leaving the flag set on any path
		// would wedge every future costRefreshMsg into believing one is
		// still outstanding forever.
		m.costFetchInFlight = false
		if msg.err == nil {
			m.costs = msg.costs
		}
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "r":
			// agent-tui#175: same fetchInFlight guard refreshMsg uses -- a
			// manual "r" while a fetch (periodic or a previous "r") is
			// already outstanding must not add a second request behind it;
			// the one already in flight will land and repaint regardless.
			if m.fetch == nil || m.fetchInFlight {
				return m, nil
			}
			m.fetchInFlight = true
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
