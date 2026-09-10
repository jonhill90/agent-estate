package embedding

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// NewReranker builds a knowledge.RerankFunc backed by cache (every
// candidate's vector already prepared, offline, by PrepareCache) and
// client (used ONLY to embed the question text itself -- one short string
// per call, never the corpus). The returned func satisfies
// QueryWithRanking's exact contract: any single problem -- one missing or
// stale cached vector, a question-embedding failure, a context deadline --
// rejects the WHOLE rerank attempt (a plain error return), never a partial
// reorder of the candidates that DID have a usable vector.
//
// ctx bounds the one network call this makes (embedding the question);
// callers should derive it from a short, query-appropriate deadline, never
// the long timeouts PrepareCache's own bulk embedding run tolerates.
func NewReranker(ctx context.Context, cache *Cache, client *Client) knowledge.RerankFunc {
	return func(question string, candidates []knowledge.RerankCandidate) ([]string, error) {
		vecs := make([][]float32, len(candidates))
		for i, c := range candidates {
			v, ok := cache.Lookup(c.ID, c.Text)
			if !ok {
				// Whole-population rejection, not a skip: a rerank missing
				// even one candidate's vector cannot honestly claim to have
				// reordered "the full eligible candidate population" the
				// plan requires -- see this package's own doc comment.
				return nil, fmt.Errorf("no valid cached vector for candidate %q (missing or stale -- rebuild with `estate knowledge embeddings`)", c.ID)
			}
			vecs[i] = v
		}
		qVecs, err := client.Embed(ctx, []string{question})
		if err != nil {
			return nil, fmt.Errorf("embedding the question: %w", err)
		}
		if len(qVecs) != 1 {
			return nil, fmt.Errorf("embedding the question returned %d vector(s), want 1", len(qVecs))
		}
		qVec := qVecs[0]
		// Dimension is validated here, against the live question vector,
		// rather than at Load time -- see Load's own doc comment for why.
		// A live model returning a different dimension than the cache was
		// built with (a different model loaded under the same name, or a
		// server misconfiguration) is exactly the "malformed rank" class
		// of failure the plan requires falling back on, not a panic or a
		// silently wrong cosine similarity.
		if len(qVec) != cache.Dimension {
			return nil, fmt.Errorf("question embedding is dimension %d, cache vectors are dimension %d -- model/cache mismatch", len(qVec), cache.Dimension)
		}

		type ranked struct {
			id  string
			sim float64
		}
		out := make([]ranked, len(candidates))
		for i, c := range candidates {
			out[i] = ranked{id: c.ID, sim: cosineSimilarity(qVec, vecs[i])}
		}
		// Stable sort: candidates with an exactly tied similarity (rare with
		// real floating-point vectors, but not impossible for near-duplicate
		// text) keep the order applyRerank's caller gave them -- BM25's own
		// order, itself already tie-broken by item id -- rather than an
		// arbitrary sort-dependent shuffle.
		sort.SliceStable(out, func(a, b int) bool { return out[a].sim > out[b].sim })

		ids := make([]string, len(out))
		for i, r := range out {
			ids[i] = r.id
		}
		return ids, nil
	}
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return -1 // maximally dissimilar -- shape mismatch should already have been caught by cache validation; this is a safety net, not the primary check
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
