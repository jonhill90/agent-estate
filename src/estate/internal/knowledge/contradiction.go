package knowledge

// This file is agent-estate#1051: retrieval can return a real,
// never-answered question and a real, confidently-worded fact about the
// same subject, adjacent, with nothing in the output saying they
// disagree. See Contradiction's own doc comment for what is detected and
// detectContradictions's for the three thresholds that keep it from
// firing on ordinary topical overlap.

// contradictionQuestionSource is the one Source value this package treats
// as "asked, not settled" for this detector -- agent-estate#1045's kind
// field, carried into Item.Source as "corpus-" + kind (see corpus.go's
// corpusSourceName). Only the corpus's own question rows: a repo-docs or
// vault-fact item can ask a rhetorical question in its own prose, but this
// detector reads Source, never text, so it only ever fires on an item the
// corpus itself classified as a question at judging time.
const contradictionQuestionSource = "corpus-question"

// contradictionAssertionSources is which Source values this detector
// treats as "states a settled position" -- exactly the two the issue
// names: a vault fact (the operator's own memory, written once and never
// automatically revised) and a corpus directive (a resolution the corpus
// itself recorded). corpus-parameter is deliberately NOT included here:
// #1051 names vault-fact/corpus-directive specifically, and widening this
// set is a separate, arguable decision this issue does not make.
var contradictionAssertionSources = map[string]bool{
	"vault-fact":       true,
	"corpus-directive": true,
}

// contradictionMinSharedTerms, contradictionMaxRank and
// contradictionMinScoreRatio are the three fixed thresholds
// detectContradictions requires before a pair fires. All three were
// picked by the SAME method: run every candidate threshold against the
// real compiled index, over both the issue's own reproduction and the
// 29 questions already checked in as this package's own golden and
// natural-language sets (goldenset/cases.json,
// goldenset/natural_cases.json), and keep tightening until the false
// positives measured against those 29 -- none of which is a real
// contradiction -- reached zero. See detectContradictions's own doc
// comment and contradiction_test.go for the measurements themselves.
const (
	// contradictionMinSharedTerms: two matched terms is the floor. One
	// shared term fired on generic words ("into", "where", "check") that
	// land in an unrelated item by coincidence far more often than they
	// mark a real shared subject -- measured directly against
	// "drive agents by typing into terminals" (agent-estate#1051's own
	// reproduction), where a threshold of 1 fired eight times on that
	// single query, most pairs sharing only "into".
	contradictionMinSharedTerms = 2
	// contradictionMaxRank: both matches must be in the first two
	// positions of the (already ranked, already capped) result set.
	// Shared terms alone, even several of them, is not enough -- measured
	// directly: "What is the rule when a Director's own PR is ready to
	// merge...?" shares three full terms (when/director/readi) between a
	// question about a person's autonomy and an unrelated delivery-method
	// directive, entirely by coincidence of common phrasing, and both
	// score high enough to be top-3. Restricting to the top two removed
	// that pair and one other (see contradiction_test.go) with no loss
	// against the one real case this package can measure.
	contradictionMaxRank = 2
	// contradictionMinScoreRatio: the lower of the two scores must be at
	// least this fraction of the higher -- both items must be genuinely
	// close in relevance, not merely both present in the top two. Measured
	// directly: "Why do all the agents in a tmux session suddenly look
	// logged out...?" put an unrelated vault fact (a locked-keychain
	// postmortem) at rank 2 with a score far enough below the question's
	// own that a ratio floor separates it from the real case (0.91) while
	// still excluding this one (0.73).
	contradictionMinScoreRatio = 0.9
)

// Contradiction is one pair of returned Matches this package flags as
// disagreeing about the same subject -- agent-estate#1051. It is
// deliberately the narrowest claim this package can make: two specific
// items, on this specific set of shared terms, both near the top of the
// same result set and close in score, are of the two kinds ("asked,
// never answered" and "asserts settled") that disagreeing usually looks
// like. It never states which one is right -- see Query's own wiring in
// query.go and this file's package doc comment for what "surface, never
// adjudicate" rules out.
type Contradiction struct {
	// QuestionID/QuestionSource name the corpus-question match -- always
	// Source == contradictionQuestionSource.
	QuestionID     string `json:"question_id"`
	QuestionSource string `json:"question_source"`
	// AssertionID/AssertionSource name the vault-fact or corpus-directive
	// match it was paired against.
	AssertionID     string `json:"assertion_id"`
	AssertionSource string `json:"assertion_source"`
	// SharedTerms are the stemmed query terms both matches were scored
	// against -- the "same terms" the issue asks for, and (alongside rank
	// and score closeness) part of the evidence this package offers that
	// the two are about the same subject rather than merely co-occurring
	// in one result set.
	SharedTerms []string `json:"shared_terms"`
}

