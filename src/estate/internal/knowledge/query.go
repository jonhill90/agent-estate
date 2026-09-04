package knowledge

import (
	"errors"
	"fmt"
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
	// Score is the number of distinct question terms this item's own
	// text matched -- see scoreItem. It is not a probability or a
	// learned weight; MatchedTerms names exactly which words produced
	// it, so the ranking basis is never opaque.
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

// queryTerms lowercases and splits question into the distinct, non-stop
// words Query scores against. Punctuation is treated as a separator, not
// part of a term, so "auth?" and "auth" match the same item text.
func queryTerms(question string) []string {
	fields := strings.FieldsFunc(strings.ToLower(question), func(r rune) bool {
		return !('a' <= r && r <= 'z' || '0' <= r && r <= '9')
	})
	seen := map[string]bool{}
	var terms []string
	for _, f := range fields {
		if len(f) <= 2 || stopWords[f] || seen[f] {
			continue
		}
		seen[f] = true
		terms = append(terms, f)
	}
	return terms
}

// searchableText is every field of an item a question term may match --
// Tier1 and Tier2 (Tier3 is a pointer, never indexed content) plus both
// tag classes. Never includes anything beyond what Item already carries;
// Query never re-reads a source.
func searchableText(it Item) string {
	return strings.ToLower(strings.Join(append([]string{it.Tier1, it.Tier2}, append(it.StructuralTags, it.SynapticTags...)...), " \x1f "))
}

// scoreItem counts how many distinct terms appear as a substring of
// it's searchable text, and names which ones -- the score IS the count,
// nothing hidden or weighted behind it. This is still the filtering and
// display score (requiredScore's floor and Match.Score both read this
// unweighted count) -- see rankingWeight for the separate, weighted
// figure that decides sort order.
func scoreItem(it Item, terms []string) (score int, matched []string) {
	text := searchableText(it)
	for _, t := range terms {
		if strings.Contains(text, t) {
			matched = append(matched, t)
		}
	}
	return len(matched), matched
}

// tier1Weight and tier2Weight are rankingWeight's per-term multipliers.
// #1027 compiled vault fact bodies (median ~2.3KB) into Tier2, and an
// unweighted match count let a passing mention deep in a body outweigh a
// short item whose title is actually about the subject -- #1043's review
// measured this directly: five previously-passing golden-set cases in
// unrelated sources (corpus-parameter, loops-research) flipped HIT->MISS
// because long vault bodies out-scored them on raw term count alone. A
// term found in Tier1 or either tag field counts for tier1Weight; the
// same term found only inside Tier2 counts for tier2Weight. 3:1 was
// picked as the smallest ratio that reliably lets a single Tier1/tag
// term outrank an item whose only matches are buried in Tier2 -- see
// query_test.go's field-weighting cases for the measured before/after.
const (
	tier1Weight = 3
	tier2Weight = 1
)

// rankingWeight scores matched (the terms scoreItem already found) by
// where each one was found, so ranking -- unlike the required-score floor
// and the printed Match.Score -- treats a Tier1/tag hit as worth more
// than the same term appearing only in Tier2. It never re-scans for new
// terms; matched is scoreItem's own output, so a term this function
// weights is always one that already cleared requiredScore.
func rankingWeight(it Item, matched []string) int {
	tier1Text := strings.ToLower(strings.Join(append([]string{it.Tier1}, append(it.StructuralTags, it.SynapticTags...)...), " \x1f "))
	tier2Text := strings.ToLower(it.Tier2)
	weight := 0
	for _, t := range matched {
		switch {
		case strings.Contains(tier1Text, t):
			weight += tier1Weight
		case strings.Contains(tier2Text, t):
			weight += tier2Weight
		}
	}
	return weight
}

// minMatchedTerms is the floor an item's score must clear to be
// returned at all -- agent-estate#1026's none-01 case ("office vending
// machine restocking schedule", 5 scoreable terms) scored 2 against an
// unrelated GitHub star purely by chance (two generic words, "machine"
// and "schedule", each appearing in a handful of items for unrelated
// reasons), and Query returned it instead of ever reaching no_match.
// Every genuine hit measured against the golden set (agent-estate#1023)
// matched 4 or more distinct terms; 3 sits below that with room, and
// above the 1- and 2-term coincidental matches the false positive and
// its neighbours produced. A minimum matched-COUNT was chosen over a
// minimum score (the two are the same axis here, since score IS the
// matched-term count) and over discounting document-frequent terms,
// because measuring this index's actual term frequencies showed
// "office"/"machine"/"schedule" are NOT common overall (well under 2%
// of items each) -- the false positive is a low-probability coincidence
// across several rare terms, not one term that clears the bar everywhere,
// so document-frequency discounting would not have caught it.
const minMatchedTerms = 3

// requiredScore is the per-question floor: min(minMatchedTerms,
// len(terms)). A question with fewer distinct terms than the floor
// cannot possibly reach it, so the floor becomes "every term must
// match" instead -- a 1- or 2-term question ("auth tokens") still finds
// its item on a full match; it is not silently unanswerable just because
// it is short.
func requiredScore(terms []string) int {
	if len(terms) < minMatchedTerms {
		return len(terms)
	}
	return minMatchedTerms
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

const rankingBasisText = "score = count of distinct question terms " +
	"(stop words and words of length <=2 removed) found as a substring " +
	"of the item's own tier1, tier2, structural_tags or synaptic_tags; " +
	"an item must match at least min(3, distinct question terms) of them " +
	"to be returned at all -- see requiredScore in query.go; ranking " +
	"itself is by a separate weighted figure, not the printed score: a " +
	"term found in tier1/tags outweighs the same term found only in " +
	"tier2 (3:1) -- see rankingWeight in query.go; ties on that weight " +
	"broken by the printed score, then by item id, oldest first"

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
		PrivateIncluded:  includePrivate,
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
			out.Reason = fmt.Sprintf("unknown tag(s): %s -- not present in the compiled index",
				strings.Join(unknown, ", "))
			return out
		}
		out.RankingBasis += fmt.Sprintf("; filtered first to items carrying the exact tag(s) %s, ranked only within that set",
			strings.Join(tagFilters, ", "))
	}

	type scored struct {
		item    Item
		score   int
		matched []string
		weight  int
	}
	required := requiredScore(terms)
	var all []scored
	withheldPrivate := 0
	for _, it := range res.Items {
		if !itemHasAllTags(it, tagFilters) {
			continue
		}
		score, matched := scoreItem(it, terms)
		if score < required {
			continue
		}
		if !includePrivate && !it.Publishable {
			withheldPrivate++
			continue
		}
		all = append(all, scored{it, score, matched, rankingWeight(it, matched)})
	}

	// Sort by weight first -- Tier1/tag matches outrank Tier2-only
	// matches (see rankingWeight) -- falling back to the unweighted
	// matched-term count, then item id, for items tied on weight.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].weight != all[j].weight {
			return all[i].weight > all[j].weight
		}
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
			return out
		}
		out.State = StateNoMatch
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
			Score:        s.score,
			MatchedTerms: s.matched,
			Publishable:  s.item.Publishable,
		})
	}
	out.NotReturned = out.TotalMatched - len(out.Matches)
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
