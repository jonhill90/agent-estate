package board

import (
	"strings"
	"testing"
	"time"
)

// TestEveryViewRendersEveryColumn is this package's equivalent of
// internal/lane's TestEveryVariantNamesEveryState -- a layout variant that
// silently drops an empty column is exactly the "forgotten state" failure
// mode the rail's picker rule warns about, just applied to board columns
// instead of lane states.
func TestEveryViewRendersEveryColumn(t *testing.T) {
	var cards []Card
	for i, col := range Columns {
		cards = append(cards, Card{Repo: testRepo, Number: i + 1, Column: col, Title: string(col) + " card"})
	}
	for _, v := range Views {
		out := v.Render(cards, nil, 80)
		for _, col := range Columns {
			if !strings.Contains(strings.ToUpper(out), strings.ToUpper(string(col))) {
				t.Errorf("view %q: output does not mention column %q:\n%s", v.ID, col, out)
			}
		}
	}
}

func TestRenderByColumnFlagsAgedCard(t *testing.T) {
	cards := []Card{
		{Repo: testRepo, Number: 95, Column: Blocked, Title: "as95 conflicting", Age: 3 * time.Hour},
	}
	out := RenderByColumn(cards, nil, 80)
	if !strings.Contains(out, "!") {
		t.Errorf("expected an aged-card marker in output:\n%s", out)
	}
}

func TestRenderIncludesWIPOverCapacity(t *testing.T) {
	wip := []WIP{{Session: "agent-tui", InProgress: 3, Capacity: 2, OverCapacity: true}}
	out := RenderByColumn(nil, wip, 80)
	if !strings.Contains(out, "OVER") {
		t.Errorf("expected an OVER marker for over-capacity WIP:\n%s", out)
	}
}

func TestCardLineTruncatesToWidth(t *testing.T) {
	c := Card{Repo: testRepo, Number: 1, Title: strings.Repeat("x", 500)}
	line := cardLine(c, 40)
	if len(line) > 40 {
		t.Errorf("line length %d exceeds width 40: %q", len(line), line)
	}
}
