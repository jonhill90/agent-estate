// Package shell is agent-tui#38's application shell -- the ONE root Bubble
// Tea model cmd/agent-tui now runs, replacing the four mutually exclusive
// tea.NewProgram call sites (rail-or-board-or-cost-or-gallery) that made
// this four separate programs rather than one application. Model owns a
// persistent left rail (internal/rail, unchanged) and a content pane that
// holds board/cost/gallery -- also unchanged packages, mounted here rather
// than rewritten. Nothing in board/cost/gallery/rail's own render or key
// logic moves; this package only composes them and routes tea.Msg between
// them, the same "views become panes, not programs" framing issue #38 asks
// for.
package shell

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/cost"
	"github.com/jonhill90/keelson/internal/flow"
	"github.com/jonhill90/keelson/internal/gallery"
	"github.com/jonhill90/keelson/internal/rail"
	"github.com/jonhill90/keelson/internal/theme"
)

// Pane names which model currently occupies the content area. paneHome is
// the default -- no view has been chosen yet, exactly what a bare "no
// flags" run rendered before #38 (a rail with nothing beside it), except
// that space is now reachable rather than permanently blank.
type Pane int

const (
	PaneHome Pane = iota
	PaneBoard
	PaneCost
	PaneGallery
	PaneFlow
)

// focus names which region the keyboard currently drives -- the rail
// (lane selection, glyph/grouping/reading pickers, session ops) or
// whichever pane is mounted in the content area. Exactly one is true at a
// time; toggled with [tab].
type focus int

const (
	focusRail focus = iota
	focusContent
)

// footerHeight is the one row Model reserves at the bottom of the terminal
// for its own nav legend (footer() below) -- rail and every content pane
// are sized to height-footerHeight, never the raw terminal height, so the
// legend never pushes the last line of real content off screen or wraps
// the terminal.
const footerHeight = 1

// Model is the application shell's root Bubble Tea model -- the single
// tea.NewProgram cmd/agent-tui now constructs. It owns one of each pane
// package's Model by value (Bubble Tea's usual immutable-update-returns-a-
// copy shape, same as every model it composes) and forwards tea.Msg to
// whichever one(s) need it: KeyMsg goes to whichever region has focus
// (routeKey), everything else (ticks, fetch results) goes to every
// pane unconditionally (routeAll) so each keeps refreshing in the
// background whether or not it is currently visible -- switching to a
// pane must show live data immediately, not a fetch that only started on
// the keypress that revealed it.
type Model struct {
	rail    rail.Model
	board   board.Model
	cost    cost.Model
	gallery gallery.Model
	flow    flow.Model

	// boardOK is false when cmd/agent-tui had no -ledger to build a real
	// board.Fetcher from -- board.go's own -board flag still refuses to
	// START on the board with no ledger (unchanged), but the shell's board
	// pane is otherwise reachable by navigation alone, so it needs its own
	// guard: selecting it with boardOK == false renders unavailableView
	// instead of an unfetchable board.Model that would sit on a permanent
	// fetch error. internal/flow reads the exact same board.Snapshot --
	// routeAll/resize push m.board.Snapshot() into flow.Model.WithSnapshot
	// after every board.Update, so flow runs no Fetcher of its own -- so
	// this single flag also gates PaneFlow; there is no separate flowOK
	// because there is no separate data dependency to fail independently
	// of the board's.
	boardOK          bool
	boardUnavailable string

	active Pane
	focus  focus

	width, height                          int
	railWidth, contentWidth, contentHeight int

	// theme is the single shared value agent-tui#51 fixes -- see
	// theme.CycleRequestedMsg's doc comment for the defect this
	// replaces: board/cost/gallery/rail (agent-tui#48) each held their
	// OWN in-memory theme.Theme, so one 't' keypress only ever changed
	// whichever ONE pane had focus, leaving the rail and the active
	// content pane able to disagree on screen simultaneously. Model is
	// the one place that composes every pane (agent-tui#38), so it is
	// the one place that now owns this value; every pane's own theme
	// field is kept in sync via WithTheme, called from every place this
	// value changes (New, WithTheme, and the CycleRequestedMsg case in
	// Update).
	theme theme.Theme
	// themeNotice is the same notice string cmd/agent-tui's theme.Load
	// already computed once and handed to every pane's own WithTheme
	// call (main.go: "every screen gets the SAME theme" -- the same is
	// true of the notice); Model keeps its own copy so a runtime 't'
	// cycle can re-apply WithTheme to every pane without losing the
	// startup notice, which none of the four pane packages expose a
	// getter for.
	themeNotice string
	// saveTheme is agent-tui#51's persistence half -- the adapter-
	// discipline seam (AGENTS.md) over theme.Save, wired by
	// cmd/agent-tui's main.go to theme.Save(theme.ConfigPath(), ...) so
	// this package never touches the filesystem itself and every test
	// in this package can inject a fake that only records what would
	// have been written. nil is a valid, silent no-op -- every Model
	// built by New has it unset until WithThemeSave is called, so a
	// test with no opinion on persistence (most of the pre-existing
	// navigation tests in this package) is unaffected.
	saveTheme func(theme.Theme) error
	// themeSaveErr is the visible half of "absence is a typed value,
	// never a bare zero" (AGENTS.md) applied to a failed persist: a
	// theme.Save error (e.g. an unwritable config directory) must not
	// be swallowed just because the in-memory cycle it rides along with
	// always succeeds. Empty means either no save has been attempted
	// yet or the last one succeeded; footer() renders it next to the
	// theme name exactly like board/cost/gallery already render their
	// own themeNotice.
	themeSaveErr string
}

