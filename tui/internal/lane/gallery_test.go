package lane

import "testing"

// TestNerdSetCodepointsAreExact pins every nerdSet codepoint to the value
// checked against ryanoasis/nerd-fonts' own glyphnames.json (see
// variants.go's doc comment on nerdSet). This exists because a first pass
// at this file typed two of these from memory and both were wrong --
// fa-pause as U+F04B instead of U+F04C, fa-skull as U+F1CB instead of
// U+EE15 -- and neither wrong guess would have failed
// TestEveryVariantNamesEveryState, since that test only checks that a
// state has SOME glyph, not that the glyph is the right one. A silently
// wrong Nerd Font icon is worse than ASCII per #11's own framing ("a nerd
// set that renders stale as blank is worse than ASCII") -- rendering the
// WRONG icon with full confidence is the same failure shape one level
// worse: not blank, not sane either.
func TestNerdSetCodepointsAreExact(t *testing.T) {
	want := map[string]rune{
		"free":         0xF111, // fa-circle
		"busy":         0xF021, // fa-refresh
		"hung":         0xF071, // fa-exclamation-triangle
		"dead":         0xF00D, // fa-times
		"stale":        0xEE15, // fa-skull
		"broken":       0xF188, // fa-bug
		"menu-blocked": 0xF04C, // fa-pause
		"text-blocked": 0xF11C, // fa-keyboard-o
		"unsent":       0xF0E0, // fa-envelope
		"scrolled":     0xF07D, // fa-arrows-v
		"service":      0xF013, // fa-cog
		"supervisor":   0xF0AD, // fa-wrench
		"unknown":      0xF059, // fa-question-circle
	}
	for state, wantRune := range want {
		style, ok := nerdSet.Styles[state]
		if !ok {
			t.Fatalf("nerdSet has no style for %q", state)
		}
		got := []rune(style.Frames[0])
		if len(got) != 1 || got[0] != wantRune {
			t.Errorf("nerdSet[%q] first frame = %U, want %U", state, got, wantRune)
		}
	}
}

// TestNerdSetMissingStateIsRejected is agent-tui#11's required mutation
// check: remove one state from the Nerd Font set and confirm validation
// rejects it, restore, confirm green. Since variants.go's init() panics
// (it must, to catch this at process start for whatever is live in
// Variants, not only in a test binary), this test exercises the same
// MissingStates function directly rather than trying to catch a panic from
// a global var block -- the panic path is exactly what init() already
// does, and TestEveryVariantNamesEveryState already proves it runs clean
// today. What this test adds is proof that TAKING SOMETHING AWAY makes it
// go red, not just that today's data happens to pass.
func TestNerdSetMissingStateIsRejected(t *testing.T) {
	mutated := GlyphSet{
		ID:       nerdSet.ID,
		Name:     nerdSet.Name,
		Unmapped: nerdSet.Unmapped,
		Styles:   map[string]Style{},
	}
	for state, style := range nerdSet.Styles {
		if state == "stale" {
			continue // the mutation: drop one state
		}
		mutated.Styles[state] = style
	}

	missing := MissingStates(mutated)
	if len(missing) != 1 || missing[0] != "stale" {
		t.Fatalf("MissingStates on a nerdSet with \"stale\" removed = %v, want exactly [\"stale\"]", missing)
	}

	// Restore: the real nerdSet (unmutated, still in Variants) must still
	// pass -- proving the check is discriminating, not just permanently red.
	if missing := MissingStates(nerdSet); len(missing) != 0 {
		t.Fatalf("the real nerdSet is missing states %v -- should be complete", missing)
	}
}

func TestClassifyNerdSetGlyphsRequireNerdFont(t *testing.T) {
	for state, style := range nerdSet.Styles {
		if got := Classify(style.Frames[0]); got != RenderRequiresNerdFont {
			t.Errorf("Classify(nerdSet[%q]) = %v, want RenderRequiresNerdFont", state, got)
		}
	}
}

func TestClassifyShippedNonNerdGlyphsAreCommon(t *testing.T) {
	// signal is the only remaining non-Nerd-Font shipped variant --
	// agent-tui#24 dropped ascii and blocks, which made this same claim.
	for _, set := range []GlyphSet{signalSet} {
		for state, style := range set.Styles {
			for _, frame := range style.Frames {
				if got := Classify(frame); got != RenderCommon {
					t.Errorf("Classify(%s[%q] frame %q) = %v, want RenderCommon", set.ID, state, frame, got)
				}
			}
		}
	}
}

func TestClassifyFlagsColorEmojiFallback(t *testing.T) {
	// Not every emoji-range glyph is unambiguous (e.g. a plain "." would be
	// used for service/supervisor) -- this only asserts that an
	// unambiguous one (a supplementary-plane pictograph, here the skull
	// the dropped agent-tui#24 emoji set used for "stale") is flagged.
	// Literal rather than referencing that set, which no longer exists.
	skull := "\U0001F480"
	if got := Classify(skull); got != RenderEmojiFallback {
		t.Errorf("Classify(colour skull %q) = %v, want RenderEmojiFallback", skull, got)
	}
}

func TestClassifyPUABeatsEmojiWhenMixed(t *testing.T) {
	// A glyph containing both an emoji-range rune and a PUA rune must
	// classify as the LEAST safe answer -- PUA -- never silently downgrade
	// to the more common emoji-fallback label.
	mixed := "\U0001F480" // emoji skull + fa-circle
	if got := Classify(mixed); got != RenderRequiresNerdFont {
		t.Errorf("Classify(mixed emoji+PUA) = %v, want RenderRequiresNerdFont", got)
	}
}

func TestCandidatesHaveRealNonEmptyGlyphs(t *testing.T) {
	if len(Candidates) == 0 {
		t.Fatal("Candidates is empty -- the gallery's whole point is showing glyphs not yet in a set")
	}
	for _, c := range Candidates {
		if c.Glyph == "" {
			t.Errorf("candidate for %q has an empty glyph", c.State)
		}
		if c.State == "" {
			t.Errorf("candidate %q has no state", c.Glyph)
		}
		if c.Note == "" {
			t.Errorf("candidate %q for %q has no explanatory note", c.Glyph, c.State)
		}
	}
}

// TestCandidatesReferenceRealStates guards against a candidate drifting
// onto a state AllStates doesn't know, the same class of silent-drift bug
// agent-tui#3 was for the sets themselves.
func TestCandidatesReferenceRealStates(t *testing.T) {
	known := map[string]bool{}
	for _, s := range AllStates {
		known[s] = true
	}
	for _, c := range Candidates {
		if !known[c.State] {
			t.Errorf("candidate glyph %q references state %q, which is not in AllStates", c.Glyph, c.State)
		}
	}
}
