package knowledge

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// rerankFixtureIndex is a small, dedicated fixture for the reordering
// tests below: three items whose Tier1 all carry a shared, unique probe
// term ("zzzrerankprobe") so a query for it reliably surfaces all three as
// real BM25 matches (the term appears in each item's own title, clearing
// any per-source matched-terms floor via the title-match rescue) without
// this file depending on testIndex()'s own term-overlap tuning, which is
// exercised and measured by query_test.go's own tests, not by this one.
func rerankFixtureIndex(t *testing.T) string {
	t.Helper()
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "rerank-a", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/rerank-a.md",
				Tier1:       "zzzrerankprobe alpha item",
				Tier2:       "the first of three rerank-test fixture items.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "rerank-b", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/rerank-b.md",
				Tier1:       "zzzrerankprobe bravo item",
				Tier2:       "the second of three rerank-test fixture items.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "rerank-c", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/rerank-c.md",
				Tier1:       "zzzrerankprobe charlie item",
				Tier2:       "the third of three rerank-test fixture items.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
		},
	}
	path := filepath.Join(t.TempDir(), "index.json")
	if err := Write(path, res); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestQueryWithRankingNilRerankerMatchesQuery is the seam's own zero-value
// contract: Query IS QueryWithRanking with a nil reranker, so the two must
// produce byte-identical results on every existing caller's behalf. If
// this ever diverges, some field the reranker path touches leaked into the
// no-reranker path too -- agent-estate#1255's "removable function seam"
// requirement means removing the caller of the seam must restore exactly
// this.
func TestQueryWithRankingNilRerankerMatchesQuery(t *testing.T) {
	path := writeTestIndex(t)
	question := "what did Jon decide about auth tokens"

	viaQuery := Query(path, question, 0, false)
	viaSeam := QueryWithRanking(path, question, 0, false, nil)

	if !reflect.DeepEqual(viaQuery, viaSeam) {
		t.Fatalf("Query and QueryWithRanking(..., nil) diverged:\nQuery:            %+v\nQueryWithRanking: %+v", viaQuery, viaSeam)
	}
	if viaSeam.RankingMethod != "bm25" {
		t.Errorf("RankingMethod = %q, want %q with no reranker supplied", viaSeam.RankingMethod, "bm25")
	}
	if viaSeam.RankingFallbackReason != "" {
		t.Errorf("RankingFallbackReason = %q, want empty -- no reranker was ever supplied, so there is nothing to have fallen back FROM", viaSeam.RankingFallbackReason)
	}
}

// reverseRerank is a trivial, deterministic RerankFunc: it returns the
// given candidates' ids in exactly reverse order. Used to prove
// QueryWithRanking actually APPLIES a valid rerank result, not merely
// accepts one without effect.
func reverseRerank(_ string, candidates []RerankCandidate) ([]string, error) {
	ids := make([]string, len(candidates))
	for i, c := range candidates {
		ids[len(candidates)-1-i] = c.ID
	}
	return ids, nil
}

func TestQueryWithRankingAppliesAValidRerank(t *testing.T) {
	path := rerankFixtureIndex(t)
	question := "zzzrerankprobe"

	bm25 := Query(path, question, 0, false)
	if len(bm25.Matches) < 2 {
		t.Fatalf("fixture needs at least 2 BM25 matches for this test to mean anything, got %d", len(bm25.Matches))
	}

	got := QueryWithRanking(path, question, 0, false, reverseRerank)
	if got.RankingMethod != "semantic" {
		t.Fatalf("RankingMethod = %q, want %q -- reverseRerank returned a valid full permutation", got.RankingMethod, "semantic")
	}
	if got.RankingFallbackReason != "" {
		t.Errorf("RankingFallbackReason = %q, want empty on a successfully applied rerank", got.RankingFallbackReason)
	}
	if len(got.Matches) != len(bm25.Matches) {
		t.Fatalf("reranked match count = %d, want %d (same count as BM25 -- reordering must not change how many are returned)", len(got.Matches), len(bm25.Matches))
	}
	for i, m := range got.Matches {
		want := bm25.Matches[len(bm25.Matches)-1-i]
		if m.ID != want.ID {
			t.Fatalf("Matches[%d].ID = %q, want %q (reverse of BM25 order) -- rerank was not actually applied", i, m.ID, want.ID)
		}
		// Score is BM25's own score, unchanged by reordering -- agent-estate#1255:
		// "scores remain explicitly identified". Reordering must never
		// invent a different score for the same item.
		if m.Score != want.Score {
			t.Errorf("Matches[%d].Score = %d, want %d (this item's own BM25 score, unchanged by reranking)", i, m.Score, want.Score)
		}
	}
}

