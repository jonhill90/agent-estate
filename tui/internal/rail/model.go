// Package rail is the left-anchored navigation rail: a Bubble Tea model that
// renders lane state as moving glyphs in a narrow column. It is a LAYOUT
// REGION, not a screen -- agent-supervisor#107 is explicit that the rail is
// meant to sit in a narrow tmux pane (~24-32 columns) beside a terminal
// pane, not fill the window as a full-screen list. This package does not
// create that split; it only renders correctly inside whatever width it is
// given, narrow or wide. Splitting panes is a tmux operation a human or the
// Director performs, per the "own window, never inject panes into live
// ones" constraint -- this program never calls tmux itself.
package rail

import (
	"errors"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/cost"
	"github.com/jonhill90/keelson/internal/lane"
	"github.com/jonhill90/keelson/internal/session"
	"github.com/jonhill90/keelson/internal/theme"
)

// RailWidth is the target column count for the rail region. Jon asked for
// "roughly 24-32 columns" (issue #107); 28 sits in the middle of that band.
const RailWidth = 28

const tickInterval = 120 * time.Millisecond
const refreshInterval = 2 * time.Second

// costRefreshInterval matches internal/cost's own refreshInterval exactly
// (see model.go's doc comment there for the measured reason: a single
// `ccusage daily --json --by-agent` call takes low-single-digit seconds).
// Wiring the cost line into the rail (agent-tui#4 -- "glanceable, always
// there, no command to run") must not make it poll any harder just because
// it is now always visible; this constant, and TestCostRefreshIntervalIsFiveMinutes
// pinning it, are what keep that true.
const costRefreshInterval = 5 * time.Minute

// Fetcher retrieves the current lane list. cmd/agent-tui supplies the real
// implementation (an MCP tools/call("lanes")); this package knows nothing
// about MCP, tmux, or lanes.sh -- only that it can ask for []lane.Lane and
// might get an error back, which it must show, never swallow into a blank
// rail (issue #107 hard-acceptance item 3, applied to the fetch path too:
// an instrument that cannot see must not look like a healthy empty estate).
type Fetcher func() ([]lane.Lane, error)

// SessionsFetcher retrieves lane state grouped by every tmux session --
// agent-tui#13, wrapping the supervisor's "sessions" MCP tool (itself
// sessions.sh wrapping lanes.sh once per session; see that script's header
// for why lanes.sh alone cannot answer this). Additive to Fetcher above,
// which stays exactly as it is: board.go's single-session read and every
// pre-#13 rail test still build a Model with New/NewWithCost and never see
// this type. NewMultiSession is the only constructor that sets it.
type SessionsFetcher func() ([]lane.Session, error)

type tickMsg time.Time
type refreshMsg time.Time
type costRefreshMsg time.Time

type fetchResultMsg struct {
	lanes []lane.Lane
	err   error
}

type sessionsFetchResultMsg struct {
	sessions []lane.Session
	err      error
}

type costFetchResultMsg struct {
	snap cost.Snapshot
	err  error
}

