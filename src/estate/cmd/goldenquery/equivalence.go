package main

// Equivalent-answer credit for the retrieval-baseline stratum
// (agent-estate#1318).
//
// The baseline used to score a case as a hit only when a returned item's
// permalink carried the fixture author's chosen identifier. Six of its 26
// cases returned the entire designated sentence, verbatim, under a
// different id -- a vault fact whose body IS that sentence -- and were
// scored as misses. The number gated nothing (the -baseline flag bypasses
// every ratchet), but it priced decisions: retrieval work was scoped
// against "17 ranking failures" when the top ten genuinely lacked the
// answer in 11.
//
// This file credits an equivalent answer under a mechanical, attributable
// rule, and refuses a passing mention by construction:
//
//   a returned item is an equivalent answer for a case only if the
//   designated text carries at least equivalenceTermFloor distinct content
//   terms (else the case is reported as not assessed), the returned item's
//   own text contains the designated item's own full text, AND one of
//     (a) OPENS: the returned item's own first statement -- its heading,
//         or its first body line when the heading is the truncated form --
//         IS the designated sentence, whole, so the document is the
//         statement rather than a document that mentions or extends it.
//         A blockquote line is never a first statement; a longer sentence
//         that merely begins with the designated words is not it; or
//     (b) CITES: the returned item's body names the designated item's own
//         id -- an explicit backlink to the source.
//
// A document that quotes the sentence -- mid-body, or as an opening
// blockquote it then contradicts -- without citing its source satisfies
// neither and is not credited, whatever it says. Nothing here widens past
// verbatim containment; a paraphrase is refused even when a reader would
// accept it, because a reader's acceptance cannot live in a test. Every
// credit reports the rank, the returned id, the designated id and which
// clause held, so no hit is untraceable.
//
// Named limit: a document that states the designated sentence as its own
// first statement and then discusses something else IS credited -- it
// states the rule as its own. And "cites" credits a quoted sentence that
// carries an explicit backlink even if the surrounding document goes on to
// supersede it: the oracle measures whether the designated text was
// returned and traceable, which is also all the strict oracle measures
// about the designated item itself.
//
// The strict identifier score is still computed and printed first; this
// adds a second number and the delta, never replaces the first.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
)

// equivalentCredit is one case credited under an id other than the one its
// fixture names.
type equivalentCredit struct {
	CaseID   string
	Rank     int
	ItemID   string // the returned item that carried the answer
	TargetID string // the designated item the fixture names
	Clause   string // "opens" or "cites"
}

// markdownNoise is the decoration normalizeText strips: heading markers,
// emphasis, code spans, link brackets. A blockquote marker (`>`) is NOT in
// this class, on purpose: a heading marker decorates the document's own
// words, while a blockquote marker is the one piece of markdown whose whole
// job is to say "these are someone else's words". Stripping it turned a
// document that opens by quoting the designated sentence and then
// contradicting it into an "opens" credit (review on #1407). Containment
// still sees a quoted sentence -- the marker precedes it, the words are
// unchanged -- so the cites clause can credit a quote that carries an
// explicit backlink; the opens clause never looks at a quoted line at all
// (see firstStatement).
var markdownNoise = regexp.MustCompile("[#*`_\\[\\]()]+")
var whitespaceRun = regexp.MustCompile(`\s+`)
var contentTerm = regexp.MustCompile(`[a-z0-9]+`)

// equivalenceTermFloor is the fewest distinct content terms (letters or
// digits, longer than three characters, the same shape internal/corpus's
// words() counts) a designated text must carry before equivalence is
// assessed at all. It is the retriever's own admission floor for a corpus
// candidate (agent-estate#1134: min(3, the question's own distinct terms)),
// reused rather than picked: a sentence the index would not admit as a
// match on its own terms is too generic to discriminate an equivalent
// answer either. It is not fitted to the fixture -- the shortest of today's
// 26 designated texts carries 5 (rb-15, 61 characters), measured 2026-09-11
// -- and a case below it is reported as not assessed, never as a miss and
// never as a credit.
const equivalenceTermFloor = 3

// normalizeText lowers case and strips markdown decoration and whitespace
// runs so a heading ("# Sentence") and a body line ("Sentence") compare
// equal. It never stems or drops words: containment stays verbatim.
func normalizeText(s string) string {
	s = strings.ToLower(s)
	s = markdownNoise.ReplaceAllString(s, " ")
	s = whitespaceRun.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// distinctContentTerms counts the distinct terms longer than three
// characters in already-normalised text.
func distinctContentTerms(normalised string) int {
	seen := map[string]bool{}
	for _, t := range contentTerm.FindAllString(normalised, -1) {
		if len(t) > 3 {
			seen[t] = true
		}
	}
	return len(seen)
}

// recordText is the text a returned item owns: its Tier2 body (a vault
// fact's or a corpus item's full statement), else its Tier1. Tier1 is a
// derived one-line summary and is never treated as the document's own
// opening.
func recordText(it knowledge.Item) string {
	if strings.TrimSpace(it.Tier2) != "" {
		return it.Tier2
	}
	return it.Tier1
}

// designatedText is the text the fixture's target item owns, normalised.
func designatedText(target knowledge.Item) string {
	return normalizeText(recordText(target))
}

// firstStatement returns the candidate's own first statement, normalised:
// its first non-blank line that is not a blockquote, and -- because a vault
// fact's heading is the sentence truncated with "..." while its first body
// line is the sentence in full -- the line after that when the first is a
// heading. A blockquote line is skipped, never stripped of its marker: a
// quoted sentence is not the document's statement, wherever it sits.
func firstStatement(it knowledge.Item) []string {
	var out []string
	for _, line := range strings.Split(recordText(it), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, ">") {
			continue
		}
		out = append(out, normalizeText(s))
		if len(out) == 1 && !strings.HasPrefix(s, "#") {
			break
		}
		if len(out) == 2 {
			break
		}
	}
	return out
}

