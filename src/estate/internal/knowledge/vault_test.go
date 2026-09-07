package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fixtureVaultFact = `---
type: project
title: Test fact
description: a fact used only by this package's own tests
created: 2026-08-10T15:00:00Z
source: fixture
---

# Test fact

Body text carrying a distinctive word: xenoglyph.
`

func writeVaultFact(t *testing.T, vaultDir, slug, content string) {
	t.Helper()
	factsDir := filepath.Join(vaultDir, "agent", "facts")
	if err := os.MkdirAll(factsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(factsDir, slug+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVaultSourceReadsEveryFactFile(t *testing.T) {
	dir := t.TempDir()
	writeVaultFact(t, dir, "one", fixtureVaultFact)
	writeVaultFact(t, dir, "two", fixtureVaultFact)

	res, items := vaultSource(dir)
	if !res.OK || res.Count != 2 {
		t.Fatalf("vaultSource() result = %+v", res)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].StructuralTags[0] != "project" {
		t.Errorf("StructuralTags = %v, want [project] from the fact's own type:", items[0].StructuralTags)
	}
}

// TestVaultSourceCompilesBodyIntoTier2 is agent-estate#1027's own
// acceptance test: a word that appears only in a fact's body (never in
// its title or description) must land in the compiled Item somewhere
// searchableText (query.go) already reads -- Tier2 -- not just in Tier3,
// which searchableText explicitly excludes. Get callers (main.go) also
// read Tier2 straight off Item, so this is the same assertion as "Get
// returns real content" from the item's own shape, without spinning up
// the CLI.
func TestVaultSourceCompilesBodyIntoTier2(t *testing.T) {
	dir := t.TempDir()
	writeVaultFact(t, dir, "one", fixtureVaultFact)

	_, items := vaultSource(dir)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !strings.Contains(items[0].Tier2, "xenoglyph") {
		t.Errorf("Tier2 = %q, want it to contain the fact's own body text", items[0].Tier2)
	}
	if !strings.Contains(items[0].Tier2, "a fact used only by this package's own tests") {
		t.Errorf("Tier2 = %q, want the fact's description kept as a lead-in", items[0].Tier2)
	}
	// searchableText (query.go) reads Tier1+Tier2, never Tier3 -- so a
	// body-only word must be findable through Tier2 without Query ever
	// being asked to read a fourth field.
	if strings.Contains(items[0].Tier1, "xenoglyph") {
		t.Errorf("Tier1 = %q, unexpectedly carries body text -- Tier1 must stay the short summary", items[0].Tier1)
	}
}

func TestVaultSourceEmptyDirIsHonestNotEmpty(t *testing.T) {
	res, items := vaultSource("")
	if res.OK {
		t.Fatal("vaultSource(\"\", ...) reported OK for an unset vault dir")
	}
	if res.Reason == "" {
		t.Fatal("vaultSource(\"\", ...) gave no reason")
	}
	if items != nil {
		t.Fatal("vaultSource(\"\", ...) returned items despite being unreadable")
	}
}

func TestVaultSourceMissingFactsDirIsHonest(t *testing.T) {
	res, _ := vaultSource(t.TempDir())
	if res.OK {
		t.Fatal("vaultSource() reported OK for a vault with no agent/facts directory")
	}
	if res.Reason == "" {
		t.Fatal("vaultSource() gave no reason for the missing directory")
	}
}

// TestVaultSourceSkipsOneUnparseableFactWithoutFailingTheSource mirrors
// agent/index.md's own tolerance for a line matching neither known
// format -- a single malformed fact file must not blank out every other
// fact in the vault.
func TestVaultSourceSkipsOneUnparseableFactWithoutFailingTheSource(t *testing.T) {
	dir := t.TempDir()
	writeVaultFact(t, dir, "good", fixtureVaultFact)
	writeVaultFact(t, dir, "bad", "no frontmatter fence here at all\n")

	res, items := vaultSource(dir)
	if !res.OK || res.Count != 1 {
		t.Fatalf("vaultSource() result = %+v, want OK with count 1", res)
	}
	if len(items) != 1 || items[0].Permalink != filepath.Join(dir, "agent", "facts", "good.md") {
		t.Fatalf("items = %+v", items)
	}
}

// TestVaultSourceTier3DeepensPastTier2 is agent-estate#1139 defect B's own
// acceptance test: Tier3 must carry MORE material than Tier2, not less --
// the old behaviour ("open <path> for the full fact") was shorter than
// Tier2's own full body and carried no new information a reader following
// the ladder didn't already have. FAILS against the reverted pointer-
// string behaviour (Tier3 shorter than Tier2, no frontmatter marker
// present) and PASSES against vaultTier3's full-file rendering.
func TestVaultSourceTier3DeepensPastTier2(t *testing.T) {
	dir := t.TempDir()
	writeVaultFact(t, dir, "one", fixtureVaultFact)

	_, items := vaultSource(dir)
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if len(it.Tier3) <= len(it.Tier2) {
		t.Fatalf("Tier3 (%d bytes) is not longer than Tier2 (%d bytes) -- tier3=%q tier2=%q",
			len(it.Tier3), len(it.Tier2), it.Tier3, it.Tier2)
	}
	// The frontmatter fence is only ever present in the raw file, never in
	// Tier2 (body only) -- its presence in Tier3 is the signal that Tier3
	// carries the WHOLE record, not merely a repeat of the body.
	if !strings.Contains(it.Tier3, "---") {
		t.Errorf("Tier3 = %q, want it to carry the fact's own frontmatter fence (the full file, not just the body)", it.Tier3)
	}
	if !strings.Contains(it.Tier3, "xenoglyph") {
		t.Errorf("Tier3 = %q, want it to still carry the body's own distinctive word", it.Tier3)
	}
}

func TestVaultSourceNestedNotesWithoutLegacyDirectory(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "01 - Notes", "01p - Parameters", "202609070001.md")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(fixtureVaultFact), 0600); err != nil {
		t.Fatal(err)
	}
	res, items := vaultSource(dir)
	if !res.OK || len(items) != 1 || items[0].Permalink != p {
		t.Fatalf("nested note not retrieved: %+v %+v", res, items)
	}
}

func TestNestedNoteQueryExcludesRetiredAndDraft(t *testing.T) {
	v := t.TempDir()
	dir := filepath.Join(v, "01 - Notes", "01p - Parameters")
	os.MkdirAll(dir, 0700)
	for i, status := range []string{"stable", "draft", "deprecated"} {
		text := strings.Replace(fixtureVaultFact, "type: project", "type: Parameter\nstatus: "+status, 1)
		if err := os.WriteFile(filepath.Join(dir, []string{"202609070001.md", "202609070002.md", "202609070003.md"}[i]), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source, items := vaultSource(v)
	addSourceTag(items)
	path := filepath.Join(t.TempDir(), "index.json")
	if err := Write(path, Result{GeneratedAt: time.Now(), Sources: []SourceResult{source}, Items: items}); err != nil {
		t.Fatal(err)
	}
	q := Query(path, "xenoglyph", 10, true)
	if len(q.Matches) != 1 {
		t.Fatalf("want current stable only: %+v", q)
	}
	t.Logf("retrieved one current nested note; draft/deprecated excluded; canonical citation %s", items[0].Permalink)
}
