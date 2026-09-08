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
	// StateMatchedWeak means at least one publishable item scored above
	// zero and was returned -- same population as StateMatched -- but the
	// top-ranked item's own score falls below weakMatchScoreFloor
	// (agent-estate#1315). #1315's own demonstration is this state's
	// reason to exist: "how do I check which issues a PR will close"
	// returned ten items, none of them the indexed answer, with no signal
	// distinguishing that list from a confident one -- a query whose
	// answer ranks nowhere read identical to a query whose answer does
	// not exist (it-d43a08d739bf32a8). This is a caution, not a verdict:
	// weakMatchScoreFloor was chosen to produce zero false positives on
	// #1315's own 26-question measured baseline (no genuine hit in that
	// set is ever flagged weak), but it only catches 7 of 17 real misses
	// -- a result NOT flagged weak is not thereby guaranteed correct. See
	// weakMatchScoreFloor's own doc comment for the measured rates.
	//
	// Deliberately maps to the SAME exit code as StateMatched (0), same
	// reasoning as StateMatchedWithheldMajority's own doc comment: real,
	// citable, publishable results were returned. The signal lives in the
	// state word and a dedicated banner line (see printKnowledgeQuery in
	// main.go), not in the exit code -- collapsing every already-passing
	// golden-set hit whose top score happens to sit below the floor (see
	// above) into a runner failure would move the golden score for a
	// reason unrelated to whether the answer is right, which #1052
	// already established must not happen for the sibling state.
	//
	// Never overrides StateMatchedWithheldMajority: a result set that is
	// BOTH weak and mostly-private reports the privacy finding, the more
	// actionable of the two -- rerunning with --private can only ever
	// fix one of them.
	StateMatchedWeak QueryState = "matched_weak"
	// StateStaleWithheldRefused means a result that would otherwise be
	// StateMatchedWithheldMajority ALSO carries a CoverageStale finding --
	// agent-estate#1306 item 3. Either alone is a defensible warn-then-
	// answer: StateMatchedWithheldMajority's own doc comment explains why
	// a majority-private result still deserves exit 0, and CoverageStale
	// is "report, never repair" by design. Together they compound instead
	// of cancelling -- the surviving, printed answer is simultaneously a
	// minority of the matches (most were withheld) AND drawn from an
	// index already known to be behind its sources, and 8 items under
	// four lines of caveat invites exactly the skim the caveats exist to
	// prevent (the same shape as #1294's "0 removed, 613 left in place",
	// which read as "tidy" for a day).
	//
	// This state does NOT invent a second majority-withheld threshold or
	// a second staleness definition -- it fires on exactly the existing
	// StateMatchedWithheldMajority condition (withheldPrivate >
	// TotalMatched, computed by Query below) and the existing
	// CoverageStale finding (computed by main.go's freshnessFindings, the
	// same comparison StateMatchedWithheldMajority's staleness-blind
	// sibling already folds into Coverage). Because that fold happens in
	// main.go, after Query returns (see CoverageState's own doc comment
	// for why staleness detection deliberately lives outside this
	// package), THIS package never assigns StateStaleWithheldRefused
	// itself -- main.go's applyStaleWithheldRefusal upgrades an already-
	// computed StateMatchedWithheldMajority result once both folds have
	// run, the same "detection lives where the filesystem access already
	// is" split foldFreshnessIntoCoverage/foldGeneratedByIntoCoverage
	// already established.
	//
	// Maps to its own exit code (4), deliberately DIFFERENT from
	// StateMatchedWithheldMajority's 0 -- see knowledgeQueryExitCode in
	// main.go. This is the one difference from its sibling: #1052 ruled
	// out a non-zero exit for majority-withheld ALONE because real,
	// citable public answers were still being returned and should not
	// read as runner failures. This state refuses to return an answer at
	// all (see printKnowledgeQuery's early return for it in main.go), so
	// a refusal that exits 0 -- indistinguishable from success to any
	// caller reading only $? -- would be its own defect (agent-estate#1306
	// asks explicitly: "a refusal that exits 0 cannot be detected by a
	// caller").
	StateStaleWithheldRefused QueryState = "stale_withheld_refused"
)

// weakMatchScoreFloor is the threshold StateMatchedWeak checks the
// top-ranked match's own (rounded) BM25 score against -- chosen
// empirically against agent-estate#1315's own 26-question operator-words
// baseline (src/estate/internal/knowledge/goldenset/retrieval_baseline_cases.json),
// never picked as a round number. Measured on that set: the 9 genuine
// top-10 hits' own top scores ranged 27-80 (minimum 27, case rb-10); the
// 17 misses' top scores ranged 21-49 (case rb-20 scored 49 while still
// missing its own answer entirely). No score threshold cleanly separates
// the two populations -- BM25 score reflects TERM OVERLAP MAGNITUDE, not
// semantic relevance, and a miss can share heavy incidental vocabulary
// with an unrelated item while a genuine hit shares only the terms that
// actually matter. 27 (one below the lowest observed hit) was chosen
// specifically to produce ZERO false positives on the measured set (no
// genuine hit is ever flagged weak) at the cost of catching only 7 of 17
// misses (41%) -- see StateMatchedWeak's own doc comment for why
// under-warning was chosen over ever contradicting a correct answer.
// This is a v0 number over a 26-case set, not a derived constant --
// restate it if the measured population changes materially (agent-estate#1315's
// own PR body carries the full per-case table this was measured from).
//
// A var, not a const -- purely so tests can move it and prove the floor is
// load-bearing rather than merely present, then restore the original --
// mirrors internal/corpus.MaxStandingLawMembers/MaxStandingLawBytes's own
// documented reason for the identical choice. Production always runs with
// the value below.
var weakMatchScoreFloor = 27