// New builds a Model from the four already-constructed pane models --
// cmd/agent-tui wires each one's Fetcher/WithOps/etc. exactly as it did
// before #38; this constructor changes none of that, it only holds the
// results. Theme is the one exception (agent-tui#51): callers no longer
// call WithTheme on each pane themselves -- New defaults every pane to
// whatever theme it already carries (theme.Default, same as a freshly
// constructed pane always has) until the caller's own WithTheme call
// fans the real starting theme out via applyTheme. Pass boardOK == false
// (with a reason) when no -ledger was available to build a real
// board.Fetcher; board stays reachable by key but renders unavailableView
// instead of running board's own fetch loop.
func New(r rail.Model, b board.Model, boardOK bool, boardUnavailable string, c cost.Model, g gallery.Model, fl flow.Model) Model {
	return Model{
		rail:             r,
		board:            b,
		boardOK:          boardOK,
		boardUnavailable: boardUnavailable,
		cost:             c,
		gallery:          g,
		flow:             fl,
		active:           PaneHome,
		focus:            focusRail,
		theme:            theme.Default,
	}
}

// WithStart returns a copy of m starting on p instead of PaneHome -- how
// cmd/agent-tui's -board/-cost/-gallery flags now express "start on this
// view" (agent-tui#38 acceptance: they stop being the ONLY way to reach it,
// but still choose where the app opens).
func (m Model) WithStart(p Pane) Model {
	m.active = p
	return m
}

// WithTheme returns a copy of m with th and notice wired in as the ONE
// shared theme value -- agent-tui#51: unlike before #51, this is no
// longer additive-only chrome for the shell's own render (the footer
// legend and the board-unavailable notice); it is now also pushed into
// every pane via applyTheme, replacing the pane-by-pane WithTheme calls
// cmd/agent-tui used to make directly (see main.go's own comment at the
// call site). notice is theme.Load's own return, the same string every
// pane was already given -- kept here too so a later runtime cycle
// (CycleRequestedMsg) can re-apply it without cmd/agent-tui's Load ever
// running twice.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m.applyTheme()
}

// WithThemeSave wires save in as the persistence seam theme.Save sits
// behind (adapter discipline, AGENTS.md) -- cmd/agent-tui's main.go passes
// a closure over theme.Save(theme.ConfigPath(), ...); every test in this
// package that does not call this leaves saveTheme nil, which Update
// treats as "nothing to persist," never a panic.
func (m Model) WithThemeSave(save func(theme.Theme) error) Model {
	m.saveTheme = save
	return m
}

