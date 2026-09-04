package knowledge

// classify decides Item.Publishable and Item.PublishBasis for source, at
// compile time (agent-estate#1028) -- see Item's own doc comment for the
// "UNCLASSIFIED MEANS PRIVATE" rule this function exists to enforce. Every
// item any of the four sources produce is run through this call before it
// enters a Result; no source constructs an Item's Publishable field
// itself.
//
// SOURCE-LEVEL DEFAULT, NOT PER-ITEM, AND HERE IS WHY. #1028 names three
// options: source-level defaults, a marker in the source itself, or
// explicit per-item classification with everything unmarked private. This
// package took the last of those and it collapses to the first, given a
// constraint #1028 also sets: sources are read-only here, so this package
// cannot add a per-item marker to the vault or corpus even if it wanted
// to, and today neither source carries one of its own -- a vault fact's
// `type:` and a corpus parameter's `weight`/`status` answer different
// questions (what kind of note, how firm a constraint) and reusing either
// as a publishability signal would be exactly the "clever classifier
// nobody can audit" #1028 warns against. Absent a real per-item signal,
// "classify every item individually, default the unmarked ones private"
// and "classify the whole source" produce an identical result for every
// item today; this function states that as a source-level rule directly
// instead of running the same branch once per item.
//
// The one source that IS safe to default public is github-stars: a
// starred repo is already public GitHub activity, visible on the
// operator's own public GitHub profile, with no personal content risk by
// construction -- starring something does not reveal employment,
// compensation, third parties, credentials, medical or family
// information, the six categories #1028's own probe was built to catch.
// Every other source defaults private until a real per-item signal
// exists to classify against.
//
// WHAT THIS WILL MISS. A source-level rule cannot see content, so it
// misses in exactly one direction: a starred repository whose name,
// owner, or description is itself sensitive (a private employer's own
// internal repo, starred; a repo named after a person) publishes
// unexamined, because github-stars is unconditionally public here. It
// does not miss in the other direction -- vault-fact, corpus-parameter
// and loops-research all default private, so a personal item living in
// any of those three is safe by default, at the cost of also keeping
// every non-personal item in them private until per-item classification
// exists. That asymmetry is deliberate (over-hiding is recoverable by a
// human deciding to publish; a leak is not) and it is the whole reason
// this is "crude but honest and auditable" rather than a complete
// answer -- #1028 asks that be said plainly, not hidden behind a green
// test.
func classify(source string) (publishable bool, basis string) {
	switch source {
	case "github-stars":
		return true, "github-stars: already public GitHub activity -- no per-item marker needed"
	default:
		return false, source + ": source defaults to private -- no per-item publishability marker exists yet (agent-estate#1028)"
	}
}
