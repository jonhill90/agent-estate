package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jonhill90/agent-estate/src/tui/internal/board"
	"github.com/jonhill90/agent-estate/src/tui/internal/lane"
	"github.com/jonhill90/agent-estate/src/tui/internal/rail"
)

// buildBoardFetch composes board.board's pure pieces (repo discovery, gh,
// the ledger, and the SAME lanes fetch the rail already uses) into one
// board.Fetcher. This is the only place in the program that decides HOW
// those facts are gathered; board.Derive (card.go) never sees a process or
// a file path, only already-decoded values -- see internal/board's doc
// comment.
//
// ledger must never resolve to the live ledger: board.ReadTaskRows sends
// `PRAGMA query_only=1` ahead of its query (not `sqlite3 -readonly`, which
// cannot open a plain copy of a WAL-mode ledger at all -- see
// internal/board/ledger.go's doc comment), but that pragma is a
// per-connection guard, not a file-open refusal, so it is not a substitute
// for the path itself being a copy -- either a human chose one deliberately
// (-ledger's flag help) or ledgerCopier made one automatically
// (agent-tui#49 item 2, ledger_copy.go). ledger is called fresh on EVERY
// fetch -- not resolved once -- so an auto-discovered live ledger gets a
// fresh copy each time this Fetcher runs, including board's own "r" key.
func buildBoardFetch(ledger ledgerSource, ghBin, sqliteBin, reposEnv string, lanesFetch func() ([]lane.Lane, error)) board.Fetcher {
	ghRun := board.GitHubRunner(board.ExecRunner(ghBin))
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))

	return func() (board.Snapshot, error) {
		ledgerPath, err := ledger()
		if err != nil {
			return board.Snapshot{}, fmt.Errorf("board: %w", err)
		}
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
// board.ExecRunner) -- not a second reader. Returns nil when ledger is nil
// (resolveLedgerSource found no explicit path and no live ledger to
// auto-copy): rail.Model.WithTasks(nil) is a no-op, so the rail's default
// screen keeps working with no ledger configured at all, exactly as before
// agent-tui#49.
func buildTaskFetch(ledger ledgerSource, sqliteBin string) rail.TaskFetcher {
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

// defaultLedgerLivePath is where agent-dotfiles-supervisor's own install
// keeps its ledger.sqlite3 (agent-tui#49 item 2 -- the QA brief names this
// path explicitly). resolveLedgerSource (main.go) stats it and, if
// present, wires a ledgerCopier around it rather than reading it directly
// -- the same "must be a copy" rule -ledger's flag help states, now
// automated instead of asked of a human. An unset $HOME (only plausible in
// a stripped-down test/CI shell) makes this undiscoverable, same as any
// other path under it; callers treat "" as "nothing found," not an error.
func defaultLedgerLivePath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "agent-dotfiles-supervisor", "ledger.sqlite3")
}

// resolveLedgerSource turns -ledger/$AGENT_TUI_LEDGER (explicit, possibly
// empty) into a ledgerSource plus whether the board pane has anything to
// read at all. An explicit path is trusted as-is (unchanged pre-agent-tui#49
// behaviour: the human already promised it is a copy). An empty explicit
// path now falls through to auto-discovery + auto-copy (agent-tui#49 item
// 2) instead of the old hard "not configured" refusal -- only when even
// that live path does not exist does this return boardOK == false, with a
// message naming the path it looked for. sqliteBin is threaded through to
// newLedgerCopier -- the SAME sqlite3 binary board's own reads use
// (-sqlite-bin), because the copy itself is now a sqlite3 ".backup" call,
// not a plain file copy (see ledger_copy.go's execBackupRunner doc
// comment for why a byte copy of a WAL-mode database is wrong).
func resolveLedgerSource(explicit, sqliteBin string) (ledger ledgerSource, boardOK bool, unavailable string) {
	if explicit != "" {
		return staticLedgerSource(explicit), true, ""
	}
	live := defaultLedgerLivePath()
	if live == "" {
		return nil, false, "no -ledger (or $AGENT_TUI_LEDGER) configured, and $HOME is unset so no live ledger could be discovered"
	}
	if _, err := os.Stat(live); err != nil {
		return nil, false, fmt.Sprintf("no -ledger (or $AGENT_TUI_LEDGER) configured, and no live ledger found at %s -- point -ledger at a copy explicitly", live)
	}
	copier, err := newLedgerCopier(live, sqliteBin)
	if err != nil {
		return nil, false, fmt.Sprintf("found a live ledger at %s but could not stage a copy: %v", live, err)
	}
	return copier.Refresh, true, ""
}
