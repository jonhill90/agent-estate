package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
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

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// writeRolloutFixture writes lines to a temp .jsonl file and returns its
// path. Every string here is invented fixture text -- never a real captured
// operator prompt (agent-estate#1139's "raw text ... never into a test
// fixture" governs the CORPUS's own text_raw column, not what a synthetic
// fixture may contain to exercise this parser).
func writeRolloutFixture(t *testing.T, dir, name string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing fixture: %v", err)
	}
	return path
}

// runCapture mirrors cmd/provenancebackfill's own test helper: run() takes
// real *os.File values, so tests capture its stdout/stderr via a pipe rather
// than reading return values that don't exist.
func runCapture(t *testing.T, args []string) (stdout, stderr string, exit int) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	exit = run(args, outW, errW)
	outW.Close()
	errW.Close()
	stdout = drainPipe(t, outR)
	stderr = drainPipe(t, errR)
	return stdout, stderr, exit
}

func drainPipe(t *testing.T, r *os.File) string {
	t.Helper()
	buf, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(buf)
}

// newTestCorpus creates a fresh SQLite corpus copy with the real schema
// (mirrors cmd/provenancebackfill's own newTestCorpus DDL) and no rows --
// codexingest mints new prompts rows, it never matches against existing
// ones.
func newTestCorpus(t *testing.T, dir string) string {
	t.Helper()
	dbPath := filepath.Join(dir, "corpus-copy.sqlite3")
	ddl := `CREATE TABLE prompts (id TEXT PRIMARY KEY, at INTEGER NOT NULL, text_raw TEXT NOT NULL, text_clean TEXT, context TEXT NOT NULL DEFAULT '', session TEXT, source_file TEXT);`
	if err := exec.Command("sqlite3", dbPath, ddl).Run(); err != nil {
		t.Fatalf("creating test corpus: %v", err)
	}
	return dbPath
}

func queryCount(t *testing.T, dbPath, sql string) int {
	t.Helper()
	out, err := exec.Command("sqlite3", dbPath, sql).Output()
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	s := strings.TrimSpace(string(out))
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("query %q returned non-numeric %q", sql, s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func queryOne(t *testing.T, dbPath, sql string) string {
	t.Helper()
	out, err := exec.Command("sqlite3", dbPath, sql).Output()
	if err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}

// manifestFixture builds a corpusextract-shaped manifest JSON file by
// running the REAL cmd/corpusextract binary against root -- proving this
// command consumes what corpusextract actually produces, not a hand-typed
// stand-in for its shape.
func manifestFixture(t *testing.T, dir, root string) string {
	t.Helper()
	out := filepath.Join(dir, "manifest.json")
	cmd := exec.Command("go", "run", "../corpusextract", "-root", root, "-json", "-out", out)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running corpusextract: %v\n%s", err, output)
	}
	return out
}

func readManifestEntryCount(t *testing.T, path string) int {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var m struct {
		Entries []json.RawMessage `json:"entries"`
	}
	if err := json.NewDecoder(f).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return len(m.Entries)
}

// TestEndToEndInsertsExactManifestCount is the core deliverable: a manifest
// built by the real corpusextract binary, ingested by codexingest, inserts
// exactly the manifest's own entry count -- not approximately.
func TestEndToEndInsertsExactManifestCount(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"fixture-session-1"}}`,
		`{"timestamp":"2026-01-01T00:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: genuine operator turn one"}]}}`,
		`{"timestamp":"2026-01-01T00:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fixture: assistant reply, never ingested"}]}}`,
		`{"timestamp":"2026-01-01T00:00:03Z","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"fixture: injected instruction, never ingested"}]}}`,
		`{"timestamp":"2026-01-01T00:00:04Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: genuine operator turn two"}]}}`,
	})

	manifestPath := manifestFixture(t, dir, root)
	wantEntries := readManifestEntryCount(t, manifestPath)
	if wantEntries != 2 {
		t.Fatalf("manifest entries = %d, want 2 (the two genuine user turns only)", wantEntries)
	}

	dbPath := newTestCorpus(t, dir)

	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-manifest", manifestPath, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "inserted: 2") {
		t.Errorf("stdout = %q, want \"inserted: 2\"", stdout)
	}
	if !strings.Contains(stdout, "accounting: 2 entries in, 2 entries decided") {
		t.Errorf("stdout = %q, want exact accounting", stdout)
	}

	gotRows := queryCount(t, dbPath, "select count(*) from prompts;")
	if gotRows != 2 {
		t.Fatalf("prompts row count = %d, want 2", gotRows)
	}
	gotProv := queryCount(t, dbPath, "select count(*) from codex_provenance;")
	if gotProv != 2 {
		t.Fatalf("codex_provenance row count = %d, want 2", gotProv)
	}

	harness := queryOne(t, dbPath, "select distinct harness from codex_provenance;")
	if harness != "codex" {
		t.Errorf("codex_provenance.harness = %q, want \"codex\"", harness)
	}
}

