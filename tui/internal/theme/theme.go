// Package theme is agent-tui#27's seam: every look-and-feel literal in the
// render path -- colour, border character, chrome padding, glyph rune --
// moves here, out of internal/board, internal/rail, internal/cost and
// internal/gallery, and those packages ask for a Role instead of naming a
// concrete colour. "Changing the entire look is editing ONE thing" (#27,
// Jon's own framing of the acceptance test) means editing registry.go, not
// any render path file.
//
// This is deliberately NOT internal/lane/variants.go's job. Glyph SETS
// (which rune animates which lane state) were already made data by
// agent-supervisor#107's addendum and agent-tui#11/#16 -- see
// internal/lane/glyph.go's own doc comment. Theme governs the chrome AROUND
// those glyphs (selection background, borders, session accents, column
// colours, warn/error colour) and the one glyph that lives outside any
// GlyphSet, the director "★" marker (sessions.go) -- it does not re-own
// glyph-set data that already has a home.
package theme

import "github.com/charmbracelet/lipgloss"

// Role is a semantic slot a theme fills with a colour -- never a concrete
// colour name at the call site (#27 item 2: "a theme answers by role
// ... never by concrete colour name"). Adding a role costs a line here plus
// one entry per Theme in registry.go; nothing else needs to know a role
// exists until a render path file asks for it.
type Role string

const (
	RoleError        Role = "error"        // fetch failures, refusals -- board/cost/rail errStyle
	RoleWarn         Role = "warn"         // aged/blocked cards, ccusage WARN lines
	RoleFlag         Role = "flag"         // gallery's [NF]/[emoji] tags
	RoleDirector     Role = "director"     // the director session's header accent
	RoleUnsupervised Role = "unsupervised" // a session sessions.sh could not vouch for
	RoleSelectedBG   Role = "selected_bg"  // the rail's one fixed selection background
	RoleBorder       Role = "border"       // the rail's vertical rule
	RoleBacklog      Role = "backlog"      // board vivid column colours --
	RoleInProgress   Role = "in_progress"  // one role per board.Column, keyed
	RoleInReview     Role = "in_review"    // by name rather than by the board
	RoleBlocked      Role = "blocked"      // package's own Column type so
	RoleDone         Role = "done"         // this package stays free of an
	RoleNeutral      Role = "neutral"      // import cycle back into board.
)

// Theme is the one struct every render path file asks for a value from.
// Everything on it is DATA -- a struct literal in registry.go -- never a
// value computed or hand-picked at a call site.
type Theme struct {
	ID          string
	Name        string
	Description string

	// Colors answers Role -> colour. Color() below is the only accessor;
	// nothing outside this package reads the map directly.
	Colors map[Role]lipgloss.Color

	// Border is the character set drawn around a boxed pane (board's
	// StyleBoxed columns, the rail's side rule). ColumnStyle/StyleRules
	// still decide WHETHER a border draws; Theme decides what it looks
	// like once one does.
	Border lipgloss.Border

	// Padding is the chrome padding every row/pane style applies
	// horizontally (today's literal was Padding(0, 1) repeated at every
	// call site -- see rail/model.go, rail/sessions.go, board/layout.go).
	Padding int

	// DirectorMark prefixes the director session's label in the rail
	// (sessions.go) -- a glyph rune, same as any lane.GlyphSet frame, but
	// naming one specific session rather than a lane state, which is why
	// it lives here and not in a GlyphSet.
	DirectorMark string
}

// Color returns t's colour for role, or lipgloss's zero Color ("") when a
// theme has nothing for it -- a theme missing a role renders that element
// in the terminal's default colour rather than panicking, the same
// fail-visible-not-fail-crashing discipline lane.StyleFor documents for an
// unmapped lane state.
func (t Theme) Color(role Role) lipgloss.Color {
	if c, ok := t.Colors[role]; ok {
		return c
	}
	return lipgloss.Color("")
}
