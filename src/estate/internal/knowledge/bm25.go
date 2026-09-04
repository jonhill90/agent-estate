package knowledge

import (
	"math"
	"strings"
)

// Scorer is query.go's matching-primitive seam (agent-estate#1054): Query
// scores every candidate item through this interface, never a hardcoded
// function, so a later semantic scorer can be built and run side by side
// against the same golden set and diffed against this one, instead of
// requiring a rewrite of Query itself -- this repo's own convention that
// every seam is a func type or a small interface (see CLAUDE.md's
// "Adapter discipline"). Score returns a relevance figure meaningful only
// for RANKING items scored by the SAME Scorer instance against the SAME
// question -- never a probability, and never comparable across two
// different Scorer implementations or two different corpora. matched
// names which distinct terms actually contributed, so MatchedTerms keeps
// naming the ranking basis the way it always did; a term absent from
// matched contributed nothing to score, even if it appears elsewhere in
// terms.
type Scorer interface {
	Score(it Item, terms []string) (score float64, matched []string)
}

// bm25K1 and bm25B are the two standard Okapi BM25 tuning constants.
// 1.2 and 0.75 are the values Robertson's own papers and effectively
// every reference implementation since have shipped as defaults; nothing
// measured about this index's text justifies hand-tuning them, and doing
// so without a measured reason would just be a second hard-coded floor
// with extra steps.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// tier1FieldWeight and tier2FieldWeight carry #1043's already-measured
// 3:1 Tier1/tag-over-Tier2 result forward as BM25 field weights, per
// #1054's explicit instruction: express the prior weighting as a BM25
// field weight rather than discard it. A term found in Tier1 or either
// tag field counts tier1FieldWeight times toward this item's term
// frequency; the same term found only in Tier2 counts tier2FieldWeight
// times -- the two fields are combined into one document (BM25F's
// single-document simplification) rather than scored as two independent
// BM25 results and summed, so one avgdl and one length normalisation
// covers both fields consistently.
const (
	tier1FieldWeight = 3.0
	tier2FieldWeight = 1.0
)

// tier1SearchableText and tier2SearchableText are searchableText's own
// two fields, kept separate here (rather than flattened into one string)
// because BM25 field weighting needs to know which field a term came
// from -- information a single joined string throws away.
func tier1SearchableText(it Item) string {
	return strings.ToLower(strings.Join(append([]string{it.Tier1}, append(it.StructuralTags, it.SynapticTags...)...), " \x1f "))
}

func tier2SearchableText(it Item) string {
	return strings.ToLower(it.Tier2)
}

// weightedTermFreqs returns one item's stemmed-term -> weighted term
// frequency map and its own weighted length, both built from the same
// field weights so the length a document's frequencies are normalised
// against (BM25's length-normalisation term) reflects the same weighting
// as the frequencies themselves.
func weightedTermFreqs(it Item) (freqs map[string]float64, length float64) {
	freqs = map[string]float64{}
	for term, c := range fieldTermCounts(tier1SearchableText(it)) {
		freqs[term] += tier1FieldWeight * float64(c)
	}
	for term, c := range fieldTermCounts(tier2SearchableText(it)) {
		freqs[term] += tier2FieldWeight * float64(c)
	}
	for _, tf := range freqs {
		length += tf
	}
	return freqs, length
}

// bm25Doc is one item's precomputed statistics -- built once per corpus
// in NewBM25Scorer, read (never recomputed) on every Score call so
// scoring N questions against the same index does not re-tokenize every
// item N times.
type bm25Doc struct {
	freqs  map[string]float64
	length float64
}

// BM25Scorer is a Scorer built once over a concrete corpus. BM25 needs
// corpus-wide statistics -- document frequency (how many items carry a
// term, which sets idf) and average document length (avgdl, which sets
// how harshly a long document's raw counts are discounted) -- neither
// knowable by looking at one item alone, so NewBM25Scorer computes both
// up front.
type BM25Scorer struct {
	docs  map[string]bm25Doc
	idf   map[string]float64
	avgdl float64
}

// NewBM25Scorer builds a BM25Scorer over items -- always the full
// compiled index being searched, so idf and avgdl reflect the actual
// corpus a question is run against rather than some other one (in
// particular: the same regardless of includePrivate, so a term's idf
// does not silently shift between a default-mode and a --private call
// against the same index).
func NewBM25Scorer(items []Item) *BM25Scorer {
	docs := make(map[string]bm25Doc, len(items))
	df := map[string]int{}
	var totalLen float64
	for _, it := range items {
		freqs, length := weightedTermFreqs(it)
		docs[it.ID] = bm25Doc{freqs: freqs, length: length}
		totalLen += length
		for term := range freqs {
			df[term]++
		}
	}
	n := float64(len(items))
	idf := make(map[string]float64, len(df))
	for term, d := range df {
		// Robertson/Sparck-Jones idf, floored at >=0 by the +1 inside the
		// log so a term appearing in every single item still contributes
		// (never negative, never divides the score below zero) rather
		// than needing a separate exclusion rule for the common-term
		// case the old hard floor existed to guard against -- weighting
		// is the whole point of #1054's replacement.
		idf[term] = math.Log(1 + (n-float64(d)+0.5)/(float64(d)+0.5))
	}
	avgdl := 1.0
	if n > 0 && totalLen > 0 {
		avgdl = totalLen / n
	}
	return &BM25Scorer{docs: docs, idf: idf, avgdl: avgdl}
}

// Score implements Scorer. terms is already lowercased, stopword-
// filtered and stemmed (queryTerms's own contract) -- Score never
// re-tokenizes a question, only looks up each term's precomputed
// document frequency against it.
func (s *BM25Scorer) Score(it Item, terms []string) (float64, []string) {
	doc, ok := s.docs[it.ID]
	if !ok {
		// it was not part of the corpus this scorer was built over --
		// score it anyway from its own text so Score stays correct for
		// any caller, just without this corpus's idf benefiting from
		// knowing about it (it contributes no document frequency of its
		// own either, which is the same trade any unseen document makes
		// against a fixed corpus statistic).
		doc.freqs, doc.length = weightedTermFreqs(it)
	}
	var score float64
	var matched []string
	for _, t := range terms {
		tf := doc.freqs[t]
		if tf <= 0 {
			continue
		}
		matched = append(matched, t)
		score += s.idf[t] * tf * (bm25K1 + 1) / (tf + bm25K1*(1-bm25B+bm25B*doc.length/s.avgdl))
	}
	return score, matched
}
