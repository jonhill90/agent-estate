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
//   a returned item is an equivalent answer for a case only if its text
//   contains the designated item's own full text AND one of
//     (a) OPENS: the returned item's text begins with that text -- its
//         title or first line IS the designated sentence, so the document
//         is the statement rather than a document that mentions it; or
//     (b) CITES: the returned item's body names the designated item's own
//         id -- an explicit backlink to the source.
//
// A document that quotes the sentence mid-body without citing its source
// satisfies neither and is not credited, whatever it says. Nothing here
// widens past verbatim containment; a paraphrase is refused even when a
// reader would accept it, because a reader's acceptance cannot live in a
// test. Every credit reports the rank, the returned id, the designated id
// and which clause held, so no hit is untraceable.
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

var markdownNoise = regexp.MustCompile("[#*`_>\\[\\]()]+")
var whitespaceRun = regexp.MustCompile(`\s+`)

// normalizeText lowers case and strips markdown decoration and whitespace
// runs so a heading ("# Sentence") and a body line ("Sentence") compare
// equal. It never stems or drops words: containment stays verbatim.
func normalizeText(s string) string {
	s = strings.ToLower(s)
	s = markdownNoise.ReplaceAllString(s, " ")
	s = whitespaceRun.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// itemText is the text a returned item exposes: its one-line Tier1 and its
// Tier2 body, in that order, so "opens with" means the title or first
// line.
func itemText(it knowledge.Item) string {
	return normalizeText(it.Tier1 + " " + it.Tier2)
}

// designatedText is the text the fixture's target item owns: its Tier2 body
// where it has one (a vault fact's or a corpus item's full statement),
// else its Tier1.
func designatedText(target knowledge.Item) string {
	if strings.TrimSpace(target.Tier2) != "" {
		return normalizeText(target.Tier2)
	}
	return normalizeText(target.Tier1)
}

// shortID is the trailing segment of a permalink such as
// "corpus:item:it-4a85802605f09d0d" -- the form a citing document names.
func shortID(identifier string) string {
	if i := strings.LastIndex(identifier, ":"); i >= 0 {
		return identifier[i+1:]
	}
	return identifier
}

// equivalentAnswer reports whether candidate is an equivalent answer for
// target under the rule in this file's comment, and which clause held.
func equivalentAnswer(target, candidate knowledge.Item, targetIdentifier string) (clause string, ok bool) {
	ref := designatedText(target)
	if ref == "" {
		return "", false
	}
	text := itemText(candidate)
	if !strings.Contains(text, ref) {
		return "", false
	}
	if strings.HasPrefix(text, ref) || strings.HasPrefix(normalizeText(candidate.Tier2), ref) {
		return "opens", true
	}
	if id := shortID(targetIdentifier); id != "" && strings.Contains(candidate.Tier2, id) {
		return "cites", true
	}
	return "", false
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
// returns the credits so the caller can print them and the delta.
func creditEquivalents(results []naturalResult, items map[string]knowledge.Item) []equivalentCredit {
	var credits []equivalentCredit
	for _, r := range results {
		if r.rank >= 1 && r.rank <= 10 {
			continue
		}
		target, found := findByIdentifier(items, r.c.ExpectedIdentifier)
		if !found {
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
	return credits
}

func describeClause(clause string) string {
	switch clause {
	case "opens":
		return "the returned item's text opens with the designated sentence (it is the statement, not a mention of it)"
	case "cites":
		return "the returned item contains the designated sentence and cites the designated item's id inline"
	}
	return clause
}

// equivalenceLine is the attributable one-line record printed per credit.
func equivalenceLine(e equivalentCredit) string {
	return fmt.Sprintf("[EQUIV] %s -- credited at rank %d via %s, not the fixture's %s: %s", e.CaseID, e.Rank, e.ItemID, e.TargetID, describeClause(e.Clause))
}
