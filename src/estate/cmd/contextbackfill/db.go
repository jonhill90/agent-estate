// db.go is contextbackfill's own SQLite CLI plumbing -- dependency-free,
// mirroring cmd/codexingest's db.go and cmd/provenancebackfill's attribute.go
// (each command keeps its own copy of this thin exec-wrapper; there is no
// shared package for it in this repo today, and inventing one here is out of
// this task's scope). It never opens the live corpus; main.go's use of
// internal/livepath.RefuseLivePath is what enforces that, before any
// function here is ever called.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// sep matches internal/corpus's own choice (0x1f, ASCII unit separator) --
// vanishingly unlikely to appear in a source_file path or a session id, and
// consistent with the rest of this codebase's sqlite3-CLI reads.
const sep = "\x1f"

const promptContextDDL = `CREATE TABLE IF NOT EXISTS prompt_context (
	prompt_id TEXT PRIMARY KEY,
	provenance_id TEXT NOT NULL,
	state TEXT NOT NULL,
	role TEXT NOT NULL,
	truncated INTEGER NOT NULL,
	char_cap INTEGER NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	derived_at_watermark TEXT NOT NULL
);`

func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// dbFileMissing mirrors cmd/codexingest's own helper: the bare sqlite3 CLI
// creates its target even for a SELECT unless -readonly/mode=ro is passed,
// and mode=ro itself fails to open a path with nothing there yet -- so a
// read-only call site must tell "database file doesn't exist yet" apart from
// a real error before deciding what "0 rows" means.
func dbFileMissing(dbPath string) bool {
	_, err := os.Stat(dbPath)
	return os.IsNotExist(err)
}

// runSQLiteReadOnly opens dbPath via the "file:...?mode=ro&immutable=1" URI
// form -- see cmd/codexingest's db.go doc comment for why this form was
// chosen over the bare -readonly flag (measured WAL-contention flakiness).
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
// derived assistant text (see this command's package doc comment: the
// derived text is metadata, but still subject to text_raw's own
// never-in-a-log discipline).
func runSQLiteWrite(dbPath, sql, context string) error {
	cmd := exec.Command("sqlite3", dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sqlite3 write (%s) failed: %s", context, strings.TrimSpace(string(out)))
	}
	return nil
}

func promptContextTableExists(dbPath string) (bool, error) {
	if dbFileMissing(dbPath) {
		return false, nil
	}
	out, err := runSQLiteReadOnly(dbPath, "select name from sqlite_master where type='table' and name='prompt_context';")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// ensurePromptContextTable creates prompt_context if it does not exist yet.
// Callers must only invoke this under -apply -- a dry run must never write
// to -db, live or not.
func ensurePromptContextTable(dbPath string) error {
	return runSQLiteWrite(dbPath, promptContextDDL, "create prompt_context table")
}

func countPromptContextRows(dbPath string) (int, error) {
	if dbFileMissing(dbPath) {
		return 0, nil
	}
	out, err := runSQLiteReadOnly(dbPath, "select count(*) from prompt_context;")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parsing prompt_context count: %w", err)
	}
	return n, nil
}

// alreadyProcessedPromptIDs returns every prompt_id already present in
// prompt_context -- a defensive belt-and-suspenders check. The PRIMARY
// idempotency mechanism is the candidate selection query itself
// (`context = ”`, see selectCandidates): once a row's context is written,
// it is non-empty and is never selected again. This second check exists
// only to make a rerun's zero-write guarantee hold even if some other future
// path ever wrote to prompt_context without also clearing prompts.context.
func alreadyProcessedPromptIDs(dbPath string) (map[string]bool, error) {
	if dbFileMissing(dbPath) {
		return map[string]bool{}, nil
	}
	exists, err := promptContextTableExists(dbPath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return map[string]bool{}, nil
	}
	out, err := runSQLiteReadOnly(dbPath, "select prompt_id from prompt_context;")
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

// candidate is one prompts row still waiting for its context to be derived,
// joined against the codex_provenance row codexingest wrote for it -- the
// exact three-field address (source_file, session_id, record_index) this
// task's brief names, plus content_hash to detect a source that changed
// shape since ingestion.
type candidate struct {
	PromptID     string
	ProvenanceID string
	SourceFile   string
	SessionID    string
	RecordIndex  int
	ContentHash  string
}

// selectCandidates reads every prompts row still carrying context = ” that
// has a codex_provenance row (rows from a different source, or rows that
// were never ingested by codexingest at all, are out of this command's
// scope -- it addresses ONLY what codex_provenance names, never a bare
// prompts row with no provenance to resolve). Ordered by prompt id so two
// runs against an unchanged db enumerate candidates in the same order,
// which is what makes two dry runs byte-identical.
func selectCandidates(dbPath string) ([]candidate, error) {
	if dbFileMissing(dbPath) {
		return nil, nil
	}
	q := `select p.id, c.id, c.source_file, c.session_id, c.record_index, c.content_hash
	      from prompts p join codex_provenance c on c.prompt_id = p.id
	      where p.context = ''
	      order by p.id;`
	out, err := runSQLiteReadOnly(dbPath, "-separator", sep, q)
	if err != nil {
		return nil, err
	}
	var out2 []candidate
	s := bufio.NewScanner(strings.NewReader(out))
	s.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for s.Scan() {
		line := s.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, sep, 6)
		if len(parts) != 6 {
			return nil, fmt.Errorf("selectCandidates: malformed row %q", line)
		}
		idx, err := strconv.Atoi(strings.TrimSpace(parts[4]))
		if err != nil {
			return nil, fmt.Errorf("selectCandidates: record_index %q: %w", parts[4], err)
		}
		out2 = append(out2, candidate{
			PromptID:     parts[0],
			ProvenanceID: parts[1],
			SourceFile:   parts[2],
			SessionID:    parts[3],
			RecordIndex:  idx,
			ContentHash:  parts[5],
		})
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	return out2, nil
}

// writeContext is one candidate's whole write: the prompts row's context is
// replaced (only if it is STILL ” -- belt-and-suspenders against a
// concurrent writer) and one prompt_context row is inserted, in a single
// transaction, so a mid-write failure leaves neither half done rather than a
// context with no citation explaining where it came from.
func writeContext(dbPath string, c candidate, env contextEnvelope, watermark string) error {
	blob, err := marshalEnvelope(env)
	if err != nil {
		return fmt.Errorf("marshalling context envelope for prompt %s: %w", c.PromptID, err)
	}
	truncatedInt := 0
	if env.Truncated {
		truncatedInt = 1
	}
	q := fmt.Sprintf(`BEGIN;
UPDATE prompts SET context = '%s' WHERE id = '%s' AND context = '';
INSERT INTO prompt_context (prompt_id, provenance_id, state, role, truncated, char_cap, detail, derived_at_watermark)
VALUES ('%s','%s','%s','%s',%d,%d,'%s','%s');
COMMIT;`,
		sqlEscape(blob), sqlEscape(c.PromptID),
		sqlEscape(c.PromptID), sqlEscape(c.ProvenanceID), sqlEscape(env.State), sqlEscape(env.Role),
		truncatedInt, maxContextRunes, sqlEscape(env.Detail), sqlEscape(watermark))
	return runSQLiteWrite(dbPath, q, fmt.Sprintf("context for prompt %s", c.PromptID))
}
