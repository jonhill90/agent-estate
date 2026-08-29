package chat

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// Sender posts text into a live thread -- SPEC-shell.md S7's own
// requirement ("sends go through the daemon (subprocess transport), never
// tmux send-keys"), expressed as this package's adapter seam, the same
// shape Source (thread.go) already is. Called from inside a tea.Cmd
// (trySend below), never inline in Update -- a real Sender's own round
// trip can legitimately run minutes (agent-supervisor#508/agent-supervisor#509's
// session_send drives a live agent turn), and blocking Update for that
// long would freeze the whole shell, not just this pane.
//
// A nil error means the daemon confirmed delivery as an observed fact.
// errors.Is(err, ErrUnknown) means the outcome could not be confirmed
// either way -- a timeout on the daemon's own side, or on the round trip
// to it -- and must render as neither delivered nor failed (see
// ErrUnknown's own doc comment; this is the exact distinction
// agent-supervisor#488 exists to preserve, and collapsing it here would
// throw away the reason that capability was built at all). Any other
// non-nil error is a confirmed failure, shown with its own text.
//
// cmd/estate wires the real implementation: session.Ops.Send translated
// into this shape, with session.ErrSendUnknown mapped to ErrUnknown at
// that seam so this package never needs to import internal/session.
type Sender func(threadID, text string) error

// ErrUnknown marks a Sender outcome that could not be confirmed as either
// delivered or failed -- SPEC-shell.md S7's own requirement ("map a
// timeout to unknown, not failed... a message whose fate is unknown must
// not render as either delivered or failed"). Model's sendMsg handling
// (Update below) is the one place this is checked, via errors.Is, never
// by inspecting error text.
var ErrUnknown = errors.New("chat: send outcome could not be confirmed")

// composerHeight is the two fixed rows the composer always reserves in
// listLayout's own budget -- the input line and one status/hint line
// below it -- reserved unconditionally (composing or not) so the frame's
// total height never changes when [i]/[esc] toggle compose mode. Same
// "fixed budget in, fixed budget out" discipline sync/renderList already
// document for scrollIndicator.
const composerHeight = 2

// Model is the chat pane's Bubble Tea sub-model -- internal/shell mounts it
// as one of the panes beside board/cost/gallery (agent-tui#38's shape),
// the same "views become panes, not programs" composition every other
// screen in this repo already follows. It owns selection, layout and focus
// state; Source (thread.go) owns what threads exist and what is in them.
//
// The thread list and the "big" transcript (the selected thread in
// listLayout, the focused thread in gridLayout) are both backed by
// bubbles/viewport rather than a plain string clipped to a height --
// agent-tui#29 was a board pane that could not scroll and silently
// dropped rows past its height; this package uses the library primitive
// that exists for exactly this instead of repeating that mistake.
type Model struct {
	source Source

	threads []Thread // source.Threads(), plus a synthetic "All" thread at index 0
	fetched time.Time
	err     error

	selected int // index into threads
	layout   int // index into Layouts
	focused  int // gridLayout only: index into threads, or -1 for "tiled, nothing focused"

	// transcriptVP is the ONE scrollable "big" transcript on screen at a
	// time -- listLayout's right column, or gridLayout's focused pane.
	// Content and size are kept in sync (sync below) on every event that
	// could invalidate either, never recomputed inline inside View --
	// View has a value receiver and cannot persist a scroll position a
	// user has moved away from the bottom.
	transcriptVP viewport.Model
	// listVP is listLayout's left column when there are more threads than
	// fit -- kept scrolled so the selected row is always visible (see
	// ensureListVisible), never independently scrolled by the user: the
	// issue's own nav ask ("n/p between threads... a nice way to switch")
	// is what drives it, not a second set of scroll keys.
	listVP viewport.Model
	// transcriptIdx is which thread transcriptVP last rendered -- sync
	// uses it to tell "the selected/focused thread changed" (always jump
	// to the newest message, like opening a conversation) apart from "a
	// resize or a live update landed on the same thread" (preserve
	// scroll position unless the user was already at the bottom).
	transcriptIdx int

	width, height int
	quitting      bool

	// composer/composing/sendErr are S7's own addition (SPEC-shell.md:
	// "List pane + transcript pane + composer"). Scoped to listLayout only
	// -- gridLayout's own tiled/focused reading has no single "the thread
	// I am typing into" without adding a mode this file's own doc comment
	// does not otherwise need; see startComposing.
	composer  textinput.Model
	composing bool
	// sendOutcome/sendErr are the composer's three-state tracker
	// (SPEC-shell.md S7: "in flight / delivered / failed, never two" --
	// see sendOutcome's own doc comment for why "unknown" is the fourth
	// value this type actually needs). sendErr carries the visible text
	// for sendFailed and sendUnknown; AGENTS.md's "absence is a typed
	// value" convention applied to sender == nil specifically: a composer
	// that accepted [enter] and did nothing would be the exact silent
	// failure this repo's own "blind, not quiet" rule (rail/board's
	// Fetcher doc comments) warns against, applied here to writes instead
	// of reads.
	sendOutcome sendOutcome
	sendErr     string
	sender      Sender

	// participants/participantsFetch are agent-tui#114's room model --
	// see Participant's own doc comment for why this is a separate seam
	// from Source rather than a field on Thread: @-mentions must resolve
	// against every lane currently in the estate, including ones with no
	// thread in this pane at all, not just the one Lane a Thread names.
	participants      []Participant
	participantsFetch ParticipantsFetcher

	// participantsFetchInFlight guards m.participantsFetch the same way
	// internal/rail.Model's and internal/agents.Model's own fetchInFlight
	// fields guard their "sessions" reads, for the exact defect
	// agent-tui#177 measured: participantsTickMsg fired every
	// participantsRefreshInterval (2s) regardless of whether the previous
	// fetch had answered, so under load this ticker kept enqueueing
	// requests behind rail/agents/monitor's own against the same
	// single-threaded mcp_server.py rather than waiting for its own to
	// drain (internal/agents/model.go's fetchInFlight doc comment has the
	// fuller measurement). Set true the moment a fetch is issued (Init and
	// the participantsTickMsg case below), cleared only in
	// participantsFetchMsg -- see that case's own comment for why clearing
	// it first, unconditionally, matters.
	participantsFetchInFlight bool

	theme       theme.Theme
	themeNotice string
}

