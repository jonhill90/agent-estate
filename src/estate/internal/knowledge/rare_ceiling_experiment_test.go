package knowledge

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

// TestRareCeilingExperiment measures agent-estate#1255's rare/high-IDF
// candidate mechanism -- NOT a fix, a ceiling: how many of the retrieval
// baseline's 17 ranking failures (present in the index, just not top-10)
// could a minimum-count-of-rare-terms rule possibly move into the top 10,
// under the single best global (idf breakpoint, count threshold) setting,
// and how many of the 9 current hits would that same setting break.
//
// BM25 sums term contributions independently, so a wrong but wordier
// document can outscore a terser correct one. The candidate this measures:
// instead of ranking purely by summed score, partition candidates into
// "clears a minimum count of matched terms whose idf is at least a
// breakpoint" and "does not", promote the first group above the second,
// and keep each group's own existing (real, already-computed) relative
// order inside itself -- a stable two-bucket re-partition of Query's own
// output, never a second scoring function.
//
// This is why it calls Query itself, unmodified, with a limit large
// enough to return the whole candidate list, rather than reimplementing
// Query's filtering (currentMemoryItem, tag filters, minMatchedTerms,
// the vault-fact title rescue, isDistilledRule, the privacy cut) or its
// BM25 math: a probe that scores differently from production measures
// nothing (see agent-estate#1255's brief). Only NewBM25Scorer's already-
// exported idf table is consulted directly, in-package, purely to label
// each of Query's own already-decided MatchedTerms as rare or not --
// nothing here changes which candidates exist or how they were scored.
//
// Opt-in on ESTATE_RANKING_EXPERIMENT_INDEX so a bare `go test ./...`
// never depends on a private index CI does not have -- it SKIPS, never
// passes or fails, when the variable is unset. Deliberately never reads
// or writes ESTATE_KNOWLEDGE_INDEX or anything under --allow-shared-write:
// the only index this test ever opens is the path this env var names.
func TestRareCeilingExperiment(t *testing.T) {
	idxPath := os.Getenv("ESTATE_RANKING_EXPERIMENT_INDEX")
	if idxPath == "" {
		t.Skip("ESTATE_RANKING_EXPERIMENT_INDEX not set -- opt-in ceiling measurement only, see agent-estate#1255")
	}

	res, err := Read(idxPath)
	if err != nil {
		t.Fatalf("could not read experiment index %s: %v", idxPath, err)
	}
	scorer := NewBM25Scorer(res.Items)

	cases, err := goldenset.LoadRetrievalBaseline()
	if err != nil {
		t.Fatalf("could not load retrieval baseline cases: %v", err)
	}

	type cand struct {
		id      string
		matched []string
		hit     bool
	}
	type caseRun struct {
		c          goldenset.Case
		candidates []cand // Query's own real, uncapped order
		origRank   int    // 1-based rank of the expected identifier; 0 if not present at all
	}

	bigLimit := len(res.Items) + 1
	var runs []caseRun
	present, absent := 0, 0
	var absentIDs []string
	maxMatched := 0
	idfSeen := map[float64]bool{}

	for _, c := range cases {
		out := Query(idxPath, c.Question, bigLimit, true) // always --private, unscoped -- this stratum's own contract
		var cds []cand
		rank := 0
		for i, m := range out.Matches {
			isHit := c.ExpectedIdentifier != "" && strings.HasSuffix(m.Permalink, c.ExpectedIdentifier)
			if isHit && rank == 0 {
				rank = i + 1
			}
			cds = append(cds, cand{id: m.ID, matched: m.MatchedTerms, hit: isHit})
			if len(m.MatchedTerms) > maxMatched {
				maxMatched = len(m.MatchedTerms)
			}
			for _, term := range m.MatchedTerms {
				idfSeen[scorer.idf[term]] = true
			}
		}
		if rank > 0 {
			present++
		} else {
			absent++
			absentIDs = append(absentIDs, c.ID)
		}
		runs = append(runs, caseRun{c: c, candidates: cds, origRank: rank})
	}

	origHits, origFails := 0, 0
	for _, r := range runs {
		if r.origRank >= 1 && r.origRank <= 10 {
			origHits++
		} else {
			origFails++
		}
	}
	t.Logf("coverage: %d/%d expected identifiers present somewhere in Query's own real candidate list (score>0, cleared minMatchedTerms), %d absent from that pipeline entirely regardless of rank: %v -- NOTE this is stricter than raw index membership (#1255's own report found 0/26 raw coverage gaps): a rerank of Query's existing candidates can never reach an item that never entered them", present, len(runs), absent, absentIDs)
	t.Logf("reproduced baseline: %d/%d top-10 hits, %d/%d ranking failures (present, not top-10)", origHits, len(runs), origFails, len(runs))

	var breakpoints []float64
	for v := range idfSeen {
		breakpoints = append(breakpoints, v)
	}
	sort.Float64s(breakpoints)
	// A breakpoint strictly above every observed idf value is the same as
	// "no term ever counts as rare" -- included once, explicitly, rather
	// than letting the sweep implicitly stop one value short of it.
	breakpoints = append(breakpoints, breakpoints[len(breakpoints)-1]+1)

	rareCount := func(matched []string, breakpoint float64) int {
		n := 0
		for _, term := range matched {
			if scorer.idf[term] >= breakpoint {
				n++
			}
		}
		return n
	}

	rerank := func(cands []cand, breakpoint float64, minCount int) int {
		var groupA, groupB []cand
		for _, c := range cands {
			if rareCount(c.matched, breakpoint) >= minCount {
				groupA = append(groupA, c)
			} else {
				groupB = append(groupB, c)
			}
		}
		merged := append(groupA, groupB...)
		for i, c := range merged {
			if c.hit {
				return i + 1
			}
		}
		return 0
	}

	type setting struct {
		breakpoint float64
		minCount   int
		fixed      int
		regressed  int
		fixedIDs   []string
		regIDs     []string
	}
	var best *setting
	var sweep []setting

	for count := 1; count <= maxMatched; count++ {
		for _, bp := range breakpoints {
			s := setting{breakpoint: bp, minCount: count}
			for _, r := range runs {
				newRank := rerank(r.candidates, bp, count)
				newHit := newRank >= 1 && newRank <= 10
				origHit := r.origRank >= 1 && r.origRank <= 10
				switch {
				case !origHit && newHit:
					s.fixed++
					s.fixedIDs = append(s.fixedIDs, r.c.ID)
				case origHit && !newHit:
					s.regressed++
					s.regIDs = append(s.regIDs, r.c.ID)
				}
			}
			sweep = append(sweep, s)
			if best == nil || s.fixed > best.fixed ||
				(s.fixed == best.fixed && s.regressed < best.regressed) {
				b := s
				best = &b
			}
		}
	}

	t.Logf("swept %d (breakpoint, count) settings, count in [1,%d], %d distinct idf breakpoints", len(sweep), maxMatched, len(breakpoints))
	for _, s := range sweep {
		if s.fixed > 0 {
			t.Logf("  breakpoint=%.4f count=%d -> fixed=%d %v regressed=%d %v", s.breakpoint, s.minCount, s.fixed, s.fixedIDs, s.regressed, s.regIDs)
		}
	}

	if best == nil || best.fixed == 0 {
		t.Logf("CEILING: 0/%d -- no (breakpoint, count) setting in the sweep moves any ranking failure into the top 10", origFails)
		return
	}
	t.Logf("CEILING: %d/%d ranking failures fixable, best at breakpoint=%.4f count=%d, %d HIT->MISS regression(s) at that setting: %v; fixed cases: %v",
		best.fixed, origFails, best.breakpoint, best.minCount, best.regressed, best.regIDs, best.fixedIDs)
}
