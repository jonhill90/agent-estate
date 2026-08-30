package board

import (
	"fmt"
	"testing"
	"time"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// benchCards builds a synthetic but realistically-shaped fixture: n cards
// spread across 5 repos and every Column, some aged, some blocked -- large
// enough to make a per-Layout cost difference visible (agent-tui#10 item 4:
// "measure and report render time; if a variant is expensive, say so").
// This is a synthetic fixture, not real data -- the acceptance screenshots
// (agent-tui#10 item 1) are what must come from a real fetch; this benchmark exists
// to catch a Layout that is disproportionately expensive at a size larger
// than what this estate's four-repo issue count happens to be today.
func benchCards(n int) []Card {
	repos := []Repo{
		{Label: "agent-dotfiles", Owner: "jonhill90", Name: "agent-dotfiles"},
		{Label: "agent-supervisor", Owner: "jonhill90", Name: "agent-supervisor"},
		{Label: "skills", Owner: "jonhill90", Name: "skills"},
		{Label: "agent-evals", Owner: "jonhill90", Name: "agent-evals"},
		{Label: "agent-tui", Owner: "jonhill90", Name: "agent-tui"},
	}
	cards := make([]Card, 0, n)
	for i := 0; i < n; i++ {
		col := Columns[i%len(Columns)]
		c := Card{
			Repo:   repos[i%len(repos)],
			Number: i + 1,
			Title:  fmt.Sprintf("card #%d doing some realistic-length work item title", i+1),
			Column: col,
			Age:    time.Duration(i%5) * time.Hour,
		}
		if col == Blocked {
			c.BlockedReason = "lane agent-tui:2 is menu-blocked"
		}
		if col == InProgress {
			c.Session = "agent-tui"
		}
		cards = append(cards, c)
	}
	return cards
}

// BenchmarkLayoutRender is run per Layout, at two sizes -- the estate's
// real current scale (~40 open issues total, measured against the four
// repos this board watches) and 10x that, to see where cost actually comes
// from before it matters. Run with:
//
//	go test ./internal/board/... -bench=BenchmarkLayoutRender -benchtime=200x -run=^$
//
// and paste real ns/op output in the PR body, not a guess.
func BenchmarkLayoutRender(b *testing.B) {
	for _, size := range []int{40, 400} {
		cards := benchCards(size)
		for _, l := range Layouts {
			l := l
			b.Run(fmt.Sprintf("%s/n=%d", l.ID, size), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					_ = l.Render(cards, nil, 200, theme.Default)
				}
			})
		}
	}
}