// weakMatchMinIndexItems gates weakMatchScoreFloor on the compiled
// index actually being close to the scale it was measured against.
// BM25's score magnitude is corpus-size-dependent through its own IDF
// term: measured directly while building this feature, the SAME
// fixture text that scores 27+ against the real ~7,700-item index
// scores single digits against a handful-of-items test fixture, because
// a term common in a tiny index is not "rare" the way IDF needs it to
// be to produce a large score. Retrofitting this file's own pre-existing
// small fixtures to score realistically would have meant rewriting them
// to simulate a large corpus merely to dodge a state neither they nor
// their own tests have any interest in -- the gate is the smaller,
// more honest change: the signal does not fire below this scale,
// because a threshold measured against one corpus size is not a
// portable fact about a different one, and claiming it is would be
// exactly the kind of unearned confidence #1315 exists to argue
// against. A var, not a const, for the same test-only reason
// weakMatchScoreFloor is.
var weakMatchMinIndexItems = 100

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
	// Weight and Status are copied straight off the source item's own
	// "weight:<value>"/"status:<value>" structural tags -- agent-estate#1128.
	// Only corpus items (corpus-parameter, corpus-directive, corpus-question,
	// corpus-correction; see corpus.go's corpusSource) carry those tags at
	// all, so both are empty ("", omitted from JSON) for every other
	// source. Never a second read of the corpus: weightAndStatus below only
	// parses StructuralTags the item already carries, the same tags
	// searchableText already folds into scoring -- this is display, not a
	// new lookup, and it changes no ranking (Score is computed before this
	// pair is ever read).
	Weight string `json:"weight,omitempty"`
	Status string `json:"status,omitempty"`
	// TiedOnScore is how many OTHER candidates -- among every item that
	// cleared scoring, before the display cap, not just the returned page
	// -- share this item's exact unrounded BM25 float, i.e. the size of its
	// tie group on the sort comparator's own primary key, minus itself
	// (agent-estate#1046). Zero means this item's unrounded score is unique
	// among candidates and its position was decided by score alone -- the
	// JSON key is always present (no omitempty) so a caller reading raw
	// JSON can tell "not tied" from "field absent because the binary is
	// older / the path forgot to set it" (agent-estate#1141 made the same
	// call for CoverageState, for the same reason: an absent key is less
	// distinguishable from a serialisation bug than an explicit zero).
	//
	// This exists because the printed Score above is deliberately rounded
	// for display while sort.SliceStable's comparator (see Query) keys on
	// the unrounded float and falls back to item ID only on an EXACT float
	// tie -- a gap #1046's own investigation found meant that fallback
	// governed a population no caller, reviewer, or golden-set run could
	// observe: two items 0.4 apart print the same integer Score and read as
	// "tied" when they never reached the ID fallback at all. Publishing the
	// unrounded float itself was rejected (see this field's issue) because
	// it invites comparing BM25 figures ACROSS different questions or
	// indexes, which is meaningless -- different terms, different idf, a
	// point the RankingBasis text and Score's own doc comment both already
	// make. A count is safe to compare anywhere: "0" always means "not
	// affected by the tie-break", regardless of which question or index
	// produced it.
	TiedOnScore int `json:"tied_on_score"`
}