// New builds a Model bound to source. It fetches once at Init and does not
// re-fetch on a timer yet -- a live Source needs push-driven updates
// (session/update is a stream, not a poll) rather than the refresh-on-tick
// shape rail/board use for MCP reads; see fixture.go for what stands in
// for that today.
func New(source Source) Model {
	ti := textinput.New()
	ti.Prompt = "> "
	ti.CharLimit = 4000
	return Model{
		source:       source,
		theme:        theme.Default,
		focused:      -1,
		transcriptVP: viewport.New(0, 0),
		listVP:       viewport.New(0, 0),
		composer:     ti,
	}
}

// WithTheme returns a copy of m with th (and, when non-empty, a visible
// notice about how th was resolved) wired in -- same seam every other
// pane's WithTheme documents.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// WithSender wires in a real Sender -- nil (never called) is New's
// default and a perfectly valid, silent state, the same "wiring is
// optional" convention WithTasks/WithThemeSave document elsewhere in this
// module. No caller in this repo passes a non-nil one yet: see Sender's
// own doc comment for why.
func (m Model) WithSender(s Sender) Model {
	m.sender = s
	return m
}

// WithParticipants wires in a real ParticipantsFetcher -- nil (New's
// default) is a valid, silent "no room roster configured yet" state, same
// convention WithSender/WithTasks document elsewhere in this module.
// ValidateMentions then refuses every @-mention as unknown against an
// empty participants slice, which is the honest answer, not a crash.
// participantsFetchInFlight starts true whenever fetch != nil, matching
// internal/rail.Model.New's and internal/agents.New's own fetchInFlight
// seed: Init() below always issues the first participants fetch
// unconditionally when a fetch is wired, so the guard must already reflect
// that before the first participantsTickMsg (participantsRefreshInterval
// later) can check it.
func (m Model) WithParticipants(fetch ParticipantsFetcher) Model {
	m.participantsFetch = fetch
	m.participantsFetchInFlight = fetch != nil
	return m
}

// sendOutcome is the composer's own answer to SPEC-shell.md S7's "three
// states in the UI, never two": in flight (sent, outcome not yet known),
// delivered (the daemon confirmed it, as a fact -- collapses straight
// back to sendIdle, see the sendMsg case in Update), failed (with the
// error text visible), or unknown (a timeout the sender could not
// confirm -- see Sender/ErrUnknown's own doc comments for why this must
// never be reported as failed). sendIdle (the zero value) is "nothing
// sent since the composer was last cleared or opened".
type sendOutcome int

const (
	sendIdle sendOutcome = iota
	sendInFlight
	sendFailed
	sendUnknown
)

type fetchMsg struct {
	threads []Thread
	err     error
}

// sendMsg is trySend's async result, posted once m.sender's Cmd returns --
// the same "Cmd now, Msg later" shape fetchMsg already uses for
// source.Threads(). threadID names which thread the send was FOR (not
// necessarily the one still selected by the time this arrives -- a user
// is free to move on while a send is in flight); this package has one
// composer and applies the result to it regardless, rather than silently
// dropping a result for a thread no longer on screen.
type sendMsg struct {
	threadID string
	err      error
}

