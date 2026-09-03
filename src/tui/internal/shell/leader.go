package shell

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/src/tui/internal/finder"
	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// Leader-key navigation, modelled on LazyVim's which-key.
//
// WHY NOT FUNCTION KEYS, AND WHY NOT DIGITS EITHER. Function keys were what
// this shell used and Jon asked for them gone. Digits were a first
// replacement and are no better in the way that matters: both are FLAT and
// UNDISCOVERABLE. A newcomer cannot find out what exists without reading the
// footer, and the footer only fits six entries, so the other panes are
// invisible.
//
// LazyVim's answer is a leader key that opens a menu of what is available
// right now. Press the leader, pause, and a popup lists every binding under
// it; press the next key and the action runs. Discoverability comes from the
// popup, not from a legend that has to be kept short.
//
// The pause matters and is not decoration: an experienced user types the
// whole chord faster than the timeout and never sees the popup, while a
// newcomer who hesitates gets shown the way. Same keys, two audiences.

// LeaderKey opens the menu. Space, as in LazyVim.
const LeaderKey = " "

// whichKeyDelay is how long the leader waits before showing the menu. Long
// enough that a fluent chord never flashes it; short enough that hesitating
// feels answered rather than ignored.
const whichKeyDelay = 400 * time.Millisecond

// whichKeyMsg fires after whichKeyDelay. It carries the generation it was
// scheduled for, so a stale timer from an abandoned leader press cannot open
// the menu over a later one.
type whichKeyMsg struct{ gen int }

// binding is one entry in the leader menu.
type binding struct {
	Key   string
	Label string
	Pane  Pane
}

// leaderBindings is the menu, in the order it is shown. Mnemonic first,
// position second: h for home, t for tasks, u for usage.
//
// This is DATA, not code: adding a pane to the menu is a line here, never a
// new branch in a key handler. Same discipline as internal/lane's glyph sets.
var leaderBindings = []binding{
	{"h", "home", PaneHome},
	{"t", "tasks", PaneBoard},
	{"u", "usage", PaneCost},
	{"g", "gallery", PaneGallery},
	{"f", "flow", PaneFlow},
	{"c", "chat", PaneChat},
	{"l", "lanes", PaneLanes},
	{"a", "agents", PaneAgents},
	{"k", "knowledge", PaneKnowledge},
	{"d", "dashboard", PaneDashboard},
}

// FinderKey opens the fuzzy jump list from the leader menu, as
// <leader><space> does in LazyVim.
const FinderKey = " "

// routeItems is every navigable destination, as jump-list items.
//
// It reads the SAME nav tree the sidebar draws, so a route can never appear
// in one and not the other -- a finder with its own hand-kept list is a
// second source of truth that goes stale silently.
func routeItems(m Model) []finder.Item {
	var out []finder.Item
	for _, n := range m.nav.Tree().Flatten() {
		if n.IsGroupHeader() || n.Item.ID == "" {
			continue
		}
		out = append(out, finder.Item{
			Kind:   "route",
			ID:     n.Item.ID,
			Label:  n.Item.Label,
			Detail: m.nav.Tree().GroupContaining(n.Item.ID),
		})
	}
	return out
}

// leaderTakesKeys reports whether the leader may claim a keystroke in the
// current pane. A pane with a text composer must keep its space bar: a leader
// that eats spaces inside a message box is a worse bug than no leader at all.
func (m Model) leaderTakesKeys() bool {
	switch m.active {
	case PaneChat, PaneLaneChatLanePrimary, PaneLaneChatRoomPrimary, PaneLaneChatUnifiedList:
		return m.focus == focusRail
	}
	return true
}

// startLeader enters the pending state and schedules the menu.
func (m Model) startLeader() (Model, tea.Cmd) {
	m.leaderPending = true
	m.leaderMenu = false
	m.leaderGen++
	gen := m.leaderGen
	return m, tea.Tick(whichKeyDelay, func(time.Time) tea.Msg { return whichKeyMsg{gen: gen} })
}

// resolveLeader consumes the key pressed after the leader.
//
// An unknown key CANCELS rather than doing nothing visible: a chord that
// silently swallows a keystroke leaves the user unsure whether the app is
// listening.
func (m Model) resolveLeader(key string) (Model, tea.Cmd) {
	m.leaderPending = false
	m.leaderMenu = false
	if key == "esc" || key == "ctrl+c" {
		return m, nil
	}
	if key == FinderKey {
		m.finder = finder.New(routeItems(m))
		m.finderOpen = true
		return m, nil
	}
	for _, b := range leaderBindings {
		if b.Key == key {
			return m.syncPane(b.Pane), nil
		}
	}
	m.leaderMiss = key
	return m, nil
}

