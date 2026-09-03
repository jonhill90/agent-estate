// Package dashboard is the estate-at-a-glance view: the one nav destination
// Jon said he'd look at first, still rendering internal/stub's placeholder
// as of this writing. It is a re-projection, not a new source -- every
// figure it shows is either read directly from a source another pane in
// this module already established (internal/agents' own "sessions" fetch
// for agent counts, internal/cost's Snapshot for spend, internal/knowledge's
// vault index for fact count) or a small, real `gh` read this package's own
// Fetcher performs the same way internal/board already does (open/merged PR
// counts) -- never a second reader of the same source invented separately
// (AGENTS.md's adapter discipline).
//
// The hard rule this package exists to enforce: every number on screen is
// either read, or it says "unknown." A dashboard that guesses is worse than
// internal/stub's honest placeholder, because a stub cannot be mistaken for
// an answer.
package dashboard

// Count is one integer that may not be known -- the same "absence is a
// typed value" shape internal/cost.Figure already establishes for a float,
// repeated here for an int rather than importing cost.Figure and asking a
// reader to wonder why a PR count carries a Value field typed float64.
type Count struct {
	Known bool
	Value int
}

// KnownCount wraps a value this package's Fetcher actually read.
func KnownCount(v int) Count { return Count{Known: true, Value: v} }

// USD is Count's shape for a dollar figure -- kept as its own type rather
// than reusing Count with a float Value crammed in, so a caller can never
// hold an int count and a spend figure in the same field by accident.
type USD struct {
	Known bool
	Value float64
}

// KnownUSD wraps a value this package's Fetcher actually read.
func KnownUSD(v float64) USD { return USD{Known: true, Value: v} }

// Stats is one fetch's worth of dashboard figures. Every field is
// independently Known/unknown -- one source failing (gh rate-limited, no
// -ledger, no $AGENT_MEMORY_VAULT) must never blank out the others, the
// same posture internal/cost.Snapshot already takes for Harnesses vs
// Quotas. There is no single top-level "Known" for the whole Stats: unlike
// internal/cost (one ccusage subprocess, one failure mode), this package's
// five figures come from four independent real sources with independent
// failure modes, and folding them into one bit would hide which one is
// actually blind.
type Stats struct {
	// AgentsByState is src/estate's own ledger.State (dispatched/complete/
	// failed/unknown) -> count, summed across every IN-FLIGHT turn the Go
	// ledger records (internal/estatus.Status.InFlight -- the same read
	// Home's own dashboard-adjacent status line already performs; see
	// cmd/estate/dashboard.go's own doc comment). This used to be a count of
	// tmux lanes read through the deleted Python MCP server
	// (agent-estate#930) -- that seam is permanently gone in this tree, so
	// AGENTS now answers "how many dispatched turns does the estate's own
	// record say are still running," not "how many tmux panes are open."
	// nil means the ledger could not be read; an empty, non-nil map with
	// AgentsKnown true means it read fine and nothing is in flight, a real
	// answer.
	AgentsByState map[string]int
	AgentsKnown   bool
	// AgentsUnavailable explains AgentsKnown == false: "absent" (no
	// dispatch has ever been recorded -- a first-run estate, not a fault),
	// "unreadable" (the ledger exists but could not be parsed -- this is
	// NOT zero), or empty (no reason was recorded, e.g. a caller that
	// never set it). agent-estate#930's own "second-order lesson": a seam
	// unavailable for one tick and a seam that has never once worked must
	// not render identically forever -- this field is how the dashboard
	// tells them apart instead of collapsing both into a bare "unknown."
	AgentsUnavailable string

	OpenPRs     Count // gh pr list --state open, summed across board.ReposFor's repos
	MergedToday Count // gh pr list --state merged --search "merged:>=<today>", same repos
	VaultFacts  Count // len(knowledge.LoadIndex(vault)) -- one file read, matching that package's own progressive-disclosure rule

	// SpendToday is summed across internal/cost.Snapshot's Harnesses --
	// Snapshot's own doc comment already establishes this figure is
	// "today," so no new time-window logic is invented here.
	SpendToday USD
}