// participantsFetchMsg/participantsTickMsg are the room roster's own
// "Cmd now, Msg later" pair -- participantsRefreshInterval matches
// internal/agents' own refreshInterval for the same "sessions" MCP read
// (internal/agents/model.go's own doc comment), since a participant going
// dead/stale mid-compose must be visible to ValidateMentions before it is
// visible in the Agents pane, not after.
type participantsFetchMsg struct {
	participants []Participant
	err          error
}
type participantsTickMsg time.Time

const participantsRefreshInterval = 2 * time.Second

func (m Model) fetchCmd() tea.Cmd {
	src := m.source
	return func() tea.Msg {
		threads, err := src.Threads()
		return fetchMsg{threads: threads, err: err}
	}
}

func (m Model) participantsFetchCmd() tea.Cmd {
	fetch := m.participantsFetch
	if fetch == nil {
		return nil
	}
	return func() tea.Msg {
		participants, err := fetch()
		return participantsFetchMsg{participants: participants, err: err}
	}
}

func participantsTickCmd() tea.Cmd {
	return tea.Tick(participantsRefreshInterval, func(t time.Time) tea.Msg { return participantsTickMsg(t) })
}

// Init fetches threads exactly as before when no ParticipantsFetcher is
// wired (WithParticipants never called) -- preserved as a single Cmd, not
// tea.Batch, so every existing caller driving Init()'s returned Cmd
// synchronously (this package's own fetched() test helper) keeps working
// unchanged. Only a Model with a real participants seam pays for the extra
// fetch + recurring tick.
func (m Model) Init() tea.Cmd {
	if m.participantsFetch == nil {
		return m.fetchCmd()
	}
	return tea.Batch(m.fetchCmd(), m.participantsFetchCmd(), participantsTickCmd())
}

// usingFixture reports whether m.threads came from FixtureSource (Thread's
// own Fixture field, set only there -- see thread.go/fallback.go). Checked
// against every thread rather than just the first: a Source is a single
// value per Model, so in practice all of them agree, but a check that only
// looked at index 0 would silently stop being true the moment index 0 (the
// synthetic "All" thread, render.go) diverges from the rest.
func (m Model) usingFixture() bool {
	for _, t := range m.threads {
		if t.Fixture {
			return true
		}
	}
	return false
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.sync(), nil

	case fetchMsg:
		m.fetched = time.Now()
		m.err = msg.err
		if msg.err != nil {
			return m, nil
		}
		all := AggregateAll(msg.threads)
		m.threads = append([]Thread{all}, msg.threads...)
		if m.selected >= len(m.threads) {
			m.selected = 0
		}
		return m.sync(), nil

	case sendMsg:
		switch {
		case msg.err == nil:
			// Delivered, as an observed fact -- back to idle. The sent
			// text is not echoed as a new transcript line here for the
			// same reason trySend never echoed it synchronously: no live
			// Source exists yet for it to diverge from (see Sender's own
			// doc comment); a future real Source's next fetch is what
			// will actually show it.
			m.sendOutcome = sendIdle
			m.sendErr = ""
			m.composer.SetValue("")
			m.composing = false
			m.composer.Blur()
		case errors.Is(msg.err, ErrUnknown):
			// SPEC-shell.md S7's own rule, checked with errors.Is and
			// never by inspecting msg.err's text: an unconfirmed outcome
			// renders as unknown, not failed. Stays in compose mode (same
			// as sendFailed below) -- there is nothing safe to auto-clear
			// when delivery itself was never confirmed.
			m.sendOutcome = sendUnknown
			m.sendErr = msg.err.Error()
		default:
			m.sendOutcome = sendFailed
			m.sendErr = msg.err.Error()
		}
		return m, nil

	case participantsFetchMsg:
		// agent-tui#177: clear the in-flight guard BEFORE anything else in
		// this case, unconditionally (success or failure) -- same
		// discipline as internal/agents' own fetchResultMsg case. Leaving
		// it set on any path (including a timeout) wedges every future
		// participantsTickMsg into believing one is still outstanding
		// forever, silently freezing the roster (and every @-mention
		// validated against it) on stale data.
		m.participantsFetchInFlight = false
		// A failed fetch leaves m.participants at its last-known value
		// rather than clearing it -- the same "blind, not quiet" choice
		// internal/rail's own Fetcher failures make: a stale roster that
		// still refuses a genuinely-gone participant is safer than an
		// empty one that would refuse every mention as "not in this room,"
		// including ones that still are.
		if msg.err == nil {
			m.participants = msg.participants
		}
		return m, nil

	case participantsTickMsg:
		// agent-tui#177: m.participantsFetch only re-fires when the
		// previous one has already answered -- see
		// participantsFetchInFlight's own doc comment for why an
		// unconditional re-fire here is exactly the request pile-up that
		// issue measured.
		cmds := []tea.Cmd{participantsTickCmd()}
		if m.participantsFetch != nil && !m.participantsFetchInFlight {
			m.participantsFetchInFlight = true
			cmds = append(cmds, m.participantsFetchCmd())
		}
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.composing {
		return m.handleComposerKey(msg)
	}
	switch msg.String() {
	case "q", "ctrl+c":
		m.quitting = true
		return m, tea.Quit

	case "i":
		return m.startComposing()

	case "t":
		// agent-tui#25 scope item 3: runtime theme comparison -- same shape
		// as every other pane's "t" case, see rail.Model's for the
		// rationale.
		m.theme = theme.Cycle(m.theme)
		return m, nil

	case "v":
		// The seam the issue explicitly asks for: cycle Layouts, never
		// choose one in prose.
		m.layout = (m.layout + 1) % len(Layouts)
		m.focused = -1 // a focus set in gridLayout means nothing in listLayout
		return m.sync(), nil

	case "f":
		if Layouts[m.layout].ID != gridLayout.ID {
			return m, nil
		}
		if m.focused == m.selected {
			m.focused = -1
		} else {
			m.focused = m.selected
		}
		return m.sync(), nil

	case "j", "down", "n":
		m.moveSelection(1)
		return m.sync(), nil

	case "k", "up", "p":
		m.moveSelection(-1)
		return m.sync(), nil

	// Transcript scrolling -- reaching content the thread list/grid tiles
	// cannot fully show (issue acceptance: "content taller than the pane
	// is reachable and the user can tell something is hidden"). Only takes
	// effect while a transcript is actually on screen (isTranscriptActive)
	// -- an unfocused grid tile has no single transcript to scroll; "f"
	// reaches one first, per layouts.go's own doc comment.
	case "pgdown", "ctrl+d":
		if m.isTranscriptActive() {
			m.transcriptVP.HalfPageDown()
		}
		return m, nil
	case "pgup", "ctrl+u":
		if m.isTranscriptActive() {
			m.transcriptVP.HalfPageUp()
		}
		return m, nil
	case "home", "g":
		if m.isTranscriptActive() {
			m.transcriptVP.GotoTop()
		}
		return m, nil
	case "end", "G":
		if m.isTranscriptActive() {
			m.transcriptVP.GotoBottom()
		}
		return m, nil

	default:
		if n, err := strconv.Atoi(msg.String()); err == nil && n >= 1 {
			m.jumpTo(n - 1)
			return m.sync(), nil
		}
		return m, nil
	}
}

