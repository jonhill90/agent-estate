package knowledge

import (
	"os"
	"path/filepath"
	"testing"
)

// fixtureIndex mirrors index.md's real, current shape (agent-estate#1304):
// URL-encoded markdown links into an earned 01 - Notes/ subdirectory,
// naming a 14-digit id -- not the pre-relayout "facts/<slug>.md" this file
// used to fixture. Measured against the live vault 2026-09-08.
const fixtureIndex = `---
okf_version: "0.1"
---

# Facts

- [memory-conventions](01%20-%20Notes/01f%20-%20Facts/20260712173000.md) — how this vault is structured
- [herdr — DECIDED, WE BUILD](01%20-%20Notes/01f%20-%20Facts/20260713000000.md) — settled; never reopen
- [[20260719040000]] — browse the deployed page + screenshot proof (2026-07-19)
- [[20260824040000]] — a guard wired to the component that fails is not deployed
`

func TestParseIndexLinkedFormat(t *testing.T) {
	entries := ParseIndex(fixtureIndex)
	if len(entries) != 4 {
		t.Fatalf("ParseIndex() = %+v, want 4 entries", entries)
	}
	e := entries[0]
	if e.Slug != "20260712173000" || e.Title != "memory-conventions" || e.Description != "how this vault is structured" {
		t.Errorf("entries[0] = %+v", e)
	}
}

// TestParseIndexTitleContainingEmDash is the case that broke a naive
// "split on the first em-dash" parse: the fact's own display TITLE
// contains an em-dash ("herdr — DECIDED, WE BUILD"), so the regex must
// anchor on the closing "](...)" structure, not the first em-dash in the
// line.
func TestParseIndexTitleContainingEmDash(t *testing.T) {
	entries := ParseIndex(fixtureIndex)
	e := entries[1]
	if e.Slug != "20260713000000" {
		t.Fatalf("Slug = %q, want %q", e.Slug, "20260713000000")
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
	if e.Slug != "20260719040000" {
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

// TestParseIndexRejectsNonNoteLinks is agent-estate#1304's own negative
// case: a bullet whose link target does not end in a noteFilename-shaped
// id (12 or 14 digits) is not a note reference at all, and must not be
// admitted just because it superficially matches "[title](.../x.md)".
func TestParseIndexRejectsNonNoteLinks(t *testing.T) {
	entries := ParseIndex("---\nokf_version: \"0.1\"\n---\n\n# Facts\n\n- [not a note](01%20-%20Notes/README.md) — a per-subdir readme, not a fact\n- [[not-a-note]] — a legacy human-readable wikilink slug, not an id\n")
	if len(entries) != 0 {
		t.Fatalf("ParseIndex() = %+v, want 0 entries -- neither line names a noteFilename-shaped id", entries)
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
		t.Fatal("LoadIndex() on a vault dir with no index.md returned no error")
	}
}

// TestLoadIndexReadsTheCurrentVaultRootFile is agent-estate#1304's own
// acceptance case, and fails against unmodified main: LoadIndex used to
// read vaultDir/agent/index.md, a directory A2-COMPLETION (agent-estate#1275)
// deleted a month before this fix -- every real call returned "no such
// file or directory" against the live vault. This fixtures index.md at
// the vault ROOT, the file's real current location, and proves it is what
// gets read.
func TestLoadIndexReadsTheCurrentVaultRootFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(fixtureIndex), 0o644); err != nil {
		t.Fatal(err)
	}
	// Prove this is really testing the vault-root file, not accidentally
	// falling back to something else: no agent/ directory exists in this
	// fixture at all, matching the live vault (`ls $AGENT_MEMORY_VAULT/agent`
	// errors).
	if _, err := os.Stat(filepath.Join(dir, "agent", "index.md")); err == nil {
		t.Fatal("fixture setup bug: agent/index.md exists, so this would not exercise the vault-root read")
	}
	entries, err := LoadIndex(dir)
	if err != nil {
		t.Fatalf("LoadIndex() error: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("LoadIndex() = %+v, want 4 entries", entries)
	}
}

// TestCountFactsCountsDiskNotIndex is agent-estate#1304's own decision,
// pinned: VaultFacts must answer "how many facts does the vault hold,"
// not "how many entries fit in the capped session-start index." A
// 2-entry index alongside 3 real fact files must report 3, not 2.
func TestCountFactsCountsDiskNotIndex(t *testing.T) {
	dir := t.TempDir()
	facts := filepath.Join(dir, "01 - Notes", "01f - Facts")
	if err := os.MkdirAll(facts, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"20260712173000", "20260713000000", "20260824040000"} {
		if err := os.WriteFile(filepath.Join(facts, id+".md"), []byte("---\ntype: fact\n---\nbody\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A non-note file in the same directory (a README, say) must not be
	// counted as a fact.
	if err := os.WriteFile(filepath.Join(facts, "README.md"), []byte("not a fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(fixtureIndex), 0o644); err != nil {
		t.Fatal(err)
	}

	n, err := CountFacts(dir)
	if err != nil {
		t.Fatalf("CountFacts() error: %v", err)
	}
	if n != 3 {
		t.Fatalf("CountFacts() = %d, want 3 real fact files (index.md's own 4 bullets must not change this)", n)
	}
}

func TestCountFactsEmptyVaultDirIsAVisibleError(t *testing.T) {
	if _, err := CountFacts(""); err == nil {
		t.Fatal("CountFacts(\"\") returned no error -- $AGENT_MEMORY_VAULT unset must be a visible error, never a silent 0")
	}
}

func TestCountFactsMissingDirIsAVisibleError(t *testing.T) {
	if _, err := CountFacts(t.TempDir()); err == nil {
		t.Fatal("CountFacts() on a vault dir with no 01 - Notes/01f - Facts/ returned no error -- must not report 0 facts for a vault it could not read")
	}
}
