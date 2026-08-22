package nav

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/theme"
)

// fullWidth/iconWidth are the sidebar's two fixed widths (SPEC-shell.md
// S2: "Fixed width, collapsible to icons-only with [b]") -- never resized
// to content, the same fixed-column discipline internal/rail.RailWidth
// already uses for the lane rail it sits beside.
const (
	fullWidth = 26
	iconWidth = 4
)

// Model is the sidebar component S2 asks for: it renders S1's Tree in a
// left column with collapsible groups, an auto-expanding active group, and
// an icons-only collapse toggle. It is NOT yet wired into internal/shell
// or given ↑/↓/Enter/←/Tab traversal -- SPEC-shell.md's own build order
// (S3, "Shell + routing") owns activeRoute and that keyboard contract;
// this Model only renders a tree and an externally-set active ID, plus the
// one key ([b]) S2 itself is asked to own.
type Model struct {
	tree   Tree
	active string

	// expanded tracks which Group IDs are open. A group auto-expands the
	// moment WithActive names one of its children (see expandFor) -- it is
	// never auto-collapsed again by a later WithActive call, matching
	// hill90's own Sidebar.tsx behaviour (the group "matches the active
	// route" but does not fight a user who left it open).
	expanded map[string]bool

	// iconsOnly is S2's own toggle (SPEC-shell.md: "collapsible to
	// icons-only with [b]") -- unlike active/expanded, this is Model's own
	// state, set by a key this Model handles itself rather than one S3's
	// shell routes in from outside.
	iconsOnly bool

	width, height int

	theme       theme.Theme
	themeNotice string
}

// New builds a Model over Build()'s tree, defaulting active to the first
// top-level item ("home") -- the same "nothing chosen yet" default
// internal/shell.PaneHome uses before any real routing exists.
func New() Model {
	t := Build()
	m := Model{
		tree:     t,
		expanded: map[string]bool{},
		width:    fullWidth,
		theme:    theme.Default,
	}
	if len(t.Items) > 0 {
		m.active = t.Items[0].ID
	}
	return m
}

// WithActive returns a copy of m with id highlighted and, if id names a
// group's child, that group expanded -- SPEC-shell.md S2: "the group
// containing the active route auto-expands (matches web Sidebar.tsx)."
// Once S3 exists this is the call it makes on every route change; today it
// is this package's own way of proving that behaviour without a shell.
func (m Model) WithActive(id string) Model {
	m.active = id
	expanded := make(map[string]bool, len(m.expanded)+1)
	for k, v := range m.expanded {
		expanded[k] = v
	}
	if gid := m.tree.GroupContaining(id); gid != "" {
		expanded[gid] = true
	}
	m.expanded = expanded
	return m
}

// Active returns the currently highlighted item's ID.
func (m Model) Active() string { return m.active }

// WithTheme returns a copy of m painted with th -- the same per-pane seam
// every other package in this repo exposes (internal/shell.applyTheme's
// doc comment) so wiring this into the shell in S3 needs no signature
// change here.
func (m Model) WithTheme(th theme.Theme, notice string) Model {
	m.theme = th
	m.themeNotice = notice
	return m
}

func (m Model) Init() tea.Cmd { return nil }

// Update handles exactly one key: [b], the icons-only toggle S2 itself
// owns (see Model's own doc comment for why ↑/↓/Enter/←/Tab are NOT here).
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "b" {
			m.iconsOnly = !m.iconsOnly
		}
	}
	return m, nil
}
