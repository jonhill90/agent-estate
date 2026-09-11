package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/estate/internal/distill"
)

func writeNote(t *testing.T, dir, id string, fields map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range []string{"type", "id", "corpus_item", "subject", "generated"} {
		if v, ok := fields[k]; ok {
			b.WriteString(k + ": \"" + v + "\"\n")
		}
	}
	b.WriteString("---\n\nbody\n")
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDedupeCatchesAnExactRepublish is the brief's own mutation-check
// direction 1 (agent-estate#1339): a second publish of the same
// (corpus_item, subject) pair -- a prior run's own output, or, live today,
// an already-existing pre-#1290 Fact landing on the identical pair -- must
// be recognised and skipped, not written again.
func TestDedupeCatchesAnExactRepublish(t *testing.T) {
	existing := map[string][]factRef{
		"it-abc123": {{ID: "20260101000000", Subject: "lane"}},
	}
	facts := []fact{
		{Subject: "lane", Chosen: distill.Item{ID: "it-abc123", Body: "do not merge your own PRs"}},
	}
	kept, skipped := dedupe(facts, nil, existing)
	if skipped != 1 {
		t.Fatalf("skipped = %d, want 1 (exact repeat must be caught)", skipped)
	}
	if len(kept) != 0 {
		t.Fatalf("kept = %d, want 0", len(kept))
	}
}

// TestDedupeDoesNotCatchADistinctCorpusItem is the brief's own mutation-check
// direction 2: a genuinely distinct corpus_item with near-identical TEXT
// (the issue's own 30-cluster case -- repeated real operator utterances,
// each its own event) must NOT be suppressed. dedupe keys on corpus_item
// alone, never on body similarity, so two different ids with the exact same
// body must both survive.
func TestDedupeDoesNotCatchADistinctCorpusItem(t *testing.T) {
	existing := map[string][]factRef{
		"it-abc123": {{ID: "20260101000000", Subject: "lane"}},
	}
	facts := []fact{
		// Same subject, same BODY text, but a DIFFERENT corpus_item -- must
		// survive: this is not the item existing already covers.
		{Subject: "lane", Chosen: distill.Item{ID: "it-xyz789", Body: "do not merge your own PRs"}},
		// Second, independent case in the SAME call: two candidates in one
		// run sharing identical body text under identical near-duplicate
		// wording (the issue's own 30-cluster shape -- "how are things
		// going?" asked on 5 separate real occasions, 5 separate ids) but
		// different corpus_items. A dedup keyed on text rather than
		// corpus_item would collapse these two into one; both must survive.
		{Subject: "lane", Chosen: distill.Item{ID: "it-qrs456", Body: "do not merge your own PRs"}},
	}
	kept, skipped := dedupe(facts, nil, existing)
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0 -- a distinct corpus_item must never be suppressed by text similarity", skipped)
	}
	if len(kept) != 2 {
		t.Fatalf("kept = %d, want 2 -- two distinct corpus_items with the same wording must both survive", len(kept))
	}
	ids := map[string]bool{kept[0].Chosen.ID: true, kept[1].Chosen.ID: true}
	if !ids["it-xyz789"] || !ids["it-qrs456"] {
		t.Fatalf("kept ids = %v, want both it-xyz789 and it-qrs456", ids)
	}
}

// TestDedupeAllowsTheSameCorpusItemUnderADifferentSubject pins the issue's
// own worked example precisely: "do not merge your own PRs" was promoted
// once under subject "lane" and once under subject "merge", each with real,
// distinct evidence -- a naive "one note per corpus_item" rule would
// destroy the second one. This must still be allowed, with the sibling
// named in Related rather than silently duplicated.
func TestDedupeAllowsTheSameCorpusItemUnderADifferentSubject(t *testing.T) {
	existing := map[string][]factRef{
		"it-eeba4187": {{ID: "20260803190138", Subject: "lane"}},
	}
	facts := []fact{
		{Subject: "merge", Chosen: distill.Item{ID: "it-eeba4187", Body: "do not merge your own PRs"}},
	}
	kept, skipped := dedupe(facts, nil, existing)
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0 -- a different subject is real, distinct content", skipped)
	}
	if len(kept) != 1 {
		t.Fatalf("kept = %d, want 1", len(kept))
	}
	if len(kept[0].Related) != 1 || kept[0].Related[0] != "20260803190138" {
		t.Fatalf("Related = %v, want the sibling Fact named, not silently omitted", kept[0].Related)
	}
}