// weightAndStatus pulls the "weight:<value>" and "status:<value>"
// structural tags off tags -- agent-estate#1128's own gap: the compiled
// index's structural_tags carries both markers already, but the printed
// match line and the JSON Match shape dropped them, so a hard directive
// and a dropped or merely-preferred one rendered identically. Returns ""
// for whichever tag is absent (every non-corpus source, and any corpus row
// whose own weight or status column was empty when corpus.go read it) --
// absence here is never defaulted to a word like "hard" or "acted", since
// that would be inventing a value the source item never actually carried.
func weightAndStatus(tags []string) (weight, status string) {
	for _, t := range tags {
		switch {
		case strings.HasPrefix(t, "weight:"):
			weight = strings.TrimPrefix(t, "weight:")
		case strings.HasPrefix(t, "status:"):
			status = strings.TrimPrefix(t, "status:")
		}
	}
	return weight, status
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
	// QueryResult carries -- see CoverageState. It is
	// {State: CoverageNotApplicable} on StateIndexMissing/
	// StateIndexUnreadable (there is no compiled index to have a coverage
	// opinion about -- agent-estate#1141: a bare zero Coverage{} there
	// serialised as `"coverage":{"state":""}`, indistinguishable on the
	// wire from a forgotten field); every other state always sets a real
	// opinion, even StateNoMatch, because a source failing at build time
	// has nothing to do with whether this particular question happened to
	// score anything.
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
// Only the complete/limited/degraded/mixed/not_applicable arms are
// populated by this package today. `stale` and `unknown` are #1047's own staleness
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
	// CoverageSourceMissing means a source the compiled index depends on
	// was successfully read when the index was built (its SourceStatuses
	// entry is OK) but, at QUERY time, could not be found or read at all --
	// agent-estate#1139 defect C. Deliberately distinct from
	// CoverageUnknownFreshness: "no local file ever exists to check"
	// (github-stars, standingly) and "a local file existed, was read at
	// build time, and is now unreadable" are different claims, and #1139's
	// own measurement found the prior code collapsed both into the same
	// "unknown" reason, indistinguishable except by reading Detail's free
	// text -- a caller branching on Coverage.State alone (the documented,
	// machine-readable surface) saw no difference between routine,
	// standing noise and a source that had actually vanished. This is the
	// louder claim of the two: a positively observed absence, not merely an
	// uncheckable one, so it must never render with the same visibility as
	// CoverageUnknownFreshness. It is also distinct from CoverageDegraded:
	// degraded is a BUILD-time failure already recorded in SourceStatuses;
	// this is a QUERY-time finding about a source that was fine when the
	// index was built and has since become unreachable, which SourceStatuses
	// (a build-time snapshot) has no way to know about on its own. Like
	// CoverageStale/CoverageUnknownFreshness, not set by this package
	// directly -- a caller with filesystem access folds it in via
	// WithFreshnessReason.
	CoverageSourceMissing CoverageState = "source_missing"
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
	// CoverageNotApplicable means there was no compiled index to have a
	// coverage opinion about at all -- StateIndexMissing or
	// StateIndexUnreadable, agent-estate#1141. Deliberately distinct from
	// CoverageComplete: "every source read cleanly" is a claim about a
	// real index this package examined, and a missing or unreadable index
	// was never examined, so making that claim would be a fabrication, not
	// a shortcut. It is also distinct from the bare zero value -- a
	// Coverage{} with an empty State is exactly what a caller cannot tell
	// apart from a forgotten field, which is the gap this state closes.
	// Reasons is always empty here: `state`/`reason` on the surrounding
	// QueryResult already say why, and duplicating that into a
	// CoverageReason would be the same fact under two names.
	CoverageNotApplicable CoverageState = "not_applicable"
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
//
// agent-estate#1150 MEASUREMENT, NOT SHIPPED -- this function is
// unchanged by it. #1150 named a specific instance of #1063's
// vocabulary-gap class: vault-02's authoritative fact talks about
// agents reporting `{"loggedIn": false}`; a caller's plain-English
// "logged" (which stems to "log") never bridges to the document's
// camelCase "loggedIn" (one token, "loggedin", which stems to itself),
// because this function only ever splits on punctuation, never on a
// case transition inside a run of letters. The dispatch note scoped
// this turn to producing the measurement, not deciding whether to ship
// a fix -- see #1150's PR body for the full per-case tables this
// comment summarises.
//
// What was swept: a variant of this function that emits, for each
// token, its original unsplit form AND its camelCase-split parts
// ("loggedIn" -> "loggedin", "logged", "in") -- built as a throwaway
// patched binary (this function plus removing the pre-lowering in
// tier1SearchableText/tier2SearchableText/ancestorSearchableText in
// distilledRuleWeight lifts a distilled rule above the evidence it was
// distilled from. 1.6 was chosen as the smallest factor that reorders the
// measured failures without letting a weak rule match outrank a strong
// evidence match: a rule scoring 12 clears evidence at 19, but not at 20.
const distilledRuleWeight = 1.6

// isDistilledRule reports whether an item is a judged rule from the vault's
// facts layer, as opposed to a projection of a single corpus item. The marker
// is the structural tag the vault source derives from the note's own `type:`
// frontmatter, plus the "rule" tag those notes carry.
func isDistilledRule(it Item) bool {
	if it.Source != VaultItemSourceTag {
		return false
	}
	hasFact, hasRule := false, false
	for _, t := range it.StructuralTags {
		if strings.EqualFold(t, "Fact") {
			hasFact = true
		}
	}
	for _, t := range it.SynapticTags {
		if strings.EqualFold(strings.TrimPrefix(t, "#"), "rule") {
			hasRule = true
		}
	}
	return hasFact && hasRule
}

// bm25.go, which otherwise destroys the case information a camelCase
// split needs before this function ever sees the text) and run against
// ONE scratch index via cmd/goldenquery. One scratch index sufficed for
// both the on and off variant binaries -- tokenisation happens entirely
// at query time (weightedTermFreqs in bm25.go rebuilds the BM25Scorer,
// and so re-tokenizes, on every Query call), never at index-write time,
// so nothing about the compiled index itself needed to change between
// variants.
//
// Findings, full binary diff (never rebuilt the index between runs):
//   - vault-02 (the case that prompted this issue): rank 5 -> rank 2,
//     cases.json's private-mode MISS -> HIT. Its matched-term set gained
//     "log" (loggedIn -> logged -> stemmed log) and nothing else changed.
//   - retrieval score (private): 16/17 -> 17/17 -- the one and only
//     aggregate line that moved. Checked per-case, not just the
//     aggregate: none of the other 16 cases flipped either direction.
//   - Every other of the eight reported lines (natural-language top-3
//     and top-10, unscoped and scoped; publishable-reachable; the
//     github-stars top-3/top-10 line; none-01) was digit-for-digit
//     unchanged, and so was every individual case's own HIT/MISS/rank
//     within them -- not just the aggregates. The only other visible
//     difference anywhere in the full report was a handful of
//     rank 4-10 reorderings among already-MISS natural-language
//     competitors (below the top-3 window that decides pass/fail);
//     no case's own target rank moved outside vault-02.
//   - Ratchet (buildRatchets, cmd/goldenquery/main.go): OK on every
//     guarded line in both variants -- no floor even approached. The
//     dispatch note's own expectation ("this moves every source by
//     construction, the ratchet firing is expected") did not hold here.
//   - Coverage: 473 of 3989 compiled index items (11.9%) carry at least
//     one camelCase token by this measurement's own transition
//     definition -- not a handful. By source: github-stars 136/391
//     (mostly repo names), corpus-directive 126/1528, corpus-parameter
//     70/1104, vault-fact 46/111, repo-docs 44/118, corpus-question
//     33/506, loops-research 4/24, corpus-correction 12/179,
//     corpus-thought 2/28. corpus-directive and corpus-parameter are
//     the operator's own words -- the cost this issue asked to weigh
//     explicitly (splitting perturbs their retrieval too) is real in
//     scale, even though this measurement found no case where it did.
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
var synapticFilterPattern = regexp.MustCompile(`^#[a-zA-Z0-9]+(?:-[a-zA-Z0-9]+)*$`)

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
		if tagFilterPattern.MatchString(f) || synapticFilterPattern.MatchString(f) {
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

// isCorpusKindTag reports whether name (already prefix-stripped and
// singularised the same way failedSourceForTag treats every other tag) is
// one of corpusKinds' own "corpus-<kind>" values -- agent-estate#1120's
// reopened gap. corpusSource (corpus.go) compiles all of corpusKinds into
// ONE reader pass and reports it as a single SourceResult{Name:
// "corpus-items"}, but each ITEM out of that pass carries its own kind as
// its Source (corpusSourceName: "corpus-parameter", "corpus-directive",
// "corpus-question", "corpus-correction", "corpus-thought") -- a different
// word entirely from "corpus-items", not a plurality mismatch, so the
// trailing-"s" trim below can never bridge it no matter how it's tuned.
// This is an explicit, enumerated exception rather than a widening of
// that plural-tolerant match: it lists exactly the five tag values
// corpusSource is known to produce and nothing else, so it cannot start
// matching some other source's name by accident the way a looser general
// rule could.
func isCorpusKindTag(name string) bool {
	for _, k := range corpusKinds {
		if name == "corpus-"+k {
			return true
		}
	}
	return false
}

// SourceNameMatches reports whether a and b plausibly name the same
// source family, tolerating a trailing "s" on either side. It exists
// because this package's naming already disagrees on plurality for at
// least one real source: vault.go's items carry Source "vault-fact",
// singular, while vaultSource's own SourceResult.Name is "vault-facts",
// plural. Every caller comparing an item/tag-derived name against a
// SourceResult.Name must go through this one function -- a second
// hand-rolled comparison is exactly how indexDependsOn (agent-estate#1139
// defect C, PR #1242 post-merge review) drifted from this exact-match-only
// case and silently stopped matching the vault in production.
func SourceNameMatches(a, b string) bool {
	return strings.TrimSuffix(strings.ToLower(a), "s") == strings.TrimSuffix(strings.ToLower(b), "s")
}

// failedSourceForTag looks for a source in sources that is both !OK
// (failed to build, so it produced zero items and could never have made
// this tag "known" no matter what it names) and whose Name plausibly
// names the same source as tag's "source:<name>" suffix. The comparison
// tolerates a trailing "s" on either side (via SourceNameMatches) because
// addSourceTag derives the tag from each ITEM's own Source string, while
// SourceResult.Name is each READER's own family name, and those two
// vocabularies already disagree on plurality for at least one real source
// (see SourceNameMatches).
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
		if SourceNameMatches(s.Name, name) {
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

// sparseMatchSources are the item sources whose sheer volume and
// fragmentation make a single coincidental term match statistically
// unremarkable -- agent-estate#1134. github-stars is ~391 heterogeneous
// one-line tool descriptions spanning every software topic; the five
// corpus-<kind> sources (see corpusKinds) are ~3345 independently
// authored, often one-sentence fragments. Measured against a real,
// freshly built index: the golden set's own no_match case (none-01,
// "the office vending machine restocking schedule", verified absent from
// every source before it was written) scored a nonzero BM25 match
// against 22 publishable items and 64 total, every one of them from
// these two source families, each hit driven by exactly one ordinary
// English word ("office", "machine", "schedule") the question happened
// to share with an unrelated repo description or corpus fragment.
// BM25's own idf weighting cannot tell that coincidence apart from a
// genuine rare-term hit on relevance alone -- measured on the same
// index, "office" (idf 7.04, the rarest word in that noise) is RARER
// than "dispatch" (idf 4.05), which is the one real matched term behind
// a genuine natural-language-stratum hit (nl-10, agent-estate#1073).
// Rarity and relevance are different axes; sparseMatchSources is the
// acknowledgment that, for these two source families specifically, this
// corpus's scale makes rarity alone an unreliable proxy for relevance.
//
// vault-fact, loops-research and repo-docs are deliberately NOT in this
// set: they are small, curated, single-purpose documents (111, 24 and
// 118 items respectively, versus 391+3345) where one weighted Tier1
// (title) term match is real signal, not noise. They still carry
// minMatchedTerms' own gentler, non-sparse floor (min(2, the question's
// distinct term count)), because none-01's own measurement found isolated
// single-term coincidences there too (a repo-docs section matching only
// "machine", a vault-fact matching only "schedule") -- just far fewer of
// them, and never enough to rank above the sparse-source noise. Measured:
// requiring sparseFloorCap (3) rather than 2 for these three sources
// would exclude 7 of the natural-language stratum's 12 genuine low-
// overlap hits (agent-estate#1073) -- nl-03/05/06/07/08/10/12, whose
// correct answer matches on only 1-2 distinct terms precisely BECAUSE the
// question is a paraphrase, not a quote; the gentler floor of 2 costs
// only nl-10/nl-12 (2 of 12, both single-term matches against a 3+-term
// question), the other five (2-term matches) survive. That population is
// exactly what #1054 removed the old floor to stop discarding; a floor of
// 3 there would be reverting #1054 for the cases it was built to fix. See
// #1134's own PR body for the measured before/after across every
// golden-set stratum.
var sparseMatchSources = map[string]bool{
	"github-stars":      true,
	"corpus-parameter":  true,
	"corpus-directive":  true,
	"corpus-question":   true,
	"corpus-correction": true,
	"corpus-thought":    true,
}

// minMatchedTerms is the per-item floor a candidate must clear on DISTINCT
// matched terms to be returned at all -- agent-estate#1134's narrow
// restoration of the floor #1054 removed, scaled by source (see
// sparseMatchSources' own doc comment for why the two families it lists
// carry a stricter bar than every other source). Both floors are adaptive
// on queryTermCount the same way the pre-#1054 floor was (#1054's own
// commit message: "excluded anything below min(3, distinct terms)
// matched"), so a short question is never held to a bar it cannot reach:
// a one-distinct-term query still only needs its one term to match,
// regardless of source -- a floor here can only ever be LOWER than its
// own cap, never impose a requirement the query itself has no way to
// satisfy.
//
// sparseFloorCap=3 and nonSparseFloorCap=2 were both measured, not
// assumed, against a real freshly built index (agent-estate#1134): 3 is
// the smallest sparse-source cap that excludes every one of none-01's 64
// matched candidates in that family (the worst, an item matching 2
// distinct terms by coincidence) while still admitting every real
// cases.json hit in a sparse source (the weakest, corpus-02, matches
// exactly 3) -- a cap of 2 would leave that one 2-term coincidental match
// standing and none-01 would still (wrongly) report matched. 2 is the
// smallest non-sparse cap that clears none-01's residual single-term
// noise in vault-fact/repo-docs (each such candidate matched exactly 1
// term) while costing only 2 of the natural-language stratum's 12 real
// hits (nl-10, nl-12 -- both single-term matches against a 3+-term
// question; every 2-term hit in that stratum still clears a floor of 2).
const (
	sparseFloorCap    = 3
	nonSparseFloorCap = 2
)

func minMatchedTerms(source string, queryTermCount int) int {
	floorCap := nonSparseFloorCap
	if sparseMatchSources[source] {
		floorCap = sparseFloorCap
	}
	if queryTermCount < floorCap {
		return queryTermCount
	}
	return floorCap
}

// titleFloorExemptSources are the sources where a single matched term
// landing in the item's own TITLE (tier1, never tier2 body text) is let
// through minMatchedTerms' floor even when the term count alone would not
// clear it -- agent-estate#1134's follow-up narrowing (see its own PR
// thread, "the cost is not yet shown to be minimal"). Originally scoped to
// repo-docs only: only repo-docs carried the tier1/tier2 split (agent-
// estate#1113's leaf-heading-as-tier1) that makes "this term is the item's
// own heading" a distinct, checkable signal separate from term count.
//
// Measured, not assumed, against a real freshly built index: every one of
// none-01's residual repo-docs single-term coincidences (8 candidates,
// terms "machine"/"schedule") matched tier2 body text ONLY -- zero of them
// hit an item's own tier1 title -- so this exemption costs none-01 nothing.
// Of the three cases minMatchedTerms=2 regressed (nl-04, nl-10, nl-12, all
// single-term matches against 3+-term questions), two (nl-10's "dispatch",
// nl-12's "refuse") match their target's own section title and are
// recovered by this exemption; nl-04's "python" matches only its target's
// tier2 body (the target section is titled "The implementation language
// is Go", which does not contain the word "python") and stays a MISS --
// tried lowering the floor further to recover it too (repo-docs floor=1,
// i.e. no floor at all there) and that reopens none-01 (its own "machine"/
// "schedule" tier2 hits return as matches again, confirmed by rerunning
// with that change), so nl-04's regression is accepted rather than traded
// for none-01 reporting matched again. See #1134's PR body for the
// measured before/after this exemption produces.
//
// vault-fact ADDED, agent-estate#1255 (the K3 gate). Every vault fact
// already has a title-shaped Tier1 ("<title> -- <description>", vault.go),
// so the same "this term is the item's own heading, not incidental body
// text" signal repo-docs uses applies to it unchanged -- no new field, no
// new scoring path. Root cause measured directly against a real index
// (see #1255's PR body): a published fact's Tier1 shares exactly one term
// ("estate") with a topically-related but lexically-different question
// ("where should I take the estate next"); every other query term (next,
// should, take, prioritise, month, direction, recommendation) appears
// nowhere in the fact's own text (checked via `knowledge get`), so this is
// a genuine single-title-term match, not a body coincidence -- and
// minMatchedTerms' floor of 2 was discarding it before BM25's own idf
// weighting ever got a chance to rank it. Reusing the existing,
// already-measured title-exemption mechanism (rather than a new synonym
// list, an embedding model, or hand-tuning the floor itself) costs
// nothing new to build and changes no other source's behaviour. Measured
// cost against cmd/goldenquery's live run (see #1255's PR body for the
// full before/after): no line regressed; none-01 (the no_match case) was
// unaffected because it names no term shared with any vault fact's own
// title. loops-research is deliberately NOT added here -- its own Tier1
// is a bare filename, not an authored title, so the same "this term is
// the item's own heading" claim does not hold for it, and adding it was
// never measured.
var titleFloorExemptSources = map[string]bool{
	"repo-docs":  true,
	"vault-fact": true,
}

// vaultFactTitleBonus is a FLAT addition to a vault-fact item's own BM25
// score, applied once matchesTitle is already true (so this never widens
// which items are candidates -- titleFloorExemptSources/minMatchedTerms
// already decided that; this only changes where a candidate that cleared
// them RANKS). agent-estate#1255 (the K3 gate): fixing candidacy alone
// (titleFloorExemptSources' vault-fact addition) was not enough, because
// BM25's own k1 saturation caps a single term occurrence's contribution
// near idf*(k1+1) regardless of field weight -- see bm25.go's own
// "measured and rejected" comment for the swept alternative (a per-source
// tier1 field weight) that tried to fix this proportionally and could
// not, by construction, ever out-score a multi-term coincidence's summed
// contributions. A flat, non-saturating addition is the only lever left
// that can. Bonus is added AFTER minMatchedTerms/titleFloorExemptSources
// have already decided candidacy, so a coincidental single-term title hit
// none-01-style is not affected by whether the bonus exists -- it was
// already excluded (or not) before this line runs; the bonus only moves a
// candidate ALREADY admitted higher in the ranking.
//
// SCOPE MATTERS as much as the number: an earlier attempt applied this
// bonus to every vault-fact item where matchesTitle held, including items
// that already cleared minMatchedTerms on their own (2+ distinct terms,
// needing no rescue at all) -- measured against cmd/goldenquery, that
// version regressed retrieval score (private) from 20/22 to 17/22 (three
// cases broke: unrelated vault facts sharing one ordinary word with their
// own titles, e.g. "should"/"next"/"where", all got the same flat bonus
// and outranked their own correct targets). Moving the addition inside
// the SAME branch titleFloorExemptSources already gates -- applied only
// to the item titleFloorExemptSources's exemption itself just rescued,
// never to an item that would have cleared the floor unaided -- removed
// that regression entirely while leaving the K3 fix intact; see #1255's
// PR body for both measurements side by side.
//
// 4.0 is the smallest value swept (4/6/8/10/12 against a real freshly
// built index, with the branch correctly scoped as above) that puts
// #1255's own published fact inside QueryLimit's top 10 for all three of
// the gate's lexically-different queries at once -- it was already
// sufficient for all three at the first value tried; 6/8/10/12 raise its
// rank further but were not needed to clear the top-10 threshold and so
// are not the smaller, equally-effective choice. Checked against
// cmd/goldenquery's full run at 4.0: no line moved -- natural-language
// top-3/top-10 (unscoped and scoped), retrieval score (private),
// publishable-reachable, github-stars, and none-01 (the no-match guard,
// still correctly StateNoMatch/exit 1) are all bit-for-bit identical
// before and after. See #1255's PR body for the full sweep table and the
// goldenquery before/after this comment summarises. This is a coarser
// lever than BM25's own idf/tf weighting --
// it does not rank one title-matching vault fact against another by
// relevance, only against everything else -- and it is scoped to
// vault-fact alone (118 curated, individually-authored items) precisely
// because that coarseness is a cost worth paying only for this package's
// smallest, highest-precision, standing-constraint source: a false
// positive here costs one extra citation shown; a false negative is
// #1255's own bug, a published constraint that never reaches an agent
// whose task it governs.
const vaultFactTitleBonus = 4.0

// matchesTitle reports whether any of matched (terms already scored
// nonzero against it by BM25Scorer.Score) is present in it's own tier1
// field specifically, as opposed to only its tier2 body -- the same field
// split bm25.go's weightedTermFreqs already computes for scoring, reused
// here as a boolean signal rather than a weight.
func matchesTitle(it Item, matched []string) bool {
	if len(matched) == 0 {
		return false
	}
	tier1Terms := fieldTermCounts(tier1SearchableText(it))
	for _, m := range matched {
		if tier1Terms[m] > 0 {
			return true
		}
	}
	return false
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
	"does not apply to it -- see bm25.go; #1054's own no-hard-floor rule " +
	"(a rare term matching once can outrank several common terms matching " +
	"by coincidence) still governs ranking everywhere, but a matched item " +
	"must ALSO clear a per-item distinct-term floor to be returned at all " +
	"-- agent-estate#1134's narrow, source-scoped, measured restoration: " +
	"vault-fact/loops-research/repo-docs need min(2, the question's own " +
	"distinct term count) distinct terms to match; github-stars and every " +
	"corpus-<kind> source need min(3, ...) -- never more than the question " +
	"itself has, so a one-term question is never held to a bar it cannot " +
	"reach. The stricter cap on the second pair is measured, not assumed: " +
	"those two source families are large and fragmented enough that a " +
	"single ordinary word reliably coincides with something unrelated -- " +
	"see minMatchedTerms and sparseMatchSources in query.go; a repo-docs " +
	"or vault-fact item is let through that floor on a single matched term " +
	"anyway when the term is the item's own TITLE (tier1), not merely body " +
	"text -- see titleFloorExemptSources and matchesTitle in query.go; " +
	"the printed score is BM25's own figure rounded to the nearest integer " +
	"for display; ties on the unrounded figure broken by item id, oldest " +
	"first -- each Match's tied_on_score (agent-estate#1046) states how " +
	"many other candidates share its exact unrounded score, so the " +
	"population that ID tie-break actually governs is visible without " +
	"publishing a raw float that would invite comparing scores across " +
	"different questions or indexes, which BM25 does not support"

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
				State:    StateIndexMissing,
				Reason:   fmt.Sprintf("no compiled index at %s -- run `estate knowledge` first", indexPath),
				Coverage: Coverage{State: CoverageNotApplicable},
			}
		}
		return QueryResult{
			State:    StateIndexUnreadable,
			Reason:   err.Error(),
			Coverage: Coverage{State: CoverageNotApplicable},
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
		if !currentMemoryItem(it) {
			continue
		}
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
		if len(terms) > 0 && len(matched) < minMatchedTerms(it.Source, len(terms)) {
			// agent-estate#1134's narrow, source-scoped floor -- see
			// minMatchedTerms's own doc comment for the measured
			// reasoning. Only ever tightens sparseMatchSources; every
			// other source keeps #1054's "score > 0 is enough" rule
			// exactly as it was.
			//
			// titleFloorExemptSources is the one measured narrowing on
			// top of that floor: a single matched term that IS the
			// item's own title, not just body text buried in it, is let
			// through anyway -- see matchesTitle's and
			// titleFloorExemptSources' own doc comments for what was
			// measured before adding this and what it costs.
			if !(titleFloorExemptSources[it.Source] && matchesTitle(it, matched)) {
				continue
			}
			if it.Source == VaultItemSourceTag {
				// vaultFactTitleBonus -- see its own doc comment for why a
				// flat addition, not a field weight, is what this needed.
				// SCOPED TO EXACTLY THIS BRANCH, not every vault-fact title
				// match: a vault fact that already clears minMatchedTerms
				// on its own (2+ distinct terms) needs no rescue and keeps
				// #1054/#1134's existing, already-measured ranking exactly
				// as it was -- applying it unconditionally to every
				// matchesTitle vault-fact was tried and measured to
				// regress cmd/goldenquery's retrieval score (private) from
				// 20/22 to 17/22 (three cases broke: other vault facts
				// sharing an ordinary word in their own titles, e.g.
				// "should"/"next"/"where", all got the same flat bonus and
				// outranked their own correct targets). Confined to the
				// single-term-rescue population only, that regression
				// disappears entirely -- see #1255's PR body for both
				// measurements.
				score += vaultFactTitleBonus
			}
		}
		// The distilled layer outranks the evidence layer. Measured
		// 2026-09-07: the vault holds ~3,070 evidence notes (one per thing
		// Jon said) against ~300 distilled rules, so on sheer population a
		// coincidental word match in evidence beat the rule that answers the
		// question -- "can I commit directly to main" returned a note about
		// chatting with the director, because it contained "directly".
		//
		// A rule is not merely another note: it was selected by reading every
		// statement on its subject and choosing the one that binds behaviour.
		// Ranking it level with its own evidence throws that judgement away.
		// The multiplier is deliberately modest -- it reorders within a
		// matched set, it does not manufacture a match that BM25 did not
		// already find.
		if isDistilledRule(it) {
			score *= distilledRuleWeight
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

	// No per-source-file cap is applied here on purpose -- agent-estate#1105
	// measured one and rejected it. Its own case (`source:repo-docs how
	// does dispatch work`) had docs/canonical/knowledge-system.md holding 6 of 10
	// slots and the correct docs/product/SPEC.md dispatch section sitting
	// at rank 4, on a build where #1113/#1117's leaf-heading fix and
	// #1134/#1137's match floor were both already in. Sweeping a cap of
	// 5/4/3/2 sections per file (built at the same compiled index, only
	// the cap value varying) never moved that section's rank -- it was
	// already above every capped-away slot, so the cap only changed which
	// wrong sections filled the remainder. At cap=2 it actively cost a
	// previously-correct case: nl-07 ("what checks stop a bad merge",
	// target AGENTS.md's own "guards that actually run" section) dropped
	// from a rank-5 hit to absent from the top 10, because two OTHER
	// AGENTS.md sections already outranked it and the cap silently
	// dropped the correct one along with them -- the golden set's own
	// unscoped and scoped natural-language top-10 lines both went 8/12 to
	// 7/12 at cap=2 and held at every looser cap. Neither the ratchet
	// (agent-estate#1066) nor the aggregate caught this by itself: the
	// top-10 lines are deliberately unratcheted (#1112, corpus-growth
	// drift), and the aggregate for cap=5/4/3 is bit-for-bit identical to
	// uncapped -- only the per-case nl-07 table shows the cost. This
	// confirmed, rather than refuted, the standing objection that a cap is
	// suppression, not correction: it can silently remove a correct answer
	// that happens to share a file with several higher-scoring sections
	// while never fixing the rank of the case that motivated it. See
	// #1105's own measurement comment for the full per-cap tables.
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
	// tieGroupSize counts, for every DISTINCT unrounded score in all, how
	// many candidates share it -- the same exact-float equality the sort
	// comparator above uses, computed once over the whole candidate set
	// (not just the returned page) so a returned item tied with one cut by
	// the display cap still reports its true group size (agent-estate#1046).
	tieGroupSize := map[float64]int{}
	for _, s := range all {
		tieGroupSize[s.score]++
	}

	n := len(all)
	if n > limit {
		n = limit
	}
	for _, s := range all[:n] {
		weight, status := weightAndStatus(s.item.StructuralTags)
		out.Matches = append(out.Matches, Match{
			ID:           s.item.ID,
			Source:       s.item.Source,
			Permalink:    s.item.Permalink,
			Tier1:        s.item.Tier1,
			Score:        int(math.Round(s.score)),
			MatchedTerms: s.matched,
			Publishable:  s.item.Publishable,
			Weight:       weight,
			Status:       status,
			TiedOnScore:  tieGroupSize[s.score] - 1,
		})
	}
	out.NotReturned = out.TotalMatched - len(out.Matches)
	out.Contradictions = detectContradictions(out.Matches)
	// agent-estate#1315: a weak top match gets its own state, checked
	// last and only when nothing more specific already claimed State --
	// see StateMatchedWeak's own doc comment for why it never overrides
	// StateMatchedWithheldMajority. Checked against out.Matches[0], the
	// actual top-ranked result a caller sees, not the pre-filter
	// candidate list -- the two differ only when the highest-scoring
	// candidate itself was private and this call did not ask for private
	// material, in which case the shown top match is the right one to
	// judge confidence against.
	if out.State == StateMatched && len(res.Items) >= weakMatchMinIndexItems &&
		len(out.Matches) > 0 && out.Matches[0].Score < weakMatchScoreFloor {
		// Deliberately does NOT touch out.Coverage: Coverage states
		// whether a SOURCE was withheld or fell behind (a completeness
		// question), never touched by score alone -- every item that
		// exists and was eligible to be returned still was; the finding
		// here is about confidence in what was shown, not about anything
		// missing from it.
		out.State = StateMatchedWeak
		out.Reason = fmt.Sprintf("top match scored %d, below the %d floor measured against agent-estate#1315's own baseline -- treat this as \"nothing matched well,\" not a confident answer",
			out.Matches[0].Score, weakMatchScoreFloor)
	}
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
			if !currentMemoryItem(it) {
				return Item{}, false, "managed fact changed or was rejected; regenerate the knowledge index"
			}
			if !includePrivate && !it.Publishable {
				return Item{}, false, fmt.Sprintf("item %s is private (%s) -- rerun with --private to fetch it", id, it.PublishBasis)
			}
			return it, true, ""
		}
	}
	return Item{}, false, fmt.Sprintf("no item with id %s in the compiled index", id)
}
