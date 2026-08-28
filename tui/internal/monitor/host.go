// Package monitor is Observe -> Monitoring's real pane: host health (load
// average, swap, this machine's own Claude process count) plus agent
// health (lane state counts, the SAME sessions fetch internal/dashboard,
// internal/rail and internal/agents already use).
//
// Checked before writing a line of this package (2026-08-22): no existing
// agent-supervisor script or MCP tool carries host load, per-core count,
// swap, or a process count -- `ls scripts/supervisor/` has quota.sh,
// quota-watch.sh, quota-watch-recover.sh and nothing named health/monitor/
// status, and quota.sh's own `summary` line (internal/cost/quota.go's
// quotaLineRE) carries only per-provider session/weekly usage percentages,
// no host metrics at all. This package is new plumbing, not a second
// reader of a source another pane already owns -- the one exception is
// agent state counts, which reuse rail.SessionsFetcher exactly as
// internal/dashboard.Stats.AgentsByState already does (cmd/estate's own
// buildMonitorFetch composes it, never a second MCP connection).
package monitor

// Figure is a float reading that may not be known -- internal/cost.Figure's
// own shape, repeated here (not imported) so a load-average carrying the
// same type as a dollar figure never invites a reader to wonder why.
type Figure struct {
	Known bool
	Value float64
}

// KnownFigure wraps a value this package's HostRunner actually read.
func KnownFigure(v float64) Figure { return Figure{Known: true, Value: v} }

// Count is an int reading that may not be known -- internal/dashboard.Count's
// own shape, repeated for the same reason Figure is.
type Count struct {
	Known bool
	Value int
}

// KnownCount wraps a value this package's HostRunner actually read.
func KnownCount(v int) Count { return Count{Known: true, Value: v} }

// Host is one read of the machine agent-tui itself is running on -- never
// the estate's aggregate state, that is AgentHealth below. Every field is
// independently Known/unknown: a swap reading failing must never blank out
// load average, the same posture internal/cost.Snapshot already takes
// per-harness.
type Host struct {
	// Cores is runtime.NumCPU() -- always known. It is read in-process, not
	// via a subprocess, and cannot fail the way the fields below can, so it
	// carries no Known bit of its own (the same reasoning
	// internal/connectors.Connection.Provider gives for a constant that is
	// "self-evident, not read from anywhere," applied here to a value the
	// Go runtime itself guarantees rather than a file this package reads).
	Cores int

	LoadAvg1  Figure
	LoadAvg5  Figure
	LoadAvg15 Figure

	SwapUsedPercent Figure

	// ClaudeProcesses counts this host's OWN processes whose command line
	// mentions "claude" (case-insensitive) -- a coarse, honest measure
	// ("how many processes look claude-shaped on this box"), not a claim
	// about which estate lane owns which process; agent-tui has no seam
	// that maps a host PID to a lane (that mapping lives in tmux/the
	// ledger, not here).
	ClaudeProcesses Count
}

// AgentHealth is estate agent state, counted -- built from the SAME
// sessions fetch every other pane in this module already shares
// (rail.SessionsFetcher), never a second MCP connection. Known false means
// the fetch itself failed; a non-nil, empty ByState with Known true means
// it succeeded and found no lanes at all -- a real answer, not a gap.
type AgentHealth struct {
	Known   bool
	ByState map[string]int
	Total   int
}

// Snapshot is one fetch's worth of Monitoring figures -- Host and
// AgentHealth fail independently, mirroring internal/dashboard.Stats' own
// "one source's failure must not blank the other" rule.
type Snapshot struct {
	Host    Host
	HostErr error // non-nil only if HostRunner itself could not even attempt a read

	Agents AgentHealth
}

// Fetcher retrieves the current Snapshot -- the one adapter seam this
// package's Model depends on (AGENTS.md's discipline). cmd/estate composes
// the real implementation from ExecHostRunner plus an existing
// rail.SessionsFetcher; every test in this package builds a fake instead.
type Fetcher func() (Snapshot, error)
