package shell

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/nav"
)

// Mouse support for the shell -- clicking a nav item switches pane, the way
// clicking a sidebar entry does in the web app. Keyboard is unchanged; this
// is additive.
//
// ADOPT-OR-BUILD, decided on evidence rather than reflex:
// lrstanley/bubblezone (904 stars, MIT, pushed 2026-08-15) is purpose-built
// for exactly this -- it marks a region of rendered output with an id, scans
// the final frame for those markers, and answers "is this click inside that
// region". Building it by hand means tracking the (x,y) extent of every
// clickable through lipgloss joins and re-deriving it on every resize, which
// is the part that would rot. Blast radius is a replaceable leaf: it touches
// rendering only, never the ledger or dispatch, so being wrong costs this
// file.
//
// REBASED 2026-08-22 onto SPEC-shell.md S3 (#74): the sidebar (internal/nav),
// not the footer's f-key legend, is the real nav now -- rail moved off the
// fixed left column entirely (PaneLanes). This file originally marked
// per-Pane zones on footer tokens (home/board/cost/gallery/flow/chat); it now
// marks one zone per VISIBLE sidebar row (nav.Tree().Flatten() order) and
// drives the exact same active/route logic routeNavKey's own "enter" case
// already does, so a click and a keyboard Enter on the same row do the same
// thing. Gallery and flow predate SPEC-shell.md and have no sidebar route at
// all (routeToPane's own doc comment) -- their footer zones stay, since the
// footer is still their only click target.
const (
	zoneGallery = "nav-gallery"
	zoneFlow    = "nav-flow"
	zoneQuit    = "nav-quit"
	zoneFocus   = "nav-focus"
)

// footerPaneZones is footerPaneZones' own small data table (the same
// "data, not a code path" shape navZones used before the rebase) for the
// two footer-only clickables that have no sidebar row.
var footerPaneZones = []struct {
	id   string
	pane Pane
}{
	{zoneGallery, PaneGallery},
	{zoneFlow, PaneFlow},
}

// navRowZone is the zone id for the sidebar row at Flatten() index i --
// stable across a render because the tree itself is static data
// (nav.Build()); a group's expand/collapse state changes which rows are
// VISIBLE, never which index a given node holds within Flatten()'s own
// fixed order.
func navRowZone(i int) string { return fmt.Sprintf("nav-row-%d", i) }

// visibleNavNodeIndices returns, in the exact order nav.Model.View() renders
// its lines, which Flatten() index each rendered line corresponds to.
// Mirrors View()'s own two skip rules (a collapsed group's children are
// skipped; every group HEADER line is skipped too once icons-only is on)
// without internal/nav needing any awareness of zones at all -- bubblezone
// stays a shell-only leaf dependency, exactly as this file's own top
// comment already claims. Kept in lockstep with view.go's loop by the pty
// drive in this package's own test file, not by trusting the two loops to
// agree forever.
func (m Model) visibleNavNodeIndices() []int {
	nodes := m.nav.Tree().Flatten()
	iconsOnly := m.nav.IconsOnly()
	out := make([]int, 0, len(nodes))
	for i, n := range nodes {
		if n.IsGroupHeader() {
			if iconsOnly {
				continue
			}
			out = append(out, i)
			continue
		}
		if n.GroupID != "" && !m.nav.IsExpanded(n.GroupID) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// renderNavWithZones is m.nav.View()'s own output, with each visible line
// wrapped in the zone that click detection needs -- called from View() in
// place of a bare m.nav.View(). Zone markers are zero-width once Scan
// strips them (View()'s own doc comment on m.zones.Scan), so this changes
// nothing about what the sidebar looks like, only what a click on it does.
func (m Model) renderNavWithZones() string {
	rendered := m.nav.View()
	lines := strings.Split(rendered, "\n")
	for li, ni := range m.visibleNavNodeIndices() {
		if li >= len(lines) {
			break
		}
		lines[li] = m.zones.Mark(navRowZone(ni), lines[li])
	}
	return strings.Join(lines, "\n")
}

// handleMouse routes a click. Returns handled=false when the click was not on
// anything this model owns, so the caller can pass it down to the focused
// pane rather than swallowing it.
//
// Only a left-button RELEASE acts. Press-to-act would fire on click-and-drag
// away, which is not what a person means by clicking a thing, and mouse
// motion events would otherwise switch panes as the pointer crossed the
// footer.
func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd, bool) {
	if msg.Action != tea.MouseActionRelease || msg.Button != tea.MouseButtonLeft {
		return m, nil, false
	}

	nodes := m.nav.Tree().Flatten()
	for _, i := range m.visibleNavNodeIndices() {
		if !m.zones.Get(navRowZone(i)).InBounds(msg) {
			continue
		}
		return m.selectNavRow(i, nodes), nil, true
	}

	for _, z := range footerPaneZones {
		if m.zones.Get(z.id).InBounds(msg) {
			m.active = z.pane
			// Clicking a view means "work in that view", so focus follows
			// the click. Requiring a separate [tab] afterwards is the kind
			// of half-wired mouse support that is worse than none.
			m.focus = focusContent
			return m, nil, true
		}
	}
	if m.zones.Get(zoneFocus).InBounds(msg) {
		m.focus = toggleFocus(m.focus)
		return m, nil, true
	}
	if m.zones.Get(zoneQuit).InBounds(msg) {
		return m, tea.Quit, true
	}
	return m, nil, false
}

// selectNavRow is a click on sidebar row i -- the exact same effect
// routeNavKey's own "enter"/"right" case has on the row the keyboard
// cursor sits on (WithActive + routeToPane, or PaneStub for a route with
// no real pane yet; WithExpandedToggled for a group header), plus moving
// both the shell's and nav's own cursor to the clicked row so the sidebar
// visibly agrees with what was just clicked, and moving focus into the
// content pane (a click is a stronger signal than a keypress that focus
// belongs there now).
func (m Model) selectNavRow(i int, nodes []nav.Node) Model {
	n := nodes[i]
	m.navCursor = i
	m.nav = m.nav.WithCursor(i)

	if n.IsGroupHeader() {
		m.nav = m.nav.WithExpandedToggled(n.Group.ID)
		return m
	}

	m.nav = m.nav.WithActive(n.Item.ID)
	if p, ok := routeToPane[n.Item.ID]; ok {
		m.active = p
	} else {
		m.active = PaneStub
	}
	m.focus = focusContent
	return m
}
