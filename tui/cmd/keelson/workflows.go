package main

import (
	"github.com/jonhill90/keelson/internal/board"
	"github.com/jonhill90/keelson/internal/workflows"
)

// buildWorkflowsFetch composes the SAME ledger seam buildTaskFetch (board.go)
// already uses for internal/rail -- board.ReadTaskRows over
// board.LedgerRunner(board.ExecRunner(sqliteBin)), against a fresh COPY of
// ledger.sqlite3 on every call (ledger()'s own contract, board.go) -- not a
// second reader. Returns nil (workflows.Model.unconfigured) when ledger is
// nil, the same degradation buildTaskFetch's own doc comment describes.
func buildWorkflowsFetch(ledger ledgerSource, sqliteBin string) workflows.Fetcher {
	if ledger == nil {
		return nil
	}
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))
	return func() ([]board.TaskRow, error) {
		ledgerPath, err := ledger()
		if err != nil {
			return nil, err
		}
		return board.ReadTaskRows(sqliteRun, ledgerPath)
	}
}
