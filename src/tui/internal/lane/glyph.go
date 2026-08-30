package lane

// This file holds only the SHAPE of a glyph set -- Motion, Style, GlyphSet
// -- and the functions that read one. The actual glyph sets are DATA in
// variants.go, per agent-supervisor#107's addendum: "the glyph set must be
// data behind an interface, not code -- adding a fifth variant costs a
// line, deleting one costs a line." Nothing in this file names a specific
// glyph or a specific variant; if it ever needs to, that is the seam
// breaking and belongs in the PR as a finding, not a workaround.

// Motion names the animation family a glyph belongs to. cmd/agent-tui's
// model advances a single tick counter and asks Frame for the glyph at that
// tick; nothing here owns a timer.
type Motion int

const (
	MotionStill  Motion = iota // a settled glyph: renders the same every tick
	MotionSpin                 // a rotating spinner: agent mid-turn
	MotionGlitch               // rapid alternation between two unlike glyphs: looks broken
	MotionPulse                // slow fade between a glyph and dim: waiting, not broken
	MotionBounce               // gentle back-and-forth: scrolled away from the live tail
)

// Style is one state's presentation within a GlyphSet: its frames, its
// color, and the label a legend line prints beside it so a reader is never
// left decoding a symbol alone.
type Style struct {
	Motion Motion
	Frames []string // one or more frames; Frame() indexes into this by tick
	Color  string   // ANSI/truecolor hex, lipgloss-compatible
	Label  string   // the word lanes.sh uses for this state, always available
}

// GlyphSet is one complete, independently swappable answer to "how does
// this rail draw lane state" -- the unit the picker in cmd/agent-tui cycles
// through. Everything a variant needs to be a candidate lives on this
// struct; nothing about a variant is encoded in control flow elsewhere.
type GlyphSet struct {
	ID          string // stable key, also what's shown as "[N] id"
	Name        string
	Description string
	Styles      map[string]Style // keyed by lanes.sh state string
	Unmapped    Style            // this variant's answer to a state it doesn't name
}

// StyleFor returns set's Style for a state, or set.Unmapped with Label set
// to the raw state string when the variant has nothing for it. This must
// never be silent -- README rule 4 in agent-supervisor/laneview, applied to
// every variant, not just the default.
func StyleFor(set GlyphSet, state string) Style {
	if s, ok := set.Styles[state]; ok {
		return s
	}
	u := set.Unmapped
	u.Label = state
	return u
}

// Frame returns the glyph to draw at the given tick for a state in set.
func Frame(set GlyphSet, state string, tick int) string {
	s := StyleFor(set, state)
	if len(s.Frames) == 0 {
		return "?"
	}
	return s.Frames[tick%len(s.Frames)]
}
