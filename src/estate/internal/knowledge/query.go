package knowledge

import (
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// QueryState is the typed shape of what Query found. #1019 requires three
// distinct absences that must never collapse into the same empty result:
// no item matched a real question against a real index, the index file
// itself could not be read at all, and (carried through per-item, not a
// top-level state) a source was down when the index was compiled. This
// type covers the first two; SourceStatuses on QueryResult covers the
// third -- see Query's own doc comment.
type QueryState string

const (
	// StateMatched means at least one item scored above zero. Matches
	// holds up to the cap; TotalMatched may exceed len(Matches).
	StateMatched QueryState = "matched"
	// StateNoMatch means the index was read fine but nothing in it
	// scored above zero against this question -- a real, empty answer,
	// not a failure.
	StateNoMatch QueryState = "no_match"
	// StateIndexMissing means no file exists at the given path yet --
	// `estate knowledge` has never run, or its output was deleted.
	StateIndexMissing QueryState = "index_missing"
	// StateIndexUnreadable means a file exists at the path but is not a
	// valid compiled index (truncated write, foreign JSON, corruption).
	StateIndexUnreadable QueryState = "index_unreadable"
	// StateWithheldPrivate means at least one item scored above zero, but
	// every one of them is Item.Publishable == false and the caller did
	// not ask for private material -- agent-estate#1033. This is
	// deliberately its own state, never collapsed into StateNoMatch: "no
	// item answers this" and "an item answers this but you may not see
	// it by default" are different answers, and conflating them is
	// exactly the error class the other three states already exist to
	// prevent. Reason names the count; WithheldPrivate on QueryResult
	// carries it as a typed field too.
	StateWithheldPrivate QueryState = "withheld_private"
	// StateMatchedWithheldMajority means at least one publishable item
	// scored above zero and was returned -- same population as
	// StateMatched -- but MORE items were withheld as private than were
	// returned (agent-estate#1052). This exists so a caller reading the
	// State field as a bare string, not just $?, can tell "answered" from
	// "technically answered, mostly hidden" without doing its own ratio
	// arithmetic on WithheldPrivate/TotalMatched.
	//
	// Deliberately maps to the SAME exit code as StateMatched (0), never
	// a new one -- see knowledgeQueryExitCode in main.go. A query in this
	// state still produced a real, citable public answer; three of the
	// golden set's own publishable-only hits (agent-estate#1023's
	// stars-01/02/03) sit well past the majority line measured against a
	// real index (24 public/58 private, 5/72, 6/97), so making this state
	// a distinct non-zero exit would turn those honest hits into runner
	// failures and move the golden score -- #1052 is explicit that must
	// not happen. The louder signal lives in the printed state word and a
	// dedicated banner line (see printKnowledgeQuery in main.go), not in
	// the exit code.
	StateMatchedWithheldMajority QueryState = "matched_withheld_majority"
)

// QueryLimit is the hard cap on items Query returns in one call --
// #1019's "small by construction" requirement. Ten was picked because it
// is small enough to read in one glance (the point of the cap) while
// rarely being so small that a genuinely on-topic item falls just short
// of it; NotReturned always states exactly how many were cut so the cap
// is never mistaken for a complete answer.
const QueryLimit = 10

// Match is one ranked, cited pointer into the compiled index. Only Tier1
// -- the one-line summary -- travels here; Tier2 and Tier3 stay behind a
// second lookup (Get), which is the progressive-disclosure two-step
// #1019 asks for: the first response is pointers, never bodies.
type Match struct {
	// ID and Source together are this item's citation -- #1019's "an
	// item that cannot name its source is not returned" requirement.
	// Neither is ever empty on a returned Match; see the citation test
	// in query_test.go.
	ID        string `json:"id"`
	Source    string `json:"source"`
	Permalink string `json:"permalink"`
	Tier1     string `json:"tier1"`
	// Score is this item's BM25 relevance figure against the question
	// (agent-estate#1054), rounded to the nearest integer for display --
	// sorting itself is done on the unrounded float (see scored.score in
	// Query), so two items a whole point apart here can still be
	// correctly ordered even though BM25Scorer.Score returned figures
	// closer than that. It is not a probability, not a raw term count,
	// and not comparable across two different questions or two different
	// indexes -- only across Matches returned for the SAME question
	// against the SAME index. MatchedTerms names exactly which (stemmed)
	// words contributed to it, so the basis stays legible even though the
	// number itself is now continuous rather than a literal count.
	Score        int      `json:"score"`
	MatchedTerms []string `json:"matched_terms"`
	// Publishable is copied from the source Item -- see Item's own doc
	// comment. Under the default, publishable-only filter every
	// returned Match has this true; it only ever reads false when the
	// caller explicitly asked for private material (includePrivate) and
	// this particular item is one of the private ones shown -- so a
	// reader of Matches can tell which entries are private even inside
	// private mode, not just that private mode was on (agent-estate#1033).
	Publishable bool `json:"publishable"`
}

// QueryResult is Query's full, typed answer.
type QueryResult struct {
	State    QueryState `json:"state"`
	Reason   string     `json:"reason,omitempty"` // set for IndexMissing/IndexUnreadable/WithheldPrivate/MatchedWithheldMajority
	Question string     `json:"question,omitempty"`
	// TagFilters is the set of exact structural/synaptic tags extracted
	// from Question and applied BEFORE term scoring (agent-estate#1024)
	// -- "status:open" filters to items carrying that exact tag, never
	// items whose text merely contains "status" and "open" as separate
	// words. Always states what was applied, even when empty, so a
	// reader of the result never has to guess whether tag filtering ran.
	TagFilters []string `json:"tag_filters,omitempty"`
	// RankingBasis states, in one sentence, how Score was computed --
	// #1019's "the output must make the basis legible" requirement.
	RankingBasis string  `json:"ranking_basis,omitempty"`
	Matches      []Match `json:"matches,omitempty"`
	// TotalMatched is every PUBLISHABLE item that scored above zero
	// (or, in private mode, every item regardless of Publishable),
	// before the cap -- the same population Matches is drawn from.
	TotalMatched int `json:"total_matched"`
	// NotReturned is TotalMatched minus len(Matches) -- how many real
	// matches this call did not show because of the display cap. Always
	// stated, never implied.
	NotReturned int `json:"not_returned"`
	// WithheldPrivate is how many otherwise-matching items were excluded
	// because Item.Publishable is false and the caller did not ask for
	// private material (agent-estate#1033) -- always 0 when
	// PrivateIncluded is true, since nothing is withheld in that mode.
	// Counted separately from NotReturned on purpose: one is "you asked
	// to see fewer than matched", the other is "you were not shown this
	// because it is private" -- collapsing them would hide the reason.
	WithheldPrivate int `json:"withheld_private"`
	// PrivateIncluded is true when this call was made with
	// includePrivate -- the explicit, visible marker #1028's point 3
	// asks for: a caller reading only this result (not the call site)
	// can still tell private material may be present below.
	PrivateIncluded bool `json:"private_included"`
	// SourceStatuses carries the compiled index's own per-source
	// OK/Reason forward unchanged -- #1019's third absence: a source
	// that was unreadable when the index was BUILT (as opposed to no
	// item matching, or the index itself being unreadable NOW).
	SourceStatuses   []SourceResult `json:"source_statuses,omitempty"`
	IndexGeneratedAt time.Time      `json:"index_generated_at,omitzero"`
	// IndexGeneratedBy carries the compiled index's own GeneratedBy
	// forward unchanged -- agent-estate#1082, the same "carry the
	// build-time record forward, never recompute it here" discipline
	// SourceStatuses already uses. The comparison against the CURRENTLY
	// RUNNING checkout's own commit needs live filesystem/git access this
	// package deliberately does not have (Query takes only an index path
	// and a question, mirroring CoverageState's own doc comment on why
	// the staleness comparison itself lives in main.go); this field is
	// what a caller with that access folds against.
	IndexGeneratedBy GeneratedBy `json:"index_generated_by,omitzero"`
	// Coverage is the machine-readable trustworthiness signal every
	// successfully-read QueryResult carries -- see CoverageState. It is
	// the zero Coverage{} on StateIndexMissing/StateIndexUnreadable
	// (there is no compiled index to have a coverage opinion about);
	// every other state always sets it, even StateNoMatch, because a
	// source failing at build time has nothing to do with whether this
	// particular question happened to score anything.
	Coverage Coverage `json:"coverage"`
	// Contradictions flags a corpus-question and a vault-fact/corpus-
	// directive that both landed in Matches on the same matched terms --
	// agent-estate#1051. Deliberately NOT part of Coverage: see
	// Contradiction's own doc comment (contradiction.go) for why this
	// needed its own field rather than a new Coverage reason. Empty on
	// every state Matches is empty on, and whenever no pair happens to
	// share a term -- absence here is not itself evidence of agreement,
	// only that this package's narrow, deterministic check found nothing.
	Contradictions []Contradiction `json:"contradictions,omitempty"`
	// IndexItemCount is len(items) in the successfully-read compiled
	// index, set on every state Read succeeded on (every state except
	// StateIndexMissing/StateIndexUnreadable, where there was no index to
	// count) -- agent-estate#1124. Without this, "no item matches the
	// question" against a valid-but-EMPTY index (a truncated or partial
	// write, a full disk, an interrupted regeneration -- #1123 narrowed
	// how this happens but did not close it) is byte-identical to the
	// same message against a real, populated index that genuinely has
	// nothing relevant: both are StateNoMatch, both mean "the index was
	// read fine", and a caller told "no match" cannot tell "rephrase your
	// question" from "your index is broken, regenerate it" without this
	// number. Always present (even 0) so a caller never has to
	// special-case "count omitted means zero".
	IndexItemCount int `json:"index_item_count"`
}

// CoverageState is the taxonomy for whether a QueryResult can be trusted
// as a complete answer -- agent-estate#1058. It replaces what would
// otherwise become three separately-shaped signals (a source that failed
// at build time, a caller-visible policy withholding, and staleness) with
// one structure a caller reads before treating a real answer and exit 0
// as a COMPLETE answer. Human prose (the "note:"/banner lines a caller
// prints) is derived from this, never the only signal -- #1052's finding,
// generalised.
//
// Only the complete/limited/degraded/mixed arms are populated by this
// package today. `stale` and `unknown` are #1047's own staleness
// comparison (printIndexFreshness / freshnessFindings in main.go), which
// needs live filesystem access this package deliberately does not have
// (Query takes only an index path and a question) -- the comparison itself
// stays in main.go; only the fold-in (WithFreshnessReason, below) lives
// here, so a caller that already has filesystem access only ever needs to
// call it, never invent a new shape or a second copy of the compose-to-
// mixed rule withLimitedReason already established. `binary_mismatch`
// (agent-estate#1082) follows the exact same split: the comparison needs
// the CURRENTLY RUNNING checkout's own commit, which main.go resolves via
// knowledge.ResolveBuildCommit and folds in the same way.
type CoverageState string

const (
	// CoverageComplete means every source that fed the compiled index was
	// read successfully at build time and nothing was withheld by policy.
	CoverageComplete CoverageState = "complete"
	// CoverageLimited means the query itself withheld eligible material by
	// policy -- StateWithheldPrivate or StateMatchedWithheldMajority's own
	// population (#1052, #1033), expressed here as the same structure a
	// degraded source uses rather than a second, differently-shaped signal.
	CoverageLimited CoverageState = "limited"
	// CoverageDegraded means a source the compiled index depends on could
	// not be read when it was built -- agent-estate#1058, the arm this
	// issue adds. Deliberately not named "incomplete": in public mode,
	// withholding by policy (CoverageLimited) is the boundary working as
	// intended, not a malfunction, and a word implying failure would train
	// a caller to ignore it. A degraded source IS a malfunction -- the two
	// must not share a label.
	CoverageDegraded CoverageState = "degraded"
	// CoverageStale means a source the compiled index depends on has been
	// OBSERVED to have changed since the index was built -- its mtime is
	// demonstrably newer than IndexGeneratedAt. Reasons names the source,
	// the way CoverageDegraded already does; report, never repair -- this
	// package (and the caller folding it in) only ever states the finding,
	// it does not regenerate anything. Not set by this package directly
	// (see CoverageState's own doc comment for why) -- a caller with
	// filesystem access folds it in via WithFreshnessReason.
	CoverageStale CoverageState = "stale"
	// CoverageUnknownFreshness means a source's freshness could NOT be
	// determined at all -- github-stars is read live via `gh api
	// user/starred` with no local file to stat, so there is nothing to
	// compare IndexGeneratedAt against. Deliberately a state distinct from
	// both CoverageComplete and CoverageStale, never collapsed into either:
	// "confirmed unchanged" (complete), "confirmed newer" (stale, an
	// actionable finding -- regenerate) and "never actually checked" are
	// three different claims, and #1080's governing rule -- absence of
	// evidence is not evidence of freshness -- forbids the third from ever
	// rendering as the reassuring first, while folding it into the second
	// would overclaim a staleness that was never actually observed. See
	// CoverageStale.
	CoverageUnknownFreshness CoverageState = "unknown"
	// CoverageBinaryMismatch means the compiled index's own GeneratedBy
	// commit and the CURRENTLY RUNNING checkout's commit were both
	// positively resolved and differ -- agent-estate#1082. Deliberately
	// its own state, not folded into CoverageStale: CoverageStale means a
	// SOURCE changed since the index was built, which says nothing about
	// whether the sources here are current -- an index-vs-binary mismatch
	// can happen with every source perfectly fresh (a doc-only commit
	// landed since the index was built) or with the source axis totally
	// unaffected. Calling that "stale" would overclaim staleness about the
	// sources when the actual finding is about the code that read them.
	// This is detection, never prevention or refusal (#1082's own
	// framing): an index built by a different commit is usually fine, so
	// this state exists for a caller to know, not to be blocked by --
	// Query never refuses on it and neither does any caller folding it in.
	// Like CoverageStale/CoverageUnknownFreshness, not set by this package
	// directly -- a caller with access to its own running checkout folds
	// it in via WithFreshnessReason, passing this state.
	CoverageBinaryMismatch CoverageState = "binary_mismatch"
	// CoverageMixed means more than one of the above applied to the same
	// result -- e.g. a source failed AND the answer was also withheld by
	// policy. Reasons names each contributing state individually; Mixed is
	// the top-level signal a caller checking only Coverage.State still
	// catches all of them without doing its own boolean arithmetic.
	CoverageMixed CoverageState = "mixed"
)

// CoverageReason is one concrete, per-cause entry behind a non-complete
// Coverage -- which source (if any), which state, and enough detail for a
// caller to act: fix the source and rebuild (degraded), widen scope or
// pass --private (limited), regenerate (stale), or nothing at all --
// unknown freshness has no fix, only a caveat. A caller
// reading only Coverage.State still knows "this may not be trustworthy";
// a caller reading Reasons knows what to do about it.
type CoverageReason struct {
	State  CoverageState `json:"state"`
	Source string        `json:"source,omitempty"`
	Detail string        `json:"detail"`
}

// Coverage is the machine-readable trustworthiness signal every
// successfully-read QueryResult carries -- see CoverageState. Complete
// with no Reasons is the good case; any other State always carries at
// least one Reason naming why.
type Coverage struct {
	State   CoverageState    `json:"state"`
	Reasons []CoverageReason `json:"reasons,omitempty"`
}

// coverageFromSources builds the degraded arm of Coverage from a compiled
// index's own per-source OK/Reason record (agent-estate#1058) -- the data
// was already there (Generate writes it; see knowledge.go's SourceResult
// doc comment). This is wiring an existing record through, not a new
// mechanism.
func coverageFromSources(sources []SourceResult) Coverage {
	var reasons []CoverageReason
	for _, s := range sources {
		if s.OK {
			continue
		}
		detail := s.Reason
		if detail == "" {
			detail = "source could not be read when the index was built"
		}
		reasons = append(reasons, CoverageReason{
			State:  CoverageDegraded,
			Source: s.Name,
			Detail: detail,
		})
	}
	if len(reasons) == 0 {
		return Coverage{State: CoverageComplete}
	}
	return Coverage{State: CoverageDegraded, Reasons: reasons}
}

// withLimitedReason folds query-time privacy withholding
// (StateWithheldPrivate / StateMatchedWithheldMajority, #1033/#1052) into
// cov as its own CoverageReason -- the same shared structure a degraded
// source uses, rather than a second, differently-shaped signal (#1058's
// sequencing note on its issue). A cov already CoverageDegraded (a source
// failed AND the query was also withheld) becomes CoverageMixed so a
// caller reading only Coverage.State still catches both; a cov that was
// CoverageComplete becomes CoverageLimited outright.
func (cov Coverage) withLimitedReason(detail string) Coverage {
	switch cov.State {
	case CoverageComplete:
		cov.State = CoverageLimited
	case CoverageDegraded:
		cov.State = CoverageMixed
	}
	cov.Reasons = append(cov.Reasons, CoverageReason{State: CoverageLimited, Detail: detail})
	return cov
}

// WithFreshnessReason folds one staleness-comparison finding (#1047,
// folded into Coverage's own structure by #1080) into cov -- state must be
// CoverageStale or CoverageUnknownFreshness, source names which of
// knowledge.Generate's sources the finding is about (empty when the
// finding covers the comparison itself, e.g. source paths could not be
// resolved at all), and detail is the human-readable why.
//
// Uses the exact compose-to-mixed rule withLimitedReason already
// established, generalised from one folded-in dimension to however many
// distinct non-complete causes a single result ends up carrying: a cov
// still CoverageComplete becomes exactly the state being folded in; a cov
// that already carries this same state (e.g. a second stale source found
// after a first) stays that state, with the new reason simply appended;
// any other existing state (Limited, Degraded, or a different freshness
// finding already folded in -- Stale and UnknownFreshness are distinct
// causes and one folding in after the other IS "more than one applied")
// becomes CoverageMixed, so a caller reading only Coverage.State still
// catches every contributing cause without doing its own boolean
// arithmetic over Reasons.
func (cov Coverage) WithFreshnessReason(state CoverageState, source, detail string) Coverage {
	switch cov.State {
	case CoverageComplete:
		cov.State = state
	case state:
		// Already this same single-cause state -- another finding of the
		// same kind doesn't change State, only adds a Reason below.
	default:
		cov.State = CoverageMixed
	}
	cov.Reasons = append(cov.Reasons, CoverageReason{State: state, Source: source, Detail: detail})
	return cov
}

// stopWords are filtered out of a question before scoring -- common
// English function words that would otherwise match nearly every item
// and dilute the score without narrowing anything. This is a fixed,
// short list, not a language model: a term not on it is scored, however
// common it may be in practice.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "of": true, "to": true, "in": true,
	"on": true, "for": true, "is": true, "are": true, "was": true, "were": true,
	"what": true, "who": true, "did": true, "does": true, "do": true,
	"about": true, "and": true, "or": true, "with": true, "at": true,
	"jon": true, "i": true, "me": true, "my": true, "that": true, "this": true,
}

