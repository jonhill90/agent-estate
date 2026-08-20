package chat

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/theme"
)

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

	theme       theme.Theme
	themeNotice string
}

// New builds a Model bound to source. It fetches once at Init and does not
// re-fetch on a timer yet -- a live Source needs push-driven updates
// (session/update is a stream, not a poll) rather than the refresh-on-tick
// shape rail/board use for MCP reads; see fixture.go for what stands in
// for that today.
func New(source Source) Model {
	return Model{
		source:       source,
		theme:        theme.Default,
		focused:      -1,
		transcriptVP: viewport.New(0, 0),
		listVP:       viewport.New(0, 0),
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

type fetchMsg struct {
	threads []Thread
	err     error
}

func (m Model) fetchCmd() tea.Cmd {
	src := m.source
	return func() tea.Msg {
		threads, err := src.Threads()
		return fetchMsg{threads: threads, err: err}
	}
}

func (m Model) Init() tea.Cmd { return m.fetchCmd() }

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
	// #29's untruncated board.
	m.listVP.Width = mx.listWidth
	m.listVP.Height = mx.bodyHeight - 1
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
	height := mx.bodyHeight - 2
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
			lines = append(lines, colorizeMessage(msg, RenderMessage(msg), st)...)
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

	out += st.dim.Render(fmt.Sprintf(
		"layout %d/%d: %s -- [j/k] move, [1-9] jump, [v] switch layout, [f] focus (grid), "+
			"[pgup/pgdn] scroll, [home/end] top/bottom, [t] theme, [q] quit",
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
	dim, sel, unread, thought, toolFail, warn, errS lipgloss.Style
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
	}
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
	listInd := scrollIndicator(m.listVP, st)
	left := m.listVP.View() + "\n" + truncate(listInd, mx.listWidth)
	left = lipgloss.NewStyle().Width(mx.listWidth).Height(mx.bodyHeight).Render(left)

	var header string
	if m.selected >= 0 && m.selected < len(m.threads) {
		t := m.threads[m.selected]
		header = st.dim.Render(fmt.Sprintf("-- %s (%d messages) --", t.Title, len(t.Messages)))
	}
	transcriptInd := scrollIndicator(m.transcriptVP, st)
	right := header + "\n" + m.transcriptVP.View() + "\n" + truncate(transcriptInd, mx.transcriptW)
	right = lipgloss.NewStyle().Width(mx.transcriptW).Height(mx.bodyHeight).Render(right)

	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", right)
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
