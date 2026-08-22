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

	"github.com/jonhill90/keelson/internal/admin"
	"github.com/jonhill90/keelson/internal/agents"
	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/chat"
	"github.com/jonhill90/keelson/internal/connectors"
	"github.com/jonhill90/keelson/internal/cost"
	"github.com/jonhill90/keelson/internal/flow"
	"github.com/jonhill90/keelson/internal/gallery"
	"github.com/jonhill90/keelson/internal/mcpservers"
	"github.com/jonhill90/keelson/internal/nav"
	"github.com/jonhill90/keelson/internal/rail"
	"github.com/jonhill90/keelson/internal/skills"
	"github.com/jonhill90/keelson/internal/stub"
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
	PaneChat
	// PaneLanes is SPEC-shell.md S3/S4: the lane rail (internal/rail),
	// reached as the nav sidebar's "Lanes" route instead of always
	// occupying the left column -- see this file's own doc comment for why
	// that changed. rail.Model's own render/key logic is unchanged; only
	// its screen position moved.
	PaneLanes
	// PaneStub renders any nav route S3/S4 has not wired to a real pane --
	// internal/stub.View (S5) over m.nav.ActiveItem()'s Label and
	// internal/stub.Descriptions.
	PaneStub
	// PaneAgents, PaneSkills, PaneMCPServers, PaneConnectors and PaneAdmin
	// are S6/S8/S9/S10/S11's own panes, each shipped standalone (its own
	// PR, its own teatest drive) before this change wires any of them into
	// a route -- see routeToPane's own doc comment for which nav route id
	// reaches each one.
	PaneAgents
	PaneSkills
	PaneMCPServers
	PaneConnectors
	PaneAdmin
)

// focus names which region the keyboard currently drives -- the nav
// sidebar (↑/↓/Enter/→/←, the icons-only toggle) or whichever pane is
// mounted in the content area. Exactly one is true at a time; toggled with
// [tab]. Named focusRail for agent-tui#38's original rail-vs-content split;
// kept unrenamed here even though the sidebar it now drives is
// internal/nav, not internal/rail -- see routeKey's own doc comment.
type focus int

const (
	focusRail focus = iota
	focusContent
)

// routeToPane maps a nav route ID (internal/nav.Item.ID) to the Pane that
// already renders it for real -- SPEC-shell.md S4's three ("Tasks" ->
// board, "Usage" -> cost, "Lanes" -> rail) plus "home" and "chat", which
// already had working panes before S3 existed (PaneHome, PaneChat) and
// cost nothing extra to wire the same way. Every OTHER route in
// nav.Build()'s tree has no entry here and falls through to PaneStub.
// This mapping grew with S6/S8/S9/S10/S11: "agents" -> PaneAgents (S6),
// "skills" -> PaneSkills (S8), "mcp-servers" -> PaneMCPServers (S9),
// "connections" -> PaneConnectors (S10). S11's own five nav leaves
// ("admin-services", "admin-profiles", "admin-users", "dependencies",
// "settings") all map to the SAME PaneAdmin -- internal/admin.Model
// renders all five of S11's named sections in one view rather than a
// fifth of a view per route, so whichever admin-group item is confirmed,
// the content pane shows the one real admin.Model, not five separate
// screens for a section split that exists on the web only.
var routeToPane = map[string]Pane{
	"home":           PaneHome,
	"tasks":          PaneBoard,
	"usage":          PaneCost,
	"lanes":          PaneLanes,
	"chat":           PaneChat,
	"agents":         PaneAgents,
	"skills":         PaneSkills,
	"mcp-servers":    PaneMCPServers,
	"connections":    PaneConnectors,
	"admin-services": PaneAdmin,
	"admin-profiles": PaneAdmin,
	"admin-users":    PaneAdmin,
	"dependencies":   PaneAdmin,
	"settings":       PaneAdmin,
}