// startComposing enters compose mode -- [i], unused elsewhere in this
// package's keymap. Refuses when there is no real selected thread to send
// into (the synthetic "All" thread, AggregateAll's ID "all", has no
// single lane behind it to address) or while gridLayout is active (this
// file's own doc comment on composer/composing: scoped to listLayout).
func (m Model) startComposing() (tea.Model, tea.Cmd) {
	if Layouts[m.layout].ID == gridLayout.ID {
		return m, nil
	}
	if m.selected < 0 || m.selected >= len(m.threads) || m.threads[m.selected].ID == "all" {
		return m, nil
	}
	m.composing = true
	m.sendOutcome = sendIdle
	m.sendErr = ""
	m.composer.SetValue("")
	m.composer.Focus()
	return m, textinput.Blink
}

// handleComposerKey is where every key goes while m.composing is true.
// "ctrl+c" is handled here explicitly rather than falling through to
// composer.Update's own rune capture -- agent-tui#22's lesson (a mode that
// swallows every key, quit included, must never be able to recur)
// requires quitting to work mid-type, the same carve-out
// internal/rail/ops.go's opsModeBusy makes for the same reason. "q" is
// deliberately NOT caught here: unlike ctrl+c, a literal 'q' is ordinary
// text a user may want to type into a message.
func (m Model) handleComposerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.quitting = true
		return m, tea.Quit
	case "esc":
		m.composing = false
		m.composer.Blur()
		m.composer.SetValue("")
		m.sendOutcome = sendIdle
		m.sendErr = ""
		return m, nil
	case "enter":
		if m.sendOutcome == sendInFlight {
			// A send already in flight -- refuse a second one rather than
			// racing two calls to the same thread. Not an error: the
			// composer's own status line already says "sending...".
			return m, nil
		}
		return m.trySend()
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	return m, cmd
}

