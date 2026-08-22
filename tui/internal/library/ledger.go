// Package library renders the SHARED prompt/decision corpus --
// ~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3's own
// live_parameters/open_questions/unacknowledged/possibility_count views,
// the counterpart internal/knowledge is not: knowledge is Jon's PERSONAL
// memory vault (agent/facts, one operator, plain markdown files);
// library is the corpus every agent in the estate is supposed to consult
// before asking Jon anything -- one shared SQLite ledger, not a vault, and
// today reachable only by hand-typing SQL. w5c.md's own brief: "put it on
// screen."
//
// This is the SAME ledger.sqlite3 internal/board already reads (source_tasks/
// tasks) -- not a second ledger reader, a second projection of the one
// this module already knows how to open safely. Reuses board.LedgerRunner/
// board.ExecRunner (the same `sqlite3 -json` + `PRAGMA query_only=1`
// pattern board.ReadTaskRows documents) and cmd/keelson's own ledgerSource
// (the auto-copying "must be a copy, never the live file" seam agent-tui#49
// item 2 built) -- see cmd/keelson/library.go for the composition.
//
// PROGRESSIVE DISCLOSURE is a hard constraint here, the same one
// internal/knowledge's own package doc comment states for the vault: this
// package must never pull a full row set to draw a list. ReadItems below
// selects a TRUNCATED body (SQL substr, not a Go slice after the fact --
// the point is never transferring the full text for a row that stays
// closed) and caps the row count; ReadItemDetail is the only function that
// reads one item's full body, and it also joins the item's own originating
// prompt for context -- called only when a caller actually opens that row.
package library

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/jonhill90/keelson/internal/board"
)

// View names one of the ledger's own three item-listing views -- these are
// the ledger's real SQL views (core.py's own CREATE VIEW live_parameters/
// open_questions/unacknowledged), not something this package invents a
// projection of. All three share one column set (items.*), verified
// against the live schema, 2026-08-22 -- this is what lets ReadItems below
// use ONE query shape for all three rather than three separate ones.
type View string

const (
	ViewLiveParameters View = "live_parameters"
	ViewOpenQuestions  View = "open_questions"
	ViewUnacknowledged View = "unacknowledged"
)

// Views is every selectable view, in the cycling order the [v] key steps
// through -- live_parameters first: "what is still binding" is the
// question this package's own package doc comment says the corpus answers
// most often.
var Views = []View{ViewLiveParameters, ViewOpenQuestions, ViewUnacknowledged}

func (v View) Label() string {
	switch v {
	case ViewOpenQuestions:
		return "open questions"
	case ViewUnacknowledged:
		return "unacknowledged"
	default:
		return "live parameters"
	}
}

// validViews/validWeights/validStatuses are fixed allow-lists, checked
// before ANY of these strings reaches a SQL statement -- board.ReadTaskRows'
// own query has no caller-supplied values to interpolate at all; this
// package's does (view name, weight/status filters), so unlike that one,
// this file cannot lean on "the query is a static string" as its whole
// injection defense. A value outside the allow-list is a caller bug
// (WithView/WithFilters, model.go, are the only callers, and both are
// key-cycled through these exact lists), refused rather than quoted and
// passed through.
var validViews = map[View]bool{ViewLiveParameters: true, ViewOpenQuestions: true, ViewUnacknowledged: true}
var validWeights = map[string]bool{"": true, "hard": true, "preference": true, "retracted": true}
var validStatuses = map[string]bool{"": true, "open": true, "acknowledged": true, "acted": true, "resolved": true, "dropped": true}

// idRE bounds an item id to itemize_prompts.py's own deterministic shape
// ("it-" + 16 hex chars, _item_id's own format) before it reaches a SQL
// statement -- ReadItemDetail's id argument is always one ReadItems itself
// already returned in practice, but it is still an external string by the
// time it crosses this package's own boundary (a teatest, a future caller),
// so it is checked here rather than trusted by provenance.
var idRE = regexp.MustCompile(`^it-[0-9a-f]+$`)

// ItemRow is one list line -- a bounded, TRUNCATED body, never the full
// text. Every field here is cheap: no join, no full-body read.
type ItemRow struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Weight      string `json:"weight"`
	Status      string `json:"status"`
	ResolvedTo  string `json:"resolved_to"`  // "" if this item does not constrain anything
	BodySnippet string `json:"body_snippet"` // truncated, SQL-side (substr), safe for a list line
}

// ItemDetail is one item's full record, PLUS its originating prompt's own
// context and text -- loaded only once a caller actually opens this row
// (this package's own progressive-disclosure constraint, package doc
// comment). The prompt half is what "the full body only when a row is
// opened" is really paying for: an ItemRow already has kind/weight/status/
// resolved_to cheaply; what a join would have cost on every list row is
// PromptContext/PromptText, both potentially long free text.
type ItemDetail struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Weight        string `json:"weight"`
	Status        string `json:"status"`
	StatusReason  string `json:"status_reason"`
	ResolvedTo    string `json:"resolved_to"`
	Body          string `json:"body"` // full, untruncated
	PromptID      string `json:"prompt_id"`
	PromptAt      int64  `json:"prompt_at"`
	PromptContext string `json:"prompt_context"`
	PromptText    string `json:"prompt_text"` // text_clean, falling back to text_raw if unset
}

