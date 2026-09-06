// review.go is the reviewable half of this package (agent-estate#1139): it
// reads the knowledge_candidates queue Derive populates and, for one
// candidate at a time, resolves its cited prompt and provenance rows ON
// DEMAND -- never by copying prompt text into a new table, file, or fixture.
// See this package's own doc comment ("Why prompt text is never copied
// here") for the discipline this file must not break: List never selects
// prompt text at all, and Get joins to prompts.text_raw/text_clean only for
// the one row a reviewer asked to read, purely to print it, never to persist
// it anywhere new.
//
// # Absence is typed, never a bare zero
//
// A candidate whose provenance_id no longer resolves (its codex_provenance
// row is gone) and a candidate whose prompt_id no longer resolves (its
// prompts row is gone) are surfaced as explicit ProvenanceGone/PromptGone
// flags, not silently dropped from a list or silently substituted with
// empty strings that read like real values. This mirrors the "surface a
// gone source loudly" discipline agent-estate#1139's own knowledge-package
// fix (PR #1242) already established for the query-time index.
package candidates

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Sentinel errors a caller (main.go) can distinguish with errors.Is, so
// "corpus not found", "queue never derived", and "no such candidate" each
// produce their own message rather than one generic failure.
var (
	ErrCorpusNotFound         = errors.New("corpus database not found")
	ErrCandidatesTableMissing = errors.New("knowledge_candidates table not found -- run `estate candidates` to derive it first")
	ErrCandidateNotFound      = errors.New("candidate id not found in knowledge_candidates")
)

// rowSep matches internal/corpus and cmd/provenancebackfill's own choice of
// field separator for `sqlite3 -separator` output -- a byte that cannot
// appear in normal text, so a field is never mis-split by content that
// happens to contain a comma or pipe.
const rowSep = "\x1f"

// runReadOnlySep is runReadOnly plus `-separator`, needed only where a query
// returns more than one column and a caller must split them back apart
// unambiguously.
func runReadOnlySep(dbPath, sql string) (string, error) {
	uri := fmt.Sprintf("file:%s?mode=ro&immutable=1", dbPath)
	cmd := exec.Command("sqlite3", "-separator", rowSep, uri, sql)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("sqlite3 read failed: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("sqlite3 read failed: %w", err)
	}
	return string(out), nil
}

// Summary is one row of a List result -- enough to pick a candidate to
// `show`, never the prompt text itself.
//
// SourceKind distinguishes the two citation shapes a row can carry
// (this task's own generalization): "conversation" (the original
// prompt_id/provenance_id citation) or "catalogue" (a citation into Lane
// B's source catalogue, held directly on this row -- see CatalogueSourceID
// etc. below). ProvenanceGone/PromptGone-style absence tracking only
// applies to "conversation" rows: a catalogue row's citation is the data
// itself, not a join that can go stale independently of this table.
type Summary struct {
	ID                string
	SourceKind        string // "conversation" or "catalogue"
	PromptID          string
	ProvenanceID      string
	Decision          string // "", "promoted", or "discarded" -- see decide.go
	SourceFile        string // "" exactly when ProvenanceGone (conversation rows only)
	RecordIndex       int    // -1 exactly when ProvenanceGone (conversation rows only)
	ProvenanceGone    bool
	CatalogueSourceID string // "" for conversation rows; contract `id` for catalogue rows
	CatalogueLocator  string // contract `locator` -- "" for conversation rows
}

// ListFilter narrows a queue of thousands to what a reviewer can actually
// use. SourceFile is a substring match against codex_provenance.source_file
// (measured 2026-09-06 against a corpus copy: 4,360 candidates over 472
// distinct source files, one file alone accounting for 483 of them -- a
// reviewer needs to be able to isolate a single session). Ordering is
// always by k.rowid ascending, i.e. ingestion order: the order Derive's own
// INSERT ... SELECT wrote rows in, which follows codex_provenance's rowid
// (itself the order cmd/codexingest wrote provenance rows in). There is no
// other ordering knob -- this package does not infer a "better" order from
// content it has never classified (kind is always 'unclassified').
type ListFilter struct {
	SourceFile string
	Limit      int // <=0 means defaultListLimit
	Offset     int // <0 means 0
}

const defaultListLimit = 50

// ListResult reports both the page returned and Total -- the full count
// matching Filter, ignoring Limit/Offset -- so a caller can print "showing
// 50 of 812" rather than leaving a reviewer to guess whether a short page
// means "end of queue" or "filter matched exactly this many".
type ListResult struct {
	Items []Summary
	Total int
}

