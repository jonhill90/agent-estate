// decide.go is this package's one write path beyond Derive, and it writes
// less than Derive does: a decision column on a row Derive already created,
// never a new row and never anything outside knowledge_candidates.
//
// # A decision is not a promotion
//
// Recording decision='promoted' marks a candidate as reviewed and approved
// for promotion. It does NOT move any content into durable knowledge, does
// not write markdown, sqlite, duckdb, or any other durable store, and does
// not decide what that store will be -- that choice is reserved to the
// operator and stays open (agent-estate#1139's own brief). A caller that
// reads decision='promoted' as "already promoted" has misread this package.
//
// # Live-corpus write gating lives in main.go, not here
//
// Decide itself is apply-gated (apply=false performs the identical
// existence check and returns the SAME result Applying would, with zero
// writes) but has no opinion about whether dbPath is the live corpus --
// that is main.go's job, via internal/livepath.RefuseLivePath and an
// -authorized-live-write flag, exactly mirroring cmd/codexingest's own
// main.go rather than duplicating that guard here. Keeping the guard in one
// place (internal/livepath) is the point; a second copy inside this
// package would be the third-and-worst-shaped fork livepath's own doc
// comment already warns about.
package candidates

import (
	"fmt"
)

// Decision is the only vocabulary Decide accepts. Anything else is a caller
// error, not a new kind quietly invented here.
const (
	DecisionPromote = "promote"
	DecisionDiscard = "discard"
)

func decisionStatus(decision string) (string, error) {
	switch decision {
	case DecisionPromote:
		return "promoted", nil
	case DecisionDiscard:
		return "discarded", nil
	default:
		return "", fmt.Errorf("invalid decision %q -- must be %q or %q", decision, DecisionPromote, DecisionDiscard)
	}
}

// DecideResult reports what Decide found and did. Applied distinguishes a
// dry run (Applied=false: the candidate was found and the decision is
// valid, but nothing was written) from a real write, so a caller can print
// "would record" vs "recorded" instead of leaving that ambiguous.
type DecideResult struct {
	ID        string
	Status    string // "promoted" or "discarded"
	Applied   bool
	DecidedAt string // "" when Applied is false
}

func columnExists(dbPath, table, col string) (bool, error) {
	out, err := readCount(dbPath, fmt.Sprintf(
		"select count(*) from pragma_table_info('%s') where name='%s';", sqlEscape(table), sqlEscape(col)))
	if err != nil {
		return false, err
	}
	return out > 0, nil
}

// ensureDecisionColumns adds decision/decided_at to knowledge_candidates if
// a still-#1244-shaped table (Derive's original DDL, which had neither)
// hasn't got them yet. sqlite3 3.51 (this repo's own, verified 2026-09-06)
// does not support `ADD COLUMN IF NOT EXISTS`, so existence is checked via
// pragma_table_info first -- an unconditional ALTER TABLE would error on
// every call after the first.
func ensureDecisionColumns(dbPath string) error {
	has, err := columnExists(dbPath, "knowledge_candidates", "decision")
	if err != nil {
		return fmt.Errorf("checking for decision column: %w", err)
	}
	if !has {
		if err := runWrite(dbPath,
			"ALTER TABLE knowledge_candidates ADD COLUMN decision TEXT NOT NULL DEFAULT '';",
			"add decision column"); err != nil {
			return err
		}
	}
	has, err = columnExists(dbPath, "knowledge_candidates", "decided_at")
	if err != nil {
		return fmt.Errorf("checking for decided_at column: %w", err)
	}
	if !has {
		if err := runWrite(dbPath,
			"ALTER TABLE knowledge_candidates ADD COLUMN decided_at TEXT NOT NULL DEFAULT '';",
			"add decided_at column"); err != nil {
			return err
		}
	}
	return nil
}

func candidateExists(dbPath, id string) (bool, error) {
	n, err := readCount(dbPath, fmt.Sprintf(
		"select count(*) from knowledge_candidates where id = '%s';", sqlEscape(id)))
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Decide records a reviewer's promote/discard call against one candidate.
// apply=false performs every check a real write would (table exists,
// candidate exists, decision is valid) and reports the result it would
// produce, with zero calls to ensureDecisionColumns or any write -- the
// same "dry run means dry run" contract cmd/codexingest's own -apply flag
// keeps.
func Decide(dbPath, id, decision string, apply bool) (DecideResult, error) {
	status, err := decisionStatus(decision)
	if err != nil {
		return DecideResult{}, err
	}

	exists, err := tableExists(dbPath, "knowledge_candidates")
	if err != nil {
		return DecideResult{}, fmt.Errorf("checking for knowledge_candidates table: %w", err)
	}
	if !exists {
		return DecideResult{}, ErrCandidatesTableMissing
	}

	found, err := candidateExists(dbPath, id)
	if err != nil {
		return DecideResult{}, fmt.Errorf("checking candidate %s: %w", id, err)
	}
	if !found {
		return DecideResult{}, fmt.Errorf("%w: %s", ErrCandidateNotFound, id)
	}

	res := DecideResult{ID: id, Status: status, Applied: apply}
	if !apply {
		return res, nil
	}

	if err := ensureDecisionColumns(dbPath); err != nil {
		return DecideResult{}, err
	}
	at := nowFunc()
	q := fmt.Sprintf("UPDATE knowledge_candidates SET decision='%s', decided_at='%s' WHERE id='%s';",
		sqlEscape(status), sqlEscape(at), sqlEscape(id))
	if err := runWrite(dbPath, q, fmt.Sprintf("decide %s -> %s", id, status)); err != nil {
		return DecideResult{}, err
	}
	res.DecidedAt = at
	return res, nil
}
