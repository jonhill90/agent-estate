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

// candidateDDL is the CURRENT shape, used only to CREATE a brand-new table
// (CREATE TABLE IF NOT EXISTS: a no-op against a table that already exists,
// migrated or not). It generalizes the original agent-estate#1244 shape
// (prompt_id/provenance_id NOT NULL, inline UNIQUE(prompt_id)) to let a
// SECOND kind of citation -- a catalogue source (this task's own
// generalization, run/execution-plan.md's knowledge-architecture run) -- coexist in the same queue without EITHER kind
// fabricating a row it doesn't have:
//
//   - source_kind distinguishes the two: 'conversation' (the original
//     prompt_id/provenance_id citation into codex_provenance/prompts) or
//     'catalogue' (a citation into Lane B's source catalogue, held directly
//     on this row -- see CatalogueSource/RegisterCatalogueSource below).
//   - prompt_id/provenance_id are now NOT NULL DEFAULT (empty string) rather
//     than plain NOT NULL: a catalogue-sourced row leaves them empty, never a fabricated
//     prompts-table reference.
//   - the inline UNIQUE(prompt_id) constraint is gone. SQLite unique
//     constraints treat two repeated empty strings as a collision (unlike NULL,
//     where every NULL is distinct), so two catalogue rows sharing an empty prompt_id would
//     have collided under the old constraint. A PARTIAL unique index
//     (WHERE column is non-empty) expresses "unique among real values, unlimited
//     empties" -- which an inline column-level UNIQUE cannot -- for BOTH
//     prompt_id and the new catalogue_source_id.
//
// A table already created under the old shape is upgraded in place by
// migrateToSourceKindSchema below; this constant is never used to alter an
// existing table.
const candidateDDL = `CREATE TABLE IF NOT EXISTS knowledge_candidates (
	id TEXT PRIMARY KEY,
	source_kind TEXT NOT NULL DEFAULT 'conversation',
	prompt_id TEXT NOT NULL DEFAULT '',
	provenance_id TEXT NOT NULL DEFAULT '',
	catalogue_source_id TEXT NOT NULL DEFAULT '',
	catalogue_locator TEXT NOT NULL DEFAULT '',
	content_hash TEXT NOT NULL,
	source TEXT NOT NULL,
	kind TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS knowledge_candidates_prompt_uq ON knowledge_candidates(prompt_id) WHERE prompt_id != '';
CREATE UNIQUE INDEX IF NOT EXISTS knowledge_candidates_catalogue_uq ON knowledge_candidates(catalogue_source_id) WHERE catalogue_source_id != '';`

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

// migrateToSourceKindSchema upgrades a knowledge_candidates table created
// under the pre-catalogue shape (inline UNIQUE(prompt_id), prompt_id/
// provenance_id plain NOT NULL, no source_kind/catalogue_* columns) to the
// current shape. A table that doesn't exist yet, or already has a
// source_kind column, needs no migration -- candidateDDL's own CREATE TABLE
// IF NOT EXISTS already produces the current shape for a brand-new database.
//
// SQLite cannot ALTER a column's NOT NULL-ness or drop an inline UNIQUE
// constraint, so the only honest fix is a full rebuild: create the
// new-shape table under a temporary name, copy every existing row across
// (whichever of decide.go/memory.go's own lazily-added decision/decided_at/
// memory_review columns this particular database actually has -- a given
// corpus copy may have none, some, or all of them), drop the old table, and
// rename -- all inside one BEGIN/COMMIT so a mid-migration crash leaves the
// original table untouched rather than half-renamed.
//
// apply=false performs the same existence/shape checks and reports whether
// a migration is needed, without writing -- the same dry-run contract every
// other write path in this package keeps.
func migrateToSourceKindSchema(dbPath string, apply bool) (migrated bool, err error) {
	exists, err := tableExists(dbPath, "knowledge_candidates")
	if err != nil {
		return false, fmt.Errorf("checking for knowledge_candidates table: %w", err)
	}
	if !exists {
		return false, nil
	}
	hasSourceKind, err := columnExists(dbPath, "knowledge_candidates", "source_kind")
	if err != nil {
		return false, fmt.Errorf("checking for source_kind column: %w", err)
	}
	if hasSourceKind {
		return false, nil
	}
	if !apply {
		return true, nil
	}

	selectCols := "id, prompt_id, provenance_id, content_hash, source, kind, status, created_at"
	insertCols := selectCols
	defaults := map[string]string{"decision": "''", "decided_at": "''", "memory_review": "''"}
	for _, col := range []string{"decision", "decided_at", "memory_review"} {
		has, e := columnExists(dbPath, "knowledge_candidates", col)
		if e != nil {
			return false, fmt.Errorf("checking for %s column: %w", col, e)
		}
		insertCols += ", " + col
		if has {
			selectCols += ", " + col
		} else {
			selectCols += ", " + defaults[col]
		}
	}

	migration := fmt.Sprintf(`BEGIN;
CREATE TABLE knowledge_candidates_v2 (
	id TEXT PRIMARY KEY,
	source_kind TEXT NOT NULL DEFAULT 'conversation',
	prompt_id TEXT NOT NULL DEFAULT '',
	provenance_id TEXT NOT NULL DEFAULT '',
	catalogue_source_id TEXT NOT NULL DEFAULT '',
	catalogue_locator TEXT NOT NULL DEFAULT '',
	content_hash TEXT NOT NULL,
	source TEXT NOT NULL,
	kind TEXT NOT NULL,
	status TEXT NOT NULL,
	created_at TEXT NOT NULL,
	decision TEXT NOT NULL DEFAULT '',
	decided_at TEXT NOT NULL DEFAULT '',
	memory_review TEXT NOT NULL DEFAULT ''
);
INSERT INTO knowledge_candidates_v2 (%s, source_kind, catalogue_source_id, catalogue_locator)
SELECT %s, 'conversation', '', ''
FROM knowledge_candidates;
DROP TABLE knowledge_candidates;
ALTER TABLE knowledge_candidates_v2 RENAME TO knowledge_candidates;
CREATE UNIQUE INDEX knowledge_candidates_prompt_uq ON knowledge_candidates(prompt_id) WHERE prompt_id != '';
CREATE UNIQUE INDEX knowledge_candidates_catalogue_uq ON knowledge_candidates(catalogue_source_id) WHERE catalogue_source_id != '';
COMMIT;`, insertCols, selectCols)

	if err := runWrite(dbPath, migration, "migrate knowledge_candidates to source-kind schema"); err != nil {
		return false, err
	}
	return true, nil
}

