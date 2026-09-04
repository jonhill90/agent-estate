package knowledge

import (
	"strings"
	"testing"
)

// disclosureFixtureDDL mirrors the real corpus's prompts table shape
// (id, text_raw, text_clean; internal/corpus/provenance.go's own DDL
// reference) with one row per DisclosureState this test exercises.
// mp-available... carries a non-empty text_clean; mp-empty... has a row
// but no text_clean (the 88% case); mp-missing... is deliberately never
// inserted, so PromptID resolving to it exercises source_missing without
// this package ever needing the real corpus's own broken-lineage row (if
// one even exists).
const disclosureFixtureDDL = `
CREATE TABLE prompts (
  id TEXT PRIMARY KEY,
  at INTEGER NOT NULL,
  text_raw TEXT NOT NULL,
  text_clean TEXT,
  context TEXT NOT NULL
);
INSERT INTO prompts (id, at, text_raw, text_clean, context) VALUES
  ('mp-aaaaaaaaaaaaaaaa', 1, 'raw text nobody may see', 'a cleaned, quotable sentence', 'ctx'),
  ('mp-bbbbbbbbbbbbbbbb', 2, 'raw text nobody may see', NULL, 'ctx'),
  ('mp-cccccccccccccccc', 3, 'raw text nobody may see', 'a cleaned, quotable sentence', 'ctx');
`

func TestResolveDisclosureAvailableClean(t *testing.T) {
	path := buildFixtureCorpus(t, disclosureFixtureDDL)
	item := Item{ID: "it-1", PromptID: "mp-aaaaaaaaaaaaaaaa", Publishable: true}
	d, err := ResolveDisclosure(path, item, false)
	if err != nil {
		t.Fatalf("ResolveDisclosure() error = %v", err)
	}
	if d.State != DisclosureAvailableClean {
		t.Fatalf("State = %q, want %q", d.State, DisclosureAvailableClean)
	}
	if strings.Contains(d.Detail, "cleaned, quotable sentence") {
		t.Fatalf("Detail leaked text_clean: %q", d.Detail)
	}
}

func TestResolveDisclosureUnavailable(t *testing.T) {
	path := buildFixtureCorpus(t, disclosureFixtureDDL)
	item := Item{ID: "it-2", PromptID: "mp-bbbbbbbbbbbbbbbb", Publishable: true}
	d, err := ResolveDisclosure(path, item, false)
	if err != nil {
		t.Fatalf("ResolveDisclosure() error = %v", err)
	}
	if d.State != DisclosureUnavailable {
		t.Fatalf("State = %q, want %q", d.State, DisclosureUnavailable)
	}
}

// TestResolveDisclosureRestricted is the "source exists but this
// caller/scope cannot receive it" state -- a corpus-parameter-shaped item
// classified private, whose prompt row DOES carry a real text_clean,
// looked up WITHOUT --private. Scope must win over the content check,
// matching ResolveDisclosure's own doc comment on scope-before-content
// ordering.
func TestResolveDisclosureRestricted(t *testing.T) {
	path := buildFixtureCorpus(t, disclosureFixtureDDL)
	item := Item{ID: "it-3", PromptID: "mp-cccccccccccccccc", Publishable: false, PublishBasis: "unclassified means private"}
	d, err := ResolveDisclosure(path, item, false)
	if err != nil {
		t.Fatalf("ResolveDisclosure() error = %v", err)
	}
	if d.State != DisclosureRestricted {
		t.Fatalf("State = %q, want %q", d.State, DisclosureRestricted)
	}
	if strings.Contains(d.Detail, "cleaned, quotable sentence") {
		t.Fatalf("Detail leaked text_clean: %q", d.Detail)
	}
}

// TestResolveDisclosureIncludePrivateUnlocksContent is the real-world
// shape every corpus-* item takes today (classify.go's default branch --
// no corpus source is in the explicit-public table, so every corpus item
// is Publishable == false): `get --private` is the only way `get` ever
// reaches an item like this at all, and once unlocked, disclosure must
// answer from CONTENT (available_clean/unavailable), not repeat
// "restricted" for material the caller already unlocked.
func TestResolveDisclosureIncludePrivateUnlocksContent(t *testing.T) {
	path := buildFixtureCorpus(t, disclosureFixtureDDL)
	item := Item{ID: "it-3b", PromptID: "mp-cccccccccccccccc", Publishable: false, PublishBasis: "unclassified means private"}
	d, err := ResolveDisclosure(path, item, true)
	if err != nil {
		t.Fatalf("ResolveDisclosure() error = %v", err)
	}
	if d.State != DisclosureAvailableClean {
		t.Fatalf("State = %q, want %q (--private must unlock content-based states)", d.State, DisclosureAvailableClean)
	}
}

// TestResolveDisclosureSourceMissing constructs an item whose prompt_id
// resolves to nothing in the fixture corpus at all -- the broken-lineage
// case the issue explicitly asks to be covered by a constructed fixture.
func TestResolveDisclosureSourceMissing(t *testing.T) {
	path := buildFixtureCorpus(t, disclosureFixtureDDL)
	item := Item{ID: "it-4", PromptID: "mp-dddddddddddddddd", Publishable: true}
	d, err := ResolveDisclosure(path, item, false)
	if err != nil {
		t.Fatalf("ResolveDisclosure() error = %v", err)
	}
	if d.State != DisclosureSourceMissing {
		t.Fatalf("State = %q, want %q", d.State, DisclosureSourceMissing)
	}
}

func TestResolveDisclosureNoPromptID(t *testing.T) {
	path := buildFixtureCorpus(t, disclosureFixtureDDL)
	item := Item{ID: "it-5", Publishable: true}
	if _, err := ResolveDisclosure(path, item, false); err == nil {
		t.Fatalf("ResolveDisclosure() with no PromptID: want error, got nil")
	}
}

func TestResolveDisclosureCorpusUnreadable(t *testing.T) {
	item := Item{ID: "it-6", PromptID: "mp-aaaaaaaaaaaaaaaa", Publishable: true}
	if _, err := ResolveDisclosure("/nonexistent/ledger.sqlite3", item, false); err == nil {
		t.Fatalf("ResolveDisclosure() with unreadable corpus: want error, got nil")
	}
}

// TestResolveDisclosureRejectsMalformedPromptID guards the injection-safety
// precondition disclosure.go's promptIDPattern exists to enforce -- a
// PromptID that does not match the corpus's own id shape must error, never
// be interpolated into the query string it is checked before reaching.
func TestResolveDisclosureRejectsMalformedPromptID(t *testing.T) {
	path := buildFixtureCorpus(t, disclosureFixtureDDL)
	item := Item{ID: "it-7", PromptID: "'; drop table prompts; --", Publishable: true}
	if _, err := ResolveDisclosure(path, item, false); err == nil {
		t.Fatalf("ResolveDisclosure() with malformed PromptID: want error, got nil")
	}
}
