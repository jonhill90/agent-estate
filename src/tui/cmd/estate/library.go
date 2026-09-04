package main

import (
	"github.com/jonhill90/agent-estate/src/tui/internal/board"
	"github.com/jonhill90/agent-estate/src/tui/internal/library"
)

// buildLibrarySources composes the library pane's two Sources
// (agent-estate#1088) -- the shared prompt/decision ledger (board's own
// ledgerSrc, unchanged, still index 0/the default) and the operator's own
// corpus (corpusSrc, resolveCorpusSource in corpus.go) -- into the slice
// library.NewSources expects. corpusSrc may be nil (resolveCorpusSource
// found nothing configured); buildLibraryFetch/DetailLoader/CountFetcher
// already degrade a nil ledgerSource to a nil seam, so the operator Source
// still gets a Name -- rendered as "not configured" rather than omitted,
// so [c] always shows the same two slots regardless of this machine's
// setup (main.go's own boardOK-style all-or-nothing refusal would be wrong
// here: no flag makes the operator corpus mandatory to start).
func buildLibrarySources(ledgerSrc, corpusSrc ledgerSource, sqliteBin string) []library.Source {
	return []library.Source{
		{
			Name:       "shared",
			Fetch:      buildLibraryFetch(ledgerSrc, sqliteBin),
			LoadDetail: buildLibraryDetailLoader(ledgerSrc, sqliteBin),
			FetchCount: buildLibraryCountFetch(ledgerSrc, sqliteBin),
			FetchQueue: buildLibraryQueueFetch(ledgerSrc, sqliteBin),
		},
		{
			Name:       "operator",
			Fetch:      buildLibraryFetch(corpusSrc, sqliteBin),
			LoadDetail: buildLibraryDetailLoader(corpusSrc, sqliteBin),
			FetchCount: buildLibraryCountFetch(corpusSrc, sqliteBin),
			FetchQueue: buildLibraryQueueFetch(corpusSrc, sqliteBin),
		},
	}
}

// buildLibraryFetch/buildLibraryDetailLoader/buildLibraryCountFetch compose
// library.ReadItems/ReadItemDetail/ReadPossibilityCount with a ledgerSource
// -- the SAME seam buildTaskFetch (board.go) uses for the shared ledger and
// resolveCorpusSource (corpus.go) uses for the operator's own corpus, so
// this file has one composition, not two. For the shared ledger, ledger()
// returns a fresh COPY path on every call (ledgerCopier.Refresh, agent-tui#49
// item 2); for the operator corpus it returns the same `file:...?mode=ro`
// path every time (corpusReadOnlyURI, corpus.go) -- never the live file
// UNWRAPPED, never a write. sqliteBin is the same -sqlite-bin flag every
// other ledger read in this program already shares. Returns nil (a
// library.Source slot with no fetch wired) when ledger is nil, the same
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

// buildLibraryQueueFetch composes library.ReadQueue with a ledgerSource --
// the Queue analogue of buildLibraryFetch above, same nil-degrades-to-nil
// rule (agent-estate#1094: a Source's queue slot can be unconfigured
// independently of its View slot, and library.Model's
// effectiveUnconfigured renders that distinctly).
func buildLibraryQueueFetch(ledger ledgerSource, sqliteBin string) library.QueueFetcher {
	if ledger == nil {
		return nil
	}
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))
	return func(q library.Queue) ([]library.ItemRow, error) {
		ledgerPath, err := ledger()
		if err != nil {
			return nil, err
		}
		return library.ReadQueue(sqliteRun, ledgerPath, q)
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