// paneToRoute is routeToPane's inverse, used to keep the nav sidebar's own
// highlight in sync when a Pane is chosen some way OTHER than confirming a
// nav row -- the -board/-cost startup flags (WithStart) and the legacy
// f1-f6 keys both bypass m.nav entirely, so without this the sidebar could
// show "Home" highlighted while the content pane is actually Board.
// PaneGallery and PaneFlow have no entry: neither is in nav.Build()'s tree
// (agent-tui#64's flow and the glyph gallery predate SPEC-shell.md and are
// not part of the hill90 nav this spec mirrors), so reaching them via their
// own f4/f5 keys leaves the sidebar's highlight exactly where it was --
// documented, not silently wrong, until a future item decides whether they
// get a route of their own.
// PaneAgents/PaneSkills/PaneMCPServers/PaneConnectors/PaneAdmin have no
// entry here, the same reason PaneGallery/PaneFlow don't: nothing bypasses
// the nav sidebar to reach them (no f-key, no WithStart case) -- selecting
// one always goes through routeNavKey, which sets the sidebar's own
// active route directly before consulting routeToPane, so there is no
// "highlight could disagree with content" case for this five to guard
// against yet.
var paneToRoute = map[Pane]string{
	PaneHome:  "home",
	PaneBoard: "tasks",
	PaneCost:  "usage",
	PaneLanes: "lanes",
	PaneChat:  "chat",
}

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
	// navCursor is the sidebar cursor. It lives here, not in nav.Model,
	// because #73's nav owns no cursor by design (see its Update doc).
	navCursor int

	// nav is SPEC-shell.md S3's app shell change: the persistent left
	// column is now the hill90-mirrored sidebar (internal/nav, S1/S2), not
	// internal/rail. rail.Model is unchanged and still held here -- it is
	// simply no longer rendered unconditionally; see PaneLanes.
	nav     nav.Model
	rail    rail.Model
	board   board.Model
	cost    cost.Model
	gallery gallery.Model
	flow    flow.Model
	chat    chat.Model

	// agents/skills/mcpservers/connectors/admin are S6/S8/S9/S10/S11's
	// panes, wired in via the With* methods below rather than New's own
	// parameter list -- New already has eight positional parameters, and
	// every one of these five is optional in exactly the way saveTheme/
	// TaskFetcher already are elsewhere in this struct: a zero-value
	// pane.Model has a nil Fetcher, whose own Init/Update already treat
	// that as "nothing to fetch" (each package's own doFetch nil-check),
	// so an un-wired pane still renders (an empty list, not a panic) --
	// wiring is additive, never required for the shell to build or run.
	agents     agents.Model
	skills     skills.Model
	mcpservers mcpservers.Model
	connectors connectors.Model
	admin      admin.Model

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

	width, height                         int
	navWidth, contentWidth, contentHeight int

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
func New(r rail.Model, b board.Model, boardOK bool, boardUnavailable string, c cost.Model, g gallery.Model, fl flow.Model, ch chat.Model) Model {
	return Model{
		nav:              nav.New(),
		rail:             r,
		board:            b,
		boardOK:          boardOK,
		boardUnavailable: boardUnavailable,
		cost:             c,
		gallery:          g,
		flow:             fl,
		chat:             ch,
		active:           PaneHome,
		focus:            focusRail,
		theme:            theme.Default,
	}
}

