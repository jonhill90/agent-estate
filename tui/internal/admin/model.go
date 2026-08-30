package admin

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// refreshInterval: docker container state and $PATH contents change by
// human action, not by the second -- the same reasoning
// internal/skills/internal/mcpservers' own refreshInterval doc comments
// give for a local read with no polling cost. 30s matches both.
const refreshInterval = 30 * time.Second

type refreshMsg time.Time

type fetchResultMsg struct {
	snap Snapshot
	err  error
}

// Model is S11's Bubble Tea program: a read-only rendering of Snapshot's
// five named sections. "Read-only first" (S11's own spec text) is
// reflected in Update below having no write path at all -- no keys beyond
// navigation, refresh, theme and quit, matching S6's own "[n] read-only
// until S7" precedent but with no equivalent write item planned here to
// defer to. Not wired into internal/shell yet -- matches every other
// pane this build order has shipped standalone (S5/S6/S8/S9).
type Model struct {
	fetch Fetcher

	snap     Snapshot
	fetchErr error

	width, height int
	quitting      bool

	theme       theme.Theme
	themeNotice string

	// section is which of S11's five nav routes is currently active --
	// internal/nav.Item.ID values ("admin-services", "admin-profiles",
	// "admin-users", "dependencies", "settings"), see Section* below.
	// Empty (the New() default) means "not wired to a route" and renders
	// every section, the same all-five behaviour this Model has always had
	// -- kept as the default so every existing standalone caller and test
	// built before agent-tui#150 keeps passing unchanged. A caller wired
	// into internal/shell's nav (WithSection) narrows View to just the one
	// section the sidebar highlighted, which is agent-tui#150's actual fix:
	// the five Admin nav entries are five real hill90 web destinations
	// (nav-items.ts's own /admin/services, /admin/profiles, /admin/users,
	// /harness/tools, /settings hrefs -- ui_fidelity=1:1, SPEC-shell.md S1),
	// so collapsing them into one nav entry would break that fidelity
	// requirement; the fix instead makes the content pane actually respond
	// to which of the five is selected, using data this Model already
	// fetches (no new fabricated section).
	section string
}

// New builds a Model with fetch wired in.
func New(fetch Fetcher) Model {
	return Model{fetch: fetch, width: 100, height: 30, theme: theme.Default}
}

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this module exposes.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

// Section* are the internal/nav.Item.ID values for S11's five nav
// destinations -- named here so internal/shell can pass one to WithSection
// without either package guessing at the other's string literals.
const (
	SectionServices     = "admin-services"
	SectionProfiles     = "admin-profiles"
	SectionUsers        = "admin-users"
	SectionDependencies = "dependencies"
	SectionSettings     = "settings"
)

// WithSection returns a copy of m scoped to id, one of the Section*
// constants above -- the shell's own routeNavKey calls this (mirroring
// syncExternal's WithDestination) whenever an Admin nav child is
// confirmed, so View renders only the section that was actually selected
// instead of always rendering all five (agent-tui#150). An id this Model
// does not recognize (including "") falls back to rendering every
// section -- the pre-agent-tui#150 behaviour -- rather than rendering
// nothing, matching the "absence is a typed value, never a bare zero"
// pattern this package's own Snapshot doc comment already follows: an
// unrecognized section is "not scoped," not "scoped to emptiness."
func (m Model) WithSection(id string) Model {
	m.section = id
	return m
}

// Section reports which of Section* m is currently scoped to, or "" if
// unscoped (rendering all five). Exported so a caller or test can assert
// on it without depending on View's rendered string.
func (m Model) Section() string { return m.section }

// Snapshot returns m's current Snapshot -- exported so a caller (a future
// shell wiring, or this package's own teatest) can assert on it without
// depending on View's rendered string.
func (m Model) Snapshot() Snapshot { return m.snap }

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
		return fetchResultMsg{snap: snap, err: err}
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
			m.snap = msg.snap
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
