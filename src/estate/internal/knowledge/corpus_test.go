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
INSERT INTO items (id, kind, body, weight, status, resolved_to) VALUES
  (1, 'parameter', 'Prefer CLI-backed workflows.', 'hard', 'live', 'tooling=cli_first'),
  (2, 'parameter', 'Use uv for Python.', 'soft', 'live', 'python=uv'),
  (3, 'parameter', 'A retracted one, must not appear.', 'retracted', 'live', 'gone=yes'),
  (4, 'prompt', 'this must never be reachable from this package', 'hard', 'live', NULL);
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