// Model is the rail's Bubble Tea model.
type Model struct {
	fetch Fetcher

	// costFetch is optional -- nil in tests and in any embedding that has
	// no ccusage to read -- so New() keeps working exactly as before it
	// existed. cmd/agent-tui always supplies one via NewWithCost so the
	// rail's default screen carries agent-tui#4's cost line with no flag
	// needed to see it.
	costFetch cost.Fetcher

	width, height int
	tick          int

	lanes    []lane.Lane
	selected int

	// glyphSet is the live picker agent-supervisor#107's addendum asks for:
	// "a key steps through numbered options; he presses a number; done in
	// two seconds." It indexes lane.Variants directly -- number key N
	// selects Variants[N-1] against the SAME real lane data already on
	// screen, never a mock (addendum rule 1). Starts at 0, lane.Default's
	// index, so silence still yields something sane (addendum rule 3).
	glyphSet int

	fetchErr    error
	lastFetched time.Time
	quitting    bool
	// fetchInFlight guards m.fetch/m.lanesFetch (both funnel through
	// doFetch/fetchResultMsg -- see lanesFetch's own doc comment) against
	// agent-tui#55: refreshMsg fires every refreshInterval (2s) regardless
	// of whether the PREVIOUS fetch has answered yet, and a real "sessions"/
	// "lanes" MCP round-trip measured against the live supervisor takes
	// 4-5s (sessions.sh fans out to lanes.sh once per tmux session) --
	// slower than the poll interval that re-fires it. mcp_server.py (see
	// its own package doc comment: "for line in stdin") answers ONE
	// tools/call at a time, so issuing a new request before the last one
	// answered does not run them in parallel -- it queues behind the one
	// already in flight. Every 2s adds another to that queue faster than
	// the queue drains, so the backlog only grows, and every request
	// eventually crosses mcp.Client's 10s timeout -- not because the
	// supervisor or the cost pane starved this client, but because this
	// package kept asking before the last answer arrived. Set true the
	// moment a fetch is issued (Init and doFetchAll both do), cleared the
	// moment its result (success or failure) is handled.
	fetchInFlight bool

	costSnap    cost.Snapshot
	costFetched time.Time

	// -- agent-tui#13: multi-session grouping. All nil/zero for a Model
	// built with New/NewWithCost, so nothing below changes their behavior;
	// see NewMultiSession.
	sessionsFetch   SessionsFetcher
	sessions        []lane.Session
	sessionsErr     error
	sessionsFetched time.Time
	// sessionsFetchInFlight is fetchInFlight's twin for m.sessionsFetch --
	// see fetchInFlight's doc comment for why this exists at all
	// (agent-tui#55).
	sessionsFetchInFlight bool
	// lanesFetch is agent-tui#18's fix for at#13's own blocking finding: a
	// Model built with NewMultiSession that has no fallback would render
	// nothing at all -- "! unavailable" with no data -- for as long as the
	// supervisor side (agent-supervisor#158, the "sessions" tool) lags
	// behind this program, which is the normal state until #158 merges and
	// stays true for anyone running an older supervisor checkout after
	// that. When set, a failed sessions fetch falls back to this single-
	// session Fetcher (the same "lanes" tool board.go already reads) and
	// View() renders a visible note above it -- never a silent narrowing
	// from "every session" to "one", and never a blank rail. Reuses
	// fetch/fetchResultMsg/m.lanes/m.fetchErr below rather than a parallel
	// set of fields, so the fallback render is exactly board.go's own flat
	// single-session view, not a second implementation of it.
	lanesFetch Fetcher
	// directorSession is the tmux session name styled distinctly (Jon:
	// "something to make it special") -- it is DATA passed in by
	// cmd/agent-tui, not a literal compared here, so a rename or a second
	// long-lived agent session is a flag change, not a code change.
	directorSession string
	// groupStyle indexes groupStyles (see sessions_view.go) -- the picker
	// rule agent-tui#13 asks for applied to grouping the same way glyphSet
	// applies it to glyphs: every candidate is real, numbered, and swapped
	// live against the same on-screen data, never decided silently.
	groupStyle int

	// -- agent-tui#14: session management (attach/detach/add/remove). ops
	// is nil on every Model built without WithOps (every pre-#14 test,
	// board.go's single-session path) -- Update below guards every one of
	// the a/d/n/x keys on ops != nil, so a Model with no write path ignores
	// them exactly as it ignored an unmapped key before #14, rather than
	// panicking on a nil interface call. See ops.go for the write flow.
	ops       session.Interface
	opsMode   opsMode
	opsInput  string              // session name being typed while opsMode == opsModeAdding
	opsStatus string              // last completed op's one-line result, shown in the footer until the next op starts
	opsCheck  session.RemoveCheck // the last session_remove_check result, live while opsMode == opsModeConfirmRemove
	opsTarget string              // the session name an in-flight add/remove/removeCheck names

	// -- agent-tui#26: ledger-derived rail content. taskFetch is nil in
	// every test and in any run started without -ledger, exactly the same
	// "additive, never assumed" discipline costFetch/ops/sessionsFetch above
	// already follow -- see work.go's package doc comment for why this
	// reuses board.ReadTaskRows rather than a second ledger reader.
	taskFetch    TaskFetcher
	tasks        []board.TaskRow
	tasksErr     error
	tasksFetched time.Time

	// reading indexes readings (readings.go) -- the content-variants picker
	// agent-tui#26 asks for, cycled with 'w' the same live-against-real-data
	// way glyphSet/groupStyle already are. Starts at 0, readings[0]'s index,
	// so silence still yields something sane.
	reading int

	// theme is agent-tui#27's seam: every colour, border and padding value
	// View() draws comes from here, never a literal at the call site.
	// Defaults to theme.Default (see New/NewMultiSession) so every rail
	// test built without WithTheme renders exactly as it did before this
	// field existed.
	theme theme.Theme
	// themeNotice is #27 acceptance item 3's "says so visibly" half --
	// set only when cmd/agent-tui's theme.Load resolved a malformed
	// config or an unknown theme name, never for a plain missing config.
	themeNotice string
}

// WithTheme returns a copy of m with th (and, when non-empty, a visible
// notice about how th was resolved) wired in -- the one call cmd/agent-tui
// makes once theme.Load has run, same shape as WithOps above.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// WithOps returns a copy of m with ops wired in -- the one call cmd/agent-tui
// makes to turn on agent-tui#14's write path (attach/detach/add/remove).
// Every rail test that does not call this gets a Model with ops == nil,
// exactly as it did before #14 existed; see the ops field's own doc comment
// on Model for why that is safe rather than a nil-panic waiting to happen.
func (m Model) WithOps(ops session.Interface) Model {
	m.ops = ops
	return m
}

