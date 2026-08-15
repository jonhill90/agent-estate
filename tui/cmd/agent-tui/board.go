package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/jonhill90/agent-tui/internal/board"
	"github.com/jonhill90/agent-tui/internal/lane"
	"github.com/jonhill90/agent-tui/internal/rail"
)

// buildBoardFetch composes board.board's pure pieces (repo discovery, gh,
// the ledger, and the SAME lanes fetch the rail already uses) into one
// board.Fetcher. This is the only place in the program that decides HOW
// those facts are gathered; board.Derive (card.go) never sees a process or
// a file path, only already-decoded values -- see internal/board's doc
// comment.
//
// ledgerPath must never be the live ledger: board.ReadTaskRows sends
// `PRAGMA query_only=1` ahead of its query (not `sqlite3 -readonly`, which
// cannot open a plain copy of a WAL-mode ledger at all -- see
// internal/board/ledger.go's doc comment), but that pragma is a
// per-connection guard, not a file-open refusal, so it is not a substitute
// for the path itself being a copy a human chose deliberately -- see
// -ledger's flag help in main(), which now has no live-ledger fallback.
func buildBoardFetch(ledgerPath, ghBin, sqliteBin, reposEnv string, lanesFetch func() ([]lane.Lane, error)) board.Fetcher {
	ghRun := board.GitHubRunner(board.ExecRunner(ghBin))
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))

	return func() (board.Snapshot, error) {
		taskRows, err := board.ReadTaskRows(sqliteRun, ledgerPath)
		if err != nil {
			return board.Snapshot{}, fmt.Errorf("board: %w", err)
		}

		repos := board.ReposFor(reposEnv, board.DiscoverRepos(taskRows))
		// Sorted once here, not left to whatever order ReposFor happened to
		// produce (base list, then discovered appended) -- Snapshot.Repos
		// drives the project-selection letter legend (internal/board/
		// model.go's repoLegend), and its letters must be stable across
		// renders of the SAME repo set, not reshuffle if a discovered repo
		// changes position.
		sort.Slice(repos, func(i, j int) bool { return repos[i].GitHubID() < repos[j].GitHubID() })

		issuesByRepo := make(map[string][]board.Issue, len(repos))
		prsByRepo := make(map[string][]board.PR, len(repos))
		for _, repo := range repos {
			issues, err := board.FetchIssues(ghRun, repo)
			if err != nil {
				return board.Snapshot{}, fmt.Errorf("board: %w", err)
			}
			issuesByRepo[repo.GitHubID()] = issues

			prs, err := board.FetchPRs(ghRun, repo)
			if err != nil {
				return board.Snapshot{}, fmt.Errorf("board: %w", err)
			}
			prsByRepo[repo.GitHubID()] = prs
		}

		lanes, err := lanesFetch()
		if err != nil {
			return board.Snapshot{}, fmt.Errorf("board: %w", err)
		}

		cards := board.Derive(time.Now(), repos, issuesByRepo, prsByRepo, taskRows, lanes)
		return board.Snapshot{Cards: cards, WIP: board.ComputeWIP(cards), Repos: repos}, nil
	}
}

// buildTaskFetch wires agent-tui#26's rail content into the SAME ledger
// read buildBoardFetch above already uses (board.ReadTaskRows over
// board.ExecRunner) -- not a second reader. Returns nil when ledgerPath is
// empty (no -ledger/$AGENT_TUI_LEDGER set): rail.Model.WithTasks(nil) is a
// no-op, so the rail's default screen keeps working with no ledger
// configured at all, exactly as -board refuses to start without one but the
// rail never has (see -ledger's own flag help for why -board is the strict
// one and this stays additive).
func buildTaskFetch(ledgerPath, sqliteBin string) rail.TaskFetcher {
	if ledgerPath == "" {
		return nil
	}
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))
	return func() ([]board.TaskRow, error) {
		return board.ReadTaskRows(sqliteRun, ledgerPath)
	}
}

// defaultLedgerPath used to fall back to $AGENT_SUPERVISOR_STATE_DIR (or
// the hardcoded state dir) when neither -ledger nor $AGENT_TUI_LEDGER was
// set -- both of which are the LIVE supervisor ledger, the exact file the
// flag help and README say -ledger must never point at. Before the
// -readonly fix (ledger.go), pointing there by accident mostly failed
// loudly (a WAL-mode live ledger opened `-readonly` needs sidecar files
// that usually aren't there either, so it errored rather than read
// anything). ReadTaskRows no longer passes `-readonly` -- it relies on
// `PRAGMA query_only=1` plus this package never opening a live path in the
// first place -- so a silent live-ledger default would now be more
// dangerous, not less: it could actually open the live file and create
// `-wal`/`-shm` sidecars next to it. So there is no live-ledger fallback
// left: an unset -ledger/$AGENT_TUI_LEDGER is now a hard requirement,
// enforced in main() with an error that says why, not a default a caller
// could hit by accident.
func defaultLedgerPath() string {
	return ""
}