// WithStart returns a copy of m starting on p instead of PaneHome -- how
// cmd/agent-tui's -board/-cost/-gallery flags now express "start on this
// view" (agent-tui#38 acceptance: they stop being the ONLY way to reach it,
// but still choose where the app opens). Also syncs the nav sidebar's own
// highlight via paneToRoute (SPEC-shell.md S3) so a -board/-cost launch
// shows "Tasks"/"Usage" already selected in the sidebar, not "Home" beside
// a content pane that disagrees with it.
func (m Model) WithStart(p Pane) Model {
	m.active = p
	if route, ok := paneToRoute[p]; ok {
		m.nav = m.nav.WithActive(route)
	}
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

// WithAgents/WithSkills/WithMCPServers/WithConnectors/WithAdmin wire
// S6/S8/S9/S10/S11's own panes in, each already built standalone by its
// own item -- cmd/keelson passes an already-constructed pane.Model
// exactly as it does for board/cost/gallery/flow/chat via New, just
// through a With* method instead of New's own parameter list (New's
// signature is deliberately left unchanged; see Model's own struct doc
// comment for why these five are optional).
func (m Model) WithAgents(a agents.Model) Model {
	m.agents = a
	return m
}

func (m Model) WithSkills(s skills.Model) Model {
	m.skills = s
	return m
}

func (m Model) WithMCPServers(s mcpservers.Model) Model {
	m.mcpservers = s
	return m
}

func (m Model) WithConnectors(c connectors.Model) Model {
	m.connectors = c
	return m
}

func (m Model) WithAdmin(a admin.Model) Model {
	m.admin = a
	return m
}

// applyTheme pushes m's current theme/themeNotice into every pane's own
// WithTheme -- the one place that fans the single shared value out to all
// four, called from both WithTheme (construction/startup) and the
// CycleRequestedMsg case in Update (a runtime 't' press), so those two
// call sites cannot drift out of sync with each other.
func (m Model) applyTheme() Model {
	m.nav = m.nav.WithTheme(m.theme, m.themeNotice)
	m.rail = m.rail.WithTheme(m.theme, m.themeNotice)
	m.board = m.board.WithTheme(m.theme, m.themeNotice)
	m.cost = m.cost.WithTheme(m.theme, m.themeNotice)
	m.gallery = m.gallery.WithTheme(m.theme, m.themeNotice)
	m.flow = m.flow.WithTheme(m.theme, m.themeNotice)
	m.chat = m.chat.WithTheme(m.theme, m.themeNotice)
	m.agents = m.agents.WithTheme(m.theme, m.themeNotice)
	m.skills = m.skills.WithTheme(m.theme, m.themeNotice)
	m.mcpservers = m.mcpservers.WithTheme(m.theme, m.themeNotice)
	m.connectors = m.connectors.WithTheme(m.theme, m.themeNotice)
	m.admin = m.admin.WithTheme(m.theme, m.themeNotice)
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.nav.Init(), m.rail.Init(), m.cost.Init(), m.gallery.Init(), m.chat.Init(),
		m.agents.Init(), m.skills.Init(), m.mcpservers.Init(), m.connectors.Init(), m.admin.Init(),
	}
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
		// f1-f6 are agent-tui#38's pre-nav keys, kept working unchanged
		// (nothing that scripts them may break, SPEC-shell.md S3) --
		// syncPane below is the same route<->Pane switch Confirm below
		// drives, called here too so the sidebar's own highlight does not
		// go stale the moment one of these bypasses it.
		case "f1":
			return m.syncPane(PaneHome), nil
		case "f2":
			return m.syncPane(PaneBoard), nil
		case "f3":
			return m.syncPane(PaneCost), nil
		case "f4":
			return m.syncPane(PaneGallery), nil
		case "f5":
			return m.syncPane(PaneFlow), nil
		case "f6":
			return m.syncPane(PaneChat), nil
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

// syncPane sets m.active to p and, when p has a nav route (routeToPane's
// inverse, paneToRoute), moves the sidebar's own highlight to match --
// the f1-f6 legacy keys and any future non-nav way of choosing a Pane
// should go through this rather than assigning m.active directly, so the
// sidebar cannot silently disagree with what is on screen.
func (m Model) syncPane(p Pane) Model {
	m.active = p
	if route, ok := paneToRoute[p]; ok {
		m.nav = m.nav.WithActive(route)
	}
	return m
}

// routeKey sends a KeyMsg to whichever single region has focus -- the nav
// sidebar, or the currently active content pane. Every pane already quits
// on its own "q"/"ctrl+c" (agent-tui#22's trap applies here too: routing,
// not intercepting, is what keeps that true for a pane this package did
// not write). PaneHome and an unavailable PaneBoard/PaneFlow have no live
// sub-model to route to; homeKey below covers both, and so does PaneStub
// (no sub-model at all, ever).
func (m Model) routeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	if m.focus == focusRail {
		return m.routeNavKey(msg)
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
	case PaneChat:
		next, cmd := m.chat.Update(msg)
		m.chat = next.(chat.Model)
		return m, cmd
	case PaneLanes:
		next, cmd := m.rail.Update(msg)
		m.rail = next.(rail.Model)
		return m, cmd
	case PaneAgents:
		next, cmd := m.agents.Update(msg)
		m.agents = next.(agents.Model)
		return m, cmd
	case PaneSkills:
		next, cmd := m.skills.Update(msg)
		m.skills = next.(skills.Model)
		return m, cmd
	case PaneMCPServers:
		next, cmd := m.mcpservers.Update(msg)
		m.mcpservers = next.(mcpservers.Model)
		return m, cmd
	case PaneConnectors:
		next, cmd := m.connectors.Update(msg)
		m.connectors = next.(connectors.Model)
		return m, cmd
	case PaneAdmin:
		next, cmd := m.admin.Update(msg)
		m.admin = next.(admin.Model)
		return m, cmd
	default:
		return m.homeKey(msg)
	}
}

