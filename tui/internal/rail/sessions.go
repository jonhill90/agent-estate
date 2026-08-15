// agent-tui#13: the multi-session rendering path -- everything New/
// NewWithCost's flat single-session View() branch in model.go does not
// need. Kept in its own file so the flat path (board.go, every pre-#13
// test) stays a diff-free read; nothing in model.go's non-sessions branch
// changed shape because of what is here.
package rail

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/lane"
)

// groupStyleDef is one candidate answer to "how are sessions grouped on
// screen" -- the picker rule agent-tui#13 requirement 5 asks for, applied
// to grouping the same way lane.GlyphSet applies it to glyphs: every entry
// here is real, numbered, and swapped live against the same on-screen data
// with the 'g' key, never decided silently (the #10 mistake this issue's
// own process note calls out). groupStyles[0] is the default.
//
// Only two of #13's three suggested shapes are implemented here --
// flat-with-headers and indented-tree. Collapsible needs per-session
// expand/collapse state that survives a refetch, a real feature of its own
// scope; the PR for this names it as a landed-half, not a silent drop.
type groupStyleDef struct {
	ID   string
	Name string
}

var groupStyles = []groupStyleDef{
	{ID: "headers", Name: "flat, session headers"},
	{ID: "tree", Name: "indented tree"},
}

// sessionRow is one selectable row in the flattened, cross-session list --
// what m.selected indexes into and what "up"/"down" walk across the whole
// tree with (agent-tui#13 requirement 4).
type sessionRow struct {
	sessionIdx int
	lane       lane.Lane
}

// sessionsFlat orders every session's lanes into one list: sessions in
// fetch order, each session's own lanes in lanes.sh's own order. Headers are
// a rendering concern renderSessionsBody inserts separately and are
// deliberately NOT rows here -- selection moves lane to lane and never
// lands on a header.
func (m Model) sessionsFlat() []sessionRow {
	var rows []sessionRow
	for si, s := range m.sessions {
		for _, l := range s.Lanes {
			rows = append(rows, sessionRow{sessionIdx: si, lane: l})
		}
	}
	return rows
}

// directorAccent is the ONE color the Director's header ever gets --
// deliberately not part of lane.GlyphSet (glyph variants style lane
// STATES, not sessions): a constant the same way selectionBackground in
// model.go is, independent of whichever glyph or grouping variant is live,
// so the Director reads distinctly under every one of them. Jon: "it should
// look a bit different. Something to make it special."
var directorAccent = lipgloss.Color("#ffd166")

// unsupervisedAccent marks a session sessions.sh could not prove the estate
// manages -- agent-supervisor#153's own marker had not landed when this was
// written, so this reads sessions.sh's interim, fail-closed signal (see
// lane.Session's doc comment: unknown reads unsupervised, never the other
// way). Reuses the amber already in this package's palette for "unsent"
// rather than inventing a new color for "be careful here". This wins over
// directorAccent when both apply -- safety over decoration: a Director
// session the ledger cannot vouch for must still read as unsupervised, not
// have that fact recolored away by the styling that makes it special.
var unsupervisedAccent = lipgloss.Color("#e0a94c")

var (
	sessionHeaderStyle = lipgloss.NewStyle().Bold(true).Padding(0, 1)
)

// renderSessionHeader renders one session's header row. isDirector adds a
// "★" marker (a glyph, not just color, so the distinction survives a
// terminal or profile that drops truecolor) and the gold accent;
// unsupervised adds a plain-English suffix and the amber accent, and wins
// the color contest over directorAccent -- see unsupervisedAccent's comment.
func (m Model) renderSessionHeader(s lane.Session, innerWidth int) string {
	isDirector := m.directorSession != "" && s.Name == m.directorSession

	label := s.Name
	if isDirector {
		label = "★ " + label
	}
	if !s.Supervised {
		label += " (unsupervised)"
	}

	style := sessionHeaderStyle
	if accent, ok := headerAccent(s.Supervised, isDirector); ok {
		style = style.Foreground(accent)
	}
	return style.Width(innerWidth).Render(truncate(label, max(0, innerWidth-2)))
}