// TestDedupePublishesAlongsideAnExistingParameterNote is the majority shape
// (agent-estate#1339: 217 of 247 duplicate clusters, 88%): a corpus_item
// already has a vault-view Parameter note. Refusing here would make this
// command promote nothing, ever -- both mechanisms draw from the same hard-
// item population, so a Parameter note exists for virtually every candidate
// this command will ever consider (measured live: 101 of 107, 94%). The
// Fact is published, with the Parameter note it now sits beside named in
// Related.
func TestDedupePublishesAlongsideAnExistingParameterNote(t *testing.T) {
	params := map[string]string{"it-abc123": "20260101000000"}
	facts := []fact{
		{Subject: "auth", Chosen: distill.Item{ID: "it-abc123", Body: "tokens rotate every 90 days"}},
	}
	kept, skipped := dedupe(facts, params, nil)
	if skipped != 0 {
		t.Fatalf("skipped = %d, want 0 -- an existing Parameter note must not block a new Fact", skipped)
	}
	if len(kept) != 1 || len(kept[0].Related) != 1 || kept[0].Related[0] != "20260101000000" {
		t.Fatalf("kept = %+v, want 1 fact with the Parameter note named in Related", kept)
	}
}

// TestExistingParamsAndFactsReadTheVaultAsWritten proves the two scan
// functions actually read what a real note file on disk carries -- not a
// reimplementation of vaultview's own writer, a fixture written by hand the
// same shape render()/vaultview.write() produce, then read back.
func TestExistingParamsAndFactsReadTheVaultAsWritten(t *testing.T) {
	vault := t.TempDir()
	writeNote(t, filepath.Join(vault, "01 - Notes", "01p - Parameters"), "20260101000000",
		map[string]string{"type": "Parameter", "id": "20260101000000", "corpus_item": "it-p1"})
	writeNote(t, filepath.Join(vault, "01 - Notes", "01f - Facts"), "20260102000000",
		map[string]string{"type": "Fact", "id": "20260102000000", "corpus_item": "it-f1", "subject": "lane", "generated": "process:distill-judged"})
	writeNote(t, filepath.Join(vault, "01 - Notes", "01f - Facts"), "20260103000000",
		map[string]string{"type": "Fact", "id": "20260103000000", "corpus_item": "it-f1", "subject": "merge", "generated": "process:distill-promote"})

	params, err := existingParams(vault)
	if err != nil {
		t.Fatal(err)
	}
	if params["it-p1"] != "20260101000000" {
		t.Fatalf("existingParams = %v, want it-p1 -> 20260101000000", params)
	}

	facts, err := existingFacts(vault)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts["it-f1"]) != 2 {
		t.Fatalf("existingFacts[it-f1] = %v, want 2 entries (one per subject, one per generator)", facts["it-f1"])
	}
	subs := map[string]bool{}
	for _, r := range facts["it-f1"] {
		subs[r.Subject] = true
	}
	if !subs["lane"] || !subs["merge"] {
		t.Fatalf("subjects = %v, want both lane and merge, regardless of which generator wrote them", subs)
	}
}

// TestExistingParamsMissingDirectoryIsEmptyNotAnError matches
// existingIDs' own established handling for a vault with no notes of a
// given kind yet -- a freshly-provisioned vault, or a scratch fixture that
// never created 01p - Parameters at all, is a legitimate empty state.
func TestExistingParamsMissingDirectoryIsEmptyNotAnError(t *testing.T) {
	vault := t.TempDir()
	params, err := existingParams(vault)
	if err != nil {
		t.Fatalf("existingParams() on a vault with no Parameters dir = %v", err)
	}
	if len(params) != 0 {
		t.Fatalf("params = %v, want empty", params)
	}
	facts, err := existingFacts(vault)
	if err != nil {
		t.Fatalf("existingFacts() on a vault with no Facts dir = %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("facts = %v, want empty", facts)
	}
}
