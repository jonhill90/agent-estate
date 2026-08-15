package lane

// This file backs agent-tui#11's gallery: "every state x every candidate
// glyph, side by side... include glyphs not yet in any set." Two things
// live here that variants.go itself has no reason to know about --
// Classify (is a glyph renderable without a Nerd Font) and Candidates
// (glyphs worth showing that no Variant has adopted) -- kept out of
// variants.go so that file stays exactly what its own doc comment claims:
// "no switch statement anywhere names a variant by ID."

// Renderability is a structural claim about a glyph's codepoints, not a
// runtime measurement of Jon's terminal. #11's own research is explicit
// that a terminal application cannot query which font is loaded, and that
// render-and-measure (print a glyph, query cursor position, compare width)
// is "fiddly under tmux and needs a real tty" -- exactly the gallery's own
// environment when captured non-interactively. Classify sidesteps that
// fiddliness by asking a question Unicode itself can answer with certainty:
// which block is this codepoint in. That is weaker than "does it actually
// render here" but it is never wrong in the way a guess can be wrong, which
// is the same "fail toward the safe answer" rule #11 asks of font
// detection, applied one level down to individual glyphs.
type Renderability int

const (
	// RenderCommon is ASCII or Unicode already shipped in a non-Nerd-Font
	// Variant (signal/ascii/blocks) -- if it weren't renderable, the rail
	// Jon already uses would already be showing tofu, which nothing has
	// reported.
	RenderCommon Renderability = iota
	// RenderEmojiFallback depends on the terminal's own emoji/colour-font
	// fallback rather than the monospace font in use -- usually fine on a
	// modern terminal, but not the same guarantee as RenderCommon, and
	// worth a distinct label rather than folding it into "safe".
	RenderEmojiFallback
	// RenderRequiresNerdFont is a Private Use Area codepoint (U+E000-
	// U+F8FF, or the supplementary PUA planes). A PUA codepoint has no
	// Unicode-assigned meaning at all -- it renders only because some font
	// chose to map an icon onto it. Without that exact font (or a
	// compatible Nerd Font), every terminal renders it as a fallback box:
	// tofu, not a downgrade.
	RenderRequiresNerdFont
)

// Label is the plain-ASCII tag the gallery prints next to a glyph. Plain
// ASCII deliberately: the flag exists to warn about a glyph that might be
// tofu, so the flag itself must never be able to become tofu.
func (r Renderability) Label() string {
	switch r {
	case RenderRequiresNerdFont:
		return "[NF]"
	case RenderEmojiFallback:
		return "[emoji]"
	default:
		return ""
	}
}

// Classify inspects glyph's runes and returns the least-safe classification
// found -- a glyph that mixes a PUA icon with a plain-space second frame
// (every nerd Style in variants.go does exactly this) must still read as
// RenderRequiresNerdFont, not "common" because a space happened to also be
// in the string. Checked per-rune, not per-frame, so a caller can pass
// Style.Frames joined or one frame at a time and get the same answer.
func Classify(glyph string) Renderability {
	best := RenderCommon
	for _, r := range glyph {
		switch {
		case isPUA(r):
			return RenderRequiresNerdFont // least-safe; nothing overrides it
		case isEmojiRange(r) && best == RenderCommon:
			best = RenderEmojiFallback
		}
	}
	return best
}

// isPUA reports whether r is in one of Unicode's three Private Use Areas.
// Nerd Fonts uses the BMP one (U+E000-U+F8FF) exclusively for the Font
// Awesome/Octicons/etc. glyphs this package uses, but a glyph sourced from
// elsewhere could land in a supplementary PUA plane, so all three are
// checked rather than hard-coding the one range nerdSet happens to use
// today.
func isPUA(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) ||
		(r >= 0xF0000 && r <= 0xFFFFD) ||
		(r >= 0x100000 && r <= 0x10FFFD)
}

// isEmojiRange reports whether r is a codepoint with no plausible
// non-emoji presentation: a supplementary-plane pictograph (the moon
// phases, the skull, the wrench -- U+1F300 and up has no BMP "plain text"
// meaning at all), or VARIATION SELECTOR-16, which is Unicode's own,
// unambiguous instruction to render the PRECEDING character as a color
// emoji rather than plain text (this is what turns emojiSet's warning sign
// into the glyph Jon actually sees, not the plain "⚠" alone).
//
// Deliberately does NOT flag the general Dingbats/Misc-Symbols block
// (U+2600-27BF) on its own: signalSet's own "✕" (U+2715, plain
// multiplication sign, dingbats block) lives there and is not an emoji --
// this codebase has been rendering it as plain text since the first
// variant shipped, and flagging it here would be exactly the kind of
// wrong-because-it-was-never-checked classification #11 warns against. A
// bare dingbat without VS16 renders as text on the overwhelming majority
// of terminals; only the supplementary-plane pictographs and an explicit
// VS16 are unambiguous enough to warn about.
func isEmojiRange(r rune) bool {
	return (r >= 0x1F000 && r <= 0x1FFFF) || r == 0xFE0F
}

// Candidate is one glyph the gallery shows for a state that no Variant has
// adopted -- #11 is explicit that the gallery's point is "discovery, not
// confirmation": showing only what shipped answers a question Jon did not
// ask ("what glyphs could I use").
type Candidate struct {
	State string
	Glyph string
	Note  string // why this is worth a look
}

// Candidates is deliberately short: a couple of real alternates per state
// where a genuinely different visual idea exists, not an exhaustive icon
// dump. Each one is a real, checkable codepoint (Nerd Font ones verified
// against glyphnames.json the same way nerdSet's were -- see variants.go's
// doc comment on that file for the two that were wrong on a first pass).
var Candidates = []Candidate{
	{State: "free", Glyph: "\uf192", Note: "fa-dot-circle-o -- hollow-ring alternative to nerdSet's filled fa-circle"},
	{State: "busy", Glyph: "\uf110", Note: "fa-spinner -- a dedicated spinner glyph instead of nerdSet's fa-refresh"},
	{State: "dead", Glyph: "\uf05e", Note: "fa-ban -- a slashed circle, reads more like 'stopped' than fa-times's X"},
	{State: "stale", Glyph: "☠", Note: "SKULL AND CROSSBONES U+2620 -- a plain-text alternative to emojiSet's colour skull (U+1F480), same meaning without needing an emoji font"},
	{State: "broken", Glyph: "\uf12a", Note: "fa-exclamation -- a plainer alternative to nerdSet's fa-bug for 'broken'"},
	{State: "hung", Glyph: "\u26d4", Note: "NO ENTRY sign U+26D4 -- common Unicode, not Nerd-Font-gated, unlike nerdSet's fa-exclamation-triangle"},
	{State: "unknown", Glyph: "\uf128", Note: "fa-question (nerdSet's own Unmapped glyph) shown here as a named candidate for 'unknown' itself"},
}
