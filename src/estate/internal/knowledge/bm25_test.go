package knowledge

import "testing"

// TestBM25ScorerSatisfiesScorer is a compile-time-ish check that
// BM25Scorer really is a Scorer -- the seam #1054 asks for, so a later
// semantic scorer is a plug rather than a rewrite of Query.
func TestBM25ScorerSatisfiesScorer(t *testing.T) {
	var _ Scorer = NewBM25Scorer(nil)
}

// TestBM25ScoreZeroOnNoOverlap -- an item sharing no term with the
// question scores exactly 0, and MatchedTerms is empty.
func TestBM25ScoreZeroOnNoOverlap(t *testing.T) {
	items := []Item{{ID: "a", Tier1: "completely unrelated content"}}
	scorer := NewBM25Scorer(items)
	score, matched := scorer.Score(items[0], []string{"nowhere", "present"})
	if score != 0 {
		t.Errorf("score = %v, want 0", score)
	}
	if len(matched) != 0 {
		t.Errorf("matched = %v, want none", matched)
	}
}

// TestBM25ScorePositiveOnOverlap -- the inverse: any shared term produces
// a strictly positive score and names itself in matched.
func TestBM25ScorePositiveOnOverlap(t *testing.T) {
	items := []Item{
		{ID: "a", Tier1: "auth tokens rotate every ninety days"},
		{ID: "b", Tier1: "deploy windows are weekday mornings only"},
	}
	scorer := NewBM25Scorer(items)
	score, matched := scorer.Score(items[0], queryTerms("auth tokens"))
	if score <= 0 {
		t.Errorf("score = %v, want > 0", score)
	}
	if len(matched) != 2 {
		t.Errorf("matched = %v, want both auth and token(s)", matched)
	}
}

// TestBM25FieldWeightFavoursTier1 pins tier1FieldWeight > tier2FieldWeight
// directly against the scorer (bm25.go), independent of Query's own
// ranking-order test in query_test.go: the SAME term, in the SAME
// otherwise-identical document, scores higher when it lives in Tier1
// than when it lives only in Tier2.
func TestBM25FieldWeightFavoursTier1(t *testing.T) {
	tier1Item := Item{ID: "a", Tier1: "keychain", Tier2: ""}
	tier2Item := Item{ID: "b", Tier1: "unrelated", Tier2: "keychain"}
	scorer := NewBM25Scorer([]Item{tier1Item, tier2Item})

	tier1Score, _ := scorer.Score(tier1Item, []string{"keychain"})
	tier2Score, _ := scorer.Score(tier2Item, []string{"keychain"})
	if tier1Score <= tier2Score {
		t.Errorf("tier1 score = %v, tier2-only score = %v -- want tier1 strictly higher", tier1Score, tier2Score)
	}
}

// TestBM25RareTermOutscoresCommonTerm is the scorer-level version of
// #1054's own required property: with df(rare)=1 and df(common)=9 out of
// 10 items, idf must make a single rare-term match worth more than a
// single common-term match, before any summing over multiple terms
// happens at all.
func TestBM25RareTermOutscoresCommonTerm(t *testing.T) {
	items := []Item{{ID: "target", Tier1: "zzrareterm here"}}
	for i := 0; i < 9; i++ {
		items = append(items, Item{ID: string(rune('a' + i)), Tier1: "common word filler"})
	}
	scorer := NewBM25Scorer(items)

	rareScore, _ := scorer.Score(items[0], []string{"zzrareterm"})
	commonScore, _ := scorer.Score(items[1], []string{"common"})
	if rareScore <= commonScore {
		t.Errorf("rare-term score = %v, common-term score = %v -- want rare strictly higher", rareScore, commonScore)
	}
}
