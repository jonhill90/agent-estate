package board

import (
	"testing"
	"time"

	"github.com/jonhill90/keelson/internal/lane"
)

var testRepo = Repo{Label: "agent-tui", Owner: "jonhill90", Name: "agent-tui"}

func derive1(t *testing.T, now time.Time, issue Issue, prs []PR, rows []TaskRow, lanes []lane.Lane) Card {
	t.Helper()
	issuesByRepo := map[string][]Issue{testRepo.GitHubID(): {issue}}
	prsByRepo := map[string][]PR{testRepo.GitHubID(): prs}
	cards := Derive(now, []Repo{testRepo}, issuesByRepo, prsByRepo, rows, lanes)
	if len(cards) != 1 {
		t.Fatalf("got %d cards, want 1: %+v", len(cards), cards)
	}
	return cards[0]
}

func TestDeriveBacklogNoLedgerTask(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 1, State: "OPEN", CreatedAt: "2026-08-14T10:00:00Z"}
	card := derive1(t, now, issue, nil, nil, nil)
	if card.Column != Backlog {
		t.Errorf("column = %s, want Backlog", card.Column)
	}
	if card.Age != 2*time.Hour {
		t.Errorf("age = %s, want 2h", card.Age)
	}
	if card.CycleTime != 0 {
		t.Errorf("cycle time = %s, want 0 (never dispatched)", card.CycleTime)
	}
}

func TestDeriveInProgressOpenTaskAssignedLane(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 2, State: "OPEN"}
	rows := []TaskRow{{
		SourceKind: "issue", Repo: testRepo, Number: "2",
		TaskStatus: "running", Lane: "agent-tui:2",
		CreatedAt: now.Add(-90 * time.Minute).Unix(), UpdatedAt: now.Add(-5 * time.Minute).Unix(),
	}}
	lanes := []lane.Lane{{Name: "agent-tui:2", State: "busy"}}
	card := derive1(t, now, issue, nil, rows, lanes)
	if card.Column != InProgress {
		t.Errorf("column = %s, want InProgress", card.Column)
	}
	if card.Lane != "agent-tui:2" || card.Session != "agent-tui" {
		t.Errorf("lane/session = %q/%q", card.Lane, card.Session)
	}
	if card.CycleTime != 90*time.Minute {
		t.Errorf("cycle time = %s, want 90m", card.CycleTime)
	}
}

func TestDeriveInReviewOpenPRClean(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 3, State: "OPEN"}
	prs := []PR{{
		Number: 30, State: "OPEN", MergeStateStatus: "CLEAN",
		CreatedAt: "2026-08-14T11:00:00Z", UpdatedAt: "2026-08-14T11:30:00Z",
		ClosingIssues: []closingRef{{Number: 3}},
	}}
	rows := []TaskRow{{SourceKind: "issue", Repo: testRepo, Number: "3", TaskStatus: "running", Lane: "agent-tui:2"}}
	lanes := []lane.Lane{{Name: "agent-tui:2", State: "free"}}
	card := derive1(t, now, issue, prs, rows, lanes)
	if card.Column != InReview {
		t.Errorf("column = %s, want InReview", card.Column)
	}
	if card.PRNumber != 30 {
		t.Errorf("PR number = %d, want 30", card.PRNumber)
	}
	if card.Age != time.Hour {
		t.Errorf("age = %s, want 1h (since PR created)", card.Age)
	}
}

func TestDeriveBlockedByConflictingPR(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 4, State: "OPEN"}
	prs := []PR{{
		Number: 40, State: "OPEN", MergeStateStatus: "CONFLICTING",
		CreatedAt: "2026-08-14T09:00:00Z", UpdatedAt: "2026-08-14T10:00:00Z",
		ClosingIssues: []closingRef{{Number: 4}},
	}}
	card := derive1(t, now, issue, prs, nil, nil)
	if card.Column != Blocked {
		t.Fatalf("column = %s, want Blocked", card.Column)
	}
	if card.BlockedReason == "" {
		t.Error("expected a non-empty BlockedReason")
	}
}

func TestDeriveBlockedByHungLane(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 5, State: "OPEN"}
	rows := []TaskRow{{
		SourceKind: "issue", Repo: testRepo, Number: "5",
		TaskStatus: "running", Lane: "agent-tui:3", UpdatedAt: now.Add(-3 * time.Hour).Unix(),
	}}
	lanes := []lane.Lane{{Name: "agent-tui:3", State: "hung"}}
	card := derive1(t, now, issue, nil, rows, lanes)
	if card.Column != Blocked {
		t.Fatalf("column = %s, want Blocked", card.Column)
	}
	if card.Age != 3*time.Hour {
		t.Errorf("age = %s, want 3h", card.Age)
	}
}

