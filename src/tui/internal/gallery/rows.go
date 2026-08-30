// Package gallery is agent-tui#11's actual deliverable: "a page or some
// view... present me all kinds of glyphs I could use." Jon cycles four (now
// five) glyph sets blind today; this package answers "what glyphs could I
// use" by laying out every lane.AllStates state against every
// lane.Variants entry AND every lane.Candidates entry not yet in a set,
// side by side, with a renderability flag on each cell -- discovery, not
// confirmation, per the issue's own framing.
//
// This package does no I/O and reads no lanes: BuildRows is a pure
// function over lane's own exported data (Variants, AllStates, Candidates,
// Classify), so the whole gallery is testable without a tty, a supervisor
// connection, or a fetch -- the same posture internal/cost's package takes
// toward ccusage, applied here to data that was already in the binary.
package gallery

import "github.com/jonhill90/agent-estate/src/tui/internal/lane"

// Cell is one glyph shown for one state from one source (a shipped
// Variant, or a not-yet-shipped Candidate).
type Cell struct {
	Source string // variant ID ("signal", "nerd", ...) or "candidate"
	Name   string // variant Name, or the candidate's own note
	Glyph  string
	Flag   string // lane.Renderability's Label() -- "" when nothing to warn about
}

// Row is one state and every glyph the gallery has for it: one Cell per
// shipped Variant, in Variants order, plus zero or more candidate Cells.
type Row struct {
	State      string
	VariantsBy []Cell // len == len(lane.Variants), same order, every time
	Candidates []Cell // 0 or more; lane.Candidates entries naming this state
}

// BuildRows composes lane.AllStates x lane.Variants (every shipped glyph
// set must name every state -- enforced by lane's own MissingStates, so
// this never has to special-case a gap) plus lane.Candidates (glyphs no
// Variant has adopted, shown for discovery). One row per state, in
// lane.AllStates order, which already lists "the forgotten ones" first
// among the easy-to-miss states -- no reason for the gallery to reorder
// what that list already got right.
func BuildRows() []Row {
	rows := make([]Row, 0, len(lane.AllStates))
	for _, state := range lane.AllStates {
		row := Row{State: state}
		for _, set := range lane.Variants {
			style := lane.StyleFor(set, state)
			glyph := restingFrame(style)
			row.VariantsBy = append(row.VariantsBy, Cell{
				Source: set.ID,
				Name:   set.Name,
				Glyph:  glyph,
				Flag:   lane.Classify(glyph).Label(),
			})
		}
		for _, c := range lane.Candidates {
			if c.State != state {
				continue
			}
			row.Candidates = append(row.Candidates, Cell{
				Source: "candidate",
				Name:   c.Note,
				Glyph:  c.Glyph,
				Flag:   lane.Classify(c.Glyph).Label(),
			})
		}
		rows = append(rows, row)
	}
	return rows
}

// restingFrame picks the representative glyph for a Style: its first
// frame. Motion sets (spin/glitch/pulse/bounce) cycle several frames at
// runtime, but the gallery is a static catalogue, not an animated rail --
// showing frame 0 is the same choice the rail itself makes for tick 0, and
// every Style is guaranteed at least one frame (lane.Frame's own doc
// comment: "?" only when Frames is empty, which no shipped Style is).
func restingFrame(s lane.Style) string {
	if len(s.Frames) == 0 {
		return "?"
	}
	return s.Frames[0]
}
