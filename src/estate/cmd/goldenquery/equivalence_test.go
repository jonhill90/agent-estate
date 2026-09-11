package main

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/knowledge"
	"github.com/jonhill90/agent-estate/estate/internal/knowledge/goldenset"
)

const designated = "Deploys run through the CI pipeline, never by SSHing to the VPS from Jon's Mac."

func target() knowledge.Item {
	return knowledge.Item{ID: "it-844b5c8eef4e9a48", Permalink: "corpus:item:it-844b5c8eef4e9a48", Tier1: "deploy_path=ci_pipeline", Tier2: designated}
}

// (a) OPENS: a vault fact whose heading and first line ARE the designated
// sentence, followed by its own justification -- the shape five of the
// six real credits on #1318 have.
func TestEquivalentWhenReturnedItemOpensWithTheDesignatedSentence(t *testing.T) {
	cand := knowledge.Item{ID: "it-60ae535a15dde4f7", Tier1: designated, Tier2: "# " + designated + "\n\n" + designated + "\n\n## Why this is a rule\n\nSelected by reading every recorded statement about deploys."}
	clause, ok := equivalentAnswer(target(), cand, "corpus:item:it-844b5c8eef4e9a48")
	if !ok || clause != "opens" {
		t.Fatalf("want opens/true, got %q/%v", clause, ok)
	}
}

// (b) CITES: a published list whose bullet carries the sentence followed by
// the designated item's own id in backticks -- rb-18's real shape.
func TestEquivalentWhenReturnedItemCitesTheDesignatedID(t *testing.T) {
	cand := knowledge.Item{ID: "it-7c227a62f286f25a", Tier1: "Published quota parameters", Tier2: "- Wind down at the end of each session block. `it-7be987e00122fc09`\n- " + designated + " `it-844b5c8eef4e9a48`\n- Never hold work back."}
	clause, ok := equivalentAnswer(target(), cand, "corpus:item:it-844b5c8eef4e9a48")
	if !ok || clause != "cites" {
		t.Fatalf("want cites/true, got %q/%v", clause, ok)
	}
}

// The objection the mechanism must defeat: a document that merely quotes
// the rule mid-body, without owning it or citing it, is not credited even
// though it contains the sentence verbatim.
func TestPassingMentionIsNotCredited(t *testing.T) {
	cand := knowledge.Item{ID: "it-mention", Tier1: "Retrospective on the August deploy incident", Tier2: "The lane pushed from a laptop again. As the standing rule says, " + designated + " That was the third time this month, and the retrospective below lists the others."}
	if clause, ok := equivalentAnswer(target(), cand, "corpus:item:it-844b5c8eef4e9a48"); ok {
		t.Fatalf("a passing mention was credited under clause %q", clause)
	}
}

// A paraphrase, however good, is refused: this oracle is verbatim
// containment, never a reader's judgement.
func TestParaphraseIsNotCredited(t *testing.T) {
	cand := knowledge.Item{ID: "it-para", Tier1: "Deploy only via CI", Tier2: "# Deploy only via CI\n\nNever ship by SSHing from a laptop; the pipeline is the only deploy path."}
	if _, ok := equivalentAnswer(target(), cand, "corpus:item:it-844b5c8eef4e9a48"); ok {
		t.Fatal("a paraphrase was credited")
	}
}

// Review on #1407, finding 1: a document that OPENS by quoting the designated
// sentence in a blockquote and then contradicts it is not an opening. The
// reviewer's exact candidate. Failed against 7bf1d55 (credited as "opens").
func TestBlockquoteOpeningIsNotAnOpening(t *testing.T) {
	cand := knowledge.Item{ID: "it-superseded", Tier2: "> " + designated + "\n\nThat was the old policy. It no longer holds: deploys now also allow a break-glass SSH path with sign-off."}
	if clause, ok := equivalentAnswer(target(), cand, "corpus:item:it-844b5c8eef4e9a48"); ok {
		t.Fatalf("a quoted-then-superseded opening was credited under clause %q", clause)
	}
	// The same quote WITH an explicit backlink is a cites credit: the
	// designated text was returned and is traceable to its source, which
	// is all either oracle measures (see the named limit in equivalence.go).
	cand.Tier2 = "> " + designated + " `it-844b5c8eef4e9a48`\n\nThat was the old policy."
	if clause, ok := equivalentAnswer(target(), cand, "corpus:item:it-844b5c8eef4e9a48"); !ok || clause != "cites" {
		t.Fatalf("a quoted sentence with an explicit backlink must be a cites credit, got %q/%v", clause, ok)
	}
}

