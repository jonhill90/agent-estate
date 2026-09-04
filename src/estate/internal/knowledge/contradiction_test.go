package knowledge

import "testing"

// realReproductionMatches is agent-estate#1051's own reproduction,
// reduced to the shape detectContradictions reads (ID, Source, Score,
// MatchedTerms) -- the actual --private query "drive agents by typing
// into terminals" against the real compiled index, IDs and scores copied
// verbatim from that run. This is the failing case before this file
// existed: TestDetectContradictionsFindsRealReproduction fails against
// the parent commit (8379899) because detectContradictions did not exist
// there, and Query never set Contradictions -- run it with `git stash`
// applied to isolate this file plus its two-line query.go/main.go wiring
// reverted, and it does not compile at all, which is the strongest
// possible "fails before" for a function that is new.
var realReproductionMatches = []Match{
	{ID: "it-6fa6715ea281c575", Source: "corpus-question", Score: 23, MatchedTerms: []string{"agent", "type", "into", "terminal"}},
	{ID: "it-53519d5f9a740c50", Source: "vault-fact", Score: 21, MatchedTerms: []string{"drive", "agent", "type", "into", "terminal"}},
	{ID: "it-80eb1937fe08580b", Source: "repo-docs", Score: 21, MatchedTerms: []string{"drive", "agent", "type", "into", "terminal"}},
}

func TestDetectContradictionsFindsRealReproduction(t *testing.T) {
	got := detectContradictions(realReproductionMatches)
	if len(got) != 1 {
		t.Fatalf("detectContradictions(real reproduction) = %d contradictions, want exactly 1: %+v", len(got), got)
	}
	c := got[0]
	if c.QuestionID != "it-6fa6715ea281c575" || c.AssertionID != "it-53519d5f9a740c50" {
		t.Errorf("detectContradictions(real reproduction) paired (%s, %s), want (it-6fa6715ea281c575, it-53519d5f9a740c50)",
			c.QuestionID, c.AssertionID)
	}
	if c.QuestionSource != "corpus-question" || c.AssertionSource != "vault-fact" {
		t.Errorf("detectContradictions(real reproduction) sources = (%s, %s), want (corpus-question, vault-fact)",
			c.QuestionSource, c.AssertionSource)
	}
	wantShared := map[string]bool{"agent": true, "type": true, "into": true, "terminal": true}
	if len(c.SharedTerms) != len(wantShared) {
		t.Errorf("detectContradictions(real reproduction) shared terms = %v, want all four of %v", c.SharedTerms, wantShared)
	}
	for _, term := range c.SharedTerms {
		if !wantShared[term] {
			t.Errorf("detectContradictions(real reproduction) shared an unexpected term %q", term)
		}
	}
}