// detectContradictions finds every (corpus-question, vault-fact-or-
// corpus-directive) pair inside matches -- deliberately the same
// already-capped, already-ranked slice a caller is shown (Query passes
// its own out.Matches, post-cap), not the full unfiltered candidate list,
// so "same small result set" means exactly what a reader of the printed
// output sees.
//
// A pair fires only when ALL THREE hold: the two matches share at least
// contradictionMinSharedTerms stemmed terms ("on the same terms", the
// issue's own phrase), both sit within the first contradictionMaxRank
// positions of matches, and their scores are within
// contradictionMinScoreRatio of each other. Every threshold is a fixed
// count or ratio, none of it inferred from content -- still fully
// deterministic, still no LLM judgement (agent-estate#1019's retrieval
// proof stays deterministic, per the issue).
//
// WHY THREE THRESHOLDS AND NOT ONE. Shared terms alone is not enough:
// measured against this package's own 29 checked-in golden and
// natural-language cases (goldenset/cases.json,
// goldenset/natural_cases.json -- none of which is a real contradiction),
// term overlap of two or more fired on multiple pairs that were
// topically adjacent by coincidence, never actually disagreeing --
// generic connector words like "where"/"put" or "when"/"director" recur
// across unrelated corpus rows. Rank and score-ratio restriction each cut
// that false-positive set further; combined, all three reduce the
// measured false-positive rate over both sets to zero (see
// contradiction_test.go) while still firing on the issue's own
// reproduction ("drive agents by typing into terminals" --private: ranks
// 1/2, four shared terms, score ratio 21/23 ≈ 0.91). See each constant's
// own doc comment above for the specific pair that motivated it.
//
// WHAT THIS WILL MISS. A real contradiction sitting at ranks 3+4, or one
// whose two items score far apart despite genuinely disagreeing, does not
// fire -- the same "crude but honest" tradeoff classify.go's own doc
// comment names for a different rule: recall is deliberately sacrificed
// for precision, because a false contradiction warning was measured to be
// worse than a missed one (this issue's own acceptance bar). Widening any
// of the three thresholds is a future, separately-measured decision, not
// a default this package leans toward.
func detectContradictions(matches []Match) []Contradiction {
	var out []Contradiction
	for qi, q := range matches {
		if q.Source != contradictionQuestionSource {
			continue
		}
		if qi >= contradictionMaxRank {
			continue
		}
		for ai, a := range matches {
			if !contradictionAssertionSources[a.Source] {
				continue
			}
			if ai >= contradictionMaxRank {
				continue
			}
			if !closeEnough(q.Score, a.Score) {
				continue
			}
			shared := sharedTerms(q.MatchedTerms, a.MatchedTerms)
			if len(shared) < contradictionMinSharedTerms {
				continue
			}
			out = append(out, Contradiction{
				QuestionID:      q.ID,
				QuestionSource:  q.Source,
				AssertionID:     a.ID,
				AssertionSource: a.Source,
				SharedTerms:     shared,
			})
		}
	}
	return out
}

// closeEnough reports whether the lower of two scores is at least
// contradictionMinScoreRatio of the higher -- both scores are >= 0 by
// construction (Match.Score is a rounded BM25 figure, never negative; see
// Query in query.go), so this never divides by zero except when both are
// zero, which cannot happen for a returned Match (BM25 score > 0 is
// required to be returned at all -- see Query's own doc comment).
func closeEnough(a, b int) bool {
	lo, hi := float64(a), float64(b)
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo/hi >= contradictionMinScoreRatio
}

// sharedTerms returns the terms common to both a and b, in a's own order,
// deduplicated -- a and b are both already-stemmed MatchedTerms lists, so
// this is a plain set intersection, never a second stemming pass.
func sharedTerms(a, b []string) []string {
	inB := make(map[string]bool, len(b))
	for _, t := range b {
		inB[t] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range a {
		if inB[t] && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