// trySend is [enter] while composing. An empty (post-trim) message is a
// no-op, not an error -- pressing enter on a blank line should not surface
// "cannot send" chrome for text that was never going to go anywhere.
//
// The actual send runs inside the returned tea.Cmd, never inline here --
// see Sender's own doc comment for why (a real Sender's round trip can
// legitimately run minutes). This method's only job is to record
// sendInFlight and hand back the Cmd; sendMsg's own case in Update is
// where delivered/failed/unknown is decided. The sent text is NOT echoed
// locally on delivery -- there is no live Source today for it to diverge
// from (see Sender's own doc comment), and a future real Source's next
// fetch is what will actually show it, exactly like every other message
// already on screen.
func (m Model) trySend() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.composer.Value())
	if text == "" {
		return m, nil
	}
	// agent-tui#114's failure mode to engineer against: an @-mention that
	// names a participant not in the room, or not running, must say so
	// HERE -- before m.sender is ever called -- not be sent and silently
	// go nowhere. sendFailed is the correct outcome, not a fourth state:
	// this is a confirmed refusal, exactly as final as any other failed
	// send, just decided locally instead of by the daemon.
	if err := ValidateMentions(text, m.participants); err != nil {
		m.sendOutcome = sendFailed
		m.sendErr = err.Error()
		return m, nil
	}
	if m.sender == nil {
		m.sendOutcome = sendFailed
		m.sendErr = "cannot send -- no lane on a structured transport yet (agent-tui#20)"
		return m, nil
	}
	thread := m.threads[m.selected]
	m.sendOutcome = sendInFlight
	m.sendErr = ""
	sender := m.sender
	return m, func() tea.Msg {
		err := sender(thread.ID, text)
		return sendMsg{threadID: thread.ID, err: err}
	}
}

func (m *Model) moveSelection(delta int) {
	if len(m.threads) == 0 {
		return
	}
	m.selected = (m.selected + delta + len(m.threads)) % len(m.threads)
	m.markRead(m.selected)
}

func (m *Model) jumpTo(idx int) {
	if idx < 0 || idx >= len(m.threads) {
		return
	}
	m.selected = idx
	m.markRead(idx)
}

// markRead clears the unread marker on the thread now being read -- the
// issue's own navigation ask ("unread/activity markers so a quiet lane
// that just moved is obvious") only has teeth if reading one clears it.
// This is local state on m.threads' copy, not written back to source: a
// FixtureSource has nothing to write to, and a live Source owns its own
// notion of read/unread once one exists.
func (m *Model) markRead(idx int) {
	if idx >= 0 && idx < len(m.threads) {
		m.threads[idx].Unread = false
	}
}

// isTranscriptActive reports whether transcriptVP is the thing on screen
// right now -- always true in listLayout (its right column IS the
// transcript), only true in gridLayout once "f" has focused a tile.
func (m Model) isTranscriptActive() bool {
	if Layouts[m.layout].ID == gridLayout.ID {
		return m.focused >= 0 && m.focused < len(m.threads)
	}
	return true
}

// metrics holds the same width/height split View and sync both need to
// agree on -- computed once so a resize, a layout switch and a render can
// never derive two different budgets for the same frame.
type metrics struct {
	width, height          int
	bodyHeight             int
	listWidth, transcriptW int
}

// focusedMainHeight is gridLayout's split of bodyHeight between the
// focused pane and the peripheral strip (one row per other thread, plus
// one row for the "-- peripherals --" label) -- a pure function of
// bodyHeight and the thread count, called from both sync (to size
// transcriptVP) and renderFocused (to size the outer box), so the two can
// never compute a different number for the same frame.
func (mx metrics) focusedMainHeight(threadCount int) int {
	stripHeight := threadCount - 1
	if stripHeight < 0 {
		stripHeight = 0
	}
	h := mx.bodyHeight - stripHeight - 1
	if h < 4 {
		h = 4
	}
	return h
}

func (m Model) metrics() metrics {
	width, height := m.width, m.height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}
	bodyHeight := height - 4 // title + notice/legend lines View always renders
	if bodyHeight < 5 {
		bodyHeight = 5
	}
	listWidth := width / 3
	if listWidth < 20 {
		listWidth = 20
	}
	transcriptW := width - listWidth - 1
	if transcriptW < 20 {
		transcriptW = 20
	}
	return metrics{
		width: width, height: height, bodyHeight: bodyHeight,
		listWidth: listWidth, transcriptW: transcriptW,
	}
}

