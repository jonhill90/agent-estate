package main

import (
	"strings"
	"time"

	"github.com/jonhill90/agent-estate/src/tui/internal/board"
	"github.com/jonhill90/agent-estate/src/tui/internal/cost"
	"github.com/jonhill90/agent-estate/src/tui/internal/dashboard"
	"github.com/jonhill90/agent-estate/src/tui/internal/estatus"
	"github.com/jonhill90/agent-estate/src/tui/internal/knowledge"
)

// buildDashboardFetch composes four seams this file already opens for other
// panes -- estatus.ReadLedger (the same Go dispatch ledger Home's own
// estateStatus already reads, agent-estate#930's fix for AGENTS having
// previously read the deleted Python MCP server), board.FetchPRs over the
// same board.ExecRunner shape buildBoardFetch already uses, costFetch (the
// same ccusage read costModel already makes), and knowledge.LoadIndex (the
// one-file vault read internal/knowledge's own package doc comment
// establishes) -- into dashboard.Stats. None of these is a second reader of
// its source: estateLedgerPath is the exact path main.go already resolves
// for WithEstateStatus, re-read here rather than duplicated as a second
// ledger.
//
// Every failure below leaves its OWN Stats field unknown rather than
// failing the whole fetch (Stats' own doc comment) -- a `gh` rate limit
// must not blank out a real agent count, and vice versa.
//
// reposEnv is SUPERVISOR_REPOSITORIES-shaped, the same flag/env
// buildBoardFetch reads. Unlike the board, this fetch does NOT union in
// discovered repos from the ledger (board.DiscoverRepos) -- the dashboard's
// "open PRs"/"merged today" figures are meant to answer "the repos this
// estate actually watches by name," not require a ledger read just to
// draw two numbers, and DefaultRepos already covers every repo any real
// dispatch has ever named (repo.go's own doc comment). Documented here as
// a deliberate narrowing, not an oversight.
func buildDashboardFetch(ghBin, reposEnv, estateLedgerPath string, costFetch cost.Fetcher, vaultDir string) dashboard.Fetcher {
	ghRun := board.GitHubRunner(board.ExecRunner(ghBin))
	repos := board.ReposFor(reposEnv, nil)

	return func() (dashboard.Stats, error) {
		var s dashboard.Stats

		switch avail, _, _, inFlight := estatus.ReadLedger(estateLedgerPath); avail {
		case estatus.Present:
			counts := make(map[string]int)
			for _, d := range inFlight {
				counts[d.State]++
			}
			s.AgentsByState = counts
			s.AgentsKnown = true
		case estatus.Absent:
			s.AgentsUnavailable = "absent"
		case estatus.Unreadable:
			s.AgentsUnavailable = "unreadable"
		}

		// One gh call per repo (board.FetchPRs already asks for every
		// state in one request, not three) -- open count and merged-today
		// count are two different aggregations of the SAME response, never
		// two gh calls answering one question. Fails CLOSED on the first
		// repo gh cannot answer, same as buildBoardFetch's own board.Snapshot
		// fetch: a partial sum presented as complete would undercount
		// silently, which is worse than "unknown."
		openTotal, mergedToday := 0, 0
		today := time.Now().Format("2006-01-02")
		prsOK := true
		for _, repo := range repos {
			prs, err := board.FetchPRs(ghRun, repo)
			if err != nil {
				prsOK = false
				break
			}
			for _, pr := range prs {
				switch pr.State {
				case "OPEN":
					openTotal++
				case "MERGED":
					if strings.HasPrefix(pr.MergedAt, today) {
						mergedToday++
					}
				}
			}
		}
		if prsOK {
			s.OpenPRs = dashboard.KnownCount(openTotal)
			s.MergedToday = dashboard.KnownCount(mergedToday)
		}

		if costFetch != nil {
			if snap, err := costFetch(); err == nil && snap.Known {
				total := 0.0
				for _, h := range snap.Harnesses {
					if h.Cost.Known {
						total += h.Cost.Value
					}
				}
				s.SpendToday = dashboard.KnownUSD(total)
			}
			// snap.Known false, or costFetch itself erroring: SpendToday
			// stays unknown -- ccusage's own "ran, found nothing" vs
			// "could not run" distinction (cost.Snapshot's own doc
			// comment) is not re-derived here, just respected.
		}

		if vaultDir != "" {
			if entries, err := knowledge.LoadIndex(vaultDir); err == nil {
				s.VaultFacts = dashboard.KnownCount(len(entries))
			}
		}

		return s, nil
	}
}
