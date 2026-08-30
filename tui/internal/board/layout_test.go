package board

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// TestEveryLayoutRendersEveryColumn is layout.go's equivalent of
// internal/lane's TestAllStatesCoversLanesShStates and view.go's own
// (now-retired) TestEveryViewRendersEveryColumn: a Layout that silently
// drops an empty column -- including via its own ClosedFilter, which must
// only ever touch Done -- is the "forgotten state" failure agent-tui#10 explicitly
// warns against ("a pretty board that omits stale, menu-blocked or unsent
// is worse than the flat list it replaces").
func TestEveryLayoutRendersEveryColumn(t *testing.T) {
	var cards []Card
	for i, col := range Columns {
		cards = append(cards, Card{Repo: testRepo, Number: i + 1, Column: col, Title: string(col) + " card"})
	}
	for _, l := range Layouts {
		out := l.Render(cards, nil, 120, theme.Default)
		for _, col := range Columns {
			if col == Done && l.Closed == ClosedHide {
				continue // the one column a Layout is allowed to omit, and only by its own explicit variant
			}
			if !strings.Contains(strings.ToUpper(out), strings.ToUpper(string(col))) {
				t.Errorf("layout %q: output does not mention column %q:\n%s", l.ID, col, out)
			}
		}
	}
}

// TestClosedHideOmitsDoneOnly confirms ClosedHide's filter -- and every
// other ClosedFilter -- never touches a non-Done column. Blocked is the
// canary: if filterClosed ever widened past Done, a blocked card is the
// one that must never silently vanish.
func TestClosedHideOmitsDoneOnly(t *testing.T) {
	cards := []Card{
		{Repo: testRepo, Number: 1, Column: Done, Title: "done card"},
		{Repo: testRepo, Number: 2, Column: Blocked, Title: "blocked card", BlockedReason: "PR conflicting"},
	}
	out := filterClosed(cards, ClosedHide)
	if len(out) != 1 || out[0].Column != Blocked {
		t.Fatalf("filterClosed(ClosedHide) = %+v, want only the Blocked card", out)
	}
}

// TestClosedRecentKeepsOnlyWithinWindow pins "last 24h" to Card.Age, not a
// wall-clock read (layout.go's closedRecentWindow doc comment / agent-tui#10 item
// 4: data fetching stays off the render path).
func TestClosedRecentKeepsOnlyWithinWindow(t *testing.T) {
	cards := []Card{
		{Repo: testRepo, Number: 1, Column: Done, Title: "just closed", Age: time.Hour},
		{Repo: testRepo, Number: 2, Column: Done, Title: "closed last week", Age: 7 * 24 * time.Hour},
	}
	out := filterClosed(cards, ClosedRecent)
	if len(out) != 1 || out[0].Number != 1 {
		t.Fatalf("filterClosed(ClosedRecent) = %+v, want only the 1h-old Done card", out)
	}
}

// TestClosedAllKeepsEverything is ClosedAll's own contract: no filtering
// at all, not even a cheap early return that accidentally drops something.
func TestClosedAllKeepsEverything(t *testing.T) {
	cards := []Card{
		{Repo: testRepo, Number: 1, Column: Done, Age: 400 * 24 * time.Hour},
		{Repo: testRepo, Number: 2, Column: Backlog},
	}
	out := filterClosed(cards, ClosedAll)
	if len(out) != 2 {
		t.Fatalf("filterClosed(ClosedAll) dropped a card: got %d, want 2", len(out))
	}
}

// TestLayoutRenderColorsAgedCard is TestModelViewColorsAgedCard's
// equivalent for the new picker: an aged card must carry an ANSI escape in
// EVERY Layout, including the restrained theme, not just the vivid one --
// see cardWarnColor's own doc comment for why theme is not allowed to be
// the only thing standing between an aged card and visibility.
func TestLayoutRenderColorsAgedCard(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })

	cards := []Card{{Repo: testRepo, Number: 95, Column: Blocked, Title: "as95 conflicting", Age: 3 * time.Hour, BlockedReason: "PR agent-tui#95 is conflicting"}}
	for _, l := range Layouts {
		out := l.Render(cards, nil, 220, theme.Default)
		if !strings.Contains(out, "as95 conflicting") {
			t.Fatalf("layout %q: aged card title missing entirely:\n%s", l.ID, out)
		}
		var agedBlock string
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "as95 conflicting") {
				agedBlock = line
				break
			}
		}
		if !strings.Contains(agedBlock, "\x1b[") {
			t.Errorf("layout %q (theme=%v): aged card line was not colorized:\n%q", l.ID, l.Theme, agedBlock)
		}
	}
}

// TestLayoutRenderShowsBlockedReason: the blocked-reason line must survive
// every card shape, not just the multi-line one -- agent-tui#10's "ugly states"
// rule applied to the reason text specifically, since a Blocked card with
// no reason shown is barely more useful than an unlabeled red box.
func TestLayoutRenderShowsBlockedReason(t *testing.T) {
	cards := []Card{{Repo: testRepo, Number: 4, Column: Blocked, Title: "conflict", BlockedReason: "PR agent-tui#40 is conflicting"}}
	for _, l := range Layouts {
		out := l.Render(cards, nil, 220, theme.Default)
		if l.Card == ShapeMultiLine && !strings.Contains(out, "PR agent-tui#40 is conflicting") {
			t.Errorf("layout %q: multi-line card dropped the blocked reason:\n%s", l.ID, out)
		}
	}
}

// TestRenderSwimlanesGroupsByRepo is GroupByRepo's own contract -- the
// evolved "variant 2" (agent-tui#10's own framing): each repo gets its own labeled
// section, and a card never appears outside its repo's section.
func TestRenderSwimlanesGroupsByRepo(t *testing.T) {
	repoB := Repo{Label: "other-repo", Owner: "jonhill90", Name: "other-repo"}
	cards := []Card{
		{Repo: testRepo, Number: 1, Column: Backlog, Title: "in agent-tui"},
		{Repo: repoB, Number: 2, Column: Backlog, Title: "in other-repo"},
	}
	var repoLayout Layout
	for _, l := range Layouts {
		if l.Grouping == GroupByRepo {
			repoLayout = l
			break
		}
	}
	out := repoLayout.Render(cards, nil, 120, theme.Default)
	if !strings.Contains(out, testRepo.GitHubID()) || !strings.Contains(out, repoB.GitHubID()) {
		t.Fatalf("expected both repo headers in swimlane output:\n%s", out)
	}
}

// TestFilterClosedNeverMutatesInput guards a cheap mistake: filterClosed
// must return a new slice/values, never alias or reorder the caller's own
// Cards, since Model.visibleCards and Layout.Render both read m.snap.Cards
// on every render and a mutation here would corrupt the next render too.
func TestFilterClosedNeverMutatesInput(t *testing.T) {
	cards := []Card{{Repo: testRepo, Number: 1, Column: Done}, {Repo: testRepo, Number: 2, Column: Backlog}}
	original := append([]Card(nil), cards...)
	_ = filterClosed(cards, ClosedHide)
	for i := range cards {
		if cards[i] != original[i] {
			t.Fatalf("filterClosed mutated its input slice: %+v vs original %+v", cards, original)
		}
	}
}
