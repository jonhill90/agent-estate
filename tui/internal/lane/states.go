package lane

// AllStates is the full state enumeration from agent-supervisor's lanes.sh
// header comment and its JSON classifier branches (state=...): free, busy,
// hung, menu-blocked, text-blocked, unsent, dead, stale, broken, service,
// supervisor, unknown. Kept here as a literal list rather than importing
// agent-supervisor (this repo imports no supervisor internals) -- it is the
// completeness bar every GlyphSet in variants.go is checked against, by
// TestEveryVariantNamesEveryState and by MissingStates below.
//
// stale, menu-blocked and unsent are named here explicitly in comments
// because the addendum calls them out as "the forgotten ones" -- the ones
// a hastily-built variant is most likely to skip.
var AllStates = []string{
	"free", "busy", "hung",
	"menu-blocked", "text-blocked", "unsent", // the ones easy to forget
	"dead", "stale", // stale: also easy to forget, and distinct from dead
	"broken", "service", "supervisor", "unknown",
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