// CatalogueSource is the minimal citation shape this package needs from Lane
// B's frozen catalogue API (run/source-api.md, version 1, frozen
// 2026-09-06): field names below are chosen to match run/contract.md's
// source-record fields exactly (`id`, `locator`, `revision`/`hash`) rather
// than inventing parallel names, so integrating Lane B's real
// internal/catalogue.RegisterEntry (once its PR merges and this package
// rebases) is a rename of the call site in main.go, not a reshaping of this
// struct. There is deliberately no "title" field: neither contract.md nor
// source-api.md's RegisterEntry defines one.
type CatalogueSource struct {
	ID          string // contract `id` -- Lane B's Register assigns this as sha256(Locator)[:8], "src-" prefixed
	Locator     string // contract `locator` -- where the original actually is (path/URL/owner/repo)
	ContentHash string // contract `revision`/`hash` -- RegisterEntry.ObservedRevision once integrated
}

// RegisterResult reports what one RegisterCatalogueSource call found and
// did. Existed distinguishes "already registered, this call changed
// nothing" from a fresh insert, mirroring Derive's own
// Inserted/WouldInsert idempotence contract so a caller can print "already
// registered" instead of a misleading "registered" on a rerun.
type RegisterResult struct {
	ID      string // the candidate id this source registers as: "catalogue:" + CatalogueSource.ID
	Existed bool
	Applied bool
}

// RegisterCatalogueSource derives ONE candidate from a catalogue source
// citation -- the catalogue-sourced counterpart to Derive's bulk
// conversation-sourced derivation. It never writes a prompts or
// codex_provenance row: prompt_id/provenance_id stay empty on this row, which
// is this task's own required contract -- external sources never require a
// fabricated prompt row. apply=false performs the same
// existence/validation checks and reports what a real run would do, with
// zero writes, mirroring every other write path in this package.
func RegisterCatalogueSource(dbPath string, src CatalogueSource, apply bool) (RegisterResult, error) {
	if strings.TrimSpace(src.ID) == "" || strings.TrimSpace(src.Locator) == "" || strings.TrimSpace(src.ContentHash) == "" {
		return RegisterResult{}, fmt.Errorf("catalogue source id, locator and content hash are all required")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return RegisterResult{}, fmt.Errorf("corpus database not found at %s: %w", dbPath, err)
	}
	id := "catalogue:" + src.ID

	exists, err := tableExists(dbPath, "knowledge_candidates")
	if err != nil {
		return RegisterResult{}, fmt.Errorf("checking for knowledge_candidates table: %w", err)
	}
	already := false
	if exists {
		n, err := readCount(dbPath, fmt.Sprintf("select count(*) from knowledge_candidates where id='%s';", sqlEscape(id)))
		if err != nil {
			return RegisterResult{}, fmt.Errorf("checking for existing catalogue candidate: %w", err)
		}
		already = n > 0
	}

	res := RegisterResult{ID: id, Existed: already, Applied: apply}
	if !apply {
		return res, nil
	}

	if _, err := migrateToSourceKindSchema(dbPath, true); err != nil {
		return RegisterResult{}, fmt.Errorf("migrating knowledge_candidates to source-kind schema: %w", err)
	}
	if err := runWrite(dbPath, candidateDDL, "create knowledge_candidates table"); err != nil {
		return RegisterResult{}, err
	}
	insertSQL := fmt.Sprintf(`INSERT OR IGNORE INTO knowledge_candidates
	(id, source_kind, catalogue_source_id, catalogue_locator, content_hash, source, kind, status, created_at)
VALUES ('%s', 'catalogue', '%s', '%s', '%s', 'catalogue', 'unclassified', 'candidate', '%s');`,
		sqlEscape(id), sqlEscape(src.ID), sqlEscape(src.Locator), sqlEscape(src.ContentHash), sqlEscape(nowFunc()))
	if err := runWrite(dbPath, insertSQL, "insert catalogue candidate"); err != nil {
		return RegisterResult{}, err
	}
	return res, nil
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
