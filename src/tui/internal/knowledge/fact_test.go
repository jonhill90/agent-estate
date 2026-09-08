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

// writeFact writes id.md under vaultDir/01 - Notes/01f - Facts/ -- the
// current vault shape (agent-estate#1304), not the pre-relayout
// agent/facts/<slug>.md this file used to fixture. id must be
// noteFilename-shaped (12 or 14 digits) for resolveNoteFile's filename
// pass to find it at all.
func writeFact(t *testing.T, dir, id, content string) {
	t.Helper()
	factsDir := filepath.Join(dir, "01 - Notes", "01f - Facts")
	if err := os.MkdirAll(factsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(factsDir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeAliasedFact is writeFact plus an aliases: frontmatter line -- the
// pre-relayout slug this note is also reachable by, mirroring what the W1
// fact migration actually produces on a real note.
func writeAliasedFact(t *testing.T, dir, id, alias, content string) {
	t.Helper()
	writeFact(t, dir, id, content)
	path := filepath.Join(dir, "01 - Notes", "01f - Facts", id+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	withAlias := "---\naliases: [" + alias + "]\n" + string(raw)[len("---\n"):]
	if err := os.WriteFile(path, []byte(withAlias), 0o644); err != nil {
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

// TestLoadFactMissingSlugIsAVisibleError is the "stale link in index.md"
// case: a slug with no corresponding file must be a real error, never an
// empty Fact silently swapped in for it.
func TestLoadFactMissingSlugIsAVisibleError(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "01 - Notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFact(dir, "20269999999999"); err == nil {
		t.Fatal("LoadFact() on a missing id returned no error")
	}
}

// TestLoadFactReadsTheCurrentVaultLayout is agent-estate#1304's own
// acceptance case, and fails against unmodified main: LoadFact used to
// join vaultDir/agent/facts/<slug>.md directly, a directory
// A2-COMPLETION (agent-estate#1275) deleted a month before this fix --
// every real call returned "no such file or directory." This fixtures a
// note at its real current location, one directory deeper than
// 01 - Notes/ itself, addressed by the id LoadIndex now actually produces.
func TestLoadFactReadsTheCurrentVaultLayout(t *testing.T) {
	dir := t.TempDir()
	writeFact(t, dir, "20260810150000", fixtureFact)
	if _, err := os.Stat(filepath.Join(dir, "agent", "facts", "20260810150000.md")); err == nil {
		t.Fatal("fixture setup bug: the legacy path exists, so this would not exercise the current layout")
	}

	f, err := LoadFact(dir, "20260810150000")
	if err != nil {
		t.Fatalf("LoadFact() error: %v", err)
	}
	if f.Slug != "20260810150000" || f.Title != "Test fact" {
		t.Errorf("LoadFact() = %+v", f)
	}
}

// TestLoadFactResolvesByAlias is agent-estate#1304's reproduction of
// standinglaw.go's own resolveStandingLawMemberFile approach (read as
// reference, not imported -- separate Go module): a pre-relayout slug a
// caller still passes must resolve via the note's own aliases:
// frontmatter, exactly like a [[wikilink]] written before the relayout
// still needs to.
func TestLoadFactResolvesByAlias(t *testing.T) {
	dir := t.TempDir()
	writeAliasedFact(t, dir, "20260810150000", "memory-conventions", fixtureFact)

	f, err := LoadFact(dir, "memory-conventions")
	if err != nil {
		t.Fatalf("LoadFact() by alias error: %v", err)
	}
	if f.Title != "Test fact" {
		t.Errorf("LoadFact() by alias = %+v", f)
	}
}

// TestLoadFactByIDNeverReadsAnyOtherFilesContent is this package's own
// hard constraint for the expected, common case (an id read straight from
// index.md): resolveNoteFile's filename pass compares fs.DirEntry.Name()
// only, never opening a candidate it is not about to return -- a vault
// with 500 OTHER fact files (deliberately unreadable) must not cause
// LoadFact to fail or slow down. This is narrower than the pre-agent-estate#1304
// version of this test ("never reads any other file, ever"): a slug that
// resolves only by ALIAS necessarily opens every candidate ahead of it in
// traversal order to check, the same cost standinglaw.go's own reference
// implementation accepts for the identical problem --
// TestLoadFactResolvesByAlias above proves that path still works, not
// that it is free.
func TestLoadFactByIDNeverReadsAnyOtherFilesContent(t *testing.T) {
	dir := t.TempDir()
	writeFact(t, dir, "20260810150000", fixtureFact)
	for i := 0; i < 500; i++ {
		// An unreadable decoy, its id sorted BEFORE the target so a pass
		// that opens files in traversal order would hit it first.
		id := fmt.Sprintf("2026%010d", i)
		path := filepath.Join(dir, "01 - Notes", "01f - Facts", id+".md")
		if err := os.WriteFile(path, []byte("should never be read"), 0o000); err != nil {
			t.Fatal(err)
		}
	}
	f, err := LoadFact(dir, "20260810150000")
	if err != nil {
		t.Fatalf("LoadFact() error (a decoy file's content must have been read): %v", err)
	}
	if f.Slug != "20260810150000" {
		t.Errorf("LoadFact() = %+v", f)
	}
}
