package knowledge

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildFixtureCorpus creates a real sqlite3 database at dir/ledger.sqlite3
// with the same items table shape as the real corpus, via the sqlite3
// CLI directly (this package's own dependency-free approach, matching
// internal/corpus's own).
func buildFixtureCorpus(t *testing.T, ddl string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ledger.sqlite3")
	cmd := exec.Command("sqlite3", path)
	cmd.Stdin = strings.NewReader(ddl)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sqlite3 fixture setup failed: %v\n%s", err, out)
	}
	return path
}

// fixtureDDL mirrors the real corpus's items table and its live_parameters
// view (CREATE VIEW live_parameters AS SELECT * FROM items WHERE kind =
// 'parameter' AND weight != 'retracted') -- corpusSource no longer reads
// live_parameters directly (agent-estate#1035 generalised it to cover
// directive/question/correction too, none of which has an equivalent
// view), but the view is kept here so a regression that reintroduces a
// view-based read is still exercised against the same shape.
//
// Rows 10-12 are agent-estate#1131's own regression fixture for the narrow
// thought carve-out: row 10 is a low-firmness thought (still bulk
// excluded), row 11 is a weight='hard' thought that must now survive, and
// row 12 is a weight='hard' thought that is ALSO retracted -- retracted
// still wins over the carve-out, the same as every other kind.
const fixtureDDL = `
CREATE TABLE items (
  id INTEGER PRIMARY KEY,
  prompt_id INTEGER,
  kind TEXT,
  body TEXT,
  weight TEXT,
  status TEXT,
  status_reason TEXT,
  resolved_to TEXT,
  acked_at TEXT
);
CREATE VIEW live_parameters AS SELECT * FROM items WHERE kind = 'parameter' AND weight != 'retracted';
INSERT INTO items (id, prompt_id, kind, body, weight, status, resolved_to) VALUES
  (1, 101, 'parameter', 'Prefer CLI-backed workflows.', 'hard', 'live', 'tooling=cli_first'),
  (2, 102, 'parameter', 'Use uv for Python.', 'soft', 'live', 'python=uv'),
  (3, 103, 'parameter', 'A retracted one, must not appear.', 'retracted', 'live', 'gone=yes'),
  (4, NULL, 'prompt', 'this must never be reachable from this package', 'hard', 'live', NULL),
  (5, 105, 'directive', 'Prefer tmux for persistent terminals.', 'hard', 'acted', NULL),
  (6, 106, 'directive', 'A retracted directive, must not appear.', 'retracted', 'acted', NULL),
  (7, 107, 'question', 'Should ACP replace tmux as the transport?', 'hard', 'resolved', 'multiplexer=KEEP_TMUX'),
  (8, 108, 'question', 'A retracted question, must not appear.', 'retracted', 'open', NULL),
  (9, 109, 'correction', 'Not X after all -- Y is correct.', 'hard', 'acted', NULL),
  (10, 110, 'thought', 'A stray low-firmness musing, must not appear -- bulk excluded.', 'preference', 'acted', NULL),
  (11, 111, 'thought', 'A hard thought carve-out row, must appear.', 'hard', 'open', NULL),
  (12, 112, 'thought', 'A retracted hard thought, must not appear -- retracted still wins.', 'retracted', 'open', NULL);
`

func TestCorpusSourceReadsLiveItemsOfCompiledKindsOnly(t *testing.T) {
	path := buildFixtureCorpus(t, fixtureDDL)
	res, items := corpusSource(path)
	// 2 live parameters + 1 live directive + 1 live question + 1 live
	// correction + 1 hard thought (row 11) = 6. The retracted rows of each
	// kind, the bare `prompt` row, the low-firmness thought (row 10) and
	// the retracted hard thought (row 12) must all be absent.
	if !res.OK || res.Count != 6 {
		t.Fatalf("corpusSource() result = %+v, want OK count=6", res)
	}
	for _, it := range items {
		if strings.Contains(it.Tier1, "must never be reachable") || strings.Contains(it.Tier2, "must never be reachable") {
			t.Fatalf("corpusSource() leaked the prompts-kind row into an item: %+v", it)
		}
		if strings.Contains(it.Tier1, "retracted") || strings.Contains(it.Tier2, "retracted") {
			t.Fatalf("corpusSource() included a retracted item: %+v", it)
		}
		if strings.Contains(it.Tier1, "low-firmness musing") || strings.Contains(it.Tier2, "low-firmness musing") {
			t.Fatalf("corpusSource() included a bulk-excluded thought item: %+v", it)
		}
	}
}

