package knowledge

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildFixtureCorpus creates a real sqlite3 database at dir/ledger.sqlite3
// with the same live_parameters view shape as the real corpus, via the
// sqlite3 CLI directly (this package's own dependency-free approach,
// matching internal/corpus's own).
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
  (4, NULL, 'prompt', 'this must never be reachable from this package', 'hard', 'live', NULL);
`

func TestCorpusSourceReadsLiveParametersOnly(t *testing.T) {
	path := buildFixtureCorpus(t, fixtureDDL)
	res, items := corpusSource(path)
	if !res.OK || res.Count != 2 {
		t.Fatalf("corpusSource() result = %+v, want OK count=2", res)
	}
	for _, it := range items {
		if strings.Contains(it.Tier1, "must never be reachable") || strings.Contains(it.Tier2, "must never be reachable") {
			t.Fatalf("corpusSource() leaked the prompts-kind row into an item: %+v", it)
		}
		if strings.Contains(it.Tier1, "retracted") {
			t.Fatalf("corpusSource() included a retracted parameter: %+v", it)
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
	if len(items) != 2 {
		t.Fatalf("corpusSource() returned %d items, want 2", len(items))
	}
	want := map[string]string{
		"1": "101",
		"2": "102",
	}
	for _, it := range items {
		id := strings.TrimPrefix(it.Tier3, "the corpus's own item ")
		id = strings.TrimSuffix(id, " (live_parameters) -- not this file")
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