// decisionColumnExprs reports the SQL expressions List and Get should
// select for decision/decided_at. A knowledge_candidates table created by
// Derive before decide.go existed (or a live corpus nobody has ever run a
// decide against) has neither column -- ensureDecisionColumns only adds
// them lazily, under an -apply write, from decide.go. A read path must
// never write those columns into existence just to read from them, so it
// asks first (a read-only pragma_table_info check, not a write) and selects
// a literal empty string in place of a column that isn't there yet, rather than
// erroring or silently requiring a write first.
func decisionColumnExprs(dbPath string) (decisionExpr, decidedAtExpr string, err error) {
	hasDecision, err := columnExists(dbPath, "knowledge_candidates", "decision")
	if err != nil {
		return "", "", fmt.Errorf("checking for decision column: %w", err)
	}
	hasDecidedAt, err := columnExists(dbPath, "knowledge_candidates", "decided_at")
	if err != nil {
		return "", "", fmt.Errorf("checking for decided_at column: %w", err)
	}
	decisionExpr = "''"
	if hasDecision {
		decisionExpr = "k.decision"
	}
	decidedAtExpr = "''"
	if hasDecidedAt {
		decidedAtExpr = "k.decided_at"
	}
	return decisionExpr, decidedAtExpr, nil
}

// catalogueColumnExprs mirrors decisionColumnExprs' own read-only,
// select-a-literal-if-missing shape for the source_kind/catalogue_* columns
// this task's generalization adds: a knowledge_candidates table derived
// before this change (or a live corpus nobody has run RegisterCatalogueSource
// against yet) has none of them, and a read path must never write columns
// into existence just to read from them.
func catalogueColumnExprs(dbPath string) (sourceKindExpr, sourceIDExpr, locatorExpr string, err error) {
	has, err := columnExists(dbPath, "knowledge_candidates", "source_kind")
	if err != nil {
		return "", "", "", fmt.Errorf("checking for source_kind column: %w", err)
	}
	if !has {
		return "'conversation'", "''", "''", nil
	}
	return "k.source_kind", "k.catalogue_source_id", "k.catalogue_locator", nil
}

// List reads a page of the candidate queue. It never returns an error for
// "zero candidates matched the filter" -- an empty ListResult.Items with
// Total==0 is a normal, reportable outcome, distinct from
// ErrCandidatesTableMissing (queue never derived) and ErrCorpusNotFound
// (nothing to read at all).
func List(dbPath string, filter ListFilter) (ListResult, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return ListResult{}, fmt.Errorf("%w at %s: %v", ErrCorpusNotFound, dbPath, err)
	}
	exists, err := tableExists(dbPath, "knowledge_candidates")
	if err != nil {
		return ListResult{}, fmt.Errorf("checking for knowledge_candidates table: %w", err)
	}
	if !exists {
		return ListResult{}, ErrCandidatesTableMissing
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	where := ""
	if filter.SourceFile != "" {
		where = fmt.Sprintf(" WHERE cp.source_file LIKE '%%%s%%'", sqlEscape(filter.SourceFile))
	}

	total, err := readCount(dbPath, fmt.Sprintf(
		"select count(*) from knowledge_candidates k left join codex_provenance cp on cp.id = k.provenance_id%s;", where))
	if err != nil {
		return ListResult{}, fmt.Errorf("counting matching candidates: %w", err)
	}

	decisionExpr, _, err := decisionColumnExprs(dbPath)
	if err != nil {
		return ListResult{}, err
	}
	sourceKindExpr, sourceIDExpr, locatorExpr, err := catalogueColumnExprs(dbPath)
	if err != nil {
		return ListResult{}, err
	}

	q := fmt.Sprintf(`select k.id, k.prompt_id, k.provenance_id, %s,
	coalesce(cp.source_file,''), coalesce(cp.record_index,-1), %s, %s, %s
from knowledge_candidates k
left join codex_provenance cp on cp.id = k.provenance_id%s
order by k.rowid asc
limit %d offset %d;`, decisionExpr, sourceKindExpr, sourceIDExpr, locatorExpr, where, limit, offset)

	out, err := runReadOnlySep(dbPath, q)
	if err != nil {
		return ListResult{}, fmt.Errorf("listing candidates: %w", err)
	}

	var items []Summary
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, rowSep)
		if len(parts) != 9 {
			return ListResult{}, fmt.Errorf("unexpected column count (%d) in list row %q", len(parts), line)
		}
		ri, _ := strconv.Atoi(parts[5])
		s := Summary{
			ID:                parts[0],
			PromptID:          parts[1],
			ProvenanceID:      parts[2],
			Decision:          parts[3],
			SourceFile:        parts[4],
			RecordIndex:       ri,
			SourceKind:        parts[6],
			CatalogueSourceID: parts[7],
			CatalogueLocator:  parts[8],
		}
		if s.SourceKind == "conversation" {
			s.ProvenanceGone = s.SourceFile == ""
			if s.ProvenanceGone {
				s.RecordIndex = -1
			}
		}
		items = append(items, s)
	}

	return ListResult{Items: items, Total: total}, nil
}