// itemsQuery builds the bounded, filtered list query for one view.
// substr(body,1,120) AS body_snippet is the progressive-disclosure
// boundary itself: the ledger never sends more than 120 bytes of any row's
// body across this read. Every column is explicitly aliased to the exact
// json tag ItemRow expects -- sqlite3 -json otherwise names a computed
// column after its own expression text (e.g. the literal string
// "substr(body,1,120)"), which is fragile the moment the expression's
// spacing changes; an alias makes the wire shape a contract this file
// controls, not an accident of sqlite3's own defaults.
//
// LIMIT 200, newest-first. Ordered by the item's own originating prompt's
// `at` (a join, not items.rowid): confirmed live, 2026-08-22, that
// live_parameters/open_questions/unacknowledged are plain `SELECT * FROM
// items WHERE ...` views, and `*` does not carry a table's hidden rowid
// through a view -- `ORDER BY rowid` fails outright ("no such column:
// rowid") against the real schema, not a style choice. Joining prompts for
// its own `at` column instead also gives the actually-useful ordering (the
// prompt's real timestamp), not an insertion-order proxy. A real, disclosed
// cap, not silent truncation: view.go's own legend states the count next
// to whatever the query actually returned.
func itemsQuery(view View, weight, status string) string {
	var where []string
	if weight != "" {
		where = append(where, fmt.Sprintf("i.weight = '%s'", weight))
	}
	if status != "" {
		where = append(where, fmt.Sprintf("i.status = '%s'", status))
	}
	q := fmt.Sprintf(`SELECT i.id AS id, i.kind AS kind, i.weight AS weight, i.status AS status,
coalesce(i.resolved_to,'') AS resolved_to, substr(i.body,1,120) AS body_snippet
FROM %s i JOIN prompts p ON p.id = i.prompt_id`, view)
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY p.at DESC LIMIT 200;"
	return q
}

// ReadItems queries dbPath (a ledger.sqlite3 COPY -- see cmd/keelson/
// library.go's own doc comment for why this package never opens the live
// file) for one view, optionally narrowed by weight/status. view/weight/
// status are checked against this file's own allow-lists before they ever
// reach the query string -- see those maps' own doc comment.
func ReadItems(run board.LedgerRunner, dbPath string, view View, weight, status string) ([]ItemRow, error) {
	if !validViews[view] {
		return nil, fmt.Errorf("library: unknown view %q", view)
	}
	if !validWeights[weight] {
		return nil, fmt.Errorf("library: unknown weight filter %q", weight)
	}
	if !validStatuses[status] {
		return nil, fmt.Errorf("library: unknown status filter %q", status)
	}
	out, err := run([]string{"-json", dbPath, "PRAGMA query_only=1;\n" + itemsQuery(view, weight, status)})
	if err != nil {
		return nil, fmt.Errorf("library: read ledger %s: %w", dbPath, err)
	}
	var rows []ItemRow
	if len(out) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("library: decode item rows: %w", err)
		}
	}
	return rows, nil
}

// ReadItemDetail reads exactly one item's full body plus its originating
// prompt's own context/text -- the one query this package runs per open,
// never as part of drawing a list. id must already match itemize_prompts.py's
// own id shape (idRE); anything else is refused before it reaches SQL.
func ReadItemDetail(run board.LedgerRunner, dbPath string, id string) (ItemDetail, error) {
	if !idRE.MatchString(id) {
		return ItemDetail{}, fmt.Errorf("library: %q is not a well-formed item id", id)
	}
	q := fmt.Sprintf(`SELECT i.id AS id, i.kind AS kind, i.weight AS weight, i.status AS status,
coalesce(i.status_reason,'') AS status_reason, coalesce(i.resolved_to,'') AS resolved_to,
i.body AS body, i.prompt_id AS prompt_id, p.at AS prompt_at,
coalesce(p.context,'') AS prompt_context,
coalesce(p.text_clean, p.text_raw, '') AS prompt_text
FROM items i JOIN prompts p ON p.id = i.prompt_id WHERE i.id = '%s' LIMIT 1;`, id)
	out, err := run([]string{"-json", dbPath, "PRAGMA query_only=1;\n" + q})
	if err != nil {
		return ItemDetail{}, fmt.Errorf("library: read ledger %s: %w", dbPath, err)
	}
	var rows []ItemDetail
	if len(out) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return ItemDetail{}, fmt.Errorf("library: decode item detail: %w", err)
		}
	}
	if len(rows) == 0 {
		return ItemDetail{}, fmt.Errorf("library: no item %s (it may have been re-judged since the list was drawn)", id)
	}
	return rows[0], nil
}

// ReadPossibilityCount reads possibility_count -- "how many hard
// constraints are live" (this package's own package doc comment) -- a
// single scalar, always shown regardless of which View is currently on
// screen (view.go's own legend).
func ReadPossibilityCount(run board.LedgerRunner, dbPath string) (int, error) {
	out, err := run([]string{"-json", dbPath, "PRAGMA query_only=1;\nSELECT count AS count FROM possibility_count;"})
	if err != nil {
		return 0, fmt.Errorf("library: read ledger %s: %w", dbPath, err)
	}
	var rows []struct {
		Count int `json:"count"`
	}
	if len(out) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return 0, fmt.Errorf("library: decode possibility_count: %w", err)
		}
	}
	if len(rows) == 0 {
		return 0, nil
	}
	return rows[0].Count, nil
}
