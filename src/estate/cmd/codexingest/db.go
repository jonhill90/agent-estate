// db.go is codexingest's write half: it turns a validated UnitPlan
// (ingest.go) into rows in a SQLite corpus COPY, via the sqlite3 CLI --
// dependency-free, matching cmd/provenancebackfill's own choice for the same
// database. It never opens the live corpus; main.go's use of
// internal/livepath.RefuseLivePath is what enforces that, before any
// function here is ever called.
//
// # Two rows, one row's worth of atomicity
//
// Each ingested unit writes two rows -- a new prompts row (the corpus's own
// table) and a new codex_provenance row naming its identity -- and both
// belong to the SAME unit, so both must land or neither does. Each insertUnit
// call wraps its two INSERTs in one BEGIN/COMMIT sent to a single sqlite3
// invocation, so a mid-write failure (a full disk, a killed process) leaves
// neither row rather than a prompts row with no provenance to explain it.
//
// # Raw text never appears in an error message
//
// insertUnit's own SQL argument does carry text_raw -- the one and only
// place this command puts an operator's raw words. If the INSERT fails,
// runSQLiteWrite reports sqlite3's own stderr (a constraint name, a syntax
// position -- never the values that were bound) and nothing else; it never
// echoes back the query string that failed, which is the one thing here
// that could leak text_raw into a log line, a CI failure output, or a PR
// comment.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const codexProvenanceDDL = `CREATE TABLE IF NOT EXISTS codex_provenance (
	id TEXT PRIMARY KEY,
	prompt_id TEXT NOT NULL,
	source_name TEXT NOT NULL,
	harness TEXT NOT NULL,
	source_file TEXT NOT NULL,
	session_id TEXT NOT NULL,
	record_index INTEGER NOT NULL,
	content_hash TEXT NOT NULL,
	ingested_at_watermark TEXT NOT NULL
);`

func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// dbFileMissing reports whether dbPath does not exist yet -- the one case a
// read-only call site here must treat as "nothing to report" rather than an
// error, exactly as cmd/provenancebackfill's own dbFileMissing does for the
// same reason (the bare sqlite3 CLI creates its target even for a SELECT
// unless -readonly is passed, and -readonly itself fails to open a path with
// nothing there yet).
func dbFileMissing(dbPath string) bool {
	_, err := os.Stat(dbPath)
	return os.IsNotExist(err)
}

// runSQLiteReadOnly opens dbPath via the "file:...?mode=ro&immutable=1" URI
// form, not the bare -readonly flag cmd/provenancebackfill uses -- this was
// measured against a REAL `.backup`'d corpus copy (agent-estate#1139's own
// evidence run) and confirmed to hit the exact flakiness
// internal/corpus.go's own doc comment already names: "-readonly has been
// observed failing with 'unable to open database file (14)' under WAL
// contention while file:...?mode=ro succeeded on the same file seconds
// later." A corpus copy inherits the live corpus's own WAL journal mode
// (confirmed via PRAGMA journal_mode against a real backup), so this is not
// a hypothetical for this command's own primary use case. immutable=1 is
// correct here specifically because dbPath is a static copy this process
// does not expect to change for the lifetime of one query.
func runSQLiteReadOnly(dbPath string, args ...string) (string, error) {
	uri := fmt.Sprintf("file:%s?mode=ro&immutable=1", dbPath)
	full := append([]string{uri}, args...)
	cmd := exec.Command("sqlite3", full...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("sqlite3 %v: %s", args, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("sqlite3 %v: %w", args, err)
	}
	return string(out), nil
}

// runSQLiteWrite issues one write statement (or one BEGIN...COMMIT block)
// against dbPath. On failure it reports sqlite3's own stderr, tagged with
// caller-supplied context -- never the sql argument itself, which may carry
// an operator's raw text (see this file's own doc comment).
func runSQLiteWrite(dbPath, sql, context string) error {
	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite3 write (%s) failed: %s", context, strings.TrimSpace(string(out)))
	}
	return nil
}

func codexProvenanceTableExists(dbPath string) (bool, error) {
	if dbFileMissing(dbPath) {
		return false, nil
	}
	out, err := runSQLiteReadOnly(dbPath, "select name from sqlite_master where type='table' and name='codex_provenance';")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// ensureCodexProvenanceTable creates codex_provenance if it does not exist
// yet. Callers must only invoke this under -apply -- a dry run must never
// write to -db, live or not (mirrors cmd/provenancebackfill's own
// apply-gated ensureAttributionTable).
func ensureCodexProvenanceTable(dbPath string) error {
	return runSQLiteWrite(dbPath, codexProvenanceDDL, "create codex_provenance table")
}

func countCodexProvenanceRows(dbPath string) (int, error) {
	if dbFileMissing(dbPath) {
		return 0, nil
	}
	out, err := runSQLiteReadOnly(dbPath, "select count(*) from codex_provenance;")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parsing codex_provenance count: %w", err)
	}
	return n, nil
}

// existingCodexProvenanceIDs returns every id already present in
// codex_provenance -- checked once per run so a rerun at the same watermark
// reports exact already_ingested counts, never an inferred one, and so
// insertPlan can skip a unit BEFORE issuing any write for it (idempotent
// rerun means zero write attempts for an already-ingested unit, not merely
// zero net rows after an INSERT OR IGNORE).
func existingCodexProvenanceIDs(dbPath string) (map[string]bool, error) {
	if dbFileMissing(dbPath) {
		return map[string]bool{}, nil
	}
	exists, err := codexProvenanceTableExists(dbPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]bool{}, nil
	}
	out, err := runSQLiteReadOnly(dbPath, "select id from codex_provenance;")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			set[line] = true
		}
	}
	return set, nil
}

// insertUnit writes one unit's prompts row and codex_provenance row in a
// single transaction. id is provenance.UnitProvenance.ID() -- the SAME
// idempotent identity hash the shared contract already defines, reused
// directly as the new prompts row's own primary key (this command MINTS new
// prompts rows, unlike cmd/provenancebackfill which only attributes existing
// ones, so there is no pre-existing prompts.id to preserve here).
func insertUnit(dbPath, id, text string, at int64, sourceName, harness, sourceFile, sessionID string, recordIndex int, contentHash, watermark string) error {
	sourceBase := filepath.Base(sourceFile)
	q := fmt.Sprintf(`BEGIN;
INSERT INTO prompts (id, at, text_raw, context, session, source_file) VALUES ('%s', %d, '%s', '', '%s', '%s');
INSERT INTO codex_provenance (id, prompt_id, source_name, harness, source_file, session_id, record_index, content_hash, ingested_at_watermark) VALUES ('%s','%s','%s','%s','%s','%s',%d,'%s','%s');
COMMIT;`,
		sqlEscape(id), at, sqlEscape(text), sqlEscape(sessionID), sqlEscape(sourceBase),
		sqlEscape(id), sqlEscape(id), sqlEscape(sourceName), sqlEscape(harness), sqlEscape(sourceFile), sqlEscape(sessionID), recordIndex, sqlEscape(contentHash), sqlEscape(watermark))
	return runSQLiteWrite(dbPath, q, fmt.Sprintf("unit %s", id))
}
