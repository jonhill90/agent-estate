package knowledge

import (
	"os"
	"path/filepath"
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

Body text this package never reads.
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

	clock := newIDClock(time.Now())
	res, items := vaultSource(dir, clock)
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

func TestVaultSourceEmptyDirIsHonestNotEmpty(t *testing.T) {
	clock := newIDClock(time.Now())
	res, items := vaultSource("", clock)
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
	clock := newIDClock(time.Now())
	res, _ := vaultSource(t.TempDir(), clock)
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

	clock := newIDClock(time.Now())
	res, items := vaultSource(dir, clock)
	if !res.OK || res.Count != 1 {
		t.Fatalf("vaultSource() result = %+v, want OK with count 1", res)
	}
	if len(items) != 1 || items[0].Permalink != filepath.Join(dir, "agent", "facts", "good.md") {
		t.Fatalf("items = %+v", items)
	}
}