// TestCorpusSourceIncludesHardThoughtOnly is agent-estate#1131's own
// regression: the ~200 thought survivors of #1035's bulk exclusion are not
// uniformly noise -- 28 of them (measured 2026-09-04) carry weight='hard'
// and are neither retracted nor dropped, the corpus's own marker for a
// binding constraint that #1035's own justification did not cover. This
// checks the carve-out is narrow: the hard thought row survives, the
// low-firmness one still does not, and neither the row count nor the
// predicate hardcodes today's 28.
func TestCorpusSourceIncludesHardThoughtOnly(t *testing.T) {
	path := buildFixtureCorpus(t, fixtureDDL)
	_, items := corpusSource(path)

	var thoughtItems []Item
	for _, it := range items {
		if it.Source == "corpus-thought" {
			thoughtItems = append(thoughtItems, it)
		}
	}
	if len(thoughtItems) != 1 {
		t.Fatalf("corpusSource() compiled %d corpus-thought item(s), want 1 (the hard row only): %+v", len(thoughtItems), thoughtItems)
	}
	if !strings.Contains(thoughtItems[0].Tier1, "hard thought carve-out") && !strings.Contains(thoughtItems[0].Tier2, "hard thought carve-out") {
		t.Fatalf("corpusSource() compiled the wrong thought row as corpus-thought: %+v", thoughtItems[0])
	}
	for _, tag := range thoughtItems[0].StructuralTags {
		if tag == "weight:hard" {
			return
		}
	}
	t.Fatalf("corpus-thought item StructuralTags = %v, want to contain \"weight:hard\"", thoughtItems[0].StructuralTags)
}

// TestBulkExcludedThoughtRowIsAPredicateNotACount guards against
// agent-estate#1131 recreating the exact defect it reports: a fixed number
// standing in for a rule that must be re-evaluated against the corpus as
// it grows. bulkExcludedThoughtRow must decide solely from (weight,
// status), never from how many rows have already been seen.
func TestBulkExcludedThoughtRowIsAPredicateNotACount(t *testing.T) {
	cases := []struct {
		weight, status string
		wantExcluded    bool
	}{
		{"hard", "open", false},
		{"hard", "acknowledged", false},
		{"hard", "acted", false},
		{"hard", "resolved", false},
		{"preference", "acted", true},
		{"preference", "acknowledged", true},
		{"soft", "open", true},
		{"", "", true},
	}
	for _, c := range cases {
		got := bulkExcludedThoughtRow(c.weight, c.status)
		if got != c.wantExcluded {
			t.Fatalf("bulkExcludedThoughtRow(%q, %q) = %v, want %v", c.weight, c.status, got, c.wantExcluded)
		}
	}
}

// TestCorpusSourceCompilesDirectiveAndQuestion is agent-estate#1035's own
// regression: the gap it was filed to close was that only kind=parameter
// was compiled, so directive and question could never be returned by
// `estate knowledge query` even when they were the on-topic answer.
func TestCorpusSourceCompilesDirectiveAndQuestion(t *testing.T) {
	path := buildFixtureCorpus(t, fixtureDDL)
	_, items := corpusSource(path)

	bySource := map[string][]Item{}
	for _, it := range items {
		bySource[it.Source] = append(bySource[it.Source], it)
	}
	if len(bySource["corpus-directive"]) != 1 {
		t.Fatalf("corpusSource() compiled %d corpus-directive item(s), want 1: %+v", len(bySource["corpus-directive"]), bySource["corpus-directive"])
	}
	if len(bySource["corpus-question"]) != 1 {
		t.Fatalf("corpusSource() compiled %d corpus-question item(s), want 1: %+v", len(bySource["corpus-question"]), bySource["corpus-question"])
	}
	if len(bySource["corpus-correction"]) != 1 {
		t.Fatalf("corpusSource() compiled %d corpus-correction item(s), want 1: %+v", len(bySource["corpus-correction"]), bySource["corpus-correction"])
	}
	if len(bySource["corpus-parameter"]) != 2 {
		t.Fatalf("corpusSource() compiled %d corpus-parameter item(s), want 2 (unchanged from before #1035): %+v", len(bySource["corpus-parameter"]), bySource["corpus-parameter"])
	}
}