// leaderView renders the which-key menu: every binding available right now,
// in columns, above the footer.
func (m Model) leaderView() string {
	if !m.leaderMenu {
		return ""
	}
	keyStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleDirector))
	dim := lipgloss.NewStyle().Foreground(m.theme.Color(theme.RoleNeutral))

	sorted := append([]binding(nil), leaderBindings...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Label < sorted[j].Label })

	var cells []string
	for _, b := range sorted {
		cells = append(cells, fmt.Sprintf("%s %s", keyStyle.Render(b.Key), b.Label))
	}
	// Three per row keeps the menu readable at 80 columns.
	var rows []string
	for i := 0; i < len(cells); i += 3 {
		end := i + 3
		if end > len(cells) {
			end = len(cells)
		}
		rows = append(rows, strings.Join(cells[i:end], "   "))
	}
	cells = append(cells, fmt.Sprintf("%s %s", keyStyle.Render("space"), "find…"))
	title := dim.Render("leader — press a key, esc to cancel")
	return lipgloss.JoinVertical(lipgloss.Left, append([]string{title}, rows...)...)
}

// leaderHint is what the footer says about the leader, including the one-shot
// report of an unrecognised chord.
func (m Model) leaderHint() string {
	switch {
	case m.leaderMiss != "":
		return fmt.Sprintf("[space] no binding for %q", m.leaderMiss)
	case m.leaderPending:
		return "[space] …"
	}
	return "[space] menu"
}

// finderKey handles a keystroke while the jump list is open. It returns
// handled=false for nothing, because the finder claims every key while it is
// open: a fuzzy prompt that lets some keys fall through to the pane behind it
// would fire actions the user cannot see.
func (m Model) finderKey(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.finderOpen = false
		return m, nil
	case "enter":
		it, ok := m.finder.Choice()
		m.finderOpen = false
		if !ok {
			// Nothing matched. Closing without jumping is the honest
			// outcome; jumping "somewhere" would be worse than doing nothing.
			return m, nil
		}
		if pane, known := routeToPane[it.ID]; known {
			return m.syncPane(pane), nil
		}
		// A route with no pane is a stub, and the sidebar still moves so the
		// user can see where they landed.
		m.nav = m.nav.WithActive(it.ID)
		m.active = PaneStub
		return m, nil
	case "up", "ctrl+p":
		m.finder = m.finder.Move(-1)
		return m, nil
	case "down", "ctrl+n":
		m.finder = m.finder.Move(1)
		return m, nil
	case "backspace":
		m.finder = m.finder.Backspace()
		return m, nil
	}
	if r := msg.Runes; len(r) == 1 {
		m.finder = m.finder.Type(r[0])
	}
	return m, nil
}

// finderView renders the prompt and the ranked matches.
func (m Model) finderView() string {
	if !m.finderOpen {
		return ""
	}
	dim := lipgloss.NewStyle().Foreground(m.theme.Color(theme.RoleNeutral))
	sel := lipgloss.NewStyle().Bold(true).Foreground(m.theme.Color(theme.RoleDirector))

	rows := []string{dim.Render("find › ") + m.finder.Query + "▌"}
	if len(m.finder.Matches) == 0 {
		rows = append(rows, dim.Render("  no match"))
		return lipgloss.JoinVertical(lipgloss.Left, rows...)
	}
	shown := m.finder.Matches
	if len(shown) > m.finder.Height {
		shown = shown[:m.finder.Height]
	}
	for i, mt := range shown {
		line := "  " + mt.Item.Label
		if mt.Item.Detail != "" {
			line += dim.Render("  " + mt.Item.Detail)
		}
		if i == m.finder.Selected {
			line = sel.Render("› " + mt.Item.Label)
			if mt.Item.Detail != "" {
				line += dim.Render("  " + mt.Item.Detail)
			}
		}
		rows = append(rows, line)
	}
	if len(m.finder.Matches) > len(shown) {
		rows = append(rows, dim.Render(fmt.Sprintf("  … %d more", len(m.finder.Matches)-len(shown))))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}