func TestQueryWithRankingFallsBackExactlyOnRerankerError(t *testing.T) {
	path := writeTestIndex(t)
	question := "what did Jon decide about auth tokens"

	bm25 := Query(path, question, 0, false)
	erroringRerank := func(string, []RerankCandidate) ([]string, error) {
		return nil, fmt.Errorf("simulated: embedding endpoint unreachable")
	}

	got := QueryWithRanking(path, question, 0, false, erroringRerank)
	if got.RankingMethod != "bm25" {
		t.Errorf("RankingMethod = %q, want %q on reranker error (exact BM25 fallback)", got.RankingMethod, "bm25")
	}
	if got.RankingFallbackReason == "" {
		t.Error("RankingFallbackReason is empty -- a reranker error must be disclosed, not silently swallowed")
	}
	if !reflect.DeepEqual(got.Matches, bm25.Matches) {
		t.Fatalf("Matches after a reranker error diverged from plain BM25 Matches -- fallback is not exact:\nfallback: %+v\nbm25:     %+v", got.Matches, bm25.Matches)
	}
	if got.TotalMatched != bm25.TotalMatched || got.NotReturned != bm25.NotReturned {
		t.Errorf("counts after a reranker error diverged from plain BM25 (TotalMatched=%d/%d, NotReturned=%d/%d) -- 'exact BM25 fallback on error' includes counts, not just order",
			got.TotalMatched, bm25.TotalMatched, got.NotReturned, bm25.NotReturned)
	}
}

// TestQueryWithRankingRejectsAnIncompleteResult covers the "malformed
// rank" half of agent-estate#1255's exact-fallback requirement: a
// reranker that returns FEWER ids than it was given (e.g. it silently
// dropped one) must be rejected as a whole, not partially applied.
func TestQueryWithRankingRejectsAnIncompleteResult(t *testing.T) {
	path := writeTestIndex(t)
	question := "what did Jon decide about auth tokens"
	bm25 := Query(path, question, 0, false)

	incomplete := func(_ string, candidates []RerankCandidate) ([]string, error) {
		if len(candidates) == 0 {
			return nil, nil
		}
		ids := make([]string, len(candidates)-1) // one short, deliberately
		for i := range ids {
			ids[i] = candidates[i].ID
		}
		return ids, nil
	}

	got := QueryWithRanking(path, question, 0, false, incomplete)
	if got.RankingMethod != "bm25" {
		t.Errorf("RankingMethod = %q, want %q -- an incomplete id list must be rejected as a whole", got.RankingMethod, "bm25")
	}
	if got.RankingFallbackReason == "" {
		t.Error("RankingFallbackReason is empty -- an incomplete rerank result must be disclosed")
	}
	if !reflect.DeepEqual(got.Matches, bm25.Matches) {
		t.Fatal("an incomplete rerank result was PARTIALLY applied -- it must be rejected as a whole, exact BM25 order/count restored")
	}
}