// Review on #1407, finding 2: a short designated sentence must not ride on
// a longer opening that merely begins with the same words. "opens" is
// equality of the whole first statement, not a prefix. Failed against
// 7bf1d55 (prefix match credited it).
func TestOpensRequiresTheWholeFirstStatementNotAPrefix(t *testing.T) {
	short := knowledge.Item{ID: "it-short", Permalink: "corpus:item:it-short", Tier2: "Never commit secrets."}
	longer := knowledge.Item{ID: "it-longer", Tier2: "Never commit secrets to the public repo, ever. This document is about rotating SSH deploy keys quarterly."}
	if clause, ok := equivalentAnswer(short, longer, "corpus:item:it-short"); ok {
		t.Fatalf("a longer opening that merely begins with the designated words was credited under %q", clause)
	}
	// One paragraph whose first line runs on past the sentence is likewise
	// not the sentence -- the reviewer's exact second example.
	unrelated := knowledge.Item{ID: "it-unrelated", Tier2: "Never commit secrets. This document is actually about rotating SSH deploy keys quarterly and has nothing to do with the credential-store write policy the fixture asked about."}
	if clause, ok := equivalentAnswer(short, unrelated, "corpus:item:it-short"); ok {
		t.Fatalf("a first line that continues past the designated sentence was credited under %q", clause)
	}
	// Named limit, pinned as accepted: a document whose OWN first statement
	// is exactly the designated sentence, followed by anything, states it.
	states := knowledge.Item{ID: "it-states", Tier2: "Never commit secrets.\n\nThis document goes on to discuss rotating SSH deploy keys quarterly."}
	if clause, ok := equivalentAnswer(short, states, "corpus:item:it-short"); !ok || clause != "opens" {
		t.Fatalf("a document whose own first statement is the sentence must be credited as opens, got %q/%v", clause, ok)
	}
}

// Below the retriever's own three-term admission floor the designated text
// is too generic to judge: not credited by any path, and reported as not
// assessed rather than as a miss. Failed against 7bf1d55 (credited).
func TestGenericDesignatedTextIsNotAssessed(t *testing.T) {
	generic := knowledge.Item{ID: "it-generic", Permalink: "corpus:item:it-generic", Tier2: "Keep going."}
	cand := knowledge.Item{ID: "it-cand", Tier2: "Keep going.\n\nA note about something else entirely."}
	if terms, tooGeneric := designatedTooGeneric(generic); !tooGeneric || terms != 2 {
		t.Fatalf("want too generic with 2 terms, got %d/%v", terms, tooGeneric)
	}
	if clause, ok := equivalentAnswer(generic, cand, "corpus:item:it-generic"); ok {
		t.Fatalf("a too-generic designated text was credited under %q", clause)
	}
	items := map[string]knowledge.Item{"it-generic": generic, "it-cand": cand}
	r := naturalResult{c: goldenset.Case{ID: "rb-g", ExpectedIdentifier: "corpus:item:it-generic"}, matches: []parsedMatch{{ID: "it-cand"}}}
	credits, skipped := creditEquivalents([]naturalResult{r}, items)
	if len(credits) != 0 || len(skipped) != 1 || skipped[0].CaseID != "rb-g" || skipped[0].Terms != 2 {
		t.Fatalf("want no credit and one skipped case, got credits=%+v skipped=%+v", credits, skipped)
	}
	if !strings.Contains(skippedLine(skipped[0]), "[EQUIV-NOT-ASSESSED] rb-g") {
		t.Fatalf("skipped line = %s", skippedLine(skipped[0]))
	}
	// The floor is the retriever's, not the fixture's: three terms pass.
	three := knowledge.Item{ID: "it-three", Permalink: "corpus:item:it-three", Tier2: "Never commit secrets."}
	if _, tooGeneric := designatedTooGeneric(three); tooGeneric {
		t.Fatal("three distinct content terms must clear the floor")
	}
}

