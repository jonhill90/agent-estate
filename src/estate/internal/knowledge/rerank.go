package knowledge

import (
	"fmt"
	"strings"
)

// scored is Query's own per-candidate working type: an eligible item, its
// BM25 score, and which terms matched. Package-scoped (rather than local
// to Query, as before this file existed) purely so applyRerank below can
// operate on it without Query having to convert to and from a separate
// exported shape on every call.
type scored struct {
	item    Item
	score   float64
	matched []string
}

// RerankCandidate is one item Query has already decided is eligible --
// past privacy, tag, and admission filtering, already BM25-scored -- for a
// RerankFunc to reorder. Text is Tier1+". "+Tier2, the exact field pair
// BM25's own searchableText (bm25.go) concatenates, so a reranker sees the
// same content BM25 does, never a richer or poorer view of the item
// (agent-estate#1344's own measurement used this same pairing).
type RerankCandidate struct {
	ID   string
	Text string
}

// RerankFunc reorders candidates by an alternative similarity measure and
// returns their ids in the new order -- a permutation of the candidate
// ids, nothing added or removed. Returning an error, or an id set that is
// not an exact permutation of what was given, is treated identically by
// QueryWithRanking: the ENTIRE rerank attempt is rejected and Query falls
// back to the exact BM25 order (agent-estate#1255: "reject incomplete or
// invalid ranker results as a whole" -- there is no partial application).
//
// nil is the default every existing caller of Query gets -- see Query's
// own doc comment. This mirrors internal/pressure's ReadQuota/
// CountWorktrees seam: nil means "no override, use BM25 alone", and only
// main.go's CLI wiring ever supplies a non-nil value, when a caller
// explicitly asked for semantic ranking and a valid local embedding cache
// exists.
type RerankFunc func(question string, candidates []RerankCandidate) (rankedIDs []string, err error)

func rerankText(it Item) string {
	return strings.TrimSpace(it.Tier1 + ". " + it.Tier2)
}

// applyRerank runs rerank over all (already privacy/tag/admission
// filtered, already BM25-sorted) and returns the reordered slice, or an
// error naming why the result was rejected. Never mutates all in place --
// returns a new slice, so a rejected result leaves the original BM25 order
// completely untouched for the caller to keep using.
func applyRerank(rerank RerankFunc, question string, all []scored) ([]scored, error) {
	candidates := make([]RerankCandidate, len(all))
	byID := make(map[string]scored, len(all))
	for i, s := range all {
		candidates[i] = RerankCandidate{ID: s.item.ID, Text: rerankText(s.item)}
		byID[s.item.ID] = s
	}
	rankedIDs, err := rerank(question, candidates)
	if err != nil {
		return nil, err
	}
	if len(rankedIDs) != len(all) {
		return nil, fmt.Errorf("reranker returned %d id(s) for %d candidate(s)", len(rankedIDs), len(all))
	}
	seen := make(map[string]bool, len(rankedIDs))
	out := make([]scored, 0, len(rankedIDs))
	for _, id := range rankedIDs {
		if seen[id] {
			return nil, fmt.Errorf("reranker returned duplicate id %q", id)
		}
		seen[id] = true
		s, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("reranker returned id %q, which was not in the candidate population it was given", id)
		}
		out = append(out, s)
	}
	// Length and duplicate checks above already guarantee out covers
	// exactly the same id set as all -- no candidate silently dropped.
	return out, nil
}