// TestQueryWithRankingRejectsDuplicateIDs covers a reranker that returns
// the right COUNT of ids but with a duplicate (so, structurally, one real
// candidate is silently missing and another is double-counted) -- just as
// invalid as an incomplete list, and easy to miss if only length were
// checked.
func TestQueryWithRankingRejectsDuplicateIDs(t *testing.T) {
	path := rerankFixtureIndex(t)
	question := "zzzrerankprobe"
	bm25 := Query(path, question, 0, false)
	if len(bm25.Matches) < 2 {
		t.Fatalf("fixture needs at least 2 matches, got %d", len(bm25.Matches))
	}

	duplicated := func(_ string, candidates []RerankCandidate) ([]string, error) {
		ids := make([]string, len(candidates))
		for i := range ids {
			ids[i] = candidates[0].ID // every slot names the same, first candidate
		}
		return ids, nil
	}

	got := QueryWithRanking(path, question, 0, false, duplicated)
	if got.RankingMethod != "bm25" {
		t.Errorf("RankingMethod = %q, want %q -- a duplicate-id result must be rejected", got.RankingMethod, "bm25")
	}
	if !reflect.DeepEqual(got.Matches, bm25.Matches) {
		t.Fatal("a duplicate-id rerank result was applied instead of rejected -- exact BM25 fallback expected")
	}
}

// TestQueryWithRankingRejectsAnUnknownID is the "it reorders; it must not
// admit" half of agent-estate#1255: a reranker cannot smuggle in an id
// that was never part of the eligible population Query decided on, even
// if the count otherwise matches.
func TestQueryWithRankingRejectsAnUnknownID(t *testing.T) {
	path := writeTestIndex(t)
	question := "what did Jon decide about auth tokens"
	bm25 := Query(path, question, 0, false)
	if len(bm25.Matches) < 1 {
		t.Fatal("fixture needs at least 1 match")
	}

	foreignID := func(_ string, candidates []RerankCandidate) ([]string, error) {
		ids := make([]string, len(candidates))
		copy(ids, []string{"this-id-was-never-a-candidate"})
		for i := 1; i < len(ids); i++ {
			ids[i] = candidates[i].ID
		}
		return ids, nil
	}

	got := QueryWithRanking(path, question, 0, false, foreignID)
	if got.RankingMethod != "bm25" {
		t.Errorf("RankingMethod = %q, want %q -- an id outside the given candidate population must be rejected", got.RankingMethod, "bm25")
	}
	for _, m := range got.Matches {
		if m.ID == "this-id-was-never-a-candidate" {
			t.Fatal("a reranker-invented id that was never in the eligible population was admitted into Matches")
		}
	}
}

// TestQueryWithRankingRerankerNeverSeesWithheldPrivateItems is
// agent-estate#1255's privacy-retention requirement, proven directly: a
// recording RerankFunc captures exactly which candidate ids it was
// handed, and a private item excluded by the default (non-private) call
// must never appear among them -- the reranker reorders Query's own
// eligible population, it is never handed a population Query itself
// would not have returned.
func TestQueryWithRankingRerankerNeverSeesWithheldPrivateItems(t *testing.T) {
	path := privateIndex(t)
	question := "credential rotation keychain"

	var seenIDs []string
	recorder := func(_ string, candidates []RerankCandidate) ([]string, error) {
		ids := make([]string, len(candidates))
		for i, c := range candidates {
			ids[i] = c.ID
			seenIDs = append(seenIDs, c.ID)
		}
		return ids, nil
	}

	got := QueryWithRanking(path, question, 0, false, recorder)
	if got.WithheldPrivate == 0 {
		t.Fatal("fixture expected at least one withheld-private item for this test to mean anything")
	}
	for _, id := range seenIDs {
		if id == "20260903130001" { // the private item in privateIndex()
			t.Fatalf("the reranker was handed the withheld-private item's id (%s) -- privacy filtering was bypassed by the rerank seam", id)
		}
	}
	if len(seenIDs) != got.TotalMatched {
		t.Errorf("reranker saw %d candidate(s), TotalMatched is %d -- the reranker must see EXACTLY Query's own eligible population, not more or fewer", len(seenIDs), got.TotalMatched)
	}

	// The same call WITH --private must include it -- confirming the
	// exclusion above is really privacy-driven, not this fixture's
	// question simply never matching that item at all.
	seenIDs = nil
	gotPrivate := QueryWithRanking(path, question, 0, true, recorder)
	found := false
	for _, id := range seenIDs {
		if id == "20260903130001" {
			found = true
		}
	}
	if !found {
		t.Fatal("the private item never appeared to the reranker even in --private mode -- the fixture question does not exercise what this test claims to")
	}
	_ = gotPrivate
}