// splitWords lowercases s and splits it on anything that isn't a letter
// or digit -- punctuation is a separator, never part of a term, so
// "auth?" and "auth" land on the same word.
func splitWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !('a' <= r && r <= 'z' || '0' <= r && r <= '9')
	})
}

// queryTerms splits question into the distinct, non-stop, stemmed words
// Query scores against -- stemmed so a question's own inflection
// ("refreshed") reaches the same term as an item's ("refresh"), per
// agent-estate#1054. Deduplicated because a question scores each
// distinct term against an item once; fieldTermCounts (below) is the
// non-deduplicated sibling used for building a document's own term
// frequencies, where repetition matters.
func queryTerms(question string) []string {
	seen := map[string]bool{}
	var terms []string
	for _, f := range splitWords(question) {
		if len(f) <= 2 || stopWords[f] {
			continue
		}
		s := stem(f)
		if seen[s] {
			continue
		}
		seen[s] = true
		terms = append(terms, s)
	}
	return terms
}

// fieldTermCounts tokenizes text with the SAME stopword and length rules
// queryTerms applies to a question -- so a document and a question that
// mean the same thing land in the same term space -- but does not
// deduplicate: BM25's term-frequency component needs to know a term
// appeared three times, not just that it appeared.
func fieldTermCounts(text string) map[string]int {
	counts := map[string]int{}
	for _, f := range splitWords(text) {
		if len(f) <= 2 || stopWords[f] {
			continue
		}
		counts[stem(f)]++
	}
	return counts
}