// headerAccent picks renderSessionHeader's foreground color in
// safety-over-decoration order: an unsupervised session must always read
// unsupervised, even when it is also the configured director session -- see
// unsupervisedAccent's doc comment above. Pulled out of renderSessionHeader
// as its own pure function (no lipgloss.Render involved) specifically so
// this precedence is directly testable: go test runs with no tty, so
// lipgloss renders no ANSI escapes at all under its default color profile
// there, and a byte-for-byte comparison of two Render() calls would not
// have caught #18's own review finding that this precedence had no test.
// ok is false when neither condition applies -- s gets the header's default
// (unstyled) look, same as before this existed.
func headerAccent(supervised, isDirector bool) (lipgloss.Color, bool) {
	switch {
	case !supervised:
		return unsupervisedAccent, true
	case isDirector:
		return directorAccent, true
	default:
		return "", false
	}
}

// indentFor is the per-lane-row prefix for the current grouping style --
// the one visible difference 'g' toggles today between the two implemented
// styles. "tree" draws a connector so the row visibly hangs off its
// session's header; "headers" (the default) just indents.
func indentFor(style int) string {
	if style >= 0 && style < len(groupStyles) && groupStyles[style].ID == "tree" {
		return "├ "
	}
	return "  "
}

// renderSessionsBody is the agent-tui#13 grouped-by-session equivalent of
// View()'s flat-list branch in model.go: every session, its own lanes
// indented beneath it, the Director marked, unsupervised sessions marked,
// and the same trailing "selected lane" legend the flat view always showed
// (session name added, since which session is now part of what "selected"
// means).
func (m Model) renderSessionsBody(innerWidth int) []string {
	var b []string

	// Mirrors the flat view's fetchErr/no-lanes branch exactly -- a fetch
	// failure must be shown, never a blank rail indistinguishable from a
	// quiet estate.
	if m.sessionsErr != nil {
		b = append(b, errStyle.Width(innerWidth).Render("! unavailable"))
		b = append(b, dimStyle.Width(innerWidth).Render(truncate(m.sessionsErr.Error(), innerWidth)))
		return b
	}
	if len(m.sessions) == 0 {
		b = append(b, dimStyle.Width(innerWidth).Render("(no sessions)"))
		return b
	}

	set := lane.Variants[m.glyphSet]
	nameWidth := innerWidth - 4
	indent := indentFor(m.groupStyle)
	indentWidth := len([]rune(indent))

	rowIdx := 0
	for _, s := range m.sessions {
		b = append(b, m.renderSessionHeader(s, innerWidth))

		if s.Error != "" {
			// The session sessions.sh could not read (see lane.Session's
			// doc comment) -- shown, not dropped; the whole point of that
			// field surviving decode.
			b = append(b, dimStyle.Width(innerWidth).Render(truncate(indent+"! "+s.Error, innerWidth)))
			continue
		}
		if len(s.Lanes) == 0 {
			b = append(b, dimStyle.Width(innerWidth).Render(indent+"(no lanes)"))
			continue
		}

		for _, l := range s.Lanes {
			style := lane.StyleFor(set, l.State)
			glyph := lane.Frame(set, l.State, m.tick)
			name := truncate(l.Name, max(0, nameWidth-indentWidth))

			var line string
			if rowIdx == m.selected {
				g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Background(selectionBackground).Render(glyph)
				rest := lipgloss.NewStyle().Background(selectionBackground).Render(" " + name)
				line = indent + g + rest
				b = append(b, selRowStyle.Width(innerWidth).Render(line))
			} else {
				g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Render(glyph)
				line = fmt.Sprintf("%s%s %s", indent, g, name)
				b = append(b, rowStyle.Width(innerWidth).Render(line))
			}
			rowIdx++
		}
	}

	// The legend: same discipline as the flat view (every state must be
	// nameable, issue #107 hard-acceptance item 3), plus which session the
	// selection is in now that "selected" spans all of them.
	flat := m.sessionsFlat()
	b = append(b, dimStyle.Width(innerWidth).Render(""))
	if len(flat) > 0 && m.selected >= 0 && m.selected < len(flat) {
		sel := flat[m.selected]
		style := lane.StyleFor(set, sel.lane.State)
		label := style.Label
		if label == "" {
			label = sel.lane.State // Unmapped: still print the raw word, never blank
		}
		sessName := m.sessions[sel.sessionIdx].Name
		b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("session: %s", truncate(sessName, max(0, innerWidth-9)))))
		b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("state:   %s", label)))
		b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("idle:    %ds", sel.lane.IdleSeconds)))
	}
	if !m.sessionsFetched.IsZero() {
		age := time.Since(m.sessionsFetched).Round(time.Second)
		b = append(b, legendStyle.Width(innerWidth).Render(fmt.Sprintf("age:     %s", age)))
	}
	return b
}