// routeNavKey is SPEC-shell.md S3's keyboard contract for the sidebar:
// "↑/↓ move, Enter/→ selects, ← collapses." Everything else (today, only
// [b], the icons-only toggle S2 itself owns) falls through to
// m.nav.Update unchanged. Confirming a route that routeToPane does not
// know how to render for real (routeToPane, above) becomes PaneStub,
// rendered from m.nav.ActiveItem() and internal/stub.Descriptions (S5) --
// this is what makes every OTHER destination in nav.Build()'s tree show
// something the moment this lands, rather than nothing at all until each
// one's own later item (S6-S12) is built.
func (m Model) routeNavKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	// CONFLICT RESOLUTION, 2026-08-22. S1/S2 (#73) and S3 (#74) were built in
	// parallel by two agents and each created its own internal/nav. The merged
	// tree keeps #73's, which deliberately owns NO cursor -- its Update doc
	// says the shell drives up/down/enter/left and nav only handles [b]. So
	// the cursor lives here, over Tree.Flatten(), which #73 documents as
	// existing for exactly this traversal.
	nodes := m.nav.Tree().Flatten()
	if len(nodes) == 0 {
		return m, nil
	}
	if m.navCursor < 0 || m.navCursor >= len(nodes) {
		m.navCursor = 0
	}
	// visitable skips the children of collapsed groups, so ↓ does not walk
	// into a group the user has closed.
	visitable := func(i int) bool {
		n := nodes[i]
		if n.IsGroupHeader() || n.GroupID == "" {
			return true
		}
		return m.nav.IsExpanded(n.GroupID)
	}
	step := func(dir int) {
		for i := m.navCursor + dir; i >= 0 && i < len(nodes); i += dir {
			if visitable(i) {
				m.navCursor = i
				return
			}
		}
	}
	// syncCursor pushes the shell's cursor into nav so View can RENDER it.
	// Without this the cursor moves and nothing on screen changes -- which is
	// exactly the bug: pressing Down twice left the highlight on Home, so you
	// could not tell what Enter would open.
	syncCursor := func(mm Model) Model { mm.nav = mm.nav.WithCursor(mm.navCursor); return mm }

	switch msg.String() {
	case "up":
		step(-1)
		return syncCursor(m), nil
	case "down":
		step(1)
		return syncCursor(m), nil
	case "left":
		n := nodes[m.navCursor]
		gid := n.GroupID
		if n.IsGroupHeader() {
			gid = n.Group.ID
		}
		if gid != "" {
			m.nav = m.nav.WithCollapsed(gid)
		}
		return syncCursor(m), nil
	case "enter", "right":
		n := nodes[m.navCursor]
		if n.IsGroupHeader() {
			m.nav = m.nav.WithExpandedToggled(n.Group.ID)
			return syncCursor(m), nil
		}
		m.nav = m.nav.WithActive(n.Item.ID)
		if p, ok := routeToPane[n.Item.ID]; ok {
			m.active = p
			return syncCursor(m), nil
		}
		// A real destination with no pane built yet renders a stub, so every
		// entry in the tree shows something the moment this lands (S5).
		m.active = PaneStub
		return syncCursor(m), nil
	}

	// [b] is nav's own (icons-only). Anything else is NOT the sidebar's --
	// forward it to the same handler an unfocused pane would use, so global
	// keys keep working while the sidebar has focus. Swallowing them here is
	// the agent-tui#22 trap (a mode that ate every key, quit included), and
	// it is exactly what broke the theme and quit tests when this resolution
	// first routed everything into nav.Update.
	if msg.String() == "b" {
		next, cmd := m.nav.Update(msg)
		m.nav = next.(nav.Model)
		return m, cmd
	}
	return m.homeKey(msg)
}

