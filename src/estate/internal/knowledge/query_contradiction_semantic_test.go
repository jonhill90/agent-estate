package knowledge

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// contradictionSemanticFixtureIndex builds four items sharing a common,
// unique probe term set so a single query surfaces all four as real BM25
// candidates, with a controlled score ordering: c/d score highest (most
// shared terms), a/b score lowest (fewest, but still clearing
// corpus-question's own sparseMatchSources floor of 3 distinct terms). a
// is a corpus-question and b is a vault-fact sharing 3 terms with it and a
// near-identical score -- a real contradiction-shaped pair by every one of
// detectContradictions' own thresholds, EXCEPT that under plain BM25 they
// rank 2 and 3 (0-indexed), never the top two.
func contradictionSemanticFixtureIndex(t *testing.T) string {
	t.Helper()
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "c-filler-top", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/c-filler.md",
				Tier1:       "zzzcontradictionprobe alpha bravo charlie delta",
				Tier2:       "unrelated filler content, matches all 5 query terms.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "d-filler-second", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/d-filler.md",
				Tier1:       "zzzcontradictionprobe alpha bravo charlie delta",
				Tier2:       "unrelated filler content, also matches all 5 query terms.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "a-question", Source: "corpus-question",
				Permalink:   "corpus:item:a-question",
				Tier1:       "zzzcontradictionprobe alpha bravo",
				Tier2:       "a real, unresolved question sharing terms with b-assertion.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "b-assertion", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/b-assertion.md",
				Tier1:       "zzzcontradictionprobe alpha bravo",
				Tier2:       "a settled fact sharing terms with a-question, same score.",
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

const contradictionSemanticProbeQuestion = "zzzcontradictionprobe alpha bravo charlie delta"

// contradictionDemotionFixtureIndex is contradictionSemanticFixtureIndex
// with the score roles inverted: the real contradiction pair
// (q-real/assertion-real) matches all 5 query terms and scores HIGHEST --
// a genuine BM25-top-two pair, exactly the position the detector's own
// thresholds were calibrated against -- while filler-1/filler-2 match
// only 3 terms and rank lower under plain BM25. Used by
// TestSemanticDemotionStillReportsAVisibleBM25Contradiction, which needs
// the opposite rank arrangement from the promotion test above.
func contradictionDemotionFixtureIndex(t *testing.T) string {
	t.Helper()
	res := Result{
		GeneratedAt:   time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
		StalenessRule: stalenessRule,
		Note:          derivedNote,
		Items: []Item{
			{
				ID: "q-real", Source: "corpus-question",
				Permalink:   "corpus:item:q-real",
				Tier1:       "zzzdemoteprobe alpha bravo charlie delta",
				Tier2:       "a real, unresolved question -- BM25 top-two by design.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "assertion-real", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/assertion-real.md",
				Tier1:       "zzzdemoteprobe alpha bravo charlie delta",
				Tier2:       "a settled fact sharing terms with q-real, same score.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "filler-1", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/filler-1.md",
				Tier1:       "zzzdemoteprobe alpha bravo",
				Tier2:       "unrelated filler, ranks lower under plain BM25.",
				Publishable: true, PublishBasis: "test fixture: marked publishable",
			},
			{
				ID: "filler-2", Source: "vault-fact",
				Permalink:   "/vault/agent/facts/filler-2.md",
				Tier1:       "zzzdemoteprobe alpha bravo",
				Tier2:       "unrelated filler, also ranks lower under plain BM25.",
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

const contradictionDemotionProbeQuestion = "zzzdemoteprobe alpha bravo charlie delta"

// promoteToFrontRerank returns a RerankFunc that places the named ids
// first, in the given order, followed by every other candidate in their
// original (BM25) order -- a controllable, deterministic reorder, the same
// shape TestQueryWithRankingAppliesAValidRerank already uses reverseRerank
// for.
func promoteToFrontRerank(front ...string) RerankFunc {
	frontSet := make(map[string]bool, len(front))
	for _, id := range front {
		frontSet[id] = true
	}
	return func(_ string, candidates []RerankCandidate) ([]string, error) {
		ids := append([]string{}, front...)
		for _, c := range candidates {
			if !frontSet[c.ID] {
				ids = append(ids, c.ID)
			}
		}
		return ids, nil
	}
}

// TestSemanticPromotionDoesNotCreateUncalibratedContradiction is
// agent-estate#1368's false-positive direction: a-question and
// b-assertion are never BM25's top two (c-filler-top/d-filler-second
// score higher) -- detectContradictions' own calibration never evaluated
// this pair at rank 0/1, only ever at rank 2/3 under real BM25 data. A
// reranker that promotes them to the displayed top two anyway (the exact
// shape a real cosine similarity score could produce -- e.g. a-question
// and b-assertion phrased closer to the literal query text than the two
// fillers) must not manufacture a contradiction the calibration was never
// shown was reliable at that position.
func TestSemanticPromotionDoesNotCreateUncalibratedContradiction(t *testing.T) {
	path := contradictionSemanticFixtureIndex(t)

	bm25 := Query(path, contradictionSemanticProbeQuestion, 0, false)
	if len(bm25.Matches) != 4 {
		t.Fatalf("fixture must surface all 4 items as real BM25 candidates, got %d: %+v", len(bm25.Matches), bm25.Matches)
	}
	if bm25.Matches[0].ID != "c-filler-top" || bm25.Matches[1].ID != "d-filler-second" {
		t.Fatalf("fixture's own BM25 order is not what this test assumes -- got %s, %s, %s, %s (want c-filler-top, d-filler-second first)",
			bm25.Matches[0].ID, bm25.Matches[1].ID, bm25.Matches[2].ID, bm25.Matches[3].ID)
	}
	if len(bm25.Contradictions) != 0 {
		t.Fatalf("sanity check failed: plain BM25 order must report zero contradictions here (a-question/b-assertion are not BM25 top-2), got %+v", bm25.Contradictions)
	}

	rerank := promoteToFrontRerank("a-question", "b-assertion")
	got := QueryWithRanking(path, contradictionSemanticProbeQuestion, 0, false, rerank)
	if got.RankingMethod != "semantic" {
		t.Fatalf("RankingMethod = %q, want %q -- promoteToFrontRerank is a valid full permutation", got.RankingMethod, "semantic")
	}
	if got.Matches[0].ID != "a-question" || got.Matches[1].ID != "b-assertion" {
		t.Fatalf("rerank did not actually promote a-question/b-assertion to the top two -- got %s, %s", got.Matches[0].ID, got.Matches[1].ID)
	}
	if len(got.Contradictions) != 0 {
		t.Fatalf("FALSE POSITIVE: a pair never evaluated as a contradiction candidate under BM25 (rank 2/3) was reported as one merely because reranking displayed it at rank 0/1: %+v", got.Contradictions)
	}
}

// TestSemanticDemotionStillReportsAVisibleBM25Contradiction is
// agent-estate#1368's false-negative direction: q-real/assertion-real
// (sharing terms, close scores) sit at BM25 rank 0/1 -- a real,
// calibration-eligible contradiction -- and filler-1/filler-2 are
// reranked ahead of them. Under the PRE-FIX behaviour (detectContradictions
// run directly on the reordered out.Matches), q-real/assertion-real would
// now sit at display rank 2/3, past contradictionMaxRank, and the
// contradiction would silently vanish even though both items are still
// fully visible on the page. The fix must still report it: a pair the
// calibration WAS shown, that a --semantic caller can still see (just not
// first), is not the same failure as promoting an uncalibrated pair into
// view.
func TestSemanticDemotionStillReportsAVisibleBM25Contradiction(t *testing.T) {
	path := contradictionDemotionFixtureIndex(t)

	bm25 := Query(path, contradictionDemotionProbeQuestion, 0, false)
	if len(bm25.Matches) != 4 {
		t.Fatalf("fixture must surface all 4 items as real BM25 candidates, got %d", len(bm25.Matches))
	}
	top2 := map[string]bool{bm25.Matches[0].ID: true, bm25.Matches[1].ID: true}
	if !top2["q-real"] || !top2["assertion-real"] {
		t.Fatalf("fixture's own BM25 order is not what this test assumes -- want q-real and assertion-real in the first two positions (either order -- they tie on score, and this test does not depend on which one sorts first), got %s, %s",
			bm25.Matches[0].ID, bm25.Matches[1].ID)
	}
	if len(bm25.Contradictions) != 1 {
		t.Fatalf("sanity check failed: plain BM25 order must report the real q-real/assertion-real contradiction: got %d: %+v", len(bm25.Contradictions), bm25.Contradictions)
	}

	// Demote the real pair by promoting BOTH fillers ahead of them --
	// q-real/assertion-real land at display rank 2/3, still inside the
	// default 10-item limit (still visible), never at rank 0/1 again.
	rerank := promoteToFrontRerank("filler-1", "filler-2")
	got := QueryWithRanking(path, contradictionDemotionProbeQuestion, 0, false, rerank)
	if got.RankingMethod != "semantic" {
		t.Fatalf("RankingMethod = %q, want %q", got.RankingMethod, "semantic")
	}
	foundAtFront := got.Matches[0].ID == "q-real" || got.Matches[0].ID == "assertion-real" ||
		got.Matches[1].ID == "q-real" || got.Matches[1].ID == "assertion-real"
	if foundAtFront {
		t.Fatalf("rerank did not actually demote q-real/assertion-real out of the top two -- got order %s, %s, %s, %s",
			got.Matches[0].ID, got.Matches[1].ID, got.Matches[2].ID, got.Matches[3].ID)
	}

	if len(got.Contradictions) != 1 {
		t.Fatalf("FALSE NEGATIVE: a real, BM25-top-two contradiction that is still fully visible on the displayed page (just not first) must still be reported after reranking; got %d: %+v",
			len(got.Contradictions), got.Contradictions)
	}
	c := got.Contradictions[0]
	if c.QuestionID != "q-real" || c.AssertionID != "assertion-real" {
		t.Fatalf("wrong pair reported: %+v", c)
	}
}

// TestSemanticRankingLeavesBM25OnlyContradictionsByteIdentical is
// agent-estate#1368's own explicit "must not change behaviour when
// --semantic is off" requirement: Query (nil reranker) and
// QueryWithRanking(..., nil) must report byte-identical Contradictions on
// a fixture that has a real one -- this fix's new branch must be a
// complete no-op on the bm25 path.
func TestSemanticRankingLeavesBM25OnlyContradictionsByteIdentical(t *testing.T) {
	path := contradictionDemotionFixtureIndex(t)
	viaQuery := Query(path, contradictionDemotionProbeQuestion, 0, false)
	viaSeamNil := QueryWithRanking(path, contradictionDemotionProbeQuestion, 0, false, nil)
	if len(viaQuery.Contradictions) != 1 {
		t.Fatalf("fixture must produce exactly one real contradiction under plain BM25, got %d", len(viaQuery.Contradictions))
	}
	if !reflect.DeepEqual(viaSeamNil.Contradictions, viaQuery.Contradictions) {
		t.Fatalf("Query and QueryWithRanking(..., nil) diverged on Contradictions:\nQuery:            %+v\nQueryWithRanking: %+v",
			viaQuery.Contradictions, viaSeamNil.Contradictions)
	}

	// A reranker that ERRORS must fall back to bm25 exactly, Contradictions
	// included -- not just Matches/order.
	erroring := func(string, []RerankCandidate) ([]string, error) {
		return nil, errFakeRerankForTest
	}
	viaError := QueryWithRanking(path, contradictionDemotionProbeQuestion, 0, false, erroring)
	if viaError.RankingMethod != "bm25" {
		t.Fatalf("RankingMethod = %q on reranker error, want %q", viaError.RankingMethod, "bm25")
	}
	if !reflect.DeepEqual(viaError.Contradictions, viaQuery.Contradictions) {
		t.Fatalf("Contradictions after a reranker error diverged from plain BM25: %+v vs %+v", viaError.Contradictions, viaQuery.Contradictions)
	}
}

type fakeRerankTestError string

func (e fakeRerankTestError) Error() string { return string(e) }

const errFakeRerankForTest = fakeRerankTestError("simulated reranker failure for TestSemanticRankingLeavesBM25OnlyContradictionsByteIdentical")
