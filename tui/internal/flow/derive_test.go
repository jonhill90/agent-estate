package flow

import (
	"testing"
	"time"

	"github.com/jonhill90/agent-tui/internal/board"
)

func card(col board.Column, age time.Duration, prNumber int, blockedReason string) board.Card {
	return board.Card{Column: col, Age: age, PRNumber: prNumber, BlockedReason: blockedReason}
}

func TestStageOfMapsEveryColumn(t *testing.T) {
	cases := []struct {
		col  board.Column
		want Stage
	}{
		{board.Backlog, StageQueued},
		{board.InProgress, StageWorking},
		{board.InReview, StageReview},
		{board.Blocked, StageBlocked},
		{board.Done, StageDone},
	}
	for _, c := range cases {
		if got := stageOf(card(c.col, 0, 0, "")); got != c.want {
			t.Errorf("stageOf(%s) = %s, want %s", c.col, got, c.want)
		}
	}
}

func TestDeriveItemsTerminalSplitsMergedFromClosed(t *testing.T) {
	items := DeriveItems([]board.Card{
		card(board.Done, 0, 42, ""), // a PR closes it -- merged
		card(board.Done, 0, 0, ""),  // no PR -- closed with no merge
	})
	if items[0].Terminal != "merged" {
		t.Errorf("items[0].Terminal = %q, want merged", items[0].Terminal)
	}
	if items[1].Terminal != "closed" {
		t.Errorf("items[1].Terminal = %q, want closed", items[1].Terminal)
	}
	if items[0].Stage != StageDone || items[1].Stage != StageDone {
		t.Errorf("both items should be StageDone")
	}
}

func TestDeriveItemsNonDoneHasNoTerminal(t *testing.T) {
	items := DeriveItems([]board.Card{card(board.InProgress, 0, 0, "")})
	if items[0].Terminal != "" {
		t.Errorf("Terminal = %q, want empty for a non-Done stage", items[0].Terminal)
	}
}

func TestInFlightExcludesQueuedAndDoneSortsOldestFirst(t *testing.T) {
	items := DeriveItems([]board.Card{
		card(board.Backlog, 10*time.Hour, 0, ""),
		card(board.InProgress, 1*time.Hour, 0, ""),
		card(board.InReview, 5*time.Hour, 0, ""),
		card(board.Blocked, 3*time.Hour, 0, "lane x is hung"),
		card(board.Done, 20*time.Hour, 7, ""),
	})
	got := InFlight(items)
	if len(got) != 3 {
		t.Fatalf("InFlight returned %d items, want 3 (Working/Review/Blocked only): %+v", len(got), got)
	}
	if got[0].Card.Age != 5*time.Hour || got[1].Card.Age != 3*time.Hour || got[2].Card.Age != 1*time.Hour {
		t.Errorf("InFlight not sorted oldest-Age-first: %+v", got)
	}
}

func TestStageCounts(t *testing.T) {
	items := DeriveItems([]board.Card{
		card(board.Backlog, 0, 0, ""),
		card(board.Backlog, 0, 0, ""),
		card(board.InProgress, 0, 0, ""),
		card(board.Done, 0, 1, ""),
	})
	counts := StageCounts(items)
	if counts[StageQueued] != 2 || counts[StageWorking] != 1 || counts[StageDone] != 1 {
		t.Errorf("counts = %+v", counts)
	}
}