// sync recomputes transcriptVP/listVP size and content from current
// selection/layout/focus/threads state -- the ONE place either viewport's
// SetContent or Width/Height is touched, called after every event that
// could invalidate them (resize, fetch, selection change, layout/focus
// toggle). View() only ever reads what sync last produced.
func (m Model) sync() Model {
	mx := m.metrics()
	st := m.styles()

	// Captured BEFORE any Width/Height mutation below: AtBottom() is
	// relative to the viewport's CURRENT Height, so reassigning Height
	// first (a resize, or the very first sync after New's 0x0 viewport)
	// changes what "at bottom" means for the OLD content and produces a
	// false reading -- the exact bug this ordering avoids.
	wasBottom := m.transcriptVP.AtBottom()

	// listVP: listLayout's left column only -- gridLayout has no list
	// column to keep sized, but keeping it sized costs nothing and avoids
	// a stale width the moment "v" switches back. Height reserves exactly
	// ONE row for scrollIndicator, always, whether or not this frame needs
	// it -- viewport.View() always returns exactly Height lines, so an
	// indicator appended AFTER sizing (rather than budgeted INTO it) would
	// push the pane's total line count past what its outer lipgloss.Height
	// box enforces, and lipgloss does not truncate overflow -- it would
	// silently push the footer down instead, the same failure class as
	// agent-tui#29's untruncated board.
	m.listVP.Width = mx.listWidth
	m.listVP.Height = mx.bodyHeight - composerHeight - 1
	var listLines []string
	for i, t := range m.threads {
		row := RenderThreadRow(t, m.fetched)
		row = truncate(row, mx.listWidth)
		switch {
		case i == m.selected:
			row = st.sel.Width(mx.listWidth).Render(row)
		case t.Unread:
			row = st.unread.Width(mx.listWidth).Render(row)
		}
		listLines = append(listLines, row)
	}
	m.listVP.SetContent(strings.Join(listLines, "\n"))
	m.ensureListVisible()

	// transcriptVP: whichever thread is the "big" one right now, per
	// isTranscriptActive's own reading of layout+focus. Height reserves a
	// header row (the "-- thread title --" line) and, same as listVP
	// above, one row for scrollIndicator -- always, so the exact-height
	// budget every render function's outer lipgloss.Height box assumes
	// never depends on whether this particular frame happens to overflow.
	width := mx.transcriptW
	height := mx.bodyHeight - composerHeight - 2
	if Layouts[m.layout].ID == gridLayout.ID {
		width = mx.width
		height = mx.focusedMainHeight(len(m.threads)) - 2
	}
	if height < 1 {
		height = 1
	}
	m.transcriptVP.Width = width
	m.transcriptVP.Height = height

	idx := m.selected
	if Layouts[m.layout].ID == gridLayout.ID {
		idx = m.focused
	}
	if idx >= 0 && idx < len(m.threads) {
		var lines []string
		for _, msg := range m.threads[idx].Messages {
			rendered := highlightMentions(RenderMessage(msg), st)
			lines = append(lines, colorizeMessage(msg, rendered, st)...)
		}
		for i, l := range lines {
			lines[i] = truncate(l, width)
		}
		m.transcriptVP.SetContent(strings.Join(lines, "\n"))
	} else {
		m.transcriptVP.SetContent("")
	}
	// Switching which thread transcriptVP shows always lands on its
	// newest message (opening a conversation reads from the bottom); a
	// resize or a live update to the SAME thread preserves scroll
	// position unless the user was already reading the bottom, in which
	// case it should keep tracking new messages as they arrive.
	if idx != m.transcriptIdx || wasBottom {
		m.transcriptVP.GotoBottom()
	}
	m.transcriptIdx = idx

	return m
}

// ensureListVisible scrolls listVP so the selected row stays on screen --
// the thread list's own answer to "content taller than the pane must be
// reachable": every row is reachable by moving selection with j/k/n/p,
// which this keeps in view rather than requiring a second set of scroll
// keys on top of thread navigation.
func (m *Model) ensureListVisible() {
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

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	mx := m.metrics()
	st := m.styles()

	var out string
	out += titleStyle.Render("chat") + "\n"
	if m.themeNotice != "" {
		out += st.errS.Render("! "+m.themeNotice) + "\n"
	}
	if m.err != nil {
		out += st.errS.Render("! "+m.err.Error()) + "\n"
		return out
	}
	if m.usingFixture() {
		// agent-b3.md's own rule: never let fixture data render as
		// though it were real. Every thread FixtureSource returns is
		// tagged Fixture (thread.go) -- this is the ONE place that
		// notice becomes visible, so a real, configured Source's empty
		// or erroring answer (which never sets Fixture) never triggers
		// it by accident.
		out += st.warn.Render("! showing fixture data -- no real chat source is configured") + "\n"
	}

	out += st.dim.Render(fmt.Sprintf(
		"layout %d/%d: %s -- [j/k] move, [1-9] jump, [v] switch layout, [f] focus (grid), "+
			"[i] compose (list), [pgup/pgdn] scroll, [home/end] top/bottom, [t] theme, [q] quit",
		m.layout+1, len(Layouts), Layouts[m.layout].Description,
	)) + "\n\n"

	switch Layouts[m.layout].ID {
	case gridLayout.ID:
		out += m.renderGrid(mx, st)
	default:
		out += m.renderList(mx, st)
	}
	return out
}

var titleStyle = lipgloss.NewStyle().Bold(true)

type chatStyles struct {
	dim, sel, unread, thought, toolFail, warn, errS, mention lipgloss.Style
}

func (m Model) styles() chatStyles {
	th := m.theme
	return chatStyles{
		dim:      lipgloss.NewStyle().Faint(true),
		sel:      lipgloss.NewStyle().Bold(true).Background(th.Color(theme.RoleSelectedBG)),
		unread:   lipgloss.NewStyle().Bold(true),
		thought:  lipgloss.NewStyle().Italic(true).Foreground(th.Color(theme.RoleThought)),
		toolFail: lipgloss.NewStyle().Foreground(th.Color(theme.RoleError)),
		warn:     lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleWarn)),
		errS:     lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleError)),
		// mention is agent-tui#114's own requirement made visible: "mentions
		// must be visible in the rendered transcript as mentions, not as
		// plain text that happens to start with @" -- see highlightMentions.
		mention: lipgloss.NewStyle().Bold(true).Foreground(th.Color(theme.RoleMention)),
	}
}

