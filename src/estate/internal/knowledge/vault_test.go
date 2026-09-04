package knowledge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