// TestIdempotentRerunInsertsZero locks agent-estate#1139's own requirement:
// re-running at the same watermark against the same db inserts zero.
func TestIdempotentRerunInsertsZero(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: rerun turn"}]}}`,
	})
	manifestPath := manifestFixture(t, dir, root)
	dbPath := newTestCorpus(t, dir)

	_, stderr, exit := runCapture(t, []string{"-db", dbPath, "-manifest", manifestPath, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("first run exit = %d, stderr=%s", exit, stderr)
	}
	firstCount := queryCount(t, dbPath, "select count(*) from prompts;")
	if firstCount != 1 {
		t.Fatalf("first run prompts count = %d, want 1", firstCount)
	}

	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-manifest", manifestPath, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("second run exit = %d, stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "inserted: 0") {
		t.Errorf("second run stdout = %q, want \"inserted: 0\"", stdout)
	}
	if !strings.Contains(stdout, "already ingested (idempotent skip): 1") {
		t.Errorf("second run stdout = %q, want 1 already-ingested", stdout)
	}
	secondCount := queryCount(t, dbPath, "select count(*) from prompts;")
	if secondCount != 1 {
		t.Fatalf("prompts count after rerun = %d, want still 1 (zero new rows)", secondCount)
	}
}

// TestRejectSourceChange locks "reject source change": a source file whose
// content no longer matches what the manifest recorded is refused, not
// ingested under a stale hash.
func TestRejectSourceChange(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: original text"}]}}`,
	})
	manifestPath := manifestFixture(t, dir, root)
	dbPath := newTestCorpus(t, dir)

	// Mutate the source file's own turn text AFTER the manifest was built,
	// same record shape and line count -- exactly the case the manifest's
	// recorded hash must catch.
	if err := os.WriteFile(fixturePath,
		[]byte(`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: CHANGED text"}]}}`+"\n"),
		0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-manifest", manifestPath, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "inserted: 0") {
		t.Errorf("stdout = %q, want \"inserted: 0\" -- the changed unit must not be ingested", stdout)
	}
	if !strings.Contains(stdout, "refused, source changed since manifest: 1") {
		t.Errorf("stdout = %q, want the changed unit refused and reported", stdout)
	}
	gotRows := queryCount(t, dbPath, "select count(*) from prompts;")
	if gotRows != 0 {
		t.Fatalf("prompts row count = %d, want 0 (changed unit must never be written)", gotRows)
	}
}

// TestDryRunAgainstFreshCorpusNeverCreatesTable locks a regression this
// command's first real-corpus evidence run hit: a dry run (no -apply) must
// treat "codex_provenance doesn't exist yet" as before=0, never a hard error
// from a bare SELECT against a missing table, and it must write nothing.
func TestDryRunAgainstFreshCorpusNeverCreatesTable(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: dry-run-only turn"}]}}`,
	})
	manifestPath := manifestFixture(t, dir, root)
	dbPath := newTestCorpus(t, dir) // fresh copy, no codex_provenance table yet

	stdout, stderr, exit := runCapture(t, []string{"-db", dbPath, "-manifest", manifestPath, "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("dry run exit = %d, want 0; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stdout, "mode: dry-run") {
		t.Errorf("stdout = %q, want dry-run mode", stdout)
	}
	if !strings.Contains(stdout, "codex_provenance rows before: 0") {
		t.Errorf("stdout = %q, want before=0 against a table that doesn't exist yet", stdout)
	}
	if !strings.Contains(stdout, "inserted: 1") {
		t.Errorf("stdout = %q, want the dry run to report what it WOULD insert", stdout)
	}

	tables := queryOne(t, dbPath, "select count(*) from sqlite_master where type='table' and name='codex_provenance';")
	if tables != "0" {
		t.Fatalf("codex_provenance table count = %s, want 0 -- a dry run must never CREATE it", tables)
	}
	gotRows := queryCount(t, dbPath, "select count(*) from prompts;")
	if gotRows != 0 {
		t.Fatalf("prompts row count = %d, want 0 -- a dry run must write nothing", gotRows)
	}
}

// TestRefusesLiveCorpusPath proves this command reuses
// internal/livepath.RefuseLivePath rather than a second guard: pointing -db
// at $ESTATE_CORPUS is refused with zero writes.
func TestRefusesLiveCorpusPath(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: live-refusal turn"}]}}`,
	})
	manifestPath := manifestFixture(t, dir, root)
	livePath := newTestCorpus(t, dir)
	t.Setenv("ESTATE_CORPUS", livePath)

	_, stderr, exit := runCapture(t, []string{"-db", livePath, "-manifest", manifestPath, "-apply", "-sessions-root", root})
	if exit == 0 {
		t.Fatalf("exit = 0, want refusal against the live corpus path; stderr=%s", stderr)
	}
	if !strings.Contains(stderr, "refusing") {
		t.Errorf("stderr = %q, want a refusal message", stderr)
	}
	gotRows := queryCount(t, livePath, "select count(*) from prompts;")
	if gotRows != 0 {
		t.Fatalf("prompts row count = %d, want 0 -- a refused run must write nothing", gotRows)
	}
}

// TestSourcesNeverTouched proves the fixture file's own mtime is unchanged
// by a run, matching the "sources stay read-only including touch"
// constraint.
func TestSourcesNeverTouched(t *testing.T) {
	sqliteAvailable(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixturePath := writeRolloutFixture(t, root, "a.jsonl", []string{
		`{"timestamp":"2026-01-01T00:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"fixture: touch-check turn"}]}}`,
	})
	manifestPath := manifestFixture(t, dir, root)
	dbPath := newTestCorpus(t, dir)

	before, err := os.Stat(fixturePath)
	if err != nil {
		t.Fatal(err)
	}

	_, stderr, exit := runCapture(t, []string{"-db", dbPath, "-manifest", manifestPath, "-apply", "-sessions-root", root})
	if exit != 0 {
		t.Fatalf("exit = %d, stderr=%s", exit, stderr)
	}

	after, err := os.Stat(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Errorf("fixture mtime changed: before=%s after=%s", before.ModTime(), after.ModTime())
	}
}
