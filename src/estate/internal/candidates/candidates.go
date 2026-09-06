// Package candidates derives a reviewable, quarantined queue of CANDIDATE
// knowledge records from the codex_provenance rows written by cmd/codexingest
// (agent-estate#1139). It is the FIRST surface over those 4,360 rows -- until
// this package existed they sat in the corpus with no queue an operator could
// look at.
//
// # Candidate inbox discipline, not promotion
//
// A candidate here is cited (it names the exact codex_provenance row and the
// exact prompts row it came from) and permanently quarantined at
// status='candidate' by this package. Nothing in this file ever writes any
// other status -- promoting a candidate into durable knowledge is a separate,
// reviewed act implemented by memory.go (the settled
// vault fact this follows is knowledge-architecture-operating-design:
// "candidate inbox = ingestion quarantine; cited, then promoted or discarded
// by review; never silent promotion").
//
// # The review surface (review.go, decide.go)
//
// This file (candidates.go) is derivation only -- it never writes anything
// but status='candidate'. review.go adds the reviewable half this task
// (agent-estate#1139's follow-up) builds: List and Get resolve a
// candidate's cited prompt/provenance ON DEMAND, never by copying text into
// a new table. decide.go adds a decision column (decision, decided_at) a
// reviewer sets to 'promoted' or 'discarded' -- a REVIEW record, not a
// promotion: it never moves anything into a durable knowledge store, never
// changes status or kind. memory.go writes reviewed facts into the existing
// Agent Memory vault and stores publication state separately. See decide.go for
// the write-gating shape (mirrors cmd/codexingest's own guard exactly).
//
// # Why kind is always "unclassified"
//
// The Codex ingest (cmd/codexingest) writes prompt-level rows only --
// itemization (deriving a kind, a clean body) has not run against them. A
// join from codex_provenance to items returns zero rows for every one of
// them (measured 2026-09-05, agent-estate#1139's own dispatch brief). Since
// there is nothing to classify FROM, Derive never infers a kind: 'unclassified'
// is a literal, not a default standing in for a real inference.
//
// # Why prompt text is never copied here
//
// A candidate is a citation, not a copy: it carries prompt_id and
// provenance_id so a reviewer reads the prompt through those foreign keys on
// demand. text_raw is never propagated into a new surface (it is the one
// column that feeds public quoting paths), and text_clean is measured absent
// for every one of these rows anyway.
//
// # Why sqlite3 CLI, not a Go driver
//
// Matches internal/corpus and cmd/codexingest's own choice for the same
// database: dependency-free, and the corpus's own doc comment already
// documents the mode=ro flakiness this package's read paths avoid by using
// the same "file:...?mode=ro&immutable=1" URI form codexingest settled on.
package candidates

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Result reports what one Derive call found and did. ProvenanceRows and
// TotalCandidates are read back from the database after the run, never
// computed in Go, so a caller's own report matches what a `select count(*)`
// against the same file would show.
//
// Applied distinguishes a dry run (Applied=false: every count below is read
// against the database exactly as it stood on entry, and nothing was
// written -- not even knowledge_candidates' own CREATE TABLE) from a real
// write, mirroring Decide's own DecideResult.Applied contract
// (agent-estate#1251).
type Result struct {
	ProvenanceRows  int  // rows in codex_provenance
	TableExists     bool // whether knowledge_candidates already existed on entry
	TotalCandidates int  // rows in knowledge_candidates on entry (0 if TableExists is false)
	WouldInsert     int  // NEW rows a run would add, valid whether or not Applied
	Inserted        int  // NEW rows this run actually added; 0 when Applied is false
	Applied         bool
}

const candidateDDL = `CREATE TABLE IF NOT EXISTS knowledge_candidates (
	id TEXT PRIMARY KEY,
	prompt_id TEXT NOT NULL,
	provenance_id TEXT NOT NULL,
	content_hash TEXT NOT NULL,
	source TEXT NOT NULL,
	kind TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	UNIQUE(prompt_id)
);`

func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// runReadOnly opens dbPath via the URI mode=ro&immutable=1 form -- see this
// package's own doc comment for why the bare -readonly flag is avoided.
func runReadOnly(dbPath string, sql string) (string, error) {
	uri := fmt.Sprintf("file:%s?mode=ro&immutable=1", dbPath)
	cmd := exec.Command("sqlite3", uri, sql)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("sqlite3 read failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("sqlite3 read failed: %w", err)
	}
	return string(out), nil
}

func runWrite(dbPath, sql, context string) error {
	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite3 write (%s) failed: %s", context, strings.TrimSpace(string(out)))
	}
	return nil
}

func readCount(dbPath, sql string) (int, error) {
	out, err := runReadOnly(dbPath, sql)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parsing count from %q: %w", sql, err)
	}
	return n, nil
}

