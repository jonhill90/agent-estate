package lane

// AllStates is the full state enumeration from agent-supervisor's lanes.sh --
// not its header comment (which, measured against the code on 2026-08-14,
// enumerates only ten and folds menu-blocked/text-blocked into one "blocked"
// entry, and omits "supervisor" and "scrolled" outright) and not AGENTS.md
// (which claims eleven) -- but the thirteen distinct `state=` literals its
// classifier branches actually assign: free, busy, hung, menu-blocked,
// text-blocked, unsent, dead, stale, broken, service, supervisor, scrolled,
// unknown. Both prose counts are stale documentation, not authority; see
// agent-tui#3 for the count taken directly from lanes.sh's emit_rows and how
// it was taken. Kept here as a literal list rather than importing
// agent-supervisor (this repo imports no supervisor internals) -- it is the
// completeness bar every GlyphSet in variants.go is checked against, by
// TestEveryVariantNamesEveryState and by MissingStates below.
//
// This list drifting from lanes.sh silently is exactly agent-tui#3: a
// hand-kept copy of another program's states can omit one while every
// GlyphSet still defines it, so the completeness check it drives passes by
// construction and proves nothing. states_lanessh_test.go's
// TestAllStatesCoversLanesShStates guards against that drift directly, by
// parsing lanes.sh's own `state=` assignments and failing if any of them is
// missing here -- not by re-asserting this same list.
//
// stale, menu-blocked, unsent and scrolled are named here explicitly in
// comments because agent-supervisor#107's addendum and agent-tui#3 both
// called out entries from this list as "the forgotten ones" -- the ones a
// hastily-built variant, or a hastily-updated enumeration, is most likely to
// skip.
//
// never-busy is the newest one, and the reason it belongs on this same
// "easy to forget" list rather than folded silently into "unknown": found
// while verifying agent-tui#14's dependencies had landed -- agent-supervisor
// split "unknown" into "unknown" and "never-busy" (as#112/agent-supervisor#149), and this
// repo's CI (which cross-checks AllStates against a live lanes.sh -- see
// states_lanessh_test.go) went red on that drift before this PR's own
// changes touched anything. Unrelated to agent-tui#14's own scope, fixed here anyway
// because "CI green by SHA" is agent-tui#14's own bar, not a separate ticket to file
// and wait on.
var AllStates = []string{
	"free", "busy", "hung",
	"menu-blocked", "text-blocked", "unsent", // the ones easy to forget
	"dead", "stale", // stale: also easy to forget, and distinct from dead
	"broken", "service", "supervisor",
	"scrolled",   // agent-tui#3: the one this list was missing -- pane in tmux copy-mode
	"never-busy", // as#112/agent-supervisor#149: split out of "unknown", never patched in here
	"unknown",
}

// MissingStates returns the states in AllStates that set has no explicit
// Style for -- i.e. would render as set.Unmapped, silently degrading a
// nameable state to "unrecognized". A candidate variant must return an
// empty slice here; the picker's own startup check refuses to register a
// variant that doesn't (see variants.go's init-time assertion).
func MissingStates(set GlyphSet) []string {
	var missing []string
	for _, state := range AllStates {
		if _, ok := set.Styles[state]; !ok {
			missing = append(missing, state)
		}
	}
	return missing
}