// TestCorpusSourceCarriesKindStructuralTag is agent-estate#1035's second
// regression: kind must survive onto the item as a structural tag (the
// same mechanism weight and status already use), specifically so a
// question renders distinguishably from a hard parameter rather than
// identically -- the advisor's own "as a question, not as law" test.
func TestCorpusSourceCarriesKindStructuralTag(t *testing.T) {
	path := buildFixtureCorpus(t, fixtureDDL)
	_, items := corpusSource(path)

	wantKind := map[string]string{
		"corpus-parameter":  "kind:parameter",
		"corpus-directive":  "kind:directive",
		"corpus-question":   "kind:question",
		"corpus-correction": "kind:correction",
		"corpus-thought":    "kind:thought",
	}
	seen := map[string]bool{}
	for _, it := range items {
		want, ok := wantKind[it.Source]
		if !ok {
			t.Fatalf("unexpected item source %q", it.Source)
		}
		found := false
		for _, tag := range it.StructuralTags {
			if tag == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("item %s (source %s) StructuralTags = %v, want to contain %q", it.ID, it.Source, it.StructuralTags, want)
		}
		seen[it.Source] = true
	}
	for source := range wantKind {
		if !seen[source] {
			t.Fatalf("no item compiled for source %q", source)
		}
	}
}

// TestCorpusSourceNewKindsDefaultPrivate is agent-estate#1035's explicit
// constraint check: "New kinds inherit [private-by-default]; confirm they
// do rather than assuming." classify()'s default branch returns false for
// any source name it does not positively know to be public
// ("github-stars" only) -- corpus-directive, corpus-question and
// corpus-correction are new source names that hit that default branch
// exactly like corpus-parameter already did.
func TestCorpusSourceNewKindsDefaultPrivate(t *testing.T) {
	path := buildFixtureCorpus(t, fixtureDDL)
	_, items := corpusSource(path)
	for _, it := range items {
		if it.Publishable {
			t.Fatalf("item %s (source %s) is Publishable=true, want false (corpus-derived items default private per #1030/#1037)", it.ID, it.Source)
		}
		if it.PublishBasis == "" {
			t.Fatalf("item %s (source %s) has empty PublishBasis", it.ID, it.Source)
		}
	}
}

// TestCorpusSourceCarriesPromptID is agent-estate#1031's own regression:
// every corpus-derived item must trace back to the prompts row behind it,
// via the bare id the corpus itself already carries in prompt_id -- never
// via the prompt's own text, which this package must never read (see
// corpusSource's own doc comment). This fails if a corpus-derived item is
// ever compiled with an empty PromptID.
func TestCorpusSourceCarriesPromptID(t *testing.T) {
	path := buildFixtureCorpus(t, fixtureDDL)
	_, items := corpusSource(path)
	if len(items) != 6 {
		t.Fatalf("corpusSource() returned %d items, want 6", len(items))
	}
	want := map[string]string{
		"1":  "101",
		"2":  "102",
		"5":  "105",
		"7":  "107",
		"9":  "109",
		"11": "111",
	}
	for _, it := range items {
		id := strings.TrimPrefix(it.Tier3, "the corpus's own item ")
		id = id[:strings.Index(id, " (kind=")]
		wantPromptID, ok := want[id]
		if !ok {
			t.Fatalf("unexpected item id %q in corpusSource() output", id)
		}
		if it.PromptID == "" {
			t.Fatalf("corpusSource() item %q has empty PromptID -- every corpus-derived item must carry one (agent-estate#1031)", id)
		}
		if it.PromptID != wantPromptID {
			t.Fatalf("corpusSource() item %q PromptID = %q, want %q", id, it.PromptID, wantPromptID)
		}
	}
}

// TestCorpusSourcePromptIDNeverCarriesText guards the hard constraint:
// the field is the bare corpus id only, never anything resembling the
// prompt's own words.
func TestCorpusSourcePromptIDNeverCarriesText(t *testing.T) {
	path := buildFixtureCorpus(t, fixtureDDL)
	_, items := corpusSource(path)
	for _, it := range items {
		for _, r := range it.PromptID {
			if r < '0' || r > '9' {
				t.Fatalf("corpusSource() item PromptID = %q is not purely numeric -- looks like it may carry more than an id", it.PromptID)
			}
		}
	}
}

func TestCorpusSourceUnreadablePathIsHonest(t *testing.T) {
	res, items := corpusSource(filepath.Join(t.TempDir(), "absent.sqlite3"))
	if res.OK {
		t.Fatal("corpusSource() reported OK for an absent database")
	}
	if res.Reason == "" {
		t.Fatal("corpusSource() gave no reason")
	}
	if items != nil {
		t.Fatal("corpusSource() returned items for an absent database")
	}
}

func TestCorpusSourceEmptyPathIsHonest(t *testing.T) {
	res, _ := corpusSource("")
	if res.OK {
		t.Fatal("corpusSource(\"\", ...) reported OK with no path configured")
	}
}