// highlightMentions re-renders every @-mention token (mentionPattern,
// mention.go) inside lines in st.mention -- agent-tui#114's own rendering
// requirement. Runs on every line RenderMessage produced, not just the
// first, because a mention can land anywhere a message's own text does
// (KindPlan's step text, for instance). Deliberately does not consult
// m.participants: a transcript can carry a message sent, or received, from
// a real source before this pane's own roster last refreshed -- refusing
// to highlight a mention merely because the roster is momentarily stale
// would repeat, at render time, the exact "quietly wrong" failure
// ValidateMentions exists to catch at compose time, where at least there is
// a gate to catch it with; here there is none, so this stays permissive.
func highlightMentions(lines []string, st chatStyles) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = mentionPattern.ReplaceAllStringFunc(l, func(tok string) string {
			return st.mention.Render(tok)
		})
	}
	return out
}

// colorizeMessage applies m's per-Kind theme role over RenderMessage's
// plain lines -- the same plain-text/colour split gallery's colorizeFlags
// documents. Only the first line of a multi-line block (KindPlan) carries
// the kind colour; plan steps render in the default style so the checkbox
// state (render.go's [x]/[ ]) stays the visible signal, not the colour.
func colorizeMessage(msg Message, lines []string, st chatStyles) []string {
	if len(lines) == 0 {
		return lines
	}
	out := make([]string, len(lines))
	copy(out, lines)
	switch msg.Kind {
	case KindThought:
		out[0] = st.thought.Render(out[0])
	case KindPermission:
		out[0] = st.warn.Render(out[0])
	case KindToolCall:
		if msg.ToolStatus == ToolFailed {
			out[0] = st.toolFail.Render(out[0])
		}
	}
	return out
}

// scrollIndicator renders a one-line "hidden content" marker for vp --
// the issue acceptance item this package is built around: a view with
// content past its edges must say so, never truncate silently (agent-tui#29).
func scrollIndicator(vp viewport.Model, st chatStyles) string {
	if vp.TotalLineCount() <= vp.Height {
		return ""
	}
	above, below := "", ""
	if !vp.AtTop() {
		above = "▲ more above "
	}
	if !vp.AtBottom() {
		below = "▼ more below "
	}
	pct := int(vp.ScrollPercent() * 100)
	return st.dim.Render(fmt.Sprintf("%s%s(%d%%)", above, below, pct))
}

// renderList composes listVP + transcriptVP against the EXACT line budget
// sync() sized them for -- one indicator row is always present (blank when
// nothing is hidden) rather than appended only when needed, because
// viewport.View() always returns exactly its own Height in lines: an
// indicator added on top of an already-full budget would make this
// pane's total line count exceed mx.bodyHeight, and lipgloss.Height()
// does not truncate an oversized render, it leaves it long -- which would
// silently push everything below (including the shell's own footer) down
// by one line instead of erroring. Fixed budget in, fixed budget out.
func (m Model) renderList(mx metrics, st chatStyles) string {
	contentHeight := mx.bodyHeight - composerHeight
	if contentHeight < 1 {
		contentHeight = 1
	}

	listInd := scrollIndicator(m.listVP, st)
	left := m.listVP.View() + "\n" + truncate(listInd, mx.listWidth)
	left = lipgloss.NewStyle().Width(mx.listWidth).Height(contentHeight).Render(left)

	var header string
	if m.selected >= 0 && m.selected < len(m.threads) {
		t := m.threads[m.selected]
		header = st.dim.Render(fmt.Sprintf("-- %s (%d messages) --", t.Title, len(t.Messages)))
	}
	transcriptInd := scrollIndicator(m.transcriptVP, st)
	right := header + "\n" + m.transcriptVP.View() + "\n" + truncate(transcriptInd, mx.transcriptW)
	right = lipgloss.NewStyle().Width(mx.transcriptW).Height(contentHeight).Render(right)

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
	return lipgloss.NewStyle().Width(mx.width).Height(mx.bodyHeight).Render(body + "\n" + m.renderComposer(mx, st))
}