func TestDeriveBlockedByMenuBlockedLaneDuringReview(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 6, State: "OPEN"}
	prs := []PR{{Number: 60, State: "OPEN", MergeStateStatus: "CLEAN", ClosingIssues: []closingRef{{Number: 6}}}}
	rows := []TaskRow{{SourceKind: "issue", Repo: testRepo, Number: "6", TaskStatus: "running", Lane: "agent-tui:4"}}
	lanes := []lane.Lane{{Name: "agent-tui:4", State: "menu-blocked"}}
	card := derive1(t, now, issue, prs, rows, lanes)
	if card.Column != Blocked {
		t.Errorf("column = %s, want Blocked (menu-blocked lane during review)", card.Column)
	}
}

func TestDeriveDoneClosedIssue(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 7, State: "CLOSED", ClosedAt: "2026-08-14T11:00:00Z"}
	rows := []TaskRow{{
		SourceKind: "issue", Repo: testRepo, Number: "7", TaskStatus: "complete",
		CreatedAt: now.Add(-4 * time.Hour).Unix(),
	}}
	card := derive1(t, now, issue, nil, rows, nil)
	if card.Column != Done {
		t.Fatalf("column = %s, want Done", card.Column)
	}
	if card.CycleTime != 3*time.Hour {
		t.Errorf("cycle time = %s, want 3h (dispatched -4h, closed -1h)", card.CycleTime)
	}
}

func TestDeriveDoneMergedPRIssueStillOpen(t *testing.T) {
	// "Done | issue closed OR PR merged" -- a merged PR alone must move the
	// card even if GitHub hasn't auto-closed the issue yet.
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 8, State: "OPEN"}
	prs := []PR{{
		Number: 80, State: "MERGED", MergedAt: "2026-08-14T11:45:00Z",
		ClosingIssues: []closingRef{{Number: 8}},
	}}
	card := derive1(t, now, issue, prs, nil, nil)
	if card.Column != Done {
		t.Errorf("column = %s, want Done", card.Column)
	}
}

func TestDeriveNeverStoresColumnAcrossCalls(t *testing.T) {
	// The whole point of agent-tui#6: calling Derive twice with the SAME
	// static inputs but nothing else in between must be idempotent -- if a
	// stray mutable field crept onto Card and were reused, this would drift.
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	issue := Issue{Number: 9, State: "OPEN"}
	rows := []TaskRow{{SourceKind: "issue", Repo: testRepo, Number: "9", TaskStatus: "running", Lane: "agent-tui:2"}}
	first := derive1(t, now, issue, nil, rows, nil)
	second := derive1(t, now, issue, nil, rows, nil)
	if first.Column != second.Column {
		t.Errorf("column drifted between identical calls: %s vs %s", first.Column, second.Column)
	}
	// Now change only the underlying fact (issue closes) and confirm the
	// SAME Derive call reflects it with no other state to update.
	issue.State = "CLOSED"
	third := derive1(t, now, issue, nil, rows, nil)
	if third.Column != Done {
		t.Errorf("column after closing the issue = %s, want Done", third.Column)
	}
}

func TestComputeWIPFlagsOverCapacityPerSession(t *testing.T) {
	cards := []Card{
		{Column: InProgress, Session: "agent-tui"},
		{Column: InProgress, Session: "agent-tui"},
		{Column: InProgress, Session: "agent-tui"}, // 3rd in the same session -- over capacity
		{Column: InProgress, Session: "agent-supervisor"},
		{Column: Backlog, Session: "agent-tui"}, // not counted: not InProgress
	}
	got := ComputeWIP(cards)
	byName := map[string]WIP{}
	for _, w := range got {
		byName[w.Session] = w
	}
	tui := byName["agent-tui"]
	if tui.InProgress != 3 || !tui.OverCapacity {
		t.Errorf("agent-tui WIP = %+v, want 3/over", tui)
	}
	sup := byName["agent-supervisor"]
	if sup.InProgress != 1 || sup.OverCapacity {
		t.Errorf("agent-supervisor WIP = %+v, want 1/under", sup)
	}
}
