package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBuildDashboardFetchReadsAgentsFromLedger is agent-estate#930's own
// reproduction: AGENTS used to read the deleted Python MCP server
// (sessionsFetch, always nil in this tree) and rendered "unknown" forever.
// buildDashboardFetch now reads src/estate's own Go ledger -- an in-flight
// "dispatched" record must count toward AgentsByState, and a terminal
// "complete" record must not (Status.InFlight's own filter).
func TestBuildDashboardFetchReadsAgentsFromLedger(t *testing.T) {
	dir := t.TempDir()
	ledger := filepath.Join(dir, "ledger.jsonl")
	body := `{"id":"930-1","issue":"#930","state":"dispatched","at":"2026-09-03T10:00:00Z"}
{"id":"920-1","issue":"#920","state":"complete","at":"2026-09-02T10:00:00Z"}
`
	if err := os.WriteFile(ledger, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	fetch := buildDashboardFetch("gh", "", ledger, nil, "")
	stats, err := fetch()
	if err != nil {
		t.Fatalf("fetch() error = %v", err)
	}
	if !stats.AgentsKnown {
		t.Fatalf("AgentsKnown = false, want true for a readable ledger; unavailable reason: %q", stats.AgentsUnavailable)
	}
	if got := stats.AgentsByState["dispatched"]; got != 1 {
		t.Errorf("AgentsByState[dispatched] = %d, want 1", got)
	}
	if _, terminal := stats.AgentsByState["complete"]; terminal {
		t.Errorf("AgentsByState carries a terminal state (complete); only in-flight turns should count")
	}
}

// TestBuildDashboardFetchDistinguishesAbsentFromUnreadable: a ledger that
// has never been written to (no dispatch has ever run) and one that exists
// but cannot be parsed must report distinct reasons, never the same bare
// "unknown" -- see dashboard.Stats.AgentsUnavailable's own doc comment.
func TestBuildDashboardFetchDistinguishesAbsentFromUnreadable(t *testing.T) {
	dir := t.TempDir()

	absentFetch := buildDashboardFetch("gh", "", filepath.Join(dir, "no-ledger.jsonl"), nil, "")
	absentStats, err := absentFetch()
	if err != nil {
		t.Fatalf("fetch() error = %v", err)
	}
	if absentStats.AgentsKnown || absentStats.AgentsUnavailable != "absent" {
		t.Errorf("absent ledger: AgentsKnown=%v AgentsUnavailable=%q, want (false, \"absent\")", absentStats.AgentsKnown, absentStats.AgentsUnavailable)
	}

	badLedger := filepath.Join(dir, "bad-ledger.jsonl")
	if err := os.WriteFile(badLedger, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	unreadableFetch := buildDashboardFetch("gh", "", badLedger, nil, "")
	unreadableStats, err := unreadableFetch()
	if err != nil {
		t.Fatalf("fetch() error = %v", err)
	}
	if unreadableStats.AgentsKnown || unreadableStats.AgentsUnavailable != "unreadable" {
		t.Errorf("unreadable ledger: AgentsKnown=%v AgentsUnavailable=%q, want (false, \"unreadable\")", unreadableStats.AgentsKnown, unreadableStats.AgentsUnavailable)
	}
}
