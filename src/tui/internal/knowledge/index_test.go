package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

const fixtureIndex = `---
okf_version: "0.1"
---

# Facts

- [memory-conventions](facts/memory-conventions.md) — how this vault is structured
- [herdr — DECIDED, WE BUILD](facts/herdr-build-vs-adopt-open.md) — settled; never reopen
- [[verify-in-browser-before-claiming-fixed]] — browse the deployed page + screenshot proof (2026-07-19)
- [[a-tool-nothing-calls-is-not-a-guard]] — a guard wired to the component that fails is not deployed
`

func TestParseIndexLinkedFormat(t *testing.T) {
	entries := ParseIndex(fixtureIndex)
	if len(entries) != 4 {
		t.Fatalf("ParseIndex() = %+v, want 4 entries", entries)
	}
	e := entries[0]
	if e.Slug != "memory-conventions" || e.Title != "memory-conventions" || e.Description != "how this vault is structured" {
		t.Errorf("entries[0] = %+v", e)
	}
}

// TestParseIndexTitleContainingEmDash is the case that broke a naive
// "split on the first em-dash" parse: the fact's own display TITLE
// contains an em-dash ("herdr — DECIDED, WE BUILD"), so the regex must
// anchor on the closing "](facts/...)" structure, not the first em-dash
// in the line.
func TestParseIndexTitleContainingEmDash(t *testing.T) {
	entries := ParseIndex(fixtureIndex)
	e := entries[1]
	if e.Slug != "herdr-build-vs-adopt-open" {
		t.Fatalf("Slug = %q, want %q", e.Slug, "herdr-build-vs-adopt-open")
	}
	if e.Title != "herdr — DECIDED, WE BUILD" {
		t.Fatalf("Title = %q, want the full em-dash title preserved", e.Title)
	}
	if e.Description != "settled; never reopen" {
		t.Fatalf("Description = %q", e.Description)
	}
}

func TestParseIndexWikiLinkFormat(t *testing.T) {
	entries := ParseIndex(fixtureIndex)
	e := entries[2]
	if e.Slug != "verify-in-browser-before-claiming-fixed" {
		t.Fatalf("Slug = %q", e.Slug)
	}
	// A wiki-link entry has no separate display title in the index --
	// Title falls back to the slug itself, never a fabricated one.
	if e.Title != e.Slug {
		t.Fatalf("Title = %q, want it to equal Slug for a wiki-link entry", e.Title)
	}
	if e.Description == "" {
		t.Fatal("Description is empty")
	}
}

func TestParseIndexSkipsFrontmatterAndHeadings(t *testing.T) {
	entries := ParseIndex("---\nokf_version: \"0.1\"\n---\n\n# Facts\n\nnot a bullet at all\n")
	if len(entries) != 0 {
		t.Fatalf("ParseIndex() = %+v, want 0 entries for a file with no bullets", entries)
	}
}

func TestLoadIndexEmptyVaultDirIsAVisibleError(t *testing.T) {
	_, err := LoadIndex("")
	if err == nil {
		t.Fatal("LoadIndex(\"\") returned no error -- $AGENT_MEMORY_VAULT unset must be a visible error, never an empty list")
	}
}

func TestLoadIndexMissingFileIsAVisibleError(t *testing.T) {
	_, err := LoadIndex(t.TempDir())
	if err == nil {
		t.Fatal("LoadIndex() on a vault dir with no agent/index.md returned no error")
	}
}

func TestLoadIndexReadsTheRealFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent", "index.md"), []byte(fixtureIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex() error: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("LoadIndex() = %+v, want 4 entries", entries)
	}
}