// Normalisation tolerates markdown and case but nothing else.
func TestNormalisationIsDecorationAndCaseOnly(t *testing.T) {
	if normalizeText("# **Deploys** run   through the `CI` pipeline") != "deploys run through the ci pipeline" {
		t.Fatalf("normalizeText = %q", normalizeText("# **Deploys** run   through the `CI` pipeline"))
	}
	cand := knowledge.Item{ID: "x", Tier1: "", Tier2: "# " + strings.ToUpper(designated)}
	if _, ok := equivalentAnswer(target(), cand, "corpus:item:it-844b5c8eef4e9a48"); !ok {
		t.Fatal("case-only difference must still count as the same sentence")
	}
	cand = knowledge.Item{ID: "y", Tier1: "", Tier2: "# Deploys run through the CI pipeline, never by SSHing to the VPS from a laptop."}
	if _, ok := equivalentAnswer(target(), cand, "corpus:item:it-844b5c8eef4e9a48"); ok {
		t.Fatal("a one-word change must not count as containment")
	}
}

// An empty designated text can never match -- an item with no body must
// not be "contained" by everything.
func TestEmptyDesignatedTextNeverMatches(t *testing.T) {
	empty := knowledge.Item{ID: "t", Permalink: "corpus:item:t", Tier1: "", Tier2: ""}
	cand := knowledge.Item{ID: "c", Tier1: "anything", Tier2: "anything at all"}
	if _, ok := equivalentAnswer(empty, cand, "corpus:item:t"); ok {
		t.Fatal("empty designated text matched")
	}
}

// creditEquivalents never touches a strict hit, credits a miss at most
// once, and reports rank, both ids and the clause.
func TestCreditEquivalentsIsAttributableAndLeavesStrictHitsAlone(t *testing.T) {
	items := map[string]knowledge.Item{
		"it-844b5c8eef4e9a48": target(),
		"it-60ae535a15dde4f7": {ID: "it-60ae535a15dde4f7", Tier1: designated, Tier2: "# " + designated + "\n\n" + designated + "\n\n## Why"},
		"it-noise":            {ID: "it-noise", Tier1: "unrelated", Tier2: "unrelated body"},
	}
	c := goldenset.Case{ID: "rb-22", ExpectedIdentifier: "corpus:item:it-844b5c8eef4e9a48"}
	miss := naturalResult{c: c, rank: 0, ran: true, matches: []parsedMatch{{ID: "it-noise"}, {ID: "it-60ae535a15dde4f7"}, {ID: "it-60ae535a15dde4f7"}}}
	hit := naturalResult{c: goldenset.Case{ID: "rb-09", ExpectedIdentifier: "corpus:item:it-844b5c8eef4e9a48"}, rank: 1, ran: true, matches: []parsedMatch{{ID: "it-60ae535a15dde4f7"}}}
	credits, skipped := creditEquivalents([]naturalResult{miss, hit}, items)
	if len(skipped) != 0 {
		t.Fatalf("nothing here is too generic to assess, got skipped=%+v", skipped)
	}
	if len(credits) != 1 {
		t.Fatalf("credits = %+v, want exactly one (the miss, once; the hit untouched)", credits)
	}
	e := credits[0]
	if e.CaseID != "rb-22" || e.Rank != 2 || e.ItemID != "it-60ae535a15dde4f7" || e.TargetID != "it-844b5c8eef4e9a48" || e.Clause != "opens" {
		t.Fatalf("credit not attributable: %+v", e)
	}
	line := equivalenceLine(e)
	for _, want := range []string{"[EQUIV] rb-22", "rank 2", "it-60ae535a15dde4f7", "it-844b5c8eef4e9a48", "first statement is the designated sentence"} {
		if !strings.Contains(line, want) {
			t.Fatalf("equivalence line missing %q: %s", want, line)
		}
	}
}

// Only the top ten are scanned: an equivalent at rank 11 is still a miss.
func TestEquivalentBeyondTopTenIsNotCredited(t *testing.T) {
	items := map[string]knowledge.Item{
		"it-844b5c8eef4e9a48": target(),
		"it-60ae535a15dde4f7": {ID: "it-60ae535a15dde4f7", Tier1: designated, Tier2: designated},
	}
	var matches []parsedMatch
	for i := 0; i < 10; i++ {
		matches = append(matches, parsedMatch{ID: "it-none"})
	}
	matches = append(matches, parsedMatch{ID: "it-60ae535a15dde4f7"})
	r := naturalResult{c: goldenset.Case{ID: "rb-x", ExpectedIdentifier: "corpus:item:it-844b5c8eef4e9a48"}, matches: matches}
	if got, _ := creditEquivalents([]naturalResult{r}, items); len(got) != 0 {
		t.Fatalf("rank-11 equivalent credited: %+v", got)
	}
}
