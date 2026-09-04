// agent-estate#1115: the github-stars stratum (agent-estate#1111) was
// saturated at 8/8 because every one of its questions reused a large
// fraction of its own target's description verbatim -- a stratum where
// every case lands at rank 1 has no headroom left to detect a
// regression, and nothing in the fixture said so. This file is the
// measurement primitive that answers "how much of this question's own
// vocabulary was copied from the answer, rather than derived from a
// caller's actual need" -- content-word overlap between a case's
// Question and its own goldenset.Case.TargetText, verbatim, no stemming.
// Kept deliberately simple (no stemming, no synonym table): the question
// this measures is "did the fixture author copy words", not "is this a
// good retrieval match" -- BM25/stemming quality is bm25.go's job, not
// this runner's.
package main

import (
	"regexp"
	"strings"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

var contentWordPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9']*`)

// stopWords is a short, self-contained function-word list -- cmd/goldenquery
// has no compile-time dependency on internal/knowledge's own stemmer/stopword
// table (see this package's top-of-file doc comment for why), so this list is
// separate and deliberately not exhaustive. A stopword that slips through
// only inflates a case's own overlap percentage; it never hides a real
// content-word match, so under-filtering here is the safe failure direction.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "to": true,
	"of": true, "in": true, "on": true, "for": true, "and": true, "or": true,
	"with": true, "as": true, "at": true, "by": true, "from": true,
	"that": true, "this": true, "these": true, "those": true, "it": true,
	"its": true, "i": true, "my": true, "you": true, "your": true, "we": true,
	"our": true, "so": true, "than": true, "then": true, "not": true,
	"no": true, "do": true, "does": true, "did": true, "have": true,
	"has": true, "had": true, "can": true, "could": true, "would": true,
	"should": true, "will": true, "shall": true, "may": true, "might": true,
	"must": true, "about": true, "into": true, "over": true, "under": true,
	"between": true, "which": true, "who": true, "whom": true, "what": true,
	"when": true, "where": true, "why": true, "how": true, "there": true,
	"me": true, "someone": true, "somebody": true, "some": true, "any": true,
}

// contentWords lowercases s, extracts alphanumeric tokens, and drops
// stopWords and tokens of length <= 2 -- the same shape of filtering as
// internal/knowledge's own BM25 term extraction, but a separate,
// independent implementation (see this file's own package comment).
func contentWords(s string) []string {
	var out []string
	for _, w := range contentWordPattern.FindAllString(strings.ToLower(s), -1) {
		if len(w) <= 2 || stopWords[w] {
			continue
		}
		out = append(out, w)
	}
	return out
}

// overlapFraction reports what fraction of question's own content words
// appear verbatim in targetText. ok is false when targetText is empty (the
// case has not wired goldenset.Case.TargetText -- see its own doc comment)
// or question has no content words at all; a caller must check ok before
// trusting frac, so a stratum with no TargetText wired reports "not
// measured" rather than a false 0%.
func overlapFraction(question, targetText string) (frac float64, ok bool) {
	if targetText == "" {
		return 0, false
	}
	qw := contentWords(question)
	if len(qw) == 0 {
		return 0, false
	}
	tw := make(map[string]bool, len(qw))
	for _, w := range contentWords(targetText) {
		tw[w] = true
	}
	hits := 0
	for _, w := range qw {
		if tw[w] {
			hits++
		}
	}
	return float64(hits) / float64(len(qw)), true
}

// meanOverlap reports the mean overlapFraction across every case in cases
// that carries a TargetText. measured is how many of total actually
// contributed to mean -- a stratum where no case carries TargetText yet
// (agent-estate#1115 wired this for the github-stars stratum only so far)
// returns measured=0, mean=0, and the caller must print that as "not
// measured", never as a 0% overlap score.
func meanOverlap(cases []goldenset.Case) (mean float64, measured, total int) {
	total = len(cases)
	var sum float64
	for _, c := range cases {
		if frac, ok := overlapFraction(c.Question, c.TargetText); ok {
			sum += frac
			measured++
		}
	}
	if measured == 0 {
		return 0, 0, total
	}
	return sum / float64(measured), measured, total
}
