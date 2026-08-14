package lane

import "fmt"

// Variants is the whole answer to agent-supervisor#107's addendum: "the
// glyph set must be data behind an interface, not code -- adding a fifth
// variant costs a line, deleting one costs a line." Every candidate the
// picker in cmd/agent-tui cycles through is one entry in this slice and
// nothing else -- no switch statement anywhere names a variant by ID, and
// the completeness check in init() below runs against whatever is here, so
// adding or removing an entry is the entire change. If that stops being
// true -- if a new variant needs an edit anywhere but this slice -- that is
// the seam breaking and belongs in the PR as a finding.
//
// Variants[0] is the DEFAULT: what renders with no selection made, per the
// addendum's "he must be able to say nothing and still get something sane."
var Variants = []GlyphSet{signalSet, asciiSet, blocksSet, emojiSet}

// Default is Variants[0], named for callers that want it without indexing.
var Default = Variants[0]

func init() {
	// Enforced at process start, not just in a test: a variant that cannot
	// render every state lanes.sh emits is not a candidate (addendum rule
	// 2), and that must hold for whatever is in this file today, not only
	// for what a test file happened to check when it was written.
	for _, set := range Variants {
		if missing := MissingStates(set); len(missing) > 0 {
			panic(fmt.Sprintf("lane: glyph set %q is missing states %v -- every variant must render every lanes.sh state", set.ID, missing))
		}
	}
}

// -- signal: the original design -- braille spinner, glitch/pulse motion --

var signalSet = GlyphSet{
	ID:          "signal",
	Name:        "Signal",
	Description: "braille spinner, glitch on hung/broken, pulse on waiting",
	Unmapped:    Style{Motion: MotionGlitch, Frames: []string{"▓", "░"}, Color: "#ff00ff", Label: ""},
	Styles: map[string]Style{
		"free":         {Motion: MotionStill, Frames: []string{"●"}, Color: "#4caf50", Label: "free"},
		"busy":         {Motion: MotionSpin, Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}, Color: "#4aa3ff", Label: "busy"},
		"hung":         {Motion: MotionGlitch, Frames: []string{"◆", "◇", "◆", " "}, Color: "#ff5555", Label: "hung"},
		"dead":         {Motion: MotionStill, Frames: []string{"✕"}, Color: "#777777", Label: "dead"},
		"stale":        {Motion: MotionPulse, Frames: []string{"✕", "·"}, Color: "#cc3333", Label: "stale"},
		"broken":       {Motion: MotionGlitch, Frames: []string{"⨯", "!", "⨯", " "}, Color: "#ff8800", Label: "broken"},
		"menu-blocked": {Motion: MotionPulse, Frames: []string{"?", "·"}, Color: "#e0c34c", Label: "menu-blocked"},
		"text-blocked": {Motion: MotionPulse, Frames: []string{"?", "·"}, Color: "#e0c34c", Label: "text-blocked"},
		"unsent":       {Motion: MotionPulse, Frames: []string{"~", "-"}, Color: "#e0a94c", Label: "unsent"},
		"scrolled":     {Motion: MotionBounce, Frames: []string{"↑", "↕", "↓", "↕"}, Color: "#7fa8ff", Label: "scrolled"},
		"service":      {Motion: MotionStill, Frames: []string{"·"}, Color: "#555555", Label: "service"},
		"supervisor":   {Motion: MotionStill, Frames: []string{"·"}, Color: "#555555", Label: "supervisor"},
		"unknown":      {Motion: MotionStill, Frames: []string{"?"}, Color: "#999999", Label: "unknown"},
	},
}

// -- ascii: portable -- no unicode assumed beyond the box border, safe over
// any terminal/font/SSH hop --

var asciiSet = GlyphSet{
	ID:          "ascii",
	Name:        "ASCII",
	Description: "plain ASCII only, for terminals or fonts unicode can't be assumed on",
	Unmapped:    Style{Motion: MotionGlitch, Frames: []string{"%", " "}, Color: "#ff00ff", Label: ""},
	Styles: map[string]Style{
		"free":         {Motion: MotionStill, Frames: []string{"o"}, Color: "#4caf50", Label: "free"},
		"busy":         {Motion: MotionSpin, Frames: []string{"|", "/", "-", "\\"}, Color: "#4aa3ff", Label: "busy"},
		"hung":         {Motion: MotionGlitch, Frames: []string{"#", " "}, Color: "#ff5555", Label: "hung"},
		"dead":         {Motion: MotionStill, Frames: []string{"x"}, Color: "#777777", Label: "dead"},
		"stale":        {Motion: MotionPulse, Frames: []string{"x", "."}, Color: "#cc3333", Label: "stale"},
		"broken":       {Motion: MotionGlitch, Frames: []string{"!", " "}, Color: "#ff8800", Label: "broken"},
		"menu-blocked": {Motion: MotionPulse, Frames: []string{"?", "."}, Color: "#e0c34c", Label: "menu-blocked"},
		"text-blocked": {Motion: MotionPulse, Frames: []string{"?", "."}, Color: "#e0c34c", Label: "text-blocked"},
		"unsent":       {Motion: MotionPulse, Frames: []string{"~", "-"}, Color: "#e0a94c", Label: "unsent"},
		"scrolled":     {Motion: MotionBounce, Frames: []string{"^", "|", "v", "|"}, Color: "#7fa8ff", Label: "scrolled"},
		"service":      {Motion: MotionStill, Frames: []string{"."}, Color: "#555555", Label: "service"},
		"supervisor":   {Motion: MotionStill, Frames: []string{"."}, Color: "#555555", Label: "supervisor"},
		"unknown":      {Motion: MotionStill, Frames: []string{"?"}, Color: "#999999", Label: "unknown"},
	},
}