// homeKey is the key handling for PaneHome, an unavailable PaneBoard/
// PaneFlow, and PaneStub -- none has a live sub-model to route to, so "q"
// must be handled here directly rather than silently doing nothing
// (agent-tui#38's "q/ctrl+c quits from every pane" acceptance item applies
// to every placeholder pane, not just the four real ones it was written
// against).
func (m Model) homeKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "t":
		// Ask for a theme cycle rather than cycling here -- Update's
		// theme.CycleRequestedMsg case is the ONE owner (agent-tui#51), which
		// is what makes a single press repaint the sidebar and the content
		// pane together. Panes with their own sub-model already emit this;
		// PaneHome, PaneStub and a focused sidebar have no sub-model to emit
		// it for them, so without this line [t] was silently dead in exactly
		// those states. Found by the theme suite going red after S3's
		// resolution, not by reading.
		return m, func() tea.Msg { return theme.CycleRequestedMsg{} }
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

	next, cmd := m.nav.Update(msg)
	m.nav = next.(nav.Model)
	cmds = append(cmds, cmd)

	// rail still gets every message even though it is no longer the
	// always-visible left column (PaneLanes, above) -- its own fetch loop
	// must keep running while some OTHER route is on screen, the same
	// "background refresh while not visible" property every other pane
	// already has (see this function's own doc comment).
	next, cmd = m.rail.Update(msg)
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

	next, cmd = m.chat.Update(msg)
	m.chat = next.(chat.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.agents.Update(msg)
	m.agents = next.(agents.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.skills.Update(msg)
	m.skills = next.(skills.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.mcpservers.Update(msg)
	m.mcpservers = next.(mcpservers.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.connectors.Update(msg)
	m.connectors = next.(connectors.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.admin.Update(msg)
	m.admin = next.(admin.Model)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// resize is the fix for agent-tui#38's named trap: every mounted view
// assumed it owned the whole terminal, so a WindowSizeMsg carrying the
// terminal's own dimensions broke every pane it hit. Here the nav sidebar
// is sized first, to its own fixed nav.Model.Width() (SPEC-shell.md S3:
// the sidebar replaces rail as the left column -- see Model's own doc
// comment); whatever terminal width is left over -- never the raw
// terminal width -- is what every content pane is sized to, including
// rail now that it is one (PaneLanes), whether it is the one currently
// visible or not (see routeAll's doc comment for why every pane is kept
// live).
func (m Model) resize(msg tea.WindowSizeMsg) (Model, tea.Cmd) {
	m.width, m.height = msg.Width, msg.Height
	innerHeight := m.height - footerHeight
	if innerHeight < 0 {
		innerHeight = 0
	}

	var cmds []tea.Cmd

	next, cmd := m.nav.Update(tea.WindowSizeMsg{Width: m.nav.Width(), Height: innerHeight})
	m.nav = next.(nav.Model)
	cmds = append(cmds, cmd)

	m.navWidth = m.nav.Width()
	m.contentWidth = m.width - m.navWidth
	if m.contentWidth < 0 {
		m.contentWidth = 0
	}
	m.contentHeight = innerHeight

	contentSize := tea.WindowSizeMsg{Width: m.contentWidth, Height: innerHeight}

	next, cmd = m.rail.Update(contentSize)
	m.rail = next.(rail.Model)
	cmds = append(cmds, cmd)

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

	next, cmd = m.chat.Update(contentSize)
	m.chat = next.(chat.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.agents.Update(contentSize)
	m.agents = next.(agents.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.skills.Update(contentSize)
	m.skills = next.(skills.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.mcpservers.Update(contentSize)
	m.mcpservers = next.(mcpservers.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.connectors.Update(contentSize)
	m.connectors = next.(connectors.Model)
	cmds = append(cmds, cmd)

	next, cmd = m.admin.Update(contentSize)
	m.admin = next.(admin.Model)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// View joins the nav sidebar, the active content pane, and the shell's own
// footer -- clampHeight is load-bearing here, not cosmetic: a pane package
// that renders one line taller than the height it was GIVEN (a trailing
// "\n" on its last output line is enough) would otherwise push m.footer()
// past the bottom of the terminal, since this whole composed string is one
// tea.WithAltScreen() frame with no scrollback to fall back on. The
// sidebar and every content pane already receive an exact height budget in
// resize(); View() is where that budget is actually enforced, once, rather
// than trusted to every pane's own internal line arithmetic (gallery's
// View() was measured to overrun its budget by exactly one blank trailing
// line -- see this package's teatest coverage).
func (m Model) View() string {
	left := clampHeight(m.nav.View(), m.contentHeight)
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
	case PaneChat:
		return m.chat.View()
	case PaneLanes:
		return m.rail.View()
	case PaneAgents:
		return m.agents.View()
	case PaneSkills:
		return m.skills.View()
	case PaneMCPServers:
		return m.mcpservers.View()
	case PaneConnectors:
		return m.connectors.View()
	case PaneAdmin:
		return m.admin.View()
	case PaneStub:
		return m.stubView()
	default:
		return m.homeView()
	}
}

// stubView renders internal/stub.View (S5) for whatever nav route
// routeToPane has no real Pane for -- title and description come from
// m.nav.ActiveItem()/internal/stub.Descriptions, keyed by Label (S5's own
// choice, made before S1's Item.ID existed as a stable key). A route with
// no Descriptions entry (should not happen for anything in nav.Build()'s
// tree, but a future addition to that tree could outrun this map) still
// renders a real stub, just with a generic description rather than a
// blank one -- "a visible stub beats a hidden screen" (S5) applies here
// too.
func (m Model) stubView() string {
	// #73's nav exposes the active route id, not the item; Descriptions is
	// keyed by the same ids nav.Build() emits, so the id is the lookup key.
	title := m.nav.Active()
	desc, ok := stub.Descriptions[title]
	if !ok {
		desc = "not built yet -- no description recorded for this route."
	}
	return lipgloss.Place(m.contentWidth, m.contentHeight, lipgloss.Left, lipgloss.Top, stub.View(m.theme, title, desc))
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
	// Compact by design past "[f2] board" (the one substring model_teatest_test.go
	// pins exactly): six pane keys plus quit must fit inside a realistic
	// terminal width alongside a themeSaveErr appended below, or the error
	// truncates before it says anything -- see this line's own git blame
	// for the width budget that broke when [f5] flow, then [f6] chat, were
	// added. [↑↓] [enter] [←] [b] (SPEC-shell.md S3) are only meaningful
	// while focus is on the sidebar -- shown always anyway, same as the
	// f-keys above being shown while a different pane is active, rather
	// than churning the legend's own width budget on every [tab] press.
	line := "[tab] focus:" + focusName + " [↑↓] [enter] [←] [b] [f1]home [f2] board [f3]cost [f4]gallery [f5]flow [f6]chat [q]quit"
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

// homeView used to advertise the f1-f6 keys as if they were the only way
// to reach a pane -- true before SPEC-shell.md S3, stale the moment the
// nav sidebar became the real navigation surface (S1-S4) and S6/S8/S9/
// S10/S11 gave most of those f-keys' destinations, and many more besides,
// a real sidebar route of their own. f1-f6 still work (S3's own "nothing
// that scripts them may break" acceptance line), so they are not removed
// from the footer below -- only no longer the thing home's own content
// teaches a human to press first.
func (m Model) homeView() string {
	lines := []string{
		"agent-tui",
		"",
		"Use the sidebar on the left to navigate: Agents, Chat, Tasks,",
		"Skills, MCP Servers, Connections, Usage, Admin, and every other",
		"destination in the tree -- an unwired one still renders an honest",
		"\"not built yet\" placeholder rather than nothing at all.",
		"",
		"[↑↓] move   [enter]/[→] select   [←] collapse   [b] icons-only",
		"[tab] move focus into the sidebar on the left",
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