// searchableText is every field of an item a question term may match --
// Tier1 and Tier2 (Tier3 is a pointer, never indexed content) plus both
// tag classes, joined into one string. Kept as the single-field view of
// an item's indexed text (e.g. "does anything here mention X at all");
// the scorer itself (bm25.go) reads Tier1/tags and Tier2 as two SEPARATE
// fields -- tier1SearchableText/tier2SearchableText -- because BM25 field
// weighting needs to know which field a term came from, information this
// flattened form throws away.
func searchableText(it Item) string {
	return tier1SearchableText(it) + " \x1f " + tier2SearchableText(it)
}

// tagFilterPattern matches one whitespace-delimited token shaped like an
// exact structural/synaptic tag -- "status:open", "weight:hard": a single
// colon, letters/digits/underscore/hyphen on the key side, letters/digits/
// underscore/dot/hyphen on the value side, and nothing else in the token.
// A URL ("https://x") never matches -- the character right after the
// colon in a URL is "/", which this pattern's value-side class excludes --
// so an ordinary question is never misread as carrying a tag filter it
// didn't intend. This is deliberately the SAME "key:value" shape every
// tag in this package is actually written in (corpus.go's weight:/status:,
// the only colon-shaped tags this index produces today) -- see
// extractTagFilters's own doc comment for why a bare word is never treated
// as a tag filter even though bare tags exist (vault.go's f.Type,
// stars.go's "github-stars").
var tagFilterPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*:[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// extractTagFilters splits a raw question into its exact-tag-filter tokens
// and everything else (agent-estate#1024). Composable and orthogonal to
// term search on purpose: "status:open auth tokens" filters to items
// carrying the exact tag status:open, then ranks by ordinary term overlap
// ONLY among that filtered set -- "filter first, rank within" from the
// issue. Tags are returned lowercased (comparison is case-insensitive,
// matching searchableText's own lowering); a bare word with no colon
// (e.g. "github-stars", a real structural tag with no key:value shape) is
// deliberately left in the remaining question and scored as an ordinary
// term instead of being treated as an exact filter -- this issue asks
// specifically for "status:open means the 54, not the 1,230", not for
// every bare structural tag to become filterable, and doing the latter
// would make an ordinary word like "auth" (which is never a whole tag on
// its own) ambiguous with a real one.
func extractTagFilters(question string) (tags []string, remaining string) {
	fields := strings.Fields(question)
	var rest []string
	seen := map[string]bool{}
	for _, f := range fields {
		if tagFilterPattern.MatchString(f) {
			lower := strings.ToLower(f)
			if !seen[lower] {
				seen[lower] = true
				tags = append(tags, lower)
			}
			continue
		}
		rest = append(rest, f)
	}
	return tags, strings.Join(rest, " ")
}

// knownTags is the lowercased vocabulary of every structural or synaptic
// tag ANY item in the index carries, regardless of that item's own
// Publishable value -- extractTagFilters's output is checked against this
// so a filter naming a tag nothing in the index has ever carried reports
// StateNoMatch honestly instead of silently returning zero items in the
// exact shape a real, empty, well-formed query also produces
// (agent-estate#1024: "an unknown tag is no_match, not an error, and
// never silently ignored"). Checked against the FULL item set, private
// items included, so a tag that exists only on private items is still
// "known" -- the privacy filter downstream is what turns that into
// StateWithheldPrivate, not this function turning it into a false
// StateNoMatch.
func knownTags(items []Item) map[string]bool {
	known := map[string]bool{}
	for _, it := range items {
		for _, t := range it.StructuralTags {
			known[strings.ToLower(t)] = true
		}
		for _, t := range it.SynapticTags {
			known[strings.ToLower(t)] = true
		}
	}
	return known
}

// unknownTagReasons renders one unresolved tag-filter's reason per entry
// in unknown, distinguishing the two readings a bare "not present in the
// compiled index" collapses together (agent-estate#1120's correction
// comment): a tag naming no known source at all (a typo -- fix the
// query) versus a tag naming a source that DID try to build and failed
// (fix the source -- the query was fine). Both facts already exist on
// QueryResult before this function runs -- sources is res.Sources,
// carrying each reader's own OK/Reason, and unknown is the tag-filter
// path's own list of what did not resolve -- so this is wording over
// data already computed, not a new field.
func unknownTagReasons(unknown []string, sources []SourceResult) []string {
	reasons := make([]string, 0, len(unknown))
	for _, tf := range unknown {
		if src, ok := failedSourceForTag(tf, sources); ok {
			reasons = append(reasons, fmt.Sprintf(
				"%s -- names source %q, which failed to build: %s",
				tf, src.Name, src.Reason))
			continue
		}
		reasons = append(reasons, fmt.Sprintf("%s -- not present in the compiled index", tf))
	}
	return reasons
}

// failedSourceForTag looks for a source in sources that is both !OK
// (failed to build, so it produced zero items and could never have made
// this tag "known" no matter what it names) and whose Name plausibly
// names the same source as tag's "source:<name>" suffix. The comparison
// tolerates a trailing "s" on either side because addSourceTag derives
// the tag from each ITEM's own Source string, while SourceResult.Name is
// each READER's own family name, and those two vocabularies already
// disagree on plurality for at least one real source (vault.go's items
// carry Source "vault-fact", singular, while vaultSource's own
// SourceResult.Name is "vault-facts", plural) -- not a bug this function
// is fixing, just the naming gap it has to see through to tell "this
// source failed" from "no such source" for that exact tag.
// isCorpusKindTag reports whether name (already prefix-stripped and
// singularised the same way failedSourceForTag treats every other tag) is
// one of corpusKinds' own "corpus-<kind>" values -- agent-estate#1120's
// reopened gap. corpusSource (corpus.go) compiles all of corpusKinds into
// ONE reader pass and reports it as a single SourceResult{Name:
// "corpus-items"}, but each ITEM out of that pass carries its own kind as
// its Source (corpusSourceName: "corpus-parameter", "corpus-directive",
// "corpus-question", "corpus-correction") -- a different word entirely
// from "corpus-items", not a plurality mismatch, so the trailing-"s" trim
// below can never bridge it no matter how it's tuned. This is an explicit,
// enumerated exception rather than a widening of that plural-tolerant
// match: it lists exactly the four tag values corpusSource is known to
// produce and nothing else, so it cannot start matching some other
// source's name by accident the way a looser general rule could.
func isCorpusKindTag(name string) bool {
	for _, k := range corpusKinds {
		if name == "corpus-"+k {
			return true
		}
	}
	return false
}

func failedSourceForTag(tag string, sources []SourceResult) (SourceResult, bool) {
	const prefix = "source:"
	if !strings.HasPrefix(tag, prefix) {
		return SourceResult{}, false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(tag, prefix), "s")
	for _, s := range sources {
		if s.OK {
			continue
		}
		if strings.TrimSuffix(strings.ToLower(s.Name), "s") == name {
			return s, true
		}
		if s.Name == "corpus-items" && isCorpusKindTag(name) {
			return s, true
		}
	}
	return SourceResult{}, false
}

// itemHasAllTags reports whether it carries EVERY one of tags (already
// lowercased) as a whole, exact structural or synaptic tag -- never a
// substring match, which is what term scoring already does elsewhere in
// this file and what this filter exists to be orthogonal to. len(tags)==0
// (no tag filter in the question) always passes, so this is a no-op for
// every question this issue's filter doesn't change.
func itemHasAllTags(it Item, tags []string) bool {
	if len(tags) == 0 {
		return true
	}
	have := map[string]bool{}
	for _, t := range it.StructuralTags {
		have[strings.ToLower(t)] = true
	}
	for _, t := range it.SynapticTags {
		have[strings.ToLower(t)] = true
	}
	for _, t := range tags {
		if !have[t] {
			return false
		}
	}
	return true
}

const rankingBasisText = "score = Okapi BM25 (k1=1.2, b=0.75) over stemmed, " +
	"stop-word-filtered question terms against the item's own tier1, tier2, " +
	"structural_tags and synaptic_tags -- tier1/tags weighted 3x tier2 " +
	"(tier1FieldWeight=3, tier2FieldWeight=1, agent-estate#1043's " +
	"measured ratio, carried forward rather than discarded) as BM25 field " +
	"weights; for repo-docs items only, a section's own LEAF heading is " +
	"scored as tier1 (the 3x weight) while its ANCESTOR heading path is a " +
	"separate field weighted 1x (ancestorFieldWeight=1, agent-estate#1113 " +
	"-- ancestors are searchable but no longer inflate score at the leaf's " +
	"weight), and every other source has no ancestor field so this split " +
	"does not apply to it -- see bm25.go; no minimum-match floor: a rare " +
	"term matching once can outrank several common terms matching by " +
	"coincidence, so an item needs only ONE weighted term match (score > 0) " +
	"to be returned at all -- see BM25Scorer in bm25.go, agent-estate#1054; " +
	"the printed score is BM25's own figure rounded to the nearest integer " +
	"for display; ties on the unrounded figure broken by item id, oldest first"

// Query reads the compiled index at indexPath and returns a small,
// ranked, cited set of items scored against question -- see QueryState
// for the four ways this can come back empty without being the same
// answer. limit <= 0 uses QueryLimit.
//
// PUBLISHABLE-ONLY BY DEFAULT (agent-estate#1033). includePrivate=false
// (what every caller gets unless it explicitly asks otherwise) drops any
// item with Publishable == false before it is ever scored into
// TotalMatched or Matches -- a caller that asks for nothing special gets
// nothing private, full stop. includePrivate=true lifts the filter and
// sets PrivateIncluded on the result, so the private material is both
// present AND visibly marked as present in the result itself, not just
// selectable via a call-site flag nobody reading the output can see.
//
// NO SYNTHESIS: every returned Match is a pointer (id, source,
// permalink, the item's own Tier1) copied verbatim from the index, never
// summarised, reworded or generated -- see this package's own doc
// comment on honest absence and #1019's "no fabrication" requirement.
func Query(indexPath, question string, limit int, includePrivate bool) QueryResult {
	if limit <= 0 {
		limit = QueryLimit
	}

	res, err := Read(indexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return QueryResult{
				State:  StateIndexMissing,
				Reason: fmt.Sprintf("no compiled index at %s -- run `estate knowledge` first", indexPath),
			}
		}
		return QueryResult{
			State:  StateIndexUnreadable,
			Reason: err.Error(),
		}
	}

	out := QueryResult{
		Question:         question,
		RankingBasis:     rankingBasisText,
		SourceStatuses:   res.Sources,
		IndexGeneratedAt: res.GeneratedAt,
		IndexGeneratedBy: res.GeneratedBy,
		PrivateIncluded:  includePrivate,
		// Set before any early return below -- a source that failed at
		// build time is reported regardless of whether this particular
		// question goes on to match anything (agent-estate#1058).
		Coverage: coverageFromSources(res.Sources),
		// Set before any early return below, same reasoning as Coverage
		// above -- agent-estate#1124 needs this on every no_match shape
		// this function can return, not just the term-scoring one at the
		// bottom.
		IndexItemCount: len(res.Items),
	}

	tagFilters, remainingQuestion := extractTagFilters(question)
	out.TagFilters = tagFilters
	terms := queryTerms(remainingQuestion)
	if len(terms) == 0 && len(tagFilters) == 0 {
		out.State = StateNoMatch
		out.Reason = "question contained no scoreable terms after stop-word removal"
		return out
	}

	if len(tagFilters) > 0 {
		known := knownTags(res.Items)
		var unknown []string
		for _, tf := range tagFilters {
			if !known[tf] {
				unknown = append(unknown, tf)
			}
		}
		if len(unknown) > 0 {
			// Silently dropping an unrecognised filter would fall through
			// to scoring the whole index and look like a real answer --
			// #1024 is explicit this must be its own honest no_match
			// instead, exactly like StateIndexMissing/StateNoMatch never
			// collapsing into each other elsewhere in this file.
			out.State = StateNoMatch
			out.Reason = fmt.Sprintf("unknown tag(s): %s", strings.Join(unknownTagReasons(unknown, res.Sources), ", "))
			return out
		}
		out.RankingBasis += fmt.Sprintf("; filtered first to items carrying the exact tag(s) %s, ranked only within that set",
			strings.Join(tagFilters, ", "))
	}

	type scored struct {
		item    Item
		score   float64
		matched []string
	}
	// Built over res.Items -- the whole index, unfiltered by tag or
	// privacy -- so a term's idf never shifts between a default-mode and
	// a --private call against the same index (see NewBM25Scorer's own
	// doc comment).
	scorer := NewBM25Scorer(res.Items)
	var all []scored
	withheldPrivate := 0
	for _, it := range res.Items {
		if !itemHasAllTags(it, tagFilters) {
			continue
		}
		score, matched := scorer.Score(it, terms)
		if score <= 0 && len(terms) > 0 {
			// No hard floor (agent-estate#1054): an item with a real,
			// nonzero weighted overlap is a real candidate, however
			// small -- BM25's own weighting, not a minimum-count
			// threshold, is what keeps a coincidental common-term match
			// from outranking a genuine one. Only a literal zero (no
			// query term appears in this item's text at all) excludes.
			// len(terms) == 0 is the tag-filter-only case (#1024,
			// "status:open" with no other words): every term-scoring
			// concept is moot there, and exclusion is already fully
			// handled by itemHasAllTags above.
			continue
		}
		if !includePrivate && !it.Publishable {
			withheldPrivate++
			continue
		}
		all = append(all, scored{it, score, matched})
	}

	// Sort by BM25 score, item id breaking ties (oldest first).
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].item.ID < all[j].item.ID
	})

	out.TotalMatched = len(all)
	out.WithheldPrivate = withheldPrivate
	if len(all) == 0 {
		if withheldPrivate > 0 {
			// A real match existed -- it was simply private, and this
			// call did not ask for private material. Distinct from
			// StateNoMatch on purpose: see StateWithheldPrivate's own
			// doc comment.
			out.State = StateWithheldPrivate
			out.Reason = fmt.Sprintf("%d item(s) matched but %s private -- rerun with --private to include them",
				withheldPrivate, plural(withheldPrivate, "is", "are"))
			out.Coverage = out.Coverage.withLimitedReason(out.Reason)
			return out
		}
		out.State = StateNoMatch
		if len(res.Items) == 0 {
			// Distinct from the ordinary "nothing scored" shape just below
			// this block (which deliberately keeps Reason empty, as it
			// does today -- agent-estate#1124's own issue narrows the fix
			// to the empty-index case specifically and flags widening
			// ordinary no_match as a separate decision, not implied by
			// this one). An index that is present, valid, readable and
			// fresh but carries zero items is a build defect (a truncated
			// write, a full disk, an interrupted regeneration -- #1123
			// narrowed how this happens but did not close it), not a
			// question that needs rephrasing, and the two must not read
			// the same. Still StateNoMatch/exit 1, not a new state or
			// exit code -- the index WAS read fine, which is exactly what
			// StateNoMatch already means.
			out.Reason = fmt.Sprintf("the compiled index at %s contains 0 items -- it was read successfully but has nothing to answer with; this is a build defect (truncated write, full disk, interrupted regeneration), not a phrasing problem -- regenerate it with `estate knowledge`, do not just rephrase the question", indexPath)
		}
		return out
	}

	out.State = StateMatched
	if withheldPrivate > out.TotalMatched {
		// More matching items were withheld as private than were
		// returned -- agent-estate#1052. Still StateMatched's exit code
		// (0): see StateMatchedWithheldMajority's own doc comment for
		// why a non-zero exit here was measured and rejected. Strict
		// majority (">"), not ">=", so an even split still reads as a
		// real, if incomplete, answer rather than a mostly-hidden one.
		out.State = StateMatchedWithheldMajority
		out.Reason = fmt.Sprintf("%d of %d matching item(s) are private -- the %d shown are a minority of the answer; rerun with --private to include the rest",
			withheldPrivate, withheldPrivate+out.TotalMatched, out.TotalMatched)
		out.Coverage = out.Coverage.withLimitedReason(out.Reason)
	}
	n := len(all)
	if n > limit {
		n = limit
	}
	for _, s := range all[:n] {
		out.Matches = append(out.Matches, Match{
			ID:           s.item.ID,
			Source:       s.item.Source,
			Permalink:    s.item.Permalink,
			Tier1:        s.item.Tier1,
			Score:        int(math.Round(s.score)),
			MatchedTerms: s.matched,
			Publishable:  s.item.Publishable,
		})
	}
	out.NotReturned = out.TotalMatched - len(out.Matches)
	out.Contradictions = detectContradictions(out.Matches)
	return out
}

