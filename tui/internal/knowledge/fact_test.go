package knowledge

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const fixtureFact = `---
type: project
title: Test fact
description: a fact used only by this package's own tests
created: 2026-08-10T15:00:00Z
source: fixture
---

# Test fact

This is the body. It has more than one line.

And a second paragraph.
`

func writeFact(t *testing.T, dir, slug, content string) {
	t.Helper()
	factsDir := filepath.Join(dir, "agent", "facts")
	if err := os.MkdirAll(factsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(factsDir, slug+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFactReadsFrontmatterAndBody(t *testing.T) {
	f, err := parseFact(fixtureFact)
	if err != nil {
		t.Fatalf("parseFact() error: %v", err)
	}
	if f.Type != "project" || f.Title != "Test fact" || f.Created != "2026-08-10T15:00:00Z" || f.Source != "fixture" {
		t.Errorf("parseFact() = %+v", f)
	}
	if f.Description != "a fact used only by this package's own tests" {
		t.Errorf("Description = %q", f.Description)
	}
	wantBody := "# Test fact\n\nThis is the body. It has more than one line.\n\nAnd a second paragraph.\n"
	if f.Body != wantBody {
		t.Errorf("Body = %q, want %q", f.Body, wantBody)
	}
}

func TestParseFactNoOpeningFenceIsAnError(t *testing.T) {
	if _, err := parseFact("no frontmatter here\n"); err == nil {
		t.Fatal("expected an error for a file with no opening --- fence")
	}
}

func TestParseFactUnclosedFenceIsAnError(t *testing.T) {
	if _, err := parseFact("---\ntype: project\n"); err == nil {
		t.Fatal("expected an error for a file whose frontmatter is never closed")
	}
}

func TestLoadFactEmptyVaultDirIsAVisibleError(t *testing.T) {
	if _, err := LoadFact("", "whatever"); err == nil {
		t.Fatal("LoadFact(\"\", ...) returned no error")
	}
}

// TestLoadFactMissingSlugIsAVisibleError is the "stale link in
// agent/index.md" case: a slug with no corresponding file must be a real
// error, never an empty Fact silently swapped in for it.
func TestLoadFactMissingSlugIsAVisibleError(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadFact(dir, "does-not-exist"); err == nil {
		t.Fatal("LoadFact() on a missing slug returned no error")
	}
}

func TestLoadFactReadsTheRealFile(t *testing.T) {
	dir := t.TempDir()
	writeFact(t, dir, "test-fact", fixtureFact)

	f, err := LoadFact(dir, "test-fact")
	if err != nil {
		t.Fatalf("LoadFact() error: %v", err)
	}
	if f.Slug != "test-fact" || f.Title != "Test fact" {
		t.Errorf("LoadFact() = %+v", f)
	}
}

// TestLoadFactNeverReadsAnyOtherFile is this package's own hard
// constraint, made a test: a vault with 500 OTHER fact files (none of
// which are ever opened, all deliberately unreadable) must not cause
// LoadFact to fail or slow down -- it opens exactly the one file it was
// asked for.
func TestLoadFactNeverReadsAnyOtherFile(t *testing.T) {
	dir := t.TempDir()
	writeFact(t, dir, "the-one-i-want", fixtureFact)
	for i := 0; i < 500; i++ {
		// A 0o000-mode file: unreadable by anything that tries to open it.
		// If LoadFact ever touched one of these, it would fail here.
		path := filepath.Join(dir, "agent", "facts", fmt.Sprintf("decoy-%03d.md", i))
		if err := os.WriteFile(path, []byte("should never be read"), 0o000); err != nil {
			t.Fatal(err)
		}
	}
	f, err := LoadFact(dir, "the-one-i-want")
	if err != nil {
		t.Fatalf("LoadFact() error (a decoy file must have been touched): %v", err)
	}
	if f.Slug != "the-one-i-want" {
		t.Errorf("LoadFact() = %+v", f)
	}
}
