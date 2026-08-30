// Package workflows is Build -> Workflows' real pane: a "workflow," in
// this estate, is a task's own path through it -- dispatched, delivered,
// accepted, completed -- read from the SAME ledger.TaskRow rows
// internal/board and internal/rail already read (board.ReadTaskRows over
// board.LedgerRunner), never a second reader of the ledger and never a
// fabricated run history. There is no separate "workflow" concept or table
// in the ledger to read instead -- source_tasks/tasks joined by id (that
// join's own doc comment, internal/board/ledger.go) already IS the record
// of every dispatch this estate has made.
package workflows

import "github.com/jonhill90/agent-estate/tui/internal/board"

// Fetcher retrieves the current dispatch history -- the one adapter seam
// this package's Model depends on. cmd/estate composes the real
// implementation from the SAME ledger seam buildTaskFetch (board.go)
// already builds for internal/rail; every test in this package builds a
// fake instead (AGENTS.md's adapter discipline). Read-only, always against
// a COPY of ledger.sqlite3 -- board.ReadTaskRows' own PRAGMA
// query_only=1 guard and this module's ledger-copy discipline apply here
// unchanged, since this package adds no new read path of its own.
type Fetcher func() ([]board.TaskRow, error)
