package board

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
)

// sourceURLRE mirrors agent-supervisor's github_source.SOURCE_URL_RE
// exactly (scripts/supervisor/github_source.py) -- the canonical shape
// `record_dispatch` writes into source_tasks.source_url. Kept as a literal
// copy, not an import, for the same reason internal/lane/states.go copies
// lanes.sh's state list instead of importing the supervisor: this repo
// imports no supervisor internals.
var sourceURLRE = regexp.MustCompile(
	`^https://github\.com/([A-Za-z0-9][A-Za-z0-9-]{0,38})/([A-Za-z0-9_.-]+)/(issues|pull)/([1-9][0-9]*)$`,
)

// TaskRow is one source_tasks row joined to its tasks row by id, projecting
// only the columns the board's derivation needs. task_id is duplicated as
// both an identifier and a join key deliberately -- callers never need to
// re-derive it.
type TaskRow struct {
	TaskID       string `json:"task_id"`
	SourceKind   string `json:"source_kind"` // "issue" or "pull"
	SourceURL    string `json:"source_url"`
	SourceRef    string `json:"source_ref"` // the issue/PR number, as a string
	SourceState  string `json:"source_state"`
	SourceStatus string `json:"source_status"` // source_tasks.status
	TaskStatus   string `json:"task_status"`   // tasks.status; empty if the task row is gone
	Lane         string `json:"lane"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	DeliveredAt  *int64 `json:"delivered_at"`
	AcceptedAt   *int64 `json:"accepted_at"`
	CompletedAt  *int64 `json:"completed_at"`

	// Repo/Number/Kind, parsed from SourceURL once here rather than by every
	// caller. Empty Repo means the URL didn't match sourceURLRE -- the same
	// "unresolvable" case reconcile_sources.py's sweep leaves as UNKNOWN
	// rather than guessing; card.go must skip these, not crash on them.
	Repo   Repo
	Number string
}

// parseSourceURL fills Repo/Number from SourceURL, matching sourceURLRE.
func (t *TaskRow) parseSourceURL() {
	m := sourceURLRE.FindStringSubmatch(t.SourceURL)
	if m == nil {
		return
	}
	t.Repo = Repo{Owner: m[1], Name: m[2]}
	t.Number = m[4]
}

// tasksQuery left-joins tasks onto source_tasks by id -- LEFT, not INNER,
// because a source_tasks row can outlive the tasks row it once pointed at
// (nothing deletes from tasks; this is defensive, not observed). tasks.lane
// and every *_at column come back NULL in that case and decode as Go's
// zero values, which downstream treats as "no ledger task", the same as a
// row this board never saw.
const tasksQuery = `
SELECT
  st.id AS task_id,
  st.source_kind,
  st.source_url,
  st.source_ref,
  st.source_state,
  st.status AS source_status,
  t.status AS task_status,
  t.lane,
  t.created_at,
  t.updated_at,
  t.delivered_at,
  t.accepted_at,
  t.completed_at
FROM source_tasks st
LEFT JOIN tasks t ON t.id = st.id;
`

// LedgerRunner executes an external command and returns its stdout.
// cmd/agent-tui supplies exec.Command; tests supply a fixture.
type LedgerRunner func(args []string) ([]byte, error)

// ExecRunner shells a command out via os/exec -- the real implementation.
func ExecRunner(name string) LedgerRunner {
	return func(args []string) ([]byte, error) {
		out, err := exec.Command(name, args...).Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("%s: %w: %s", name, err, ee.Stderr)
			}
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		return out, nil
	}
}

// ReadTaskRows queries dbPath (a ledger.sqlite3 file) and returns every
// source_tasks/tasks join row.
//
// This does NOT pass sqlite3's `-readonly`. A `-readonly` open of a
// WAL-mode database needs a `-shm` sidecar file already present, and
// read-only mode is refused permission to create one -- so `-readonly`
// against a plain `cp` of a WAL ledger (no `-wal`/`-shm` sidecars) fails
// outright with "unable to open database file (14)" before a single row is
// read. That is exactly the workflow this board exists for (copy the live
// ledger, read the copy), so `-readonly` cannot be the write guard here.
//
// Instead the query itself opens with `PRAGMA query_only=1;` first --
// sqlite3's own per-connection write-refusal, enforced by the query
// planner rather than the file-open path, so it does not depend on any
// sidecar file existing. The write guard this package actually relies on
// is discipline at the call site: dbPath must be a copy, never the live
// ledger (see cmd/agent-tui's -ledger flag doc and defaultLedgerPath,
// which refuses to default to the live ledger for exactly this reason).
func ReadTaskRows(run LedgerRunner, dbPath string) ([]TaskRow, error) {
	out, err := run([]string{"-json", dbPath, "PRAGMA query_only=1;\n" + tasksQuery})
	if err != nil {
		return nil, fmt.Errorf("board: read ledger %s: %w", dbPath, err)
	}
	var rows []TaskRow
	if len(out) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("board: decode ledger rows: %w", err)
		}
	}
	for i := range rows {
		rows[i].parseSourceURL()
	}
	return rows, nil
}

// laneSessionsQuery reads the ledger's own lanes table directly (not
// source_tasks/tasks, tasksQuery's join) -- lanes.harness_session_id is
// where dispatch.sh's own record-dispatch (--harness-session-id) writes the
// harness's OWN conversation/session id once it resolves one, per lane. A
// lane with no resolved session id (codex has no resolver at all -- the
// schema's own comment on this column says so; a claude lane that has not
// completed a turn yet) is EXCLUDED here rather than returned with an empty
// string, so a caller never has to re-derive "no session id" from an empty
// field the way ReadTaskRows' callers already must for TaskRow.Lane == "".
const laneSessionsQuery = `SELECT lane, harness_session_id FROM lanes WHERE harness_session_id != '';`

// LaneSession is one lanes-table row narrowed to the two columns
// internal/agents needs to attribute cost per lane: the ledger's own lane
// key (identical format to TaskRow.Lane -- "<session>:<window-index>",
// lanes.sh's own numeric window column, NOT the pane's display name) and
// the harness's resolved session id, the join key into `ccusage session
// --json`'s own per-session totals (see internal/cost.ParseSessionCosts).
type LaneSession struct {
	Lane             string `json:"lane"`
	HarnessSessionID string `json:"harness_session_id"`
}

// ReadLaneSessions queries dbPath (a ledger.sqlite3 file, same "copy, never
// the live file" rule ReadTaskRows' own doc comment states) for every lane
// with a resolved harness session id. Same PRAGMA query_only=1 write guard
// as ReadTaskRows -- see that function's own doc comment for why -readonly
// cannot be used here.
func ReadLaneSessions(run LedgerRunner, dbPath string) ([]LaneSession, error) {
	out, err := run([]string{"-json", dbPath, "PRAGMA query_only=1;\n" + laneSessionsQuery})
	if err != nil {
		return nil, fmt.Errorf("board: read ledger %s: %w", dbPath, err)
	}
	var rows []LaneSession
	if len(out) > 0 {
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil, fmt.Errorf("board: decode lane sessions: %w", err)
		}
	}
	return rows, nil
}

// DiscoverRepos returns the distinct repos named by every row's source_url
// that sourceURLRE could resolve -- the real-data half of ReposFor's union
// (repo.go). A row left unresolved by parseSourceURL contributes nothing;
// it is already the sweep's own UNKNOWN case, not a repo to add.
func DiscoverRepos(rows []TaskRow) []Repo {
	seen := map[string]Repo{}
	for _, r := range rows {
		if r.Repo.Owner == "" {
			continue
		}
		key := r.Repo.Owner + "/" + r.Repo.Name
		if _, ok := seen[key]; !ok {
			r.Repo.Label = r.Repo.Name
			seen[key] = r.Repo
		}
	}
	out := make([]Repo, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	return out
}