// renderComposer is the composer's own two rows (composerHeight): the
// input line, and a status line whose content depends on state. The
// status line is SPEC-shell.md S7's three-state requirement made visible
// -- in flight, failed (with its error), and unknown (with its own,
// differently-styled text) are three distinct branches below, never
// collapsed into one "something went wrong" line. Always exactly two
// rows, composing or not (see composerHeight's own doc comment).
func (m Model) renderComposer(mx metrics, st chatStyles) string {
	canCompose := m.selected >= 0 && m.selected < len(m.threads) && m.threads[m.selected].ID != "all"

	input := st.dim.Render(truncate("> (press [i] to compose)", mx.width))
	if m.composing {
		input = truncate(m.composer.View(), mx.width)
	}

	var status string
	switch {
	case m.sendOutcome == sendInFlight:
		status = st.dim.Render(truncate("sending... (delivery not yet confirmed)", mx.width))
	case m.sendOutcome == sendUnknown:
		// Deliberately not st.errS (failed's own style) and deliberately
		// says "unknown", not "failed" -- SPEC-shell.md S7's own rule: "a
		// message whose fate is unknown must not render as either
		// delivered or failed."
		status = st.warn.Render(truncate("? unknown -- "+m.sendErr, mx.width))
	case m.sendOutcome == sendFailed:
		status = st.errS.Render(truncate("! "+m.sendErr, mx.width))
	case m.composing:
		status = st.dim.Render("[enter] send  [esc] cancel")
	case !canCompose:
		status = st.dim.Render("[i] compose -- select a real thread first")
	default:
		status = st.dim.Render("[i] compose a message")
	}

	return lipgloss.NewStyle().Width(mx.width).Height(1).Render(input) + "\n" +
		lipgloss.NewStyle().Width(mx.width).Height(1).Render(status)
}

func (m Model) renderGrid(mx metrics, st chatStyles) string {
	if m.focused >= 0 && m.focused < len(m.threads) {
		return m.renderFocused(mx, st)
	}

	n := len(m.threads)
	if n == 0 {
		return st.dim.Render("(no threads)")
	}
	cols := n
	if cols > 3 {
		cols = 3
	}
	rows := (n + cols - 1) / cols
	paneWidth := mx.width/cols - 1
	if paneWidth < 16 {
		paneWidth = 16
	}
	paneHeight := mx.bodyHeight/rows - 1
	if paneHeight < 4 {
		paneHeight = 4
	}

	var out string
	for r := 0; r < rows; r++ {
		var line string
		for c := 0; c < cols; c++ {
			idx := r*cols + c
			if idx >= n {
				continue
			}
			line = lipgloss.JoinHorizontal(lipgloss.Top, line, m.renderPane(m.threads[idx], idx, paneWidth, paneHeight, st))
		}
		out += line + "\n"
	}
	return out
}

// renderPane is one gridLayout tile -- a live tail of the thread's most
// recent messages. When there are more messages than the tile can show,
// a "N more above -- [f] to focus" marker replaces the oldest visible
// line rather than the tile just silently having fewer lines than a
// thread with less history: the reachability path is "f" (renderFocused,
// backed by the same scrollable transcriptVP listLayout uses), and the
// marker is what tells a user that path exists at all.
func (m Model) renderPane(t Thread, idx, width, height int, st chatStyles) string {
	body := st.dim.Render(truncate(t.Title, width)) + "\n"
	avail := height - 2
	if avail < 1 {
		avail = 1
	}
	msgs := t.Messages
	hidden := 0
	if len(msgs) > avail {
		shown := avail - 1 // reserve one line for the "N more" marker
		if shown < 0 {
			shown = 0
		}
		hidden = len(msgs) - shown
		msgs = msgs[len(msgs)-shown:]
	}
	if hidden > 0 {
		body += st.dim.Render(fmt.Sprintf("▲ %d more -- [f] to focus", hidden)) + "\n"
	}
	for _, msg := range msgs {
		body += truncate(summarize(msg), width) + "\n"
	}
	style := lipgloss.NewStyle().Width(width).Height(height).Border(lipgloss.NormalBorder())
	if idx == m.selected {
		style = style.BorderForeground(m.theme.Color(theme.RoleDirector))
	}
	return style.Render(body)
}

// renderFocused is gridLayout's "f"-toggled reading -- layout [4]'s
// "focus + peripherals" collapsed into this same layout, per layouts.go's
// doc comment: one large, scrollable pane (transcriptVP, same as
// listLayout's) plus a compact strip of the other threads.
func (m Model) renderFocused(mx metrics, st chatStyles) string {
	mainHeight := mx.focusedMainHeight(len(m.threads))

	focused := m.threads[m.focused]
	ind := scrollIndicator(m.transcriptVP, st)
	main := st.dim.Render(fmt.Sprintf("-- focused: %s --", focused.Title)) + "\n" +
		m.transcriptVP.View() + "\n" + truncate(ind, mx.width)

	var strip string
	for i, t := range m.threads {
		if i == m.focused {
			continue
		}
		strip += truncate(RenderThreadRow(t, m.fetched), mx.width) + "\n"
	}

	return lipgloss.NewStyle().Width(mx.width).Height(mainHeight).Render(main) + "\n" +
		st.dim.Render("-- peripherals --") + "\n" + strip
}