// New builds a Model bound to the given fetch function, with no cost line
// (costFetch is nil). Use NewWithCost to get agent-tui#4's rail line.
func New(fetch Fetcher) Model {
	// fetchInFlight starts true whenever fetch != nil: Init() below always
	// issues that first fetch unconditionally, so the in-flight guard must
	// already reflect that before the first refreshMsg (refreshInterval
	// later) can check it -- see fetchInFlight's doc comment.
	return Model{fetch: fetch, fetchInFlight: fetch != nil, width: RailWidth, height: 24, theme: theme.Default}
}

// NewWithCost builds a Model that also renders a compact, per-harness cost
// line (agent-tui#4) below the lane list -- the default-visible spot issue
// #4 asked for, no flag required. costFetch follows the same "unknown,
// never zero" discipline as internal/cost.Fetcher; pass nil to fall back to
// New's behavior (no cost line at all) when there is genuinely nothing to
// read it from.
func NewWithCost(fetch Fetcher, costFetch cost.Fetcher) Model {
	m := New(fetch)
	m.costFetch = costFetch
	return m
}

// NewMultiSession builds a Model that renders lanes grouped by tmux session
// (agent-tui#13) instead of one session's flat list -- cmd/agent-tui's
// default screen, replacing what New/NewWithCost rendered before #13.
// fetch is left nil deliberately: this Model reads sessionsFetch (and,
// on failure, lanesFetch) only, and Init/Update below never call a nil
// fetch because of that, not because of a special case for this
// constructor.
//
// lanesFetch is at#18's degrade-gracefully fallback: pass the same "lanes"
// Fetcher board.go uses, and a sessions fetch that fails (agent-tui#18 --
// most commonly agent-supervisor#158's "sessions" tool not existing on an
// older or not-yet-updated supervisor checkout) falls back to rendering
// that one session instead of an empty/error-only rail, with a visible
// note that the multi-session view needs the supervisor half. Pass nil to
// get at#13's original behavior (an unavailable sessions fetch renders
// only "! unavailable", no fallback) -- every pre-#18 rail test still
// builds Models this way.
//
// directorSession names the one session styled distinctly (Jon: "something
// to make it special") -- pass "" to disable that styling entirely rather
// than have it silently match nothing.
func NewMultiSession(sessionsFetch SessionsFetcher, lanesFetch Fetcher, costFetch cost.Fetcher, directorSession string) Model {
	return Model{
		fetch:         nil,
		sessionsFetch: sessionsFetch,
		// sessionsFetchInFlight mirrors New's fetchInFlight seed: Init()
		// always issues the first sessions fetch when sessionsFetch != nil,
		// so the guard must start true to match. fetchInFlight itself stays
		// false here -- m.fetch is nil on this constructor, and the
		// lanesFetch fallback it also guards (see lanesFetch's doc comment)
		// only starts once the first sessions fetch actually fails.
		sessionsFetchInFlight: sessionsFetch != nil,
		lanesFetch:            lanesFetch,
		costFetch:             costFetch,
		directorSession:       directorSession,
		width:                 RailWidth,
		height:                24,
		theme:                 theme.Default,
	}
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd(), refreshCmd()}
	// Exactly one of fetch/sessionsFetch is set on any Model this package
	// hands out (New/NewWithCost set fetch only; NewMultiSession sets
	// sessionsFetch only) -- both are guarded here anyway rather than
	// assumed, so a Model built by hand (every existing rail test) with
	// neither set still starts cleanly instead of calling a nil func.
	if m.fetch != nil {
		cmds = append(cmds, doFetch(m.fetch))
	}
	if m.sessionsFetch != nil {
		cmds = append(cmds, doSessionsFetch(m.sessionsFetch))
	}
	if m.costFetch != nil {
		cmds = append(cmds, costRefreshCmd(), doCostFetch(m.costFetch))
	}
	if m.taskFetch != nil {
		cmds = append(cmds, doTaskFetch(m.taskFetch))
	}
	return tea.Batch(cmds...)
}

func tickCmd() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func refreshCmd() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg { return refreshMsg(t) })
}

func costRefreshCmd() tea.Cmd {
	return tea.Tick(costRefreshInterval, func(t time.Time) tea.Msg { return costRefreshMsg(t) })
}

func doFetch(fetch Fetcher) tea.Cmd {
	return func() tea.Msg {
		lanes, err := fetch()
		return fetchResultMsg{lanes: lanes, err: err}
	}
}

func doCostFetch(fetch cost.Fetcher) tea.Cmd {
	return func() tea.Msg {
		snap, err := fetch()
		return costFetchResultMsg{snap: snap, err: err}
	}
}

