package candidates

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sqliteAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 CLI not on PATH")
	}
}

// baseDDL matches the live corpus's own prompts/codex_provenance shape
// closely enough to exercise this package's joins -- not every column
// (source_file, at, etc. are trimmed) since Derive never reads them.
const baseDDL = `
CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT, context TEXT NOT NULL DEFAULT '', session TEXT, source_file TEXT);
CREATE TABLE codex_provenance (
	id TEXT PRIMARY KEY,
	prompt_id TEXT NOT NULL,
	source_name TEXT NOT NULL,
	harness TEXT NOT NULL,
	source_file TEXT NOT NULL,
	session_id TEXT NOT NULL,
	record_index INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	ingested_at_watermark TEXT NOT NULL
);
`

func newFixtureDB(t *testing.T, extraSQL string) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "corpus-copy.sqlite3")
	sql := baseDDL + extraSQL
	if err := exec.Command("sqlite3", dbPath, sql).Run(); err != nil {
		t.Fatalf("creating fixture db: %v", err)
	}
	return dbPath
}

func query(t *testing.T, dbPath, sql string) string {
	t.Helper()
	out, err := exec.Command("sqlite3", dbPath, sql).Output()
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return strings.TrimSpace(string(out))
}

func withOneUnit(promptID, provID string) string {
	return `
INSERT INTO prompts (id, at, text_raw, context) VALUES ('` + promptID + `', 1, 'raw text', 'ctx');
INSERT INTO codex_provenance (id, prompt_id, source_name, harness, source_file, session_id, record_index, content_hash, ingested_at_watermark)
VALUES ('` + provID + `', '` + promptID + `', 'codex-rollout', 'codex', 'f.jsonl', 'sess-1', 0, 'hash-1', '2026-09-05T00:00:00Z');
`
}

func TestEndToEndOneCandidatePerJoiningRow(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))

	res, err := Derive(db)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.ProvenanceRows != 1 || res.TotalCandidates != 1 || res.Inserted != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}

	status := query(t, db, "select status from knowledge_candidates;")
	if status != "candidate" {
		t.Fatalf("status = %q, want candidate", status)
	}
	kind := query(t, db, "select kind from knowledge_candidates;")
	if kind != "unclassified" {
		t.Fatalf("kind = %q, want unclassified", kind)
	}
	promptID := query(t, db, "select prompt_id from knowledge_candidates;")
	if promptID != "p1" {
		t.Fatalf("prompt_id = %q, want p1", promptID)
	}
	provID := query(t, db, "select provenance_id from knowledge_candidates;")
	if provID != "prov1" {
		t.Fatalf("provenance_id = %q, want prov1", provID)
	}
}

func TestIdempotentRerunInsertsZero(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1"))

	if _, err := Derive(db); err != nil {
		t.Fatalf("first Derive: %v", err)
	}
	res, err := Derive(db)
	if err != nil {
		t.Fatalf("second Derive: %v", err)
	}
	if res.Inserted != 0 {
		t.Fatalf("second run Inserted = %d, want 0", res.Inserted)
	}
	if res.TotalCandidates != 1 {
		t.Fatalf("second run TotalCandidates = %d, want 1", res.TotalCandidates)
	}
}

func TestMissingDatabaseFailsClosed(t *testing.T) {
	sqliteAvailable(t)
	_, err := Derive(filepath.Join(t.TempDir(), "does-not-exist.sqlite3"))
	if err == nil {
		t.Fatal("expected error for missing database, got nil")
	}
}

func TestMissingProvenanceTableFailsClosed(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "corpus-copy.sqlite3")
	// Only prompts, no codex_provenance -- the table itself is absent.
	if err := exec.Command("sqlite3", dbPath,
		"CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT, context TEXT NOT NULL DEFAULT '', session TEXT, source_file TEXT);").Run(); err != nil {
		t.Fatalf("creating fixture db: %v", err)
	}

	_, err := Derive(dbPath)
	if err == nil {
		t.Fatal("expected error when codex_provenance table is absent, got nil")
	}
}

// TestZeroProvenanceRowsFailsClosed is the mutation-check's first direction
// (agent-estate#1139's brief §4C.1): an empty codex_provenance table must be
// a reported, non-zero-exit condition, never a silent "0 candidates" success.
// This test is written to go RED if Derive is changed to return a zero-value
// Result with a nil error for this case -- see the inline note below.
func TestZeroProvenanceRowsFailsClosed(t *testing.T) {
	sqliteAvailable(t)
	// codex_provenance exists (DDL only, no rows) -- the empty-table case,
	// distinct from the missing-table case above.
	db := newFixtureDB(t, "")

	res, err := Derive(db)
	if err == nil {
		// RED CHECK: if Derive silently succeeded with an empty table, this
		// branch runs and fails the test -- confirming the test can actually
		// catch the silent-empty-run defect this guards against.
		t.Fatalf("expected non-zero-exit error for 0 provenance rows, got success: %+v", res)
	}
	if !strings.Contains(err.Error(), "0 sources found") {
		t.Fatalf("error %q does not report the absence explicitly", err.Error())
	}
}

// TestBrokenTraceabilityFailsClosed is the mutation-check's second direction
// (§4C.2): a provenance row whose prompt_id does NOT resolve to a real
// prompts row must never produce a candidate -- a candidate with no backing
// prompt is a fabricated citation, which this brief calls out as strictly
// worse than a missing candidate.
func TestBrokenTraceabilityFailsClosed(t *testing.T) {
	sqliteAvailable(t)
	// codex_provenance row points at a prompt_id with NO matching prompts row.
	extra := `
INSERT INTO codex_provenance (id, prompt_id, source_name, harness, source_file, session_id, record_index, content_hash, ingested_at_watermark)
VALUES ('prov-orphan', 'no-such-prompt', 'codex-rollout', 'codex', 'f.jsonl', 'sess-1', 0, 'hash-1', '2026-09-05T00:00:00Z');
`
	db := newFixtureDB(t, extra)

	res, err := Derive(db)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if res.TotalCandidates != 0 {
		t.Fatalf("orphaned provenance row produced %d candidates, want 0 -- fabricated citation", res.TotalCandidates)
	}

	orphans := query(t, db, "select count(*) from knowledge_candidates k where not exists (select 1 from prompts p where p.id = k.prompt_id);")
	if orphans != "0" {
		t.Fatalf("found %s candidate(s) with no backing prompt row", orphans)
	}
}

func TestEveryCandidateCitesARealProvenanceRow(t *testing.T) {
	sqliteAvailable(t)
	db := newFixtureDB(t, withOneUnit("p1", "prov1")+withOneUnit("p2", "prov2"))

	if _, err := Derive(db); err != nil {
		t.Fatalf("Derive: %v", err)
	}

	dangling := query(t, db, "select count(*) from knowledge_candidates k where not exists (select 1 from codex_provenance cp where cp.id = k.provenance_id);")
	if dangling != "0" {
		t.Fatalf("found %s candidate(s) with no backing provenance row", dangling)
	}
	total := query(t, db, "select count(*) from knowledge_candidates;")
	if total != "2" {
		t.Fatalf("total candidates = %s, want 2", total)
	}
}
