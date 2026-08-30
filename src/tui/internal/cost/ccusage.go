package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// Runner executes an external command and returns its stdout, mirroring
// board.LedgerRunner/board.GitHubRunner exactly -- same seam, same reason:
// cmd/agent-tui supplies the real exec.Command, tests supply a fixture.
type Runner func(args []string) ([]byte, error)

// execTimeout bounds every ExecRunner invocation -- the same gap
// board.ExecRunner's own execTimeout closes, for the same reason: a
// stalled `npx ccusage` (npm registry stall, a wedged node process, any
// hang short of the process actually exiting) blocked Dashboard's own
// SPEND TODAY fetch (buildDashboardFetch's costFetch call) forever, with
// no bound and no way for the pane to ever say so (agent-b3.md's own
// finding). A package var, not a literal, so a test can shrink it the same
// way internal/mcp's own callTimeout test does.
var execTimeout = 15 * time.Second

// ExecRunner shells a command out via os/exec. bin is the full command
// (cmd/agent-tui defaults it to "npx", with baseArgs ["ccusage"], so the
// blindness test -- agent-tui#4 acceptance item 2 -- can point it at a
// binary that does not exist and get a real exec failure, not a mock).
func ExecRunner(bin string, baseArgs ...string) Runner {
	return func(args []string) ([]byte, error) {
		full := append(append([]string{}, baseArgs...), args...)
		ctx, cancel := context.WithTimeout(context.Background(), execTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, full...)
		// Setpgid + a custom Cancel -- see board.ExecRunner's own doc
		// comment for the full instrumented root cause (exec.CommandContext's
		// default cmd.Process.Kill() only signals the direct child; a shell
		// or wrapper that forks rather than exec-replaces leaves a
		// grandchild alive holding Output()'s stdout/stderr pipes open, so
		// Wait() blocks on a pipe read until that grandchild exits on its
		// own). Same fix agent-supervisor's own daemon/internal/agent/
		// procgroup.go uses: put the whole tree in its own process group,
		// kill the group (negative pid) on cancellation.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		out, err := cmd.Output()
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil, fmt.Errorf("%s: timed out after %s -- process was killed rather than left to hang the fetch forever", bin, execTimeout)
			}
			if ee, ok := err.(*exec.ExitError); ok {
				return nil, fmt.Errorf("%s: %w: %s", bin, err, ee.Stderr)
			}
			return nil, fmt.Errorf("%s: %w", bin, err)
		}
		return out, nil
	}
}

// rawDailyReport/rawDailyRow/rawAgent mirror only the fields this package
// reads from `ccusage daily --json --by-agent`'s real output (captured
// running ccusage 20.0.19 against live usage logs, 2026-08-14 -- see the
// PR description for the pasted sample). Everything else ccusage emits
// (per-model breakdowns, metadata, modelsUsed) is real data this panel does
// not need yet and is left off these structs rather than modeled and
// ignored.
type rawDailyReport struct {
	Daily []rawDailyRow `json:"daily"`
}

type rawDailyRow struct {
	Period string     `json:"period"`
	Agents []rawAgent `json:"agents"`
}

type rawAgent struct {
	Agent           string  `json:"agent"`
	TotalCost       float64 `json:"totalCost"`
	TotalTokens     int64   `json:"totalTokens"`
	CacheReadTokens int64   `json:"cacheReadTokens"`
}

// ParseDaily extracts one day's per-harness figures from `ccusage daily
// --json --by-agent` output. period must be the exact YYYY-MM-DD ccusage
// itself prints in its "period" field -- cmd/agent-tui scopes the ccusage
// call itself with -since/-until set to that same day, so there is at most
// one matching row.
//
// No matching row is NOT an error: it means ccusage ran successfully and
// found no usage yet today, which this reports as a nil slice with a nil
// error -- the caller (cmd/agent-tui) must not confuse "ccusage said zero"
// with "the fetch failed"; only the latter is the blindness case that must
// render "unknown".
func ParseDaily(data []byte, period string) ([]Harness, error) {
	var report rawDailyReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("cost: decode ccusage daily --json: %w", err)
	}
	for _, row := range report.Daily {
		if row.Period != period {
			continue
		}
		out := make([]Harness, 0, len(row.Agents))
		for _, a := range row.Agents {
			out = append(out, Harness{
				Name:      a.Agent,
				Cost:      KnownFigure(a.TotalCost),
				Tokens:    KnownFigure(float64(a.TotalTokens)),
				CacheRead: KnownFigure(float64(a.CacheReadTokens)),
			})
		}
		return out, nil
	}
	return nil, nil
}