func doSessionsFetch(fetch SessionsFetcher) tea.Cmd {
	return func() tea.Msg {
		sessions, err := fetch()
		return sessionsFetchResultMsg{sessions: sessions, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		// agent-tui#14: every write-path key (a/d/n/x, plus whatever a live
		// opsMode is currently intercepting -- typing a new session's name,
		// or confirming/cancelling a remove) is handled in ops.go, and takes
		// priority over the read-only keys below so that, e.g., typing a
		// session name containing 'r' or 'g' cannot be misread as a refresh
		// or a group-style cycle. handleOpsKey returns handled == false for
		// every key it has no opinion on (including every key when
		// m.ops == nil), so a Model with no write path falls straight
		// through to the exact same switch it used before #14 existed.
		if next, cmd, handled := m.handleOpsKey(msg); handled {
			return next, cmd
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		case "up", "k":
			// Selection spans every session's lanes as one ordered list
			// (agent-tui#13 requirement 4: "up/down should move across the
			// whole tree") -- rowCount is that list's length regardless of
			// which grouping variant is currently drawing headers between
			// sessions; see sessionsFlat in sessions.go.
			if m.selected > 0 {
				m.selected--
			}
			return m, nil
		case "down", "j":
			if m.selected < m.rowCount()-1 {
				m.selected++
			}
			return m, nil
		case "r":
			return m.doFetchAll()
		case "g":
			// Cycles the grouping-style picker (flat-with-headers /
			// indented-tree) the same live-against-real-data way glyphSet
			// does for glyphs -- agent-tui#13 requirement 5, applied only
			// when there is something to group: a Model built with
			// New/NewWithCost has no sessions and ignores this key, exactly
			// as it ignored an unmapped key before #13.
			if m.sessionsFetch != nil && len(groupStyles) > 0 {
				m.groupStyle = (m.groupStyle + 1) % len(groupStyles)
			}
			return m, nil
		case "t":
			// agent-tui#25 scope item 3: switchable at runtime, not just at
			// startup -- "he has consistently wanted to compare rather than
			// commit." Agent-tui#51: this used to cycle m.theme in place,
			// which is exactly why a keypress here never touched board/
			// cost/gallery's own copies -- see theme.CycleRequestedMsg's
			// doc comment. shell.Model is the single owner now; this only
			// asks for a cycle, reached only when handleOpsKey above has no
			// opinion (i.e. not mid-typed session name), so a literal 't' in
			// a session name is still delivered as text, unaffected by this
			// change.
			return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
		case "w":
			// agent-tui#26: cycles the content-reading picker (work-centric
			// / status-centric) the same live-against-real-data way 'g'
			// does for grouping -- always available, even with no taskFetch
			// wired, since both readings render sanely with "(no task)"/
			// "health: ok" when there is no ledger row behind a lane.
			if len(readings) > 0 {
				m.reading = (m.reading + 1) % len(readings)
			}
			return m, nil
		}
		// Number keys select a glyph set directly, live, against whatever
		// is already on screen -- the whole picker is this one branch plus
		// the legend line in View(). "1" through "9" cover up to nine
		// variants without needing a mode to enter or leave first.
		if n, ok := digitKey(msg.String()); ok && n >= 1 && n <= len(lane.Variants) {
			m.glyphSet = n - 1
		}
		return m, nil

	case tickMsg:
		m.tick++
		return m, tickCmd()

	case refreshMsg:
		next, cmd := m.doFetchAll()
		return next, tea.Batch(refreshCmd(), cmd)

	case fetchResultMsg:
		// agent-tui#55: clear the in-flight guard BEFORE anything else in
		// this case, unconditionally (success or failure) -- see
		// fetchInFlight's doc comment. This is the one place a fetch this
		// package issued via m.fetch or the at#18 lanesFetch fallback ever
		// completes; leaving the flag set on any path would wedge every
		// future poll into believing one is still outstanding forever.
		m.fetchInFlight = false
		m.fetchErr = msg.err
		if msg.err == nil {
			m.lanes = msg.lanes
			m.lastFetched = time.Now()
			if m.selected >= len(m.lanes) {
				m.selected = max(0, len(m.lanes)-1)
			}
		}
		return m, nil

	case sessionsFetchResultMsg:
		// Same discipline as fetchResultMsg: a failed fetch leaves the prior
		// (possibly stale) sessions on screen alongside a visible error --
		// see View()'s "! unavailable" branch -- rather than clearing them
		// to a blank rail a reader could mistake for a quiet estate.
		m.sessionsFetchInFlight = false
		m.sessionsErr = msg.err
		if msg.err == nil {
			m.sessions = msg.sessions
			m.sessionsFetched = time.Now()
			if n := m.rowCount(); m.selected >= n {
				m.selected = max(0, n-1)
			}
			return m, nil
		}
		// at#18: the sessions fetch just failed. If a fallback Fetcher was
		// wired in (NewMultiSession's lanesFetch), go get the one session it
		// can still read RIGHT NOW rather than wait for the next
		// refreshInterval tick -- the whole point is not to sit on a blank/
		// error-only rail for up to refreshInterval while a perfectly
		// readable single session goes unfetched. Guarded on fetchInFlight
		// too (agent-tui#55): a refreshMsg's own doFetchAll may already have
		// a lanesFetch fallback in flight from the PRIOR failure when this
		// one lands, and firing a second would be exactly the unbounded
		// pile-up against the supervisor's single-threaded MCP server this
		// issue exists to stop.
		if m.lanesFetch != nil && !m.fetchInFlight {
			m.fetchInFlight = true
			return m, doFetch(m.lanesFetch)
		}
		return m, nil

	case costRefreshMsg:
		return m, tea.Batch(costRefreshCmd(), doCostFetch(m.costFetch))

	case costFetchResultMsg:
		if msg.err == nil {
			m.costSnap = msg.snap
		} else {
			// Same discipline as internal/cost.Model: a failed fetch must
			// not leave a stale-but-real snapshot looking current. Fold in
			// Unknown() so the rail's cost line reads "unknown", never last
			// cycle's numbers going silently stale.
			m.costSnap = cost.Unknown()
		}
		m.costFetched = time.Now()
		return m, nil

	case taskFetchResultMsg:
		// Same discipline as fetchResultMsg/sessionsFetchResultMsg: a failed
		// ledger read leaves the prior (possibly stale) rows on screen --
		// tasksErr is surfaced as a small legend note in View(), never a
		// silent revert to "(no task)" for every lane that had one a moment
		// ago.
		m.tasksErr = msg.err
		if msg.err == nil {
			m.tasks = msg.rows
			m.tasksFetched = time.Now()
		}
		return m, nil

	// agent-tui#14: results of the write operations (agent-tui#23: attach
	// and detach no longer produce one of these -- see ops.go's package doc
	// comment). Each is produced by exactly one doXxx tea.Cmd in ops.go and
	// handled there too, kept out of this switch's other cases so the write
	// path's state machine lives in one file end to end.
	case addResultMsg, removeCheckResultMsg, removeResultMsg:
		return m.handleOpsResult(msg)
	}
	return m, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// rowCount is the length of the selectable list "up"/"down" move through --
// every session's lanes, in display order, headers excluded (a header is
// not a lane and enter has nothing to do with it). A Model built with
// New/NewWithCost has no sessions, so this falls back to the flat len(lanes)
// it always used, unchanged. m.fallbackActive (at#18) selects the same flat
// len(lanes) too: while the sessions fetch is down, selection must move
// through what View() is actually drawing (the fallback single session),
// not a cross-session list that has nothing behind it right now.
func (m Model) rowCount() int {
	if m.sessionsFetch != nil && !m.fallbackActive() {
		return len(m.sessionsFlat())
	}
	return len(m.lanes)
}

// fallbackActive reports whether View()/rowCount() should be drawing at#18's
// single-session fallback instead of the grouped multi-session body: the
// sessions fetch has failed AND a fallback Fetcher was wired in via
// NewMultiSession. A Model with no lanesFetch (every pre-#18 test) never
// activates it -- a failed sessions fetch there still renders exactly what
// it always did, "! unavailable" and nothing else.
func (m Model) fallbackActive() bool {
	return m.sessionsErr != nil && m.lanesFetch != nil
}

// doFetchAll re-issues whichever fetch(es) this Model was built with. "r"
// and the periodic refreshMsg both go through this so neither has to know
// which constructor built the Model.
//
// agent-tui#55: every fetch that funnels through mcp.Client (m.fetch,
// m.sessionsFetch, and the at#18 lanesFetch fallback, which reuses m.fetch's
// own doFetch/fetchResultMsg/fetchInFlight -- see lanesFetch's doc comment)
// is guarded on its own inFlight flag before being reissued. Measured
// against the real supervisor: a "sessions" call (sessions.sh fanning out to
// lanes.sh per tmux session) took 4-5s wall clock with six sessions up,
// slower than refreshInterval (2s) re-firing this method. mcp_server.py
// answers exactly one tools/call at a time (a plain `for line in stdin`
// loop, no concurrency of its own) -- so a Model that fired a new request
// every 2s regardless of whether the last one had answered was not running
// two requests in parallel, it was queuing a new one behind a backlog that
// grows every cycle, guaranteeing every request eventually crosses
// mcp.Client's 10s callTimeout even though the supervisor itself was never
// unavailable. This is what "! multi-session unavailable ... mcp: no reply"
// actually was: a self-inflicted request pile-up, not supervisor-side
// contention or the cost pane's ccusage/quota.sh subprocesses (neither goes
// through mcp.Client at all -- see cmd/keelson/cost.go). Returns the updated
// Model because setting these flags is a state change the caller must keep.
func (m Model) doFetchAll() (Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.fetch != nil && !m.fetchInFlight {
		m.fetchInFlight = true
		cmds = append(cmds, doFetch(m.fetch))
	}
	if m.sessionsFetch != nil && !m.sessionsFetchInFlight {
		m.sessionsFetchInFlight = true
		cmds = append(cmds, doSessionsFetch(m.sessionsFetch))
	}
	// at#18: while the fallback is on screen, "r" (and the periodic
	// refreshMsg, which also calls this) must refresh what is actually
	// being shown, not just retry the sessions fetch and leave the visible
	// fallback data to go stale until that retry itself fails again.
	if m.fallbackActive() && !m.fetchInFlight {
		m.fetchInFlight = true
		cmds = append(cmds, doFetch(m.lanesFetch))
	}
	if m.taskFetch != nil {
		cmds = append(cmds, doTaskFetch(m.taskFetch))
	}
	return m, tea.Batch(cmds...)
}

// digitKey reports whether s is a single ASCII digit key and, if so, its
// value. tea.KeyMsg.String() renders a bare digit key as itself ("1", "2",
// ...), so no key-code table is needed.
func digitKey(s string) (int, bool) {
	if len(s) != 1 || s[0] < '1' || s[0] > '9' {
		return 0, false
	}
	return int(s[0] - '0'), true
}

// railStyles is built fresh from m.theme on every View() call -- there is
// no package-level var carrying a literal theme.Role colour any more (that
// was the actual bug agent-tui#27 exists to prevent: a colour baked into a
// package var at init time cannot change when the active theme does). Every
// render helper in this package (sessions.go, ops.go included) takes one of
// these rather than reaching for a package-level style.
type railStyles struct {
	title, row, selRow, dim, err, legend lipgloss.Style
	// warn is agent-tui#26's "needs human" flag -- theme.RoleWarn, the same
	// role board's aged/blocked cards use, distinct from err (RoleError,
	// reserved for fetch failures and refusals) so a lane that merely needs
	// a look never reads as loud as one this program cannot see at all.
	warn                        lipgloss.Style
	border                      lipgloss.Style
	selectionBG, directorAccent lipgloss.Color
	unsupervisedAccent          lipgloss.Color
}

// styles builds m's live style set from m.theme -- selectionBackground's
// old doc comment about why the selected-row background is a FIXED colour,
// never `Reverse(true)`, still applies; it is now theme.RoleSelectedBG
// instead of a literal, but still one constant colour regardless of the
// selected lane's own state (as#109).
func (m Model) styles() railStyles {
	th := m.theme
	pad := th.Padding
	row := lipgloss.NewStyle().Padding(0, pad)
	selectionBG := th.Color(theme.RoleSelectedBG)
	return railStyles{
		title:              lipgloss.NewStyle().Bold(true).Padding(0, pad),
		row:                row,
		selRow:             row.Background(selectionBG),
		dim:                lipgloss.NewStyle().Faint(true),
		err:                lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleError)),
		warn:               lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleWarn)),
		legend:             lipgloss.NewStyle().Faint(true),
		border:             lipgloss.NewStyle().Border(th.Border, false, true, false, false).BorderForeground(th.Color(theme.RoleBorder)),
		selectionBG:        selectionBG,
		directorAccent:     th.Color(theme.RoleDirector),
		unsupervisedAccent: th.Color(theme.RoleUnsupervised),
	}
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	width := m.width
	if width <= 0 || width > RailWidth+8 {
		width = RailWidth
	}
	innerWidth := width - 2 // padding
	st := m.styles()

	set := lane.Variants[m.glyphSet]

	var b []string
	if m.themeNotice != "" {
		b = append(b, st.err.Width(innerWidth).Render(truncate("! "+m.themeNotice, innerWidth)))
	}
	b = append(b, st.title.Width(innerWidth).Render("lanes"))

	// agent-tui#13: a Model built with NewMultiSession renders every
	// session, grouped; one built with New/NewWithCost (board.go, every
	// pre-#13 test) renders the flat single-session list exactly as before
	// -- see sessions.go for the grouped path. at#18: a NewMultiSession
	// Model whose sessions fetch has failed AND has a fallback Fetcher
	// wired renders that fallback instead -- see fallbackActive.
	switch {
	case m.sessionsFetch != nil && m.fallbackActive():
		b = append(b, m.renderFallbackNote(innerWidth, st)...)
		b = append(b, m.renderFlatBody(innerWidth, set, st)...)
	case m.sessionsFetch != nil:
		b = append(b, m.renderSessionsBody(innerWidth, st)...)
	default:
		b = append(b, m.renderFlatBody(innerWidth, set, st)...)
	}

	// The picker itself: which glyph set is live, and how to change it.
	// Every option is real and numbered here, not described in prose
	// elsewhere -- pressing the number is the whole interaction.
	b = append(b, st.dim.Width(innerWidth).Render(""))
	b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("glyphs %d/%d: %s", m.glyphSet+1, len(lane.Variants), set.Name)))
	b = append(b, st.legend.Width(innerWidth).Render(truncate(set.Description, innerWidth)))
	b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("[1-%d] to switch", len(lane.Variants))))

	// agent-tui#26's reading picker: which content variant is live now --
	// same numbered-and-real discipline as the glyph picker above, one key
	// ('w') rather than a number since there are, deliberately, only two
	// candidates today (see readings.go's doc comment on why more than one
	// exists at all).
	if len(readings) > 0 {
		rd := readings[m.reading]
		b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("reading %d/%d: %s", m.reading+1, len(readings), rd.Name)))
		b = append(b, st.legend.Width(innerWidth).Render(truncate(rd.Description, innerWidth)))
		b = append(b, st.legend.Width(innerWidth).Render("[w] to switch"))
	}
	if m.taskFetch != nil && m.tasksErr != nil {
		// Never a silent revert to "(no task)" everywhere -- see
		// taskFetchResultMsg's own comment for why stale rows stay on
		// screen while this note says why they might be stale.
		b = append(b, st.err.Width(innerWidth).Render(truncate("! ledger unavailable", innerWidth)))
	}

	// The grouping-style picker (agent-tui#13 requirement 5) -- only shown
	// when there is grouping to pick between at all.
	if m.sessionsFetch != nil && len(groupStyles) > 0 {
		gs := groupStyles[m.groupStyle]
		b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("group %d/%d: %s", m.groupStyle+1, len(groupStyles), gs.Name)))
		b = append(b, st.legend.Width(innerWidth).Render("[g] to switch"))
	}

	// The theme picker (agent-tui#25 scope item 3): which theme is live,
	// right now. Agent-tui#51: 't' now persists via theme.Save (shell.Model
	// owns the write, this pane only asks for a cycle -- see
	// theme.CycleRequestedMsg), so this line no longer claims the choice is
	// session-only; it is the same value cmd/agent-tui's theme.Load will
	// read back on the next launch.
	b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("theme: %s", m.theme.Name)))
	b = append(b, st.legend.Width(innerWidth).Render("[t] to switch"))

	// agent-tui#14's write path: the current mode's prompt (typing a new
	// session's name, or a remove confirmation naming exactly what would be
	// destroyed) or, when idle, the last operation's one-line result. Only
	// present when cmd/agent-tui wired ops in (WithOps) -- see ops.go.
	if m.ops != nil {
		b = append(b, st.dim.Width(innerWidth).Render(""))
		b = append(b, m.renderOpsStatus(innerWidth, st)...)
	}

	// The cost line (agent-tui#4): glanceable, always in the rail, no flag
	// or command needed to see it. Only present when cmd/agent-tui wired a
	// costFetch in (NewWithCost) -- New() alone still renders nothing here,
	// same as before this existed.
	if m.costFetch != nil {
		b = append(b, st.dim.Width(innerWidth).Render(""))
		b = append(b, st.legend.Width(innerWidth).Render("cost:"))
		for _, line := range cost.RenderCompact(m.costSnap, innerWidth) {
			b = append(b, st.legend.Width(innerWidth).Render(truncate(line, innerWidth)))
		}
		if !m.costFetched.IsZero() {
			age := time.Since(m.costFetched).Round(time.Second)
			b = append(b, st.legend.Width(innerWidth).Render(truncate(fmt.Sprintf("age: %s", age), innerWidth)))
		}
	}

	content := lipgloss.JoinVertical(lipgloss.Left, b...)
	return st.border.Height(m.height - 1).Render(content)
}

