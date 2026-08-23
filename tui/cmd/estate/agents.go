package main

import (
	"github.com/jonhill90/agent-tui/internal/agents"
	"github.com/jonhill90/agent-tui/internal/board"
	"github.com/jonhill90/agent-tui/internal/cost"
)

// buildAgentCostFetch composes two real seams this file already opens for
// other panes -- the same ledger read buildTaskFetch (board.go) uses via
// board.ExecRunner, and the same ccusage subprocess buildCostFetch (cost.go)
// uses via cost.ExecRunner -- into agents.Model's per-lane Cost column.
// Neither is a second reader: board.ExecRunner/cost.ExecRunner are called
// again here with the SAME sqliteBin/ccusageBin/ccusageArgs this file
// already resolved from flags, not a new binary or a new path.
//
// See internal/agents/row.go's own package doc comment for exactly what
// this joins (lanes.harness_session_id -> `ccusage session --json`'s
// per-session totalCost, keyed by the harness's own session id) and why
// that is genuinely per-AGENT cost rather than the per-HARNESS total
// internal/cost otherwise reports.
//
// Returns nil when ledger is nil (no -ledger and no live ledger to
// auto-copy) -- agents.Model.WithCosts(nil) is a no-op, same degradation
// buildTaskFetch's own doc comment describes for the Task column.
func buildAgentCostFetch(ledger ledgerSource, sqliteBin, ccusageBin string, ccusageArgs []string) agents.CostFetcher {
	if ledger == nil {
		return nil
	}
	sqliteRun := board.LedgerRunner(board.ExecRunner(sqliteBin))
	ccusageRun := cost.Runner(cost.ExecRunner(ccusageBin, ccusageArgs...))
	return func() (map[string]cost.Figure, error) {
		ledgerPath, err := ledger()
		if err != nil {
			return nil, err
		}
		laneSessions, err := board.ReadLaneSessions(sqliteRun, ledgerPath)
		if err != nil {
			return nil, err
		}
		if len(laneSessions) == 0 {
			// No lane has a resolved harness session id yet -- a real,
			// nameable state (every Cost stays nil), not worth a ccusage
			// call that could only ever produce an empty join.
			return nil, nil
		}

		out, err := ccusageRun([]string{"session", "--json"})
		if err != nil {
			return nil, err
		}
		bySessionID, err := cost.ParseSessionCosts(out)
		if err != nil {
			return nil, err
		}

		byLane := make(map[string]cost.Figure, len(laneSessions))
		for _, ls := range laneSessions {
			if fig, ok := bySessionID[ls.HarnessSessionID]; ok {
				byLane[ls.Lane] = fig
			}
		}
		return byLane, nil
	}
}
