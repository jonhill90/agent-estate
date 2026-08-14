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
func buildCostFetch(ccusageBin string, ccusageArgs []string, claudeBlockLimit int64, now func() time.Time) cost.Fetcher {
	run := cost.ExecRunner(ccusageBin, ccusageArgs...)

	return func() (cost.Snapshot, error) {
		today := now().Format("2006-01-02")
		dateArgs := []string{"--since", now().Format("20060102"), "--until", now().Format("20060102")}

		dailyOut, err := run(append([]string{"daily", "--json", "--by-agent"}, dateArgs...))
		if err != nil {
			return cost.Unknown(), err
		}
		harnesses, err := cost.ParseDaily(dailyOut, today)
		if err != nil {
			return cost.Unknown(), err
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