// renderFallbackNote is at#18's visible note: a NewMultiSession Model whose
// sessions fetch failed must never quietly narrow from "every session" to
// "one" -- the reviewer's own reproduction of this PR's original defect was
// exactly that narrowing going all the way to zero and nothing on screen
// saying why. Shown above renderFlatBody's single-session render, never in
// place of it -- the one session that IS readable must still show.
//
// agent-tui#55: this used to hardcode "needs agent-supervisor#158" -- true
// the day at#18 shipped (that supervisor checkout genuinely had no
// "sessions" tool), false the moment as#158 merged, and never re-checked
// after that: a fixed string cannot know a REAL failure (a timed-out call
// against a supervisor that has "sessions" and is simply slow, per this
// file's fetchInFlight fix) from the one it was written to describe. The
// cause line below is picked from m.sessionsErr AT RENDER TIME via
// isTimeoutErr, not asserted once and baked in, so it stays honest as the
// underlying cause changes.
func (m Model) renderFallbackNote(innerWidth int, st railStyles) []string {
	cause := "sessions tool unavailable"
	if isTimeoutErr(m.sessionsErr) {
		cause = "sessions call timed out"
	}
	return []string{
		st.err.Width(innerWidth).Render(truncate("! multi-session unavailable", innerWidth)),
		st.dim.Width(innerWidth).Render(truncate("this session only:", innerWidth)),
		st.dim.Width(innerWidth).Render(truncate(cause, innerWidth)),
		st.dim.Width(innerWidth).Render(truncate(m.sessionsErr.Error(), innerWidth)),
	}
}

