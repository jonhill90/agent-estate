package knowledge

import (
	"errors"
	"fmt"
	"os"
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
}

// QueryResult is Query's full, typed answer.
type QueryResult struct {
	State    QueryState `json:"state"`
	Reason   string     `json:"reason,omitempty"` // set for IndexMissing/IndexUnreadable
	Question string     `json:"question,omitempty"`
	// RankingBasis states, in one sentence, how Score was computed --
	// #1019's "the output must make the basis legible" requirement.
	RankingBasis string  `json:"ranking_basis,omitempty"`
	Matches      []Match `json:"matches,omitempty"`
	// TotalMatched is every item that scored above zero, before the cap.
	TotalMatched int `json:"total_matched"`
	// NotReturned is TotalMatched minus len(Matches) -- how many real
	// matches this call did not show. Always stated, never implied.
	NotReturned int `json:"not_returned"`
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
// nothing hidden or weighted behind it.
func scoreItem(it Item, terms []string) (score int, matched []string) {
	text := searchableText(it)
	for _, t := range terms {
		if strings.Contains(text, t) {
			matched = append(matched, t)
		}
	}
	return len(matched), matched
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

const rankingBasisText = "score = count of distinct question terms " +
	"(stop words and words of length <=2 removed) found as a substring " +
	"of the item's own tier1, tier2, structural_tags or synaptic_tags; " +
	"an item must match at least min(3, distinct question terms) of them " +
	"to be returned at all -- see requiredScore in query.go; " +
	"ties broken by item id, oldest first"

// Query reads the compiled index at indexPath and returns a small,
// ranked, cited set of items scored against question -- see QueryState
// for the three ways this can come back empty without being the same
// answer. limit <= 0 uses QueryLimit.
//
// NO SYNTHESIS: every returned Match is a pointer (id, source,
// permalink, the item's own Tier1) copied verbatim from the index, never
// summarised, reworded or generated -- see this package's own doc
// comment on honest absence and #1019's "no fabrication" requirement.
func Query(indexPath, question string, limit int) QueryResult {
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
	}

	terms := queryTerms(question)
	if len(terms) == 0 {
		out.State = StateNoMatch
		out.Reason = "question contained no scoreable terms after stop-word removal"
		return out
	}

	type scored struct {
		item    Item
		score   int
		matched []string
	}
	required := requiredScore(terms)
	var all []scored
	for _, it := range res.Items {
		score, matched := scoreItem(it, terms)
		if score < required {
			continue
		}
		all = append(all, scored{it, score, matched})
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		return all[i].item.ID < all[j].item.ID
	})

	out.TotalMatched = len(all)
	if len(all) == 0 {
		out.State = StateNoMatch
		return out
	}

	out.State = StateMatched
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
		})
	}
	out.NotReturned = out.TotalMatched - len(out.Matches)
	return out
}

// Get looks up one item by its full id -- the second step of #1019's
// progressive disclosure: Query returns pointers, Get returns the one
// body a caller actually asked for (Tier1 + Tier2 + Tier3, still cited
// by Source and Permalink, still nothing beyond what the index itself
// stored). ok is false when the index couldn't be read or id matches
// nothing in it; reason names which.
func Get(indexPath, id string) (item Item, ok bool, reason string) {
	res, err := Read(indexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Item{}, false, fmt.Sprintf("no compiled index at %s -- run `estate knowledge` first", indexPath)
		}
		return Item{}, false, err.Error()
	}
	for _, it := range res.Items {
		if it.ID == id {
			return it, true, ""
		}
	}
	return Item{}, false, fmt.Sprintf("no item with id %s in the compiled index", id)
}