// plural picks singular or plural phrasing for a count without pulling
// in a full pluralization dependency -- withheldPrivate's own message is
// the only caller.
func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// Get looks up one item by its full id -- the second step of #1019's
// progressive disclosure: Query returns pointers, Get returns the one
// body a caller actually asked for (Tier1 + Tier2 + Tier3, still cited
// by Source and Permalink, still nothing beyond what the index itself
// stored). ok is false when the index couldn't be read, id matches
// nothing in it, or (agent-estate#1033) the item is private and
// includePrivate is false -- reason names which. Stable ids
// (agent-estate#1032) made this last case necessary: a private item's id
// can be written down and re-fetched later, so Query filtering Publishable
// out of its own Matches is not enough on its own -- Get must refuse the
// direct lookup too, or the filter is only cosmetic.
func Get(indexPath, id string, includePrivate bool) (item Item, ok bool, reason string) {
	res, err := Read(indexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Item{}, false, fmt.Sprintf("no compiled index at %s -- run `estate knowledge` first", indexPath)
		}
		return Item{}, false, err.Error()
	}
	for _, it := range res.Items {
		if it.ID == id {
			if !includePrivate && !it.Publishable {
				return Item{}, false, fmt.Sprintf("item %s is private (%s) -- rerun with --private to fetch it", id, it.PublishBasis)
			}
			return it, true, ""
		}
	}
	return Item{}, false, fmt.Sprintf("no item with id %s in the compiled index", id)
}
