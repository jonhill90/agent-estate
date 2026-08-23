package main

import (
	"strconv"
	"time"

	"github.com/jonhill90/agent-tui/internal/cost"
)

// buildCostFetch composes cost.ParseDaily/cost.ParseActiveBlockLimit (pure)
// with two real `ccusage` invocations into one cost.Fetcher. This is the
// only place in the program that decides HOW ccusage is run -- internal/cost
// never sees a process, only bytes already on disk in ccusage's own JSON
// shape (internal/cost/ccusage.go's own doc comment).
//
// ccusageBin/ccusageArgs let the blindness test (agent-tui#4 acceptance
// item 2) point this at a binary that does not exist -- e.g.
// -ccusage-bin=ccusage-does-not-exist -- and get a real exec failure, the
// same "unknown, never zero" path a genuinely broken `ccusage` install
// would hit.
//
// claudeBlockLimit is 0 by default, meaning "no limit configured": ccusage
// has no notion of what a real token-limit number should be for anyone's
// account, and this program must not invent one either (internal/cost's own
// doc comment on Limit). Only when a caller supplies a real number -- from
// their own plan's known session-block ceiling -- does Claude's limit
// pressure become Known; until then it renders "unknown", not 0%.
//
// quotaRun is nil when no quota.sh could be resolved (main.go's
// resolveQuotaRunner) -- FetchQuotaSummary is simply never called and
// quotas stays nil, rendering "unknown" exactly as it did before this
// source existed (agent-tui#49 item 3). When non-nil, quota.sh's own
// contract decides Known per FetchQuotaSummary/ParseQuotaSummary; this
// function never guesses a percentage quota.sh did not report.
//
// quota.sh and ccusage are fetched as two INDEPENDENT sources, quota.sh
// first and unconditionally -- a real production machine reviewed for
// agent-tui#49 had a working quota.sh but no working ccusage ("cost:
// unknown (ccusage unreadable)" with no quota line at all, because the old
// code below returned before quota.sh was ever called). Every return path
// below now carries quotas along regardless of whether ccusage's own daily
// fetch succeeded, and none of them return a non-nil error for a plain
// ccusage failure -- cost.Model.Update folds ANY non-nil error into
// cost.Unknown() (its own blindness-test discipline, model.go), which
// would silently drop quotas the moment ccusage failed. Known=false on
// the returned Snapshot already carries "ccusage is unreadable" for the
// view to render; a Go error here would only lose data, not add
// information.
func buildCostFetch(ccusageBin string, ccusageArgs []string, claudeBlockLimit int64, quotaRun cost.QuotaRunner, now func() time.Time) cost.Fetcher {
	return buildCostFetchFromRunner(cost.ExecRunner(ccusageBin, ccusageArgs...), claudeBlockLimit, quotaRun, now)
}

// buildCostFetchFromRunner is buildCostFetch's actual logic, split out so
// tests can inject a fake cost.Runner directly (ccusage_test.go-style
// fixtures) instead of shelling a real binary out -- cost_test.go's
// TestBuildCostFetch_QuotaSurvivesCcusageFailure exercises this exact
// function, not a duplicate of it.
func buildCostFetchFromRunner(run cost.Runner, claudeBlockLimit int64, quotaRun cost.QuotaRunner, now func() time.Time) cost.Fetcher {
	return func() (cost.Snapshot, error) {
		var quotas map[string]cost.Quota
		if quotaRun != nil {
			quotas = cost.FetchQuotaSummary(quotaRun)
		}

		today := now().Format("2006-01-02")
		dateArgs := []string{"--since", now().Format("20060102"), "--until", now().Format("20060102")}

		dailyOut, err := run(append([]string{"daily", "--json", "--by-agent"}, dateArgs...))
		if err != nil {
			return cost.UnknownWithQuota(quotas), nil
		}
		harnesses, err := cost.ParseDaily(dailyOut, today)
		if err != nil {
			return cost.UnknownWithQuota(quotas), nil
		}

		for i := range harnesses {
			if q, ok := quotas[harnesses[i].Name]; ok {
				harnesses[i].Quota = q
			}
		}

		claudeLimit := cost.Limit{} // unknown by default
		if claudeBlockLimit > 0 {
			blockArgs := []string{"blocks", "--active", "--json", "--token-limit", strconv.FormatInt(claudeBlockLimit, 10)}
			blocksOut, err := run(blockArgs)
			if err != nil {
				// The daily figures are real even if the block-limit call
				// failed -- surface the harness costs and leave Claude's
				// Limit unknown rather than failing the whole fetch over a
				// second, optional call.
				return cost.Compose(harnesses, claudeLimit), nil
			}
			if l, err := cost.ParseActiveBlockLimit(blocksOut); err == nil {
				claudeLimit = l
			}
		}

		return cost.Compose(harnesses, claudeLimit), nil
	}
}
