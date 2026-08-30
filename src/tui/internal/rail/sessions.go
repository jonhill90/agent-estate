// agent-tui#13: the multi-session rendering path -- everything New/
// NewWithCost's flat single-session View() branch in model.go does not
// need. Kept in its own file so the flat path (board.go, every pre-agent-tui#13
// test) stays a diff-free read; nothing in model.go's non-sessions branch
// changed shape because of what is here.
package rail

import (
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
)

// groupStyleDef is one candidate answer to "how are sessions grouped on
// screen" -- the picker rule agent-tui#13 requirement 5 asks for, applied
// to grouping the same way lane.GlyphSet applies it to glyphs: every entry
// here is real, numbered, and swapped live against the same on-screen data
// with the 'g' key, never decided silently (the agent-tui#10 mistake this issue's
// own process note calls out). groupStyles[0] is the default.
//
// Only two of agent-tui#13's three suggested shapes are implemented here --
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

// renderSessionHeader renders one session's header row. isDirector adds
// theme.DirectorMark (a glyph, not just color, so the distinction survives
// a terminal or profile that drops truecolor) and theme.RoleDirector's
// accent; unsupervised adds a plain-English suffix and theme.RoleUnsupervised's
// accent, and wins the color contest over the director accent -- see
// headerAccent's doc comment. Both colours and the mark itself are
// agent-tui#27 theme data now, not literals in this file.
func (m Model) renderSessionHeader(s lane.Session, innerWidth int, st railStyles) string {
	isDirector := m.directorSession != "" && s.Name == m.directorSession

	label := s.Name
	if isDirector {
		label = m.theme.DirectorMark + " " + label
	}
	if !s.Supervised {
		label += " (unsupervised)"
	}

	style := lipgloss.NewStyle().Bold(true).Padding(0, m.theme.Padding)
	if accent, ok := headerAccent(s.Supervised, isDirector, st); ok {
		style = style.Foreground(accent)
	}
	return style.Width(innerWidth).Render(truncate(label, max(0, innerWidth-2)))
}

// headerAccent picks renderSessionHeader's foreground color in
// safety-over-decoration order: an unsupervised session must always read
// unsupervised, even when it is also the configured director session --
// theme.RoleUnsupervised wins over theme.RoleDirector when both apply, the
// same precedence the pre-agent-tui#27 unsupervisedAccent/directorAccent constants
// documented. Pulled out of renderSessionHeader as its own pure function
// (no lipgloss.Render involved) specifically so this precedence is directly
// testable: go test runs with no tty, so lipgloss renders no ANSI escapes
// at all under its default color profile there, and a byte-for-byte
// comparison of two Render() calls would not have caught agent-tui#18's own review
// finding that this precedence had no test. ok is false when neither
// condition applies -- s gets the header's default (unstyled) look, same as
// before this existed.
func headerAccent(supervised, isDirector bool, st railStyles) (lipgloss.Color, bool) {
	switch {
	case !supervised:
		return st.unsupervisedAccent, true
	case isDirector:
		return st.directorAccent, true
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
func (m Model) renderSessionsBody(innerWidth int, st railStyles) []string {
	var b []string

	// Mirrors the flat view's fetchErr/no-lanes branch exactly -- a fetch
	// failure must be shown, never a blank rail indistinguishable from a
	// quiet estate.
	if m.sessionsErr != nil {
		b = append(b, st.err.Width(innerWidth).Render("! unavailable"))
		b = append(b, st.dim.Width(innerWidth).Render(truncate(m.sessionsErr.Error(), innerWidth)))
		return b
	}
	if len(m.sessions) == 0 {
		b = append(b, st.dim.Width(innerWidth).Render("(no sessions)"))
		return b
	}

	set := lane.Variants[m.glyphSet]
	// See model.go's renderFlatBody for why this tracks m.theme.Padding
	// rather than a fixed literal.
	nameWidth := innerWidth - 2*m.theme.Padding - 2
	indent := indentFor(m.groupStyle)
	indentWidth := len([]rune(indent))

	rowIdx := 0
	for _, s := range m.sessions {
		b = append(b, m.renderSessionHeader(s, innerWidth, st))

		if s.Error != "" {
			// The session sessions.sh could not read (see lane.Session's
			// doc comment) -- shown, not dropped; the whole point of that
			// field surviving decode.
			b = append(b, st.dim.Width(innerWidth).Render(truncate(indent+"! "+s.Error, innerWidth)))
			continue
		}
		if len(s.Lanes) == 0 {
			b = append(b, st.dim.Width(innerWidth).Render(indent+"(no lanes)"))
			continue
		}

		for _, l := range s.Lanes {
			style := lane.StyleFor(set, l.State)
			glyph := lane.Frame(set, l.State, m.tick)
			name := truncate(l.Name, max(0, nameWidth-indentWidth))

			var line string
			if rowIdx == m.selected {
				g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Background(st.selectionBG).Render(glyph)
				rest := lipgloss.NewStyle().Background(st.selectionBG).Render(" " + name)
				line = indent + g + rest
				b = append(b, st.selRow.Width(innerWidth).Render(line))
			} else {
				g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Render(glyph)
				line = fmt.Sprintf("%s%s %s", indent, g, name)
				b = append(b, st.row.Width(innerWidth).Render(line))
			}
			rowIdx++
		}
	}

	// The legend: same discipline as the flat view (every state must be
	// nameable, issue agent-tui#107 hard-acceptance item 3), plus which session the
	// selection is in now that "selected" spans all of them.
	flat := m.sessionsFlat()
	b = append(b, st.dim.Width(innerWidth).Render(""))
	if len(flat) > 0 && m.selected >= 0 && m.selected < len(flat) {
		sel := flat[m.selected]
		style := lane.StyleFor(set, sel.lane.State)
		sessName := m.sessions[sel.sessionIdx].Name
		b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("session: %s", truncate(sessName, max(0, innerWidth-9)))))
		// agent-tui#26: was a fixed "state:/idle:" pair -- now the
		// reading-driven detail block; see readings_view.go. The ledger
		// join key is built HERE, from sessName (already on hand for the
		// "session: ..." legend above) + sel.lane.Window -- agent-tui#86
		// found and fixed the identical sel.Name-keyed bug in
		// internal/agents' own copy of this join; this was the same
		// defect in this package.
		ledgerLane := sessName + ":" + strconv.Itoa(sel.lane.Window)
		b = append(b, m.renderReadingDetail(sel.lane, ledgerLane, style, st, innerWidth)...)
	}
	if !m.sessionsFetched.IsZero() {
		age := time.Since(m.sessionsFetched).Round(time.Second)
		b = append(b, st.legend.Width(innerWidth).Render(fmt.Sprintf("age:     %s", age)))
	}
	return b
}
