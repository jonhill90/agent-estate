package main

import (
	"strings"
	"time"

	"github.com/jonhill90/agent-tui/internal/board"
	"github.com/jonhill90/agent-tui/internal/cost"
	"github.com/jonhill90/agent-tui/internal/dashboard"
	"github.com/jonhill90/agent-tui/internal/knowledge"
	"github.com/jonhill90/agent-tui/internal/lane"
)

// buildDashboardFetch composes four seams this file already opens for other
// panes -- sessionsFetch (the same "sessions" MCP call the rail and
// agentsModel already make), board.FetchPRs over the same board.ExecRunner
// shape buildBoardFetch already uses, costFetch (the same ccusage read
// costModel already makes), and knowledge.LoadIndex (the one-file vault
// read internal/knowledge's own package doc comment establishes) -- into
// dashboard.Stats. None of these is a second reader of its source: each
// call below re-invokes an existing Fetcher-shaped VALUE or exported
// function with the same construction this file already resolved from
// flags/env, exactly how sessionsFetch is already shared between railModel
// and agentsModel (main.go's own comment on that closure).
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
func buildDashboardFetch(ghBin, reposEnv string, sessionsFetch func() ([]lane.Session, error), costFetch cost.Fetcher, vaultDir string) dashboard.Fetcher {
	ghRun := board.GitHubRunner(board.ExecRunner(ghBin))
	repos := board.ReposFor(reposEnv, nil)

	return func() (dashboard.Stats, error) {
		var s dashboard.Stats

		if sessions, err := sessionsFetch(); err == nil {
			counts := make(map[string]int)
			for _, session := range sessions {
				for _, l := range session.Lanes {
					counts[l.State]++
				}
			}
			s.AgentsByState = counts
			s.AgentsKnown = true
		}
		// err != nil: AgentsKnown stays false, AgentsByState stays nil --
		// the sessions fetch itself already distinguishes "no supervisor
		// connection" from "connected, zero lanes" (main.go's own
		// sessionsFetch), and that distinction is preserved here by simply
		// not guessing when it errors.

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
