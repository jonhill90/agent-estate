package shell

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"

	"github.com/jonhill90/agent-tui/internal/nav"
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
// REBASED 2026-08-22 onto SPEC-shell.md S3 (agent-tui#74): the sidebar (internal/nav),
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

	// settleZones closes a real race between the last View()'s zone.Scan()
	// and this InBounds check -- see that function's own doc comment. A
	// click is a human-timescale event; this cost belongs here, not on
	// every render.
	settleZones(m.zones, m.allZoneIDs())

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

// allZoneIDs returns every zone id THIS render marks -- the sidebar rows
// visibleNavNodeIndices() names, plus the footer's own zoneFocus/zoneQuit
// and footerPaneZones (model.go's own footer()). settleZones (below) uses
// this to know exactly what to wait for, not to guess at "however many
// zones there probably are." Kept in lockstep with renderNavWithZones()'s
// and footer()'s own Mark() calls by the same discipline
// visibleNavNodeIndices() already documents for itself -- if either drifts
// from what actually gets marked, settleZones degrades to waiting out its
// own deadline every render rather than silently doing nothing, which is
// the direction this kind of drift should fail in.
func (m Model) allZoneIDs() []string {
	visible := m.visibleNavNodeIndices()
	ids := make([]string, 0, len(visible)+2+len(footerPaneZones))
	for _, i := range visible {
		ids = append(ids, navRowZone(i))
	}
	ids = append(ids, zoneFocus, zoneQuit)
	for _, z := range footerPaneZones {
		ids = append(ids, z.id)
	}
	return ids
}

// zoneSettleDeadline bounds settleZones -- see that function's own doc
// comment for why it exists at all and why this is a wall-clock bound,
// not an iteration count or a blind sleep.
const zoneSettleDeadline = 50 * time.Millisecond

// settleZones waits for bubblezone's own Manager to actually record every
// id in ids before returning -- working around a real race in how this
// package uses that library, not a guess.
//
// zone.Manager.Scan() (called once, from View(), wrapping this render's
// whole output) parses each Mark()'d zone's bounds and enqueues them onto
// an internal channel for a background goroutine (zoneWorker(), started by
// zone.New()) to apply to the map Get() reads -- then returns immediately,
// WITHOUT waiting for that goroutine to run. Get() only ever sees whatever
// is already in the map at the moment it is called (manager.go's own
// Get(): a plain locked map read, no wait). In real interactive use this
// never bites: the bubbletea runtime always waits for a fresh terminal
// input event before the next Update/handleMouse call, and that gap is
// vastly longer than one map write takes. It bites exactly the shape this
// package's own mouse_test.go uses -- render, then simulate a click in the
// SAME goroutine with zero gap -- and, per estate-loop/w2e.md, it bites
// unpredictably depending on host scheduling: reproduced reliably on
// linux/amd64 (Docker, golang:1.26, matching this repo's own CI image),
// never reproduced on darwin/arm64 locally across 20 runs of the same
// tests against the same commit. Not a rendering bug, not a row that
// moved -- the zones this render marks are computed correctly (bubblezone
// itself measures each one with ansi-aware width math, scanner.go's own
// ansi.PrintableRuneWidth); they just are not always VISIBLE to Get() the
// instant Scan() returns.
//
// Called from handleMouse, NOT from View(): View() runs on every repaint
// (every keystroke, every cursor-blink tick, far more often than a human
// clicks anything), and settling there was tried first -- it fixed the
// race but cost enough added latency on that hot path to shift a
// completely unrelated real-Program test's timing
// (TestRightArrowOnGroupHeaderExpandsThenLeftCollapses, which never sends
// a mouse event at all) into failing. handleMouse only runs when an
// actual MouseMsg arrives, so paying this cost there -- confining it to
// the one path that actually needs a fresh answer -- fixes the same race
// without slowing down a render loop that never asked for it.
//
// Bounded by wall-clock time, not an iteration count or a fixed sleep:
// returns the moment every id in ids resolves, so the common case (the
// worker goroutine gets a scheduling slot almost immediately) costs at
// most a few runtime.Gosched() calls, and gives up after
// zoneSettleDeadline rather than hanging forever if an id is never
// produced at all (a real caller bug -- allZoneIDs drifting from what
// View() actually marks -- which this must surface as "clicks don't work"
// exactly like today, never as a hang).
func settleZones(zones *zone.Manager, ids []string) {
	deadline := time.Now().Add(zoneSettleDeadline)
	for {
		allSet := true
		for _, id := range ids {
			if zones.Get(id).IsZero() {
				allSet = false
				break
			}
		}
		if allSet || time.Now().After(deadline) {
			return
		}
		runtime.Gosched()
	}
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
