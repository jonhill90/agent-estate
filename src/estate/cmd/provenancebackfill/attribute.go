// attribute.go is the read/write half of provenancebackfill: it matches
// extracted Unit values (extract.go) against the prompts rows already in a
// SQLite database and, in -apply mode, writes one claude_provenance row per
// match. It shells out to the sqlite3 CLI rather than importing a driver --
// the same "dependency-free" choice internal/corpus documents for its own
// reads, kept consistent here since this is the same corpus database.
//
// # This file NEVER opens the live corpus
//
// Every function here takes dbPath as an explicit argument and does nothing
// to resolve or default it. main.go is the one place that refuses to run
// -apply against a path that resolves to the live corpus (internal/corpus.Path()
// or the retired agent-dotfiles-supervisor location) -- see main.go's
// refuseLivePath. That refusal is enforced in-process, on top of (not
// instead of) the ledger-write-guard hook, because this task's brief is
// explicit that the live corpus is a separate, later, explicitly authorized
// step this tool must never take on its own.
package main

import (
	"bufio"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"github.com/jonhill90/agent-estate/estate/internal/provenance"
)

const sep = "\x1f"

// promptRow is one existing prompts row, read back only far enough to match
// it against an extracted Unit: its id and a hash of its own text_raw. The
// raw text itself is never retained past the hash computation, in keeping
// with the "no raw operator prompts anywhere durable this tool writes" rule
// -- see hashPromptRow.
type promptRow struct {
	ID   string
	Hash string
}

func sqlEscape(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func runSQLite(dbPath string, args ...string) (string, error) {
	full := append([]string{dbPath}, args...)
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

// ensureAttributionTable creates the attribution table if it does not exist
// yet. id is the PRIMARY KEY and is exactly provenance.UnitProvenance.ID()
// -- the SAME idempotent identity hash the shared contract already defines,
// never a second identity scheme (this task's brief: "rely on the existing
// length-prefixed identity hash for idempotence; do not invent a second
// identity scheme"). A rerun's INSERT against an id already present is
// therefore a genuine primary-key collision, not a heuristic dedup check.
func ensureAttributionTable(dbPath string) error {
	const ddl = `CREATE TABLE IF NOT EXISTS claude_provenance (
		id TEXT PRIMARY KEY,
		prompt_id TEXT NOT NULL,
		source_name TEXT NOT NULL,
		harness TEXT NOT NULL,
		source_file TEXT NOT NULL,
		session_id TEXT NOT NULL,
		record_index INTEGER NOT NULL,
		content_hash TEXT NOT NULL,
		attributed_at_watermark TEXT NOT NULL
	);`
	_, err := runSQLite(dbPath, ddl)
	return err
}

// fetchPromptsForFile returns every prompts row whose source_file equals
// basename, ordered by `at` ascending (file/session order), each carrying a
// sha256 hash of its own text_raw so it can be paired against an extracted
// Unit's ContentHash without this process ever holding the raw text longer
// than one hash computation.
func fetchPromptsForFile(dbPath, basename string) ([]promptRow, error) {
	q := fmt.Sprintf(
		`select id, text_raw from prompts where source_file = '%s' order by at asc;`,
		sqlEscape(basename))
	out, err := runSQLite(dbPath, "-separator", sep, q)
	if err != nil {
		return nil, err
	}
	var rows []promptRow
	scanner := bufio.NewScanner(strings.NewReader(out))
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		id, text, ok := strings.Cut(scanner.Text(), sep)
		if !ok {
			continue
		}
		rows = append(rows, promptRow{ID: id, Hash: provenance.HashContent(text)})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// alreadyAttributedIDs returns the set of claude_provenance.id values
// already present -- checked once per apply run so a rerun's "skipped:
// already_attributed" count is exact, not inferred from an INSERT OR IGNORE
// row-count side effect.
func alreadyAttributedIDs(dbPath string) (map[string]bool, error) {
	out, err := runSQLite(dbPath, "select id from claude_provenance;")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			set[line] = true
		}
	}
	return set, scanner.Err()
}

func insertAttribution(dbPath string, u Unit, promptID string, watermark string) error {
	p := u.Provenance
	q := fmt.Sprintf(
		`INSERT INTO claude_provenance (id, prompt_id, source_name, harness, source_file, session_id, record_index, content_hash, attributed_at_watermark) VALUES ('%s','%s','%s','%s','%s','%s',%d,'%s','%s');`,
		sqlEscape(p.ID()), sqlEscape(promptID), sqlEscape(p.SourceName), sqlEscape(p.Harness),
		sqlEscape(p.SourceFile), sqlEscape(p.SessionID), p.RecordIndex, sqlEscape(p.ContentHash),
		sqlEscape(watermark))
	_, err := runSQLite(dbPath, q)
	return err
}

func countAttributionRows(dbPath string) (int, error) {
	out, err := runSQLite(dbPath, "select count(*) from claude_provenance;")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parsing claude_provenance count: %w", err)
	}
	return n, nil
}

// Decision is one prompts row's disposition -- attributed, or skipped with a
// named reason. Nothing is a silent zero: every row this run examines ends
// up in exactly one Decision.
type Decision struct {
	PromptID string
	Basename string
	Unit     *Unit // nil when skipped for a reason other than a genuine pairing
	Outcome  string
	Reason   string
}

const (
	outcomeAttributed      = "attributed"
	outcomeAlready         = "already_attributed"
	outcomeNoMatch         = "no_matching_extracted_unit"
	outcomeExcludedWmark   = "excluded_file_changed_after_watermark"
	outcomeUnparseableFile = "source_file_unparseable"
	outcomeNotFoundOnDisk  = "source_file_not_found_on_disk"
	outcomeCollision       = "source_file_basename_ambiguous_collision"
)

// pairFileRows matches one file's extracted Units against its prompts rows
// by ContentHash, FIFO within each distinct hash bucket (both sides ordered
// -- prompts by `at`, units by RecordIndex -- so N identical-text turns in
// the same session pair up in the order they actually occurred, never
// arbitrarily). A prompts row whose hash has no remaining unit to pair with
// is reported outcomeNoMatch; the reverse (a unit with no remaining prompts
// row) is not itself a row decision -- there is no existing row to report --
// but is folded into the run's own extra-units accounting for honesty.
func pairFileRows(rows []promptRow, units []Unit) (decisions []Decision, extraUnits int) {
	byHash := map[string][]Unit{}
	for _, u := range units {
		byHash[u.Provenance.ContentHash] = append(byHash[u.Provenance.ContentHash], u)
	}
	for _, r := range rows {
		bucket := byHash[r.Hash]
		if len(bucket) == 0 {
			decisions = append(decisions, Decision{PromptID: r.ID, Outcome: outcomeNoMatch,
				Reason: "no extracted unit in this file matches this row's text_raw hash"})
			continue
		}
		u := bucket[0]
		byHash[r.Hash] = bucket[1:]
		uCopy := u
		decisions = append(decisions, Decision{PromptID: r.ID, Unit: &uCopy, Outcome: outcomeAttributed})
	}
	for _, bucket := range byHash {
		extraUnits += len(bucket)
	}
	return decisions, extraUnits
}

func sortedBasenames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