// applyTheme pushes m's current theme/themeNotice into every pane's own
// WithTheme -- the one place that fans the single shared value out to all
// four, called from both WithTheme (construction/startup) and the
// CycleRequestedMsg case in Update (a runtime 't' press), so those two
// call sites cannot drift out of sync with each other.
func (m Model) applyTheme() Model {
	m.rail = m.rail.WithTheme(m.theme, m.themeNotice)
	m.board = m.board.WithTheme(m.theme, m.themeNotice)
	m.cost = m.cost.WithTheme(m.theme, m.themeNotice)
	m.gallery = m.gallery.WithTheme(m.theme, m.themeNotice)
	m.flow = m.flow.WithTheme(m.theme, m.themeNotice)
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.rail.Init(), m.cost.Init(), m.gallery.Init()}
	if m.boardOK {
		cmds = append(cmds, m.board.Init(), m.flow.Init())
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.resize(msg)

	case theme.CycleRequestedMsg:
		// Agent-tui#51: the ONE place a 't' keypress actually takes
		// effect, however it arrived -- rail's opsMode-gated case or
		// board/cost/gallery's plain one, all of which now only ask for
		// this message rather than cycling their own theme field (see
		// theme.CycleRequestedMsg's doc comment). Cycling here, once,
		// and re-applying via applyTheme is what makes a single
		// keypress repaint the rail AND the active content pane
		// together -- the defect #48's four-owner diff could not fix
		// even after a rebase.
		return m.cycleTheme()

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			// A universal escape hatch, not routed through any pane: the
			// trap agent-tui#22 shipped (a mode that swallowed every key,
			// quit included) must never be able to recur just because a
			// future pane or ops-mode adds a new key-swallowing state.
			// Plain "q" is deliberately NOT caught here -- rail's own
			// opsModeAdding must still be free to accept a literal 'q' as
			// part of a typed session name (see routeKey/handleOpsKey);
			// every pane already quits on bare "q" itself when idle.
			return m, tea.Quit
		case "tab":
			m.focus = toggleFocus(m.focus)
			return m, nil
		case "f1":
			m.active = PaneHome
			return m, nil
		case "f2":
			m.active = PaneBoard
			return m, nil
		case "f3":
			m.active = PaneCost
			return m, nil
		case "f4":
			m.active = PaneGallery
			return m, nil
		case "f5":
			m.active = PaneFlow
			return m, nil
		}
		return m.routeKey(msg)
	}
	return m.routeAll(msg)
}

// cycleTheme advances m.theme by one (theme.Cycle, the same wrap-around
// logic every pane used to run against its own copy), persists it via
// saveTheme when one is wired in, and re-applies it to every pane via
// applyTheme -- the single write, single fan-out this issue asks for. A
// save failure is recorded in themeSaveErr rather than silently dropped
// (AGENTS.md's "absence is a typed value" convention) but never blocks
// the in-memory cycle itself or the panes' repaint: a user comparing
// themes live must not be stopped by an unwritable config directory,
// only told about it.
func (m Model) cycleTheme() (Model, tea.Cmd) {
	m.theme = theme.Cycle(m.theme)
	m.themeSaveErr = ""
	if m.saveTheme != nil {
		if err := m.saveTheme(m.theme); err != nil {
			m.themeSaveErr = err.Error()
		}
	}
	return m.applyTheme(), nil
}

func toggleFocus(f focus) focus {
	if f == focusRail {
		return focusContent
	}
	return focusRail
}

// routeKey sends a KeyMsg to whichever single region has focus -- the rail,
// or the currently active content pane. Every pane already quits on its own
// "q"/"ctrl+c" (agent-tui#22's trap applies here too: routing, not
// intercepting, is what keeps that true for a pane this package did not
// write). PaneHome and an unavailable PaneBoard have no live sub-model to
// route to; homeKey below covers both.
func (m Model) routeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.focus == focusRail {
		next, cmd := m.rail.Update(msg)
		m.rail = next.(rail.Model)
		return m, cmd
	}
	switch m.active {
	case PaneBoard:
		if !m.boardOK {
			return m.homeKey(msg)
		}
		next, cmd := m.board.Update(msg)
		m.board = next.(board.Model)
		return m, cmd
	case PaneCost:
		next, cmd := m.cost.Update(msg)
		m.cost = next.(cost.Model)
		return m, cmd
	case PaneGallery:
		next, cmd := m.gallery.Update(msg)
		m.gallery = next.(gallery.Model)
		return m, cmd
	case PaneFlow:
		if !m.boardOK {
			return m.homeKey(msg)
		}
		next, cmd := m.flow.Update(msg)
		m.flow = next.(flow.Model)
		return m, cmd
	default:
		return m.homeKey(msg)
	}
}

