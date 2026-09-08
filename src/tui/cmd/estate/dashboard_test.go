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

// TestBuildDashboardFetchReadsVaultFacts is agent-estate#1304's own
// acceptance case, and fails against unmodified main: VaultFacts used to
// be len(knowledge.LoadIndex(vault)) against vaultDir/agent/index.md, a
// path A2-COMPLETION (agent-estate#1275) had already deleted a month
// earlier -- every real fetch left VaultFacts permanently unknown. This
// fixtures the vault's current shape (01 - Notes/01f - Facts/<id>.md) and
// proves the real disk count is read, not the capped index's length: 5
// real fact files, 1 index.md entry, VaultFacts must report 5.
func TestBuildDashboardFetchReadsVaultFacts(t *testing.T) {
	dir := t.TempDir()
	factsDir := filepath.Join(dir, "01 - Notes", "01f - Facts")
	if err := os.MkdirAll(factsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"20260810150000", "20260810150001", "20260810150002", "20260810150003", "20260810150004"} {
		content := "---\ntype: fact\ntitle: t\ncreated: 2026-08-10T15:00:00Z\nsource: fixture\n---\nbody\n"
		if err := os.WriteFile(filepath.Join(factsDir, id+".md"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	index := "---\nokf_version: \"0.1\"\n---\n\n# Facts\n\n- [[20260810150000]] — one entry, deliberately fewer than the real 5 fact files\n"
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}

	fetch := buildDashboardFetch("gh", "", filepath.Join(dir, "no-ledger.jsonl"), nil, dir)
	stats, err := fetch()
	if err != nil {
		t.Fatalf("fetch() error = %v", err)
	}
	if !stats.VaultFacts.Known {
		t.Fatalf("VaultFacts.Known = false, want true; unavailable reason: %q", stats.VaultFactsUnavailable)
	}
	if stats.VaultFacts.Value != 5 {
		t.Errorf("VaultFacts.Value = %d, want 5 (the real fact-file count, not index.md's 1 entry)", stats.VaultFacts.Value)
	}
}

// TestBuildDashboardFetchDistinguishesVaultAbsentFromUnreadable is
// agent-estate#1304's own second acceptance case: dashboard.go must stop
// swallowing knowledge.CountFacts' error into a bare "unknown" that
// cannot be told apart from "no vault configured" -- invariant 6, unknown
// means not offered, not broken. No vault configured and a vault whose
// 01 - Notes/01f - Facts/ cannot be read must report distinct reasons.
func TestBuildDashboardFetchDistinguishesVaultAbsentFromUnreadable(t *testing.T) {
	absentFetch := buildDashboardFetch("gh", "", filepath.Join(t.TempDir(), "no-ledger.jsonl"), nil, "")
	absentStats, err := absentFetch()
	if err != nil {
		t.Fatalf("fetch() error = %v", err)
	}
	if absentStats.VaultFacts.Known || absentStats.VaultFactsUnavailable != "absent" {
		t.Errorf("no vault configured: VaultFacts.Known=%v VaultFactsUnavailable=%q, want (false, \"absent\")", absentStats.VaultFacts.Known, absentStats.VaultFactsUnavailable)
	}

	// A vault dir that exists but has no 01 - Notes/01f - Facts/ at all --
	// unlike "" (never offered), this is a configured vault CountFacts
	// genuinely could not read.
	dir := t.TempDir()
	unreadableFetch := buildDashboardFetch("gh", "", filepath.Join(dir, "no-ledger.jsonl"), nil, dir)
	unreadableStats, err := unreadableFetch()
	if err != nil {
		t.Fatalf("fetch() error = %v", err)
	}
	if unreadableStats.VaultFacts.Known || unreadableStats.VaultFactsUnavailable != "unreadable" {
		t.Errorf("unreadable vault: VaultFacts.Known=%v VaultFactsUnavailable=%q, want (false, \"unreadable\")", unreadableStats.VaultFacts.Known, unreadableStats.VaultFactsUnavailable)
	}
	if absentStats.VaultFactsUnavailable == unreadableStats.VaultFactsUnavailable {
		t.Fatal("absent and unreadable rendered the same reason -- a reader cannot tell 'not offered' from 'read failed'")
	}
}