// -- blocks: unicode block elements -- solid/hollow reads as filled/empty --

var blocksSet = GlyphSet{
	ID:          "blocks",
	Name:        "Blocks",
	Description: "unicode block elements -- solid reads filled, hollow reads empty",
	Unmapped:    Style{Motion: MotionGlitch, Frames: []string{"▚", "▞"}, Color: "#ff00ff", Label: ""},
	Styles: map[string]Style{
		"free":         {Motion: MotionStill, Frames: []string{"█"}, Color: "#4caf50", Label: "free"},
		"busy":         {Motion: MotionSpin, Frames: []string{"▖", "▘", "▝", "▗"}, Color: "#4aa3ff", Label: "busy"},
		"hung":         {Motion: MotionGlitch, Frames: []string{"█", "░"}, Color: "#ff5555", Label: "hung"},
		"dead":         {Motion: MotionStill, Frames: []string{"░"}, Color: "#777777", Label: "dead"},
		"stale":        {Motion: MotionPulse, Frames: []string{"▒", "░"}, Color: "#cc3333", Label: "stale"},
		"broken":       {Motion: MotionGlitch, Frames: []string{"▓", " "}, Color: "#ff8800", Label: "broken"},
		"menu-blocked": {Motion: MotionPulse, Frames: []string{"▨", "▧"}, Color: "#e0c34c", Label: "menu-blocked"},
		"text-blocked": {Motion: MotionPulse, Frames: []string{"▧", "▨"}, Color: "#e0c34c", Label: "text-blocked"},
		"unsent":       {Motion: MotionPulse, Frames: []string{"▮", "▯"}, Color: "#e0a94c", Label: "unsent"},
		"scrolled":     {Motion: MotionBounce, Frames: []string{"▲", "▬", "▼", "▬"}, Color: "#7fa8ff", Label: "scrolled"},
		"service":      {Motion: MotionStill, Frames: []string{"▪"}, Color: "#555555", Label: "service"},
		"supervisor":   {Motion: MotionStill, Frames: []string{"▪"}, Color: "#555555", Label: "supervisor"},
		"unknown":      {Motion: MotionStill, Frames: []string{"▯"}, Color: "#999999", Label: "unknown"},
	},
}

// -- emoji: expressive, color-carrying glyphs; also the only variant where
// menu-blocked and text-blocked get visually distinct (not just labeled)
// glyphs --

var emojiSet = GlyphSet{
	ID:          "emoji",
	Name:        "Emoji",
	Description: "color emoji glyphs, menu-blocked and text-blocked visually distinct",
	Unmapped:    Style{Motion: MotionGlitch, Frames: []string{"❔", " "}, Color: "#ff00ff", Label: ""},
	Styles: map[string]Style{
		"free":         {Motion: MotionStill, Frames: []string{"🟢"}, Color: "#4caf50", Label: "free"},
		"busy":         {Motion: MotionSpin, Frames: []string{"🌑", "🌒", "🌓", "🌔", "🌕", "🌖", "🌗", "🌘"}, Color: "#4aa3ff", Label: "busy"},
		"hung":         {Motion: MotionGlitch, Frames: []string{"⚠️", " "}, Color: "#ff5555", Label: "hung"},
		"dead":         {Motion: MotionStill, Frames: []string{"⬛"}, Color: "#777777", Label: "dead"},
		"stale":        {Motion: MotionPulse, Frames: []string{"💀", "·"}, Color: "#cc3333", Label: "stale"},
		"broken":       {Motion: MotionGlitch, Frames: []string{"💥", " "}, Color: "#ff8800", Label: "broken"},
		"menu-blocked": {Motion: MotionPulse, Frames: []string{"⏸️", "·"}, Color: "#e0c34c", Label: "menu-blocked"},
		"text-blocked": {Motion: MotionPulse, Frames: []string{"⌨️", "·"}, Color: "#e0c34c", Label: "text-blocked"},
		"unsent":       {Motion: MotionPulse, Frames: []string{"✉️", "·"}, Color: "#e0a94c", Label: "unsent"},
		"scrolled":     {Motion: MotionBounce, Frames: []string{"⬆️", "↕️", "⬇️", "↕️"}, Color: "#7fa8ff", Label: "scrolled"},
		"service":      {Motion: MotionStill, Frames: []string{"⚙️"}, Color: "#555555", Label: "service"},
		"supervisor":   {Motion: MotionStill, Frames: []string{"🛠️"}, Color: "#555555", Label: "supervisor"},
		"unknown":      {Motion: MotionStill, Frames: []string{"❓"}, Color: "#999999", Label: "unknown"},
	},
}