// homeKey is the key handling for PaneHome and an unavailable PaneBoard --
// neither has a live sub-model to route to, so "q" must be handled here
// directly rather than silently doing nothing (agent-tui#38's "q/ctrl+c
// quits from every pane" acceptance item applies to this placeholder pane
// too, not just the four real ones).
func (m Model) homeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if msg.String() == "q" {
		return m, tea.Quit
	}
	return m, nil
}

// routeAll forwards any non-key, non-resize message (ticks, fetch results)
// to every pane unconditionally, focused or not, visible or not -- each
// pane's own Update type-switches on its own unexported message types and
// no-ops on everything else, the standard Bubble Tea parent-composes-
// children shape. This is what keeps every pane's background refresh loop
// (rail's tick/refresh, cost's 5-minute poll, board's own interval) running
// while a different pane is on screen, so switching to it shows data that
// was already fresh rather than a blank first frame.
func (m Model) routeAll(msg tea.Msg) (Model, tea.Cmd) {
	var cmds []tea.Cmd

	next, cmd := m.rail.Update(msg)
	m.rail = next.(rail.Model)
	cmds = append(cmds, cmd)

	if m.boardOK {
		next, cmd := m.board.Update(msg)
		m.board = next.(board.Model)
		cmds = append(cmds, cmd)

		// flow.Model.Update still needs msg itself (its own motionMsg
		// tick), but its DATA comes from board.Model, never a fetch of
		// its own -- see flow.Model.WithSnapshot's doc comment. Syncing
		// after every board.Update, unconditionally, is what makes flow
		// current even while some OTHER pane is on screen, the same
		// "background refresh while not visible" property every other
		// pane already has (routeAll's own doc comment above).
		next, cmd = m.flow.Update(msg)
		m.flow = next.(flow.Model)
		cmds = append(cmds, cmd)
		m.flow = m.flow.WithSnapshot(m.board.Snapshot(), m.board.LastFetched(), m.board.FetchErr())
	}

	next, cmd = m.cost.Update(msg)
	m.cost = next.(cost.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.gallery.Update(msg)
	m.gallery = next.(gallery.Model)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// resize is the fix for agent-tui#38's named trap: every mounted view
// assumed it owned the whole terminal, so a WindowSizeMsg carrying the
// terminal's own dimensions broke every pane it hit. Here the rail is sized
// first, to its own fixed rail.RailWidth; its ACTUAL rendered width (border
// and padding included) is then measured with lipgloss.Width rather than
// recomputed by hand, so this file never has to track rail's internal
// padding/border arithmetic to stay correct. Whatever terminal width is
// left over -- never the raw terminal width -- is what every content pane
// is sized to, whether it is the one currently visible or not (see
// routeAll's doc comment for why every pane is kept live).
func (m Model) resize(msg tea.WindowSizeMsg) (Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	innerHeight := m.height - footerHeight
	if innerHeight < 0 {
		innerHeight = 0
	}

	var cmds []tea.Cmd

	next, cmd := m.rail.Update(tea.WindowSizeMsg{Width: rail.RailWidth, Height: innerHeight})
	m.rail = next.(rail.Model)
	cmds = append(cmds, cmd)

	m.railWidth = lipgloss.Width(m.rail.View())
	m.contentWidth = m.width - m.railWidth
	if m.contentWidth < 0 {
		m.contentWidth = 0
	}
	m.contentHeight = innerHeight

	contentSize := tea.WindowSizeMsg{Width: m.contentWidth, Height: innerHeight}

	if m.boardOK {
		next, cmd := m.board.Update(contentSize)
		m.board = next.(board.Model)
		cmds = append(cmds, cmd)

		next, cmd = m.flow.Update(contentSize)
		m.flow = next.(flow.Model)
		cmds = append(cmds, cmd)
		m.flow = m.flow.WithSnapshot(m.board.Snapshot(), m.board.LastFetched(), m.board.FetchErr())
	}

	next, cmd = m.cost.Update(contentSize)
	m.cost = next.(cost.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.gallery.Update(contentSize)
	m.gallery = next.(gallery.Model)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// View joins the rail, the active content pane, and the shell's own footer
// -- clampHeight is load-bearing here, not cosmetic: a pane package that
// renders one line taller than the height it was GIVEN (a trailing "\n" on
// its last output line is enough) would otherwise push m.footer() past the
// bottom of the terminal, since this whole composed string is one
// tea.WithAltScreen() frame with no scrollback to fall back on. The rail
// and every content pane already receive an exact height budget in
// resize(); View() is where that budget is actually enforced, once, rather
// than trusted to every pane's own internal line arithmetic (gallery's
// View() was measured to overrun its budget by exactly one blank trailing
// line -- see this package's teatest coverage).
func (m Model) View() string {
	left := clampHeight(m.rail.View(), m.contentHeight)
	right := clampHeight(m.contentView(), m.contentHeight)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return lipgloss.JoinVertical(lipgloss.Left, body, m.footer())
}

// clampHeight forces s to exactly n lines: extra trailing lines are
// dropped, a short render is padded with blank lines. n <= 0 (no resize
// message received yet, e.g. teatest's very first frame) passes s through
// unchanged rather than emptying it -- clamping to a height that has not
// been measured yet would be a guess, not an enforcement.
func clampHeight(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		return strings.Join(lines[:n], "\n")
	}
	for len(lines) < n {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) contentView() string {
	switch m.active {
	case PaneBoard:
		if !m.boardOK {
			return m.unavailableView()
		}
		return m.board.View()
	case PaneCost:
		return m.cost.View()
	case PaneGallery:
		return m.gallery.View()
	case PaneFlow:
		if !m.boardOK {
			return m.unavailableView()
		}
		return m.flow.View()
	default:
		return m.homeView()
	}
}

// legendStyle is Faint-only, deliberately: every OTHER style in this
// package's footer/home/unavailable views is the same, matching how
// rail/board/cost's own legend lines are drawn (Faint, no explicit
// colour) so the shell's chrome does not introduce a literal colour
// agent-tui#27/#36 already moved out of every other render path (the "do
// not restyle" trap named in this issue).
var legendStyle = lipgloss.NewStyle().Faint(true)

// footer is the shell's one persistent line of chrome: the nav keys that
// exist now that there is more than one pane to move between. No existing
// pane package renders this -- adding it there would be the rewrite this
// issue explicitly says not to do -- so it lives here, the one place new
// enough to own it.
func (m Model) footer() string {
	focusName := "rail"
	if m.focus == focusContent {
		focusName = "content"
	}
	line := "[tab] focus:" + focusName + "  [f1] home [f2] board [f3] cost [f4] gallery [f5] flow  [q/ctrl+c] quit"
	// themeSaveErr (agent-tui#51) is folded onto this same line rather
	// than given its own -- footerHeight is a fixed one row every pane's
	// own size budget is computed against (see resize()), so a second
	// line here would silently shrink every pane by one row rather than
	// actually reporting the failure.
	if m.themeSaveErr != "" {
		line += "  ! theme not saved: " + m.themeSaveErr
	}
	return legendStyle.Width(m.width).Render(truncate(line, m.width))
}

func (m Model) homeView() string {
	lines := []string{
		"agent-tui",
		"",
		"[f2] board    view the task board (agent-tui#6)",
		"[f3] cost     view the cost panel (agent-tui#4)",
		"[f4] gallery  view the glyph gallery (agent-tui#11)",
		"[f5] flow     watch work move through the pipeline (agent-tui#64)",
		"[tab] move focus into the rail on the left",
	}
	return legendStyle.Width(m.contentWidth).Height(m.contentHeight).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)
}

// unavailableView renders in PaneBoard's place when cmd/agent-tui had no
// -ledger to build a real board.Fetcher from. -board itself still refuses
// to START this way (unchanged, main.go's own check) -- this is only for
// reaching the board by navigation from some OTHER starting view, which
// #38 makes possible for the first time.
func (m Model) unavailableView() string {
	errStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleError))
	lines := []string{
		errStyle.Render("! board unavailable"),
		m.boardUnavailable,
	}
	return legendStyle.Width(m.contentWidth).Height(m.contentHeight).Render(
		lipgloss.JoinVertical(lipgloss.Left, lines...),
	)
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
