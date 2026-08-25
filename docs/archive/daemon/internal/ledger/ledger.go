// Package ledger is the daemon's view of the existing SQLite store.
//
// The schema is NOT redesigned. `tasks`, `lanes` and their status enum are the
// ones the shell supervisor already writes, so the daemon and the old scripts
// can read the same store during a cutover. What changes is WHO is allowed to
// write a terminal status and on what evidence.
//
// THE RULE THIS PACKAGE ENFORCES (agent-supervisor#488): a terminal stamp
// (`complete` / `failed`) may only be written by the code that observed the
// process exit. The reconciler that stamped `failed` on a task whose process
// was still running an hour later did so from wall-clock dwell alone. There is
// no path in this package that writes a terminal status without an outcome
// handed to it by agent.Run.
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no cgo, so the daemon stays a static binary
)

type Status string

const (
	StatusCreated   Status = "created"
	StatusRunning   Status = "running"
	StatusComplete  Status = "complete"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

type Task struct {
	ID           string
	Lane         string
	Summary      string
	Status       Status
	WorktreePath string
	CreatedAt    int64
	UpdatedAt    int64
	CompletedAt  sql.NullInt64
}

type DB struct{ db *sql.DB }

// Open the ledger. `busy_timeout` is set because the shell scripts may still
// be writing concurrently during a cutover -- twelve concurrent writers on one
// SQLite file contend, which the corpus tooling learned the hard way.
func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("ledger: open %s: %w", path, err)
	}
	if err := d.Ping(); err != nil {
		return nil, fmt.Errorf("ledger: ping %s: %w", path, err)
	}
	return &DB{db: d}, nil
}

func (l *DB) Close() error { return l.db.Close() }

// ErrNotFound is returned rather than a zero Task, so a missing row can never
// be mistaken for an empty one.
var ErrNotFound = errors.New("ledger: task not found")

func (l *DB) Task(id string) (*Task, error) {
	row := l.db.QueryRow(`SELECT id, lane, summary, status, worktree_path,
		created_at, updated_at, completed_at FROM tasks WHERE id = ?`, id)
	var t Task
	err := row.Scan(&t.ID, &t.Lane, &t.Summary, &t.Status, &t.WorktreePath,
		&t.CreatedAt, &t.UpdatedAt, &t.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ledger: read task %s: %w", id, err)
	}
	return &t, nil
}

// EnsureLane registers the lane a task will reference.
//
// FOUND BY RUNNING IT, not by reading the schema: the first real dispatch died
// with `FOREIGN KEY constraint failed` because `tasks.lane` REFERENCES
// `lanes(lane)` and the daemon's lane had never been registered. Recorded here
// rather than quietly fixed, because "I ran it and it failed" is the evidence
// that separates this from code that merely compiles.
//
// `transport='claude-print'` is the honest value for EVERY adapter this
// daemon drives, Codex included: it names "this daemon drives a subprocess,
// it does not type into a pane", not literally "claude". The enum has no
// slot for a second non-interactive subprocess transport (`send-keys`,
// `acp`, `pi-rpc`, `claude-print` are the only legal values -- see
// ledger_test.go's schema mirror) and widening it is a live-ledger schema
// migration this package's own top-of-file comment rules out ("The schema
// is NOT redesigned"). Reusing `claude-print` for Codex is the same
// "closest honest thing, not a fabricated shape" call codex.go's own doc
// comment makes about CostUSD -- flagged here, not silently done, and
// tracked as a real gap: a reader of `lanes.transport` cannot currently
// tell a Claude-driven lane from a Codex-driven one from that column alone.
// `lanes.harness`, below, is the column that CAN and DOES tell them apart.
//
// `harness` (the parameter, not just the column) is what makes the ledger
// match what actually ran: before this, EnsureLane hardcoded harness='claude'
// unconditionally, so a lane dispatched by the (then nonexistent) Codex
// adapter would have recorded a false 'claude' in a column whose own CHECK
// constraint already listed 'codex' as legal -- caught by running a real
// Codex dispatch end-to-end against a scratch ledger and reading the row
// back, not by inspection.
func (l *DB) EnsureLane(lane, repo, harness string) error {
	now := time.Now().Unix()
	h := harness
	if h == "" {
		h = "claude"
	}
	command := "claude -p"
	if h == "codex" {
		command = "codex exec"
	}
	_, err := l.db.Exec(`INSERT INTO lanes
		(lane, pane_id, nonce, harness, repo, server_id, session_id, command,
		 harness_session_id, harness_project_dir, transport, updated_at)
		VALUES (?, '', '', ?, ?, 'supervisord', '', ?, '', '', 'claude-print', ?)
		ON CONFLICT(lane) DO UPDATE SET harness=excluded.harness, command=excluded.command, updated_at=excluded.updated_at`,
		lane, h, repo, command, now)
	if err != nil {
		return fmt.Errorf("ledger: ensure lane %s: %w", lane, err)
	}
	return nil
}

// Create records a task before anything is dispatched, so a crash between
// create and dispatch leaves a visible `created` row rather than nothing. The
// shell path could lose a dispatch entirely in that window.
func (l *DB) Create(t Task) error {
	now := time.Now().Unix()
	_, err := l.db.Exec(`INSERT INTO tasks
		(id, lane, pane_nonce, summary, status, worktree_path, created_at, updated_at)
		VALUES (?, ?, '', ?, ?, ?, ?, ?)`,
		t.ID, t.Lane, t.Summary, StatusCreated, t.WorktreePath, now, now)
	if err != nil {
		return fmt.Errorf("ledger: create task %s: %w", t.ID, err)
	}
	return nil
}

// MarkRunning is written AFTER the subprocess has started.
func (l *DB) MarkRunning(id string) error { return l.setStatus(id, StatusRunning, false) }

// Finish writes the single terminal stamp for a task, from an observed
// outcome. `ok` comes from agent.Run's error being nil -- an exit code and a
// parsed result, never a screen scrape and never wall-clock dwell.
//
// It refuses to overwrite an existing terminal state: #488's damage was a
// second writer stamping over a task that had already resolved.
func (l *DB) Finish(id string, ok bool) error {
	t, err := l.Task(id)
	if err != nil {
		return err
	}
	if t.Status == StatusComplete || t.Status == StatusFailed || t.Status == StatusCancelled {
		return fmt.Errorf("ledger: %s already terminal (%s) -- refusing to restamp", id, t.Status)
	}
	st := StatusFailed
	if ok {
		st = StatusComplete
	}
	return l.setStatus(id, st, true)
}

func (l *DB) setStatus(id string, st Status, terminal bool) error {
	now := time.Now().Unix()
	var res sql.Result
	var err error
	if terminal {
		res, err = l.db.Exec(`UPDATE tasks SET status=?, updated_at=?, completed_at=? WHERE id=?`, st, now, now, id)
	} else {
		res, err = l.db.Exec(`UPDATE tasks SET status=?, updated_at=? WHERE id=?`, st, now, id)
	}
	if err != nil {
		return fmt.Errorf("ledger: set %s=%s: %w", id, st, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Counts is what a human actually wants to see: how many tasks are in each
// state. The old board could not answer this on a 27-merge day.
func (l *DB) Counts() (map[Status]int, error) {
	rows, err := l.db.Query(`SELECT status, count(*) FROM tasks GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("ledger: counts: %w", err)
	}
	defer rows.Close()
	out := map[Status]int{}
	for rows.Next() {
		var s Status
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			return nil, err
		}
		out[s] = n
	}
	return out, rows.Err()
}