// rawSessionReport/rawSessionRow mirror only the fields this package reads
// from `ccusage session --json`'s real output (captured running ccusage
// against live usage logs, 2026-08-22). ccusage overloads its "period"
// field across report modes: for `daily`/`monthly`/`weekly` it is a date
// string (ParseDaily, above); for `session` it is the harness's own
// session/conversation id (a Claude Code session UUID, observed live) --
// exactly the id `agent-supervisor`'s ledger records per lane in
// lanes.harness_session_id (board.LaneSession). This is the ONLY seam in
// either codebase that attributes a dollar figure to one agent rather than
// a whole harness -- see internal/agents/row.go's own doc comment for why
// ccusage's `daily --by-agent` total (ParseDaily) cannot be shown per lane.
type rawSessionReport struct {
	Session []rawSessionRow `json:"session"`
}

type rawSessionRow struct {
	Period    string  `json:"period"` // the session id in this report mode, not a date
	TotalCost float64 `json:"totalCost"`
}

// ParseSessionCosts extracts every session's total cost from `ccusage
// session --json`, keyed by session id. A session ccusage reports with no
// id (should not happen; defensive) is skipped rather than collapsed onto
// an empty-string key some caller could accidentally look up. No matching
// session for a given id is NOT this function's problem to report --
// exactly ParseDaily's own "no matching row is not an error" rule -- the
// caller (internal/agents' Derive) treats a missing key as "unknown," not
// as a parse failure.
func ParseSessionCosts(data []byte) (map[string]Figure, error) {
	var report rawSessionReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("cost: decode ccusage session --json: %w", err)
	}
	out := make(map[string]Figure, len(report.Session))
	for _, s := range report.Session {
		if s.Period == "" {
			continue
		}
		out[s.Period] = KnownFigure(s.TotalCost)
	}
	return out, nil
}

// rawBlocksReport/rawBlock/rawTokenLimitStatus mirror `ccusage blocks
// --active --token-limit N --json`'s real output. tokenLimitStatus only
// appears at all when --token-limit was passed with a real number --
// ccusage has no default of its own to fall back on, which is exactly why
// this package never invents one either (see ParseActiveBlockLimit).
type rawBlocksReport struct {
	Blocks []rawBlock `json:"blocks"`
}

type rawBlock struct {
	IsActive         bool                 `json:"isActive"`
	TokenLimitStatus *rawTokenLimitStatus `json:"tokenLimitStatus"`
}

type rawTokenLimitStatus struct {
	Limit       int64   `json:"limit"`
	PercentUsed float64 `json:"percentUsed"`
	Status      string  `json:"status"` // observed values: "ok", "warning", "exceeds"
}

// ParseActiveBlockLimit extracts the active 5-hour session block's
// token-limit pressure from `ccusage blocks --active --token-limit N
// --json`. This is the ONLY quota-pressure figure ccusage can compute
// locally, and only for Claude -- `ccusage codex --help` / `ccusage pi
// --help` have no blocks or token-limit concept at all, verified by
// reading their --help output, not assumed -- and only when a caller
// supplies a real N (cmd/agent-tui's -claude-block-limit flag / the
// AGENT_TUI_CLAUDE_BLOCK_LIMIT env var). ccusage has no notion of what
// number to use on its own; this package must not approximate one either,
// so an unset limit or no active block both return Limit{} (Known: false),
// never a synthesized percentage.
func ParseActiveBlockLimit(data []byte) (Limit, error) {
	var report rawBlocksReport
	if err := json.Unmarshal(data, &report); err != nil {
		return Limit{}, fmt.Errorf("cost: decode ccusage blocks --json: %w", err)
	}
	for _, b := range report.Blocks {
		if !b.IsActive || b.TokenLimitStatus == nil {
			continue
		}
		return Limit{
			Known:   true,
			Percent: b.TokenLimitStatus.PercentUsed,
			Label:   "active 5h block",
			Warn:    b.TokenLimitStatus.Status != "ok",
		}, nil
	}
	// No active block right now (between sessions) is a real state, not
	// blindness -- but there is nothing to report a percentage against, so
	// this stays unknown rather than reading as 0% used.
	return Limit{}, nil
}