// shortID is the trailing segment of a permalink such as
// "corpus:item:it-4a85802605f09d0d" -- the form a citing document names.
func shortID(identifier string) string {
	if i := strings.LastIndex(identifier, ":"); i >= 0 {
		return identifier[i+1:]
	}
	return identifier
}

// designatedTooGeneric reports whether target's text carries fewer distinct
// content terms than equivalenceTermFloor -- in which case equivalence is
// not assessed for the case, and the caller says so.
func designatedTooGeneric(target knowledge.Item) (terms int, tooGeneric bool) {
	terms = distinctContentTerms(designatedText(target))
	return terms, terms < equivalenceTermFloor
}

// equivalentAnswer reports whether candidate is an equivalent answer for
// target under the rule in this file's comment, and which clause held.
// Callers check designatedTooGeneric first; this function refuses a
// too-generic target as well so it cannot be credited by any path.
//
// "opens" is equality, not prefix: the candidate's own first statement (its
// heading, or its first body line when the heading is the truncated form)
// must BE the designated sentence, whole. A document whose first statement
// merely starts with the designated words and continues ("Never commit
// secrets to the public repo, ever.") is not stating that sentence, and a
// short designated sentence cannot ride on a longer one that happens to
// begin the same way. A blockquote line is never a first statement.
func equivalentAnswer(target, candidate knowledge.Item, targetIdentifier string) (clause string, ok bool) {
	ref := designatedText(target)
	if ref == "" {
		return "", false
	}
	if _, tooGeneric := designatedTooGeneric(target); tooGeneric {
		return "", false
	}
	text := normalizeText(recordText(candidate))
	if !strings.Contains(text, ref) {
		return "", false
	}
	for _, statement := range firstStatement(candidate) {
		if statement == ref {
			return "opens", true
		}
	}
	if id := shortID(targetIdentifier); id != "" && strings.Contains(candidate.Tier2, id) {
		return "cites", true
	}
	return "", false
}

// skippedCase is a strict miss whose designated text was too generic for
// equivalence to be assessed -- reported, never folded into either score.
type skippedCase struct {
	CaseID string
	Terms  int
}

func skippedLine(s skippedCase) string {
	return fmt.Sprintf("[EQUIV-NOT-ASSESSED] %s -- designated text carries %d distinct content terms, below the %d-term floor; equivalence not assessed (still a strict miss)", s.CaseID, s.Terms, equivalenceTermFloor)
}

// indexItems loads the compiled index the estate binary itself is reading
// (the same ESTATE_KNOWLEDGE_INDEX resolution) and keys it by item id.
func indexItems() (map[string]knowledge.Item, string, error) {
	path, err := knowledge.DefaultOutputPath()
	if err != nil {
		return nil, "", err
	}
	res, err := knowledge.Read(path)
	if err != nil {
		return nil, path, err
	}
	m := make(map[string]knowledge.Item, len(res.Items))
	for _, it := range res.Items {
		m[it.ID] = it
	}
	return m, path, nil
}

// findByIdentifier returns the index item whose permalink carries
// identifier as a suffix -- the same match the strict oracle uses.
func findByIdentifier(items map[string]knowledge.Item, identifier string) (knowledge.Item, bool) {
	for _, it := range items {
		if strings.HasSuffix(it.Permalink, identifier) {
			return it, true
		}
	}
	return knowledge.Item{}, false
}

// creditEquivalents scans every strict miss's top ten for an equivalent
// answer. It never touches a strict hit and never changes a rank; it
// returns the credits so the caller can print them and the delta, and the
// misses whose designated text was too generic to assess, so the caller
// can print those as a third, distinct outcome.
func creditEquivalents(results []naturalResult, items map[string]knowledge.Item) ([]equivalentCredit, []skippedCase) {
	var credits []equivalentCredit
	var skipped []skippedCase
	for _, r := range results {
		if r.rank >= 1 && r.rank <= 10 {
			continue
		}
		target, found := findByIdentifier(items, r.c.ExpectedIdentifier)
		if !found {
			continue
		}
		if terms, tooGeneric := designatedTooGeneric(target); tooGeneric {
			skipped = append(skipped, skippedCase{CaseID: r.c.ID, Terms: terms})
			continue
		}
		for i, m := range r.matches {
			if i >= 10 {
				break
			}
			cand, ok := items[m.ID]
			if !ok {
				continue
			}
			if clause, ok := equivalentAnswer(target, cand, r.c.ExpectedIdentifier); ok {
				credits = append(credits, equivalentCredit{CaseID: r.c.ID, Rank: i + 1, ItemID: m.ID, TargetID: shortID(r.c.ExpectedIdentifier), Clause: clause})
				break
			}
		}
	}
	return credits, skipped
}

func describeClause(clause string) string {
	switch clause {
	case "opens":
		return "the returned item's own first statement is the designated sentence, whole (it is the statement, not a mention of it)"
	case "cites":
		return "the returned item contains the designated sentence and cites the designated item's id inline"
	}
	return clause
}

// equivalenceLine is the attributable one-line record printed per credit.
func equivalenceLine(e equivalentCredit) string {
	return fmt.Sprintf("[EQUIV] %s -- credited at rank %d via %s, not the fixture's %s: %s", e.CaseID, e.Rank, e.ItemID, e.TargetID, describeClause(e.Clause))
}
