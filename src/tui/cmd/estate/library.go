package main

import (
	"github.com/jonhill90/agent-estate/src/tui/internal/board"
	"github.com/jonhill90/agent-estate/src/tui/internal/library"
)

// buildLibraryFetch/buildLibraryDetailLoader/buildLibraryCountFetch compose
// library.ReadItems/ReadItemDetail/ReadPossibilityCount with the SAME
// ledger seam buildTaskFetch (board.go) already uses -- ledger() returns a
// fresh COPY path on every call (ledgerCopier.Refresh, agent-tui#49 item
// 2), never the live file; sqliteBin is the same -sqlite-bin flag every
// other ledger read in this program already shares. Returns nil (a
// library.Model with no fetch wired) when ledger is nil, the same
// degradation buildTaskFetch's own doc comment describes.
func buildLibraryFetch(ledger ledgerSource, sqliteBin string) library.Fetcher {
	if ledger == nil {
		return nil
	}
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))
	return func(view library.View, weight, status string) ([]library.ItemRow, error) {
		ledgerPath, err := ledger()
		if err != nil {
			return nil, err
		}
		return library.ReadItems(sqliteRun, ledgerPath, view, weight, status)
	}
}

func buildLibraryDetailLoader(ledger ledgerSource, sqliteBin string) library.DetailLoader {
	if ledger == nil {
		return nil
	}
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))
	return func(id string) (library.ItemDetail, error) {
		ledgerPath, err := ledger()
		if err != nil {
			return library.ItemDetail{}, err
		}
		return library.ReadItemDetail(sqliteRun, ledgerPath, id)
	}
}

func buildLibraryCountFetch(ledger ledgerSource, sqliteBin string) library.CountFetcher {
	if ledger == nil {
		return nil
	}
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))
	return func() (int, error) {
		ledgerPath, err := ledger()
		if err != nil {
			return 0, err
		}
		return library.ReadPossibilityCount(sqliteRun, ledgerPath)
	}
}