// Detail is one candidate's full reviewable content: the citation Summary
// carries, plus the prompt it names, resolved fresh from the prompts table
// for this call only -- never cached, never written anywhere.
type Detail struct {
	Summary
	CreatedAt     string
	DecidedAt     string
	Harness       string
	SessionID     string
	ContentHash   string
	PromptFound   bool
	PromptGone    bool
	PromptContext string
	PromptText    string // text_clean if present, else text_raw; "" when PromptGone
}

// Get resolves one candidate by id, joining codex_provenance and prompts on
// demand. It returns ErrCandidateNotFound when id names no row in
// knowledge_candidates at all -- distinct from PromptGone/ProvenanceGone,
// which mean the candidate row exists but one of its two citations no
// longer resolves.
func Get(dbPath, id string) (Detail, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return Detail{}, fmt.Errorf("%w at %s: %v", ErrCorpusNotFound, dbPath, err)
	}
	exists, err := tableExists(dbPath, "knowledge_candidates")
	if err != nil {
		return Detail{}, fmt.Errorf("checking for knowledge_candidates table: %w", err)
	}
	if !exists {
		return Detail{}, ErrCandidatesTableMissing
	}

	decisionExpr, decidedAtExpr, err := decisionColumnExprs(dbPath)
	if err != nil {
		return Detail{}, err
	}
	sourceKindExpr, sourceIDExpr, locatorExpr, err := catalogueColumnExprs(dbPath)
	if err != nil {
		return Detail{}, err
	}

	q := fmt.Sprintf(`select k.id, k.prompt_id, k.provenance_id, %s, k.created_at,
	%s,
	coalesce(cp.source_file,''), coalesce(cp.record_index,-1), coalesce(cp.harness,''),
	coalesce(cp.session_id,''), coalesce(cp.content_hash,''),
	coalesce(p.id,''), coalesce(p.context,''), coalesce(p.text_clean,''), coalesce(p.text_raw,''),
	%s, %s, %s, k.content_hash
from knowledge_candidates k
left join codex_provenance cp on cp.id = k.provenance_id
left join prompts p on p.id = k.prompt_id
where k.id = '%s';`, decisionExpr, decidedAtExpr, sourceKindExpr, sourceIDExpr, locatorExpr, sqlEscape(id))

	out, err := runReadOnlySep(dbPath, q)
	if err != nil {
		return Detail{}, fmt.Errorf("reading candidate %s: %w", id, err)
	}
	line := strings.TrimSpace(out)
	if line == "" {
		return Detail{}, fmt.Errorf("%w: %s", ErrCandidateNotFound, id)
	}
	parts := strings.Split(line, rowSep)
	if len(parts) != 19 {
		return Detail{}, fmt.Errorf("unexpected column count (%d) reading candidate %s", len(parts), id)
	}

	ri, _ := strconv.Atoi(parts[7])
	d := Detail{
		Summary: Summary{
			ID:                parts[0],
			PromptID:          parts[1],
			ProvenanceID:      parts[2],
			Decision:          parts[3],
			SourceFile:        parts[6],
			RecordIndex:       ri,
			SourceKind:        parts[15],
			CatalogueSourceID: parts[16],
			CatalogueLocator:  parts[17],
		},
		CreatedAt:     parts[4],
		DecidedAt:     parts[5],
		Harness:       parts[8],
		SessionID:     parts[9],
		ContentHash:   parts[10],
		PromptContext: parts[12],
	}
	promptRowID := parts[11]
	textClean := parts[13]
	textRaw := parts[14]
	ownContentHash := parts[18] // k.content_hash -- the row's own citation hash, distinct from cp.content_hash above

	if d.SourceKind == "catalogue" {
		// A catalogue row's citation is data held directly on this row, not
		// a join that can go stale independently of it: ContentHash is the
		// row's own column (RegisterCatalogueSource writes it from
		// CatalogueSource.ContentHash), never cp.content_hash (always ''
		// here, since there is no codex_provenance row to join). There is
		// no prompt/provenance to resolve or report gone for this kind.
		d.ContentHash = ownContentHash
		return d, nil
	}

	d.ProvenanceGone = d.SourceFile == ""
	if d.ProvenanceGone {
		d.RecordIndex = -1
	}
	d.PromptFound = promptRowID != ""
	d.PromptGone = !d.PromptFound
	if d.PromptFound {
		if textClean != "" {
			d.PromptText = textClean
		} else {
			d.PromptText = textRaw
		}
	}
	return d, nil
}