func tableExists(dbPath, name string) (bool, error) {
	out, err := runReadOnly(dbPath, fmt.Sprintf(
		"select name from sqlite_master where type='table' and name='%s';", sqlEscape(name)))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// nowFunc is overridable in tests so a fixture's created_at is deterministic.
var nowFunc = func() string { return time.Now().UTC().Format(time.RFC3339) }

// insertSQL is shared between the real write and the dry-run WouldInsert
// count below -- the dry run wraps the exact same SELECT in `select
// count(*) from (...)` rather than hand-maintaining a second predicate that
// could drift from what an -apply run actually inserts.
func deriveSelectSQL() string {
	return `SELECT cp.id, cp.prompt_id, cp.id, cp.content_hash, 'codex_provenance', 'unclassified', 'candidate'
FROM codex_provenance cp
JOIN prompts p ON p.id = cp.prompt_id`
}

// wouldInsertCount reports how many NEW rows a real run would add, read-only
// and without requiring knowledge_candidates to exist yet: when it doesn't,
// every joining row is new by definition, so the NOT EXISTS clause (which
// would fail against a table that isn't there) is only added once the table
// is known to exist.
func wouldInsertCount(dbPath string, tableExists bool) (int, error) {
	sql := deriveSelectSQL()
	if tableExists {
		sql += " WHERE NOT EXISTS (SELECT 1 FROM knowledge_candidates k WHERE k.prompt_id = cp.prompt_id)"
	}
	return readCount(dbPath, fmt.Sprintf("select count(*) from (%s);", sql))
}

// Derive is the one entry point: it reads dbPath's codex_provenance and
// prompts tables and reports (apply=false) or writes (apply=true) one
// candidate row per codex_provenance row whose prompt_id resolves to a real
// prompts row. It is idempotent (UNIQUE(prompt_id) plus INSERT OR IGNORE): a
// re-run against an unchanged database inserts zero new rows and reports the
// same TotalCandidates.
//
// apply=false performs every read a real run would -- provenance and
// existing-candidate counts, and the exact WouldInsert count a real run
// would produce -- but calls neither the knowledge_candidates CREATE TABLE
// nor the INSERT. This is the same "dry run means dry run" contract
// Decide's own apply flag keeps (agent-estate#1251): the live-corpus
// authorization gate lives in main.go, via internal/livepath, exactly as it
// does for Decide; this function's only job is to not write when told not
// to.
//
// Fails closed, never silently exit-0-empty:
//   - dbPath does not exist -> error
//   - codex_provenance table does not exist -> error
//   - codex_provenance has zero rows -> error ("0 sources found" is a
//     reported condition, not a quiet empty run)
func Derive(dbPath string, apply bool) (Result, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return Result{}, fmt.Errorf("corpus database not found at %s: %w", dbPath, err)
	}

	exists, err := tableExists(dbPath, "codex_provenance")
	if err != nil {
		return Result{}, fmt.Errorf("checking for codex_provenance table: %w", err)
	}
	if !exists {
		return Result{}, fmt.Errorf("codex_provenance table not found in %s -- nothing to derive candidates from", dbPath)
	}

	provenanceRows, err := readCount(dbPath, "select count(*) from codex_provenance;")
	if err != nil {
		return Result{}, fmt.Errorf("counting codex_provenance rows: %w", err)
	}
	if provenanceRows == 0 {
		return Result{}, fmt.Errorf("0 sources found in codex_provenance -- refusing rather than reporting an empty run as success")
	}

	candidatesExists, err := tableExists(dbPath, "knowledge_candidates")
	if err != nil {
		return Result{}, fmt.Errorf("checking for knowledge_candidates table: %w", err)
	}

	before := 0
	if candidatesExists {
		before, err = readCount(dbPath, "select count(*) from knowledge_candidates;")
		if err != nil {
			return Result{}, fmt.Errorf("counting existing knowledge_candidates rows: %w", err)
		}
	}

	wouldInsert, err := wouldInsertCount(dbPath, candidatesExists)
	if err != nil {
		return Result{}, fmt.Errorf("counting rows this run would insert: %w", err)
	}

	res := Result{
		ProvenanceRows:  provenanceRows,
		TableExists:     candidatesExists,
		TotalCandidates: before,
		WouldInsert:     wouldInsert,
		Applied:         apply,
	}
	if !apply {
		return res, nil
	}

	if err := runWrite(dbPath, candidateDDL, "create knowledge_candidates table"); err != nil {
		return Result{}, err
	}

	insertSQL := fmt.Sprintf(`INSERT OR IGNORE INTO knowledge_candidates
	(id, prompt_id, provenance_id, content_hash, source, kind, status, created_at)
SELECT cp.id, cp.prompt_id, cp.id, cp.content_hash, 'codex_provenance', 'unclassified', 'candidate', '%s'
FROM codex_provenance cp
JOIN prompts p ON p.id = cp.prompt_id;`, sqlEscape(nowFunc()))
	if err := runWrite(dbPath, insertSQL, "insert candidates"); err != nil {
		return Result{}, err
	}

	after, err := readCount(dbPath, "select count(*) from knowledge_candidates;")
	if err != nil {
		return Result{}, fmt.Errorf("counting knowledge_candidates rows after insert: %w", err)
	}

	res.TableExists = true
	res.TotalCandidates = after
	res.Inserted = after - before
	return res, nil
}