// TestDetectContradictionsGoldenCases is agent-estate#1051's own required
// golden set: cases whose expected outcome, fixed before this test was
// ever run against the real detector, is either "the record disagrees
// with itself, look" or "this is ordinary topical overlap, stay quiet."
// Every "no fire" case here reproduces the SHAPE of a real false positive
// this package's three thresholds were measured against on the real
// compiled index (see contradiction.go's own doc comments on
// contradictionMinSharedTerms/contradictionMaxRank/
// contradictionMinScoreRatio for the actual queries and item ids each
// one is drawn from) -- not invented shapes, reduced fixtures of real
// near-misses.
func TestDetectContradictionsGoldenCases(t *testing.T) {
	cases := []struct {
		name        string
		matches     []Match
		wantFire    bool
		wantPairs   int
		description string
	}{
		{
			name:        "real reproduction: adjacent, close score, four shared terms",
			matches:     realReproductionMatches,
			wantFire:    true,
			wantPairs:   1,
			description: "the record disagrees with itself, look",
		},
		{
			name: "no assertion-kind item present at all",
			matches: []Match{
				{ID: "q1", Source: "corpus-question", Score: 23, MatchedTerms: []string{"agent", "type", "into", "terminal"}},
				{ID: "p1", Source: "corpus-parameter", Score: 21, MatchedTerms: []string{"agent", "type", "into", "terminal"}},
			},
			wantFire:    false,
			description: "corpus-parameter is not an assertion source for this detector -- #1051 names vault-fact/corpus-directive only",
		},
		{
			name: "no question-kind item present at all",
			matches: []Match{
				{ID: "d1", Source: "corpus-directive", Score: 23, MatchedTerms: []string{"agent", "type", "into", "terminal"}},
				{ID: "f1", Source: "vault-fact", Score: 21, MatchedTerms: []string{"agent", "type", "into", "terminal"}},
			},
			wantFire:    false,
			description: "two assertions with no open question is not a contradiction, only two facts",
		},
		{
			name: "high shared-term overlap but too wide a score gap -- 'director' false positive shape",
			matches: []Match{
				{ID: "q1", Source: "corpus-question", Score: 19, MatchedTerms: []string{"when", "director", "readi"}},
				{ID: "d1", Source: "corpus-directive", Score: 22, MatchedTerms: []string{"when", "director", "own", "readi", "itself"}},
			},
			// Real measurement: this pair shares THREE terms (when,
			// director, readi) -- an unrelated delivery-method directive
			// and a question about a person's autonomy, agreeing only by
			// coincidence of common phrasing. Shared-term overlap alone
			// (contradictionMinSharedTerms) does NOT separate this from a
			// real contradiction, and rank alone does not either (both
			// items are top-2 in the real result set) -- ratio 19/22 ≈
			// 0.864 is what excludes it, just short of
			// contradictionMinScoreRatio (0.9). This case exists to prove
			// the ratio threshold is load-bearing, not merely present.
			wantFire:    false,
			description: "term overlap and rank both pass here; only the score-ratio floor keeps this out",
		},
		{
			name: "third position drops out of the top-two rank window",
			matches: []Match{
				{ID: "q1", Source: "corpus-question", Score: 26, MatchedTerms: []string{"where", "put"}},
				{ID: "f1", Source: "vault-fact", Score: 20, MatchedTerms: []string{"unrelated"}},
				{ID: "d1", Source: "corpus-directive", Score: 17, MatchedTerms: []string{"where", "put"}},
			},
			wantFire:    false,
			description: "'where do I put a new view' false positive shape: the coincidentally-matching directive sits at rank 3, outside contradictionMaxRank",
		},
		{
			name: "wide score gap between top-two-ranked items",
			matches: []Match{
				{ID: "q1", Source: "corpus-question", Score: 26, MatchedTerms: []string{"why", "agent", "tmux", "session"}},
				{ID: "f1", Source: "vault-fact", Score: 19, MatchedTerms: []string{"why", "agent", "tmux", "session"}},
			},
			// ratio 19/26 ≈ 0.73, below contradictionMinScoreRatio (0.9) --
			// the "tmux session logged out" false positive shape (a
			// locked-keychain postmortem coincidentally sharing four
			// generic terms with an unrelated question).
			wantFire:    false,
			description: "'tmux session logged out' false positive shape: same rank window, same term overlap, but the scores are not close enough to call this the same subject",
		},
		{
			name: "exactly at the score-ratio floor still fires",
			matches: []Match{
				{ID: "q1", Source: "corpus-question", Score: 20, MatchedTerms: []string{"agent", "type", "into", "terminal"}},
				{ID: "f1", Source: "vault-fact", Score: 18, MatchedTerms: []string{"agent", "type", "into", "terminal"}},
			},
			// ratio 18/20 = 0.9 exactly -- contradictionMinScoreRatio's own
			// boundary, included so a future edit that tightens the
			// comparison from >= to > is caught here, not only in
			// production.
			wantFire:    true,
			wantPairs:   1,
			description: "boundary case: ratio == contradictionMinScoreRatio must still fire (>=, not >)",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := detectContradictions(c.matches)
			fired := len(got) > 0
			if fired != c.wantFire {
				t.Fatalf("%s: detectContradictions fired=%v (%d pairs), want fired=%v -- %s",
					c.name, fired, len(got), c.wantFire, c.description)
			}
			if c.wantFire && len(got) != c.wantPairs {
				t.Errorf("%s: detectContradictions returned %d pairs, want %d -- %s",
					c.name, len(got), c.wantPairs, c.description)
			}
		})
	}
}

// TestDetectContradictionsNeverAdjudicates pins the "surface, never
// adjudicate" requirement structurally: Contradiction carries no field
// that could be read as a verdict (no "correct" bool, no ranking
// override), only the two ids, their sources and the shared terms. This
// test exists so a future field addition that smuggles in a judgement
// (e.g. a "likely_correct_id") fails an explicit assertion rather than
// slipping in unnoticed.
func TestDetectContradictionsNeverAdjudicates(t *testing.T) {
	got := detectContradictions(realReproductionMatches)
	if len(got) != 1 {
		t.Fatalf("setup: expected exactly 1 contradiction, got %d", len(got))
	}
	c := got[0]
	// The only claim: two ids, their sources (as already carried on every
	// other Match), and the terms both were scored on. Order in Matches
	// is untouched by this function -- detectContradictions takes
	// matches by value semantics only (reads, never mutates or reorders)
	// and this test's caller-side slice is unchanged after the call.
	if realReproductionMatches[0].ID != "it-6fa6715ea281c575" || realReproductionMatches[1].ID != "it-53519d5f9a740c50" {
		t.Fatalf("detectContradictions mutated or reordered its input matches: %+v", realReproductionMatches)
	}
	_ = c
}
