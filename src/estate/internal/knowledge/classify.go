package knowledge

// classify decides Item.Publishable and Item.PublishBasis for source, at
// compile time (agent-estate#1028) -- see Item's own doc comment for the
// "UNCLASSIFIED MEANS PRIVATE" rule this function exists to enforce. Every
// item any of the seven sources produce is run through this call before it
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
// The sources safe to default public are github-stars and, as of
// agent-estate#1034, repo-docs: a starred repo is already public GitHub
// activity, visible on the operator's own public GitHub profile, with no
// personal content risk by construction -- starring something does not
// reveal employment, compensation, third parties, credentials, medical
// or family information, the six categories #1028's own probe was built
// to catch. repo-docs is AGENTS.md and docs/**/*.md in this repository,
// itself public (agent-estate is a public repo) -- the same reasoning,
// applied deliberately rather than left to the default branch below, per
// #1034's own instruction that this be an explicit table entry, never a
// fallthrough. loops-research and vault-fact are explicit table entries
// too, as of agent-estate#1059: both were previously private only by the
// default branch below, which made their privacy an accident of an
// unrelated fallthrough rather than a stated decision -- loops-research
// reads from jonhill90/Loops-Research, verified PRIVATE on GitHub
// 2026-09-04, and a plausible future edit to that repo's own visibility
// would have silently flipped 24 items publishable with no test catching
// it; vault-fact is the operator's own memory vault and is never public
// by the same reasoning #1028 applies to everything personal. Naming them
// explicitly does not change their outcome -- both were already private
// under the default -- it removes the accident: the default branch can no
// longer be the thing keeping them private. corpus-parameter and the
// other corpus-* sources remain on the default branch; #1059 scoped this
// pass to loops-research and vault-fact only.
//
// WHAT THIS WILL MISS. A source-level rule cannot see content, so it
// misses in exactly one direction: a starred repository whose name,
// owner, or description is itself sensitive (a private employer's own
// internal repo, starred; a repo named after a person) publishes
// unexamined, because github-stars is unconditionally public here. It
// does not miss in the other direction -- every source not explicitly
// listed above defaults private, so a personal item living in one of them
// is safe by default, at the cost of also keeping every non-personal item
// in them private until per-item classification exists. That asymmetry is
// deliberate (over-hiding is recoverable by a human deciding to publish; a
// leak is not) and it is the whole reason this is "crude but honest and
// auditable" rather than a complete answer -- #1028 asks that be said
// plainly, not hidden behind a green test.
func classify(source string) (publishable bool, basis string) {
	switch source {
	case "github-stars":
		return true, "github-stars: already public GitHub activity -- no per-item marker needed"
	case "repo-docs":
		return true, "repo-docs: AGENTS.md and docs/**/*.md are checked into a public repository -- explicit classification, not a fallthrough (agent-estate#1034)"
	case "loops-research":
		return false, "loops-research: sourced from jonhill90/Loops-Research, a private GitHub repository (verified 2026-09-04) -- explicit classification, not a fallthrough (agent-estate#1059)"
	case "vault-fact":
		return false, "vault-fact: the operator's own memory vault, never public -- explicit classification, not a fallthrough (agent-estate#1059)"
	case "catalogue-source":
		return false, "catalogue-source: the private source register may name locators under the operator's own home directory or catalogue an unpublished source -- explicit classification, not a fallthrough (agent-estate#1139 lane B)"
	case "skill-registry":
		// Explicit, not the default fallthrough -- agent-estate#1021 asks
		// "public or private? argue it rather than inheriting a default."
		// The file this source reads (docs/skills-registry.jsonl) IS
		// committed to this public repository, which is the exact fact
		// that makes repo-docs public above -- but repo-docs' own case is
		// scoped to already-written prose (AGENTS.md, docs/**/*.md) the
		// operator composed knowing it was public from the start. A
		// skills-registry row is different in kind: it is an ONGOING,
		// per-entry evaluation note (StatusReason for a rejected
		// candidate), the kind of unvetted content this doc comment
		// already names as the thing a source-level public default
		// cannot see into. It is also not the same shape as github-stars:
		// starring a repo is one already-public act with no content of
		// its own; a rejection reason is freshly composed prose whose
		// content has never been reviewed for publication the way this
		// repo's docs/ prose has. Kept private, on its own explicit line,
		// so a future entry with a candid rejection reason is private by
		// construction rather than by nobody having objected yet.
		return false, "skill-registry: per-entry evaluation notes are unvetted, ongoing content, unlike repo-docs' already-published prose or github-stars' content-free public act -- explicit classification, not a fallthrough (agent-estate#1021)"
	default:
		return false, source + ": source defaults to private -- no per-item publishability marker exists yet (agent-estate#1028)"
	}
}