// timeouter matches the standard net.Error convention (Timeout() bool) --
// internal/mcp's timeout error implements it, and checking the interface
// here rather than importing internal/mcp keeps this package's own
// discipline intact (model.go's package doc: rail knows lanes, not MCP).
// Any other Fetcher/SessionsFetcher this package is ever handed (a fake in
// a test, a future non-MCP source) gets the same honest classification for
// free by implementing the same one-method interface.
type timeouter interface{ Timeout() bool }

// isTimeoutErr reports whether err (or anything it wraps) is a timeout, per
// timeouter above -- "call timed out" and "tool not available" have
// different causes and different fixes (agent-tui#55's second, `#158`-
// shaped defect: one message covering both is why a live concurrency bug
// read as a stale supervisor-side gap).
func isTimeoutErr(err error) bool {
	var t timeouter
	return errors.As(err, &t) && t.Timeout()
}

// renderFlatBody is the single-session list every pre-#13 rail render used,
// extracted unchanged so at#18's fallback path (renderFallbackNote above)
// and the plain New/NewWithCost path share exactly one implementation of it
// instead of two that could drift apart.
func (m Model) renderFlatBody(innerWidth int, set lane.GlyphSet, st railStyles) []string {
	var b []string

	// A fetch failure is the load-bearing case (mirrors
	// supervisor_view.SupervisorUnavailable): show it, never render a
	// blank or stale-looking rail as if the estate were quietly idle.
	if m.fetchErr != nil {
		b = append(b, st.err.Width(innerWidth).Render("! unavailable"))
		b = append(b, st.dim.Width(innerWidth).Render(truncate(m.fetchErr.Error(), innerWidth)))
	} else if len(m.lanes) == 0 {
		b = append(b, st.dim.Width(innerWidth).Render("(no lanes)"))
	}

	// name budget: innerWidth minus the row's own Padding(0, th.Padding)
	// (2*th.Padding cols) minus the glyph column and the single space
	// after it (2 cols). Both must be subtracted -- reserving only for the
	// glyph+space and not for the row's own padding let a name run one
	// cell past the available width and wrap onto a second line with no
	// glyph or indent (as#109's second defect). Padding is theme data
	// (agent-tui#27), so this budget must track it rather than assume the
	// old Padding(0,1) literal -- fixed at "- 4" it silently drifted wrong
	// under any theme with a different Padding.
	nameWidth := innerWidth - 2*m.theme.Padding - 2

	for i, l := range m.lanes {
		style := lane.StyleFor(set, l.State)
		glyph := lane.Frame(set, l.State, m.tick)
		name := truncate(l.Name, nameWidth)

		var line string
		if i == m.selected {
			// Glyph and the rest of the row are rendered as two
			// separately closed ANSI spans. A single Render() over a
			// string that already contains the glyph's own embedded
			// reset code would have that inner reset wipe the outer
			// selection background for everything after it -- measured:
			// the trailing " name" text came out with no background at
			// all. Giving " "+name its own Background()-carrying span
			// keeps the highlight unbroken across the whole row.
			g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Background(st.selectionBG).Render(glyph)
			rest := lipgloss.NewStyle().Background(st.selectionBG).Render(" " + name)
			line = g + rest
			b = append(b, st.selRow.Width(innerWidth).Render(line))
		} else {
			g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Render(glyph)
			line = fmt.Sprintf("%s %s", g, name)
			b = append(b, st.row.Width(innerWidth).Render(line))
		}
	}

	// The legend: every state must be nameable (issue #107
	// hard-acceptance item 3). This line always shows the SELECTED
	// lane's real state word, verbatim, so a glyph is never the only
	// source of truth on screen -- the same discipline laneview/text.sh
	// applies by printing the state name beside its glyph on every row.
	b = append(b, st.dim.Width(innerWidth).Render(""))
	if len(m.lanes) > 0 && m.selected < len(m.lanes) {
		sel := m.lanes[m.selected]
		style := lane.StyleFor(set, sel.State)
		// agent-tui#26: was a fixed "state:/idle:" pair -- now the
		// reading-driven detail block; see readings_view.go.
		b = append(b, m.renderReadingDetail(sel, style, st, innerWidth)...)
	}
	if !m.lastFetched.IsZero() {
		age := time.Since(m.lastFetched).Round(time.Second)
		b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("age:   %s", age)))
	}
	return b
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
