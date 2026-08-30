package flow

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/board"
)

func TestArrowTrackMarkerMovesWithFrame(t *testing.T) {
	for frame := 0; frame < 6; frame++ {
		track := arrowTrack(6, frame)
		if len(track) != 6 {
			t.Fatalf("arrowTrack(6, %d) = %q, want length 6", frame, track)
		}
		if strings.IndexByte(track, '>') != frame%6 {
			t.Errorf("arrowTrack(6, %d) = %q, marker not at expected position", frame, track)
		}
	}
}

func TestArrowTrackClampsShortWidth(t *testing.T) {
	if got := len(arrowTrack(1, 0)); got != 3 {
		t.Errorf("arrowTrack(1, 0) length = %d, want clamped to 3", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{-5 * time.Second, "0s"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "2m0s"},
		{25 * time.Hour, "25h0m0s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.d); got != c.want {
			t.Errorf("formatDuration(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}

func fakeSnap() board.Snapshot {
	return board.Snapshot{Cards: []board.Card{
		{Repo: board.Repo{Label: "estate"}, Number: 1, Title: "in progress", Column: board.InProgress, Age: 3 * time.Hour, CycleTime: 3 * time.Hour},
		{Repo: board.Repo{Label: "estate"}, Number: 2, Title: "in review", Column: board.InReview, Age: 10 * time.Minute, PRNumber: 9},
		{Repo: board.Repo{Label: "estate"}, Number: 3, Title: "blocked one", Column: board.Blocked, Age: 5 * time.Hour, BlockedReason: "lane x is hung"},
		{Repo: board.Repo{Label: "estate"}, Number: 4, Title: "queued", Column: board.Backlog},
		{Repo: board.Repo{Label: "estate"}, Number: 5, Title: "done", Column: board.Done, PRNumber: 11},
	}}
}

func TestWithSnapshotPopulatesItems(t *testing.T) {
	m := New().WithSnapshot(fakeSnap(), time.Now(), nil)
	if len(m.items) != 5 {
		t.Fatalf("items = %d, want 5", len(m.items))
	}
	if m.lastFetched.IsZero() {
		t.Error("lastFetched not set after WithSnapshot(..., nil)")
	}
}

func TestWithSnapshotErrLeavesItemsAloneButSetsFetchErr(t *testing.T) {
	m := New().WithSnapshot(fakeSnap(), time.Now(), nil)
	m = m.WithSnapshot(board.Snapshot{}, time.Time{}, errFake{})
	if len(m.items) != 5 {
		t.Errorf("items = %d, want the previous 5 kept on an error push (never cleared to look like a healthy empty estate)", len(m.items))
	}
	if m.fetchErr == nil {
		t.Error("fetchErr not set")
	}
}

func TestUpdateToggleShowAll(t *testing.T) {
	m := New().WithSnapshot(fakeSnap(), time.Now(), nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	if !strings.Contains(m.bodyContent(), "blocked one") || strings.Contains(m.bodyContent(), "queued") {
		t.Errorf("default body should show in-flight only: %q", m.bodyContent())
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = next.(Model)
	if !m.showAll {
		t.Fatal("'a' should toggle showAll on")
	}
	if !strings.Contains(m.bodyContent(), "queued") || !strings.Contains(m.bodyContent(), "done") {
		t.Errorf("showAll body should include queued/done: %q", m.bodyContent())
	}
}

func TestFetchErrSurfacesInHeader(t *testing.T) {
	m := New().WithSnapshot(board.Snapshot{}, time.Time{}, errFake{})
	view := strings.Join(m.header(), "\n")
	if !strings.Contains(view, "! unavailable") {
		t.Errorf("header should surface a fetch error, got %q", view)
	}
}

type errFake struct{}

func (errFake) Error() string { return "fake fetch failure" }

func TestQuitKeys(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c"} {
		m := New()
		var msg tea.KeyMsg
		if key == "ctrl+c" {
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
		}
		next, cmd := m.Update(msg)
		nm := next.(Model)
		if !nm.quitting {
			t.Errorf("%s should set quitting", key)
		}
		if cmd == nil {
			t.Errorf("%s should return tea.Quit", key)
		}
	}
}

// manyInFlightSnap returns enough Working cards that, at a realistic pane
// height, the body viewport cannot show them all at once -- the fixture
// TestScrollIndicatorAppearsWhenContentOverflows needs to exercise
// scrollIndicator's non-empty branch.
func manyInFlightSnap(n int) board.Snapshot {
	var cards []board.Card
	for i := 1; i <= n; i++ {
		cards = append(cards, board.Card{Repo: board.Repo{Label: "x"}, Number: i, Title: "item", Column: board.InProgress, Age: time.Duration(i) * time.Minute})
	}
	return board.Snapshot{Cards: cards}
}

// TestScrollIndicatorAppearsWhenContentOverflows is agent-tui#64's own
// verification requirement (AGENTS.md): content taller than the pane must
// be reachable AND the user must be able to tell something is hidden.
// bubbles/viewport gives the former for free (scrolling); this pins that
// scrollIndicator supplies the latter, which viewport does not render on
// its own.
func TestScrollIndicatorAppearsWhenContentOverflows(t *testing.T) {
	m := New().WithSnapshot(manyInFlightSnap(50), time.Now(), nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	if got := m.scrollIndicator(); got == "" || !strings.Contains(got, "more below") {
		t.Errorf("scrollIndicator() = %q, want a non-empty hint that more content is hidden below", got)
	}
	if !strings.Contains(m.View(), "more below") {
		t.Errorf("View() does not surface the scroll indicator anywhere:\n%s", m.View())
	}
}

// TestScrollIndicatorEmptyWhenEverythingFits is the flip side: a short
// list must not show a hint pointing at nothing.
func TestScrollIndicatorEmptyWhenEverythingFits(t *testing.T) {
	m := New().WithSnapshot(fakeSnap(), time.Now(), nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	if got := m.scrollIndicator(); got != "" {
		t.Errorf("scrollIndicator() = %q, want empty when every in-flight item already fits", got)
	}
}

// TestViewNeverExceedsHeightBudget is flow's own version of
// shell.TestViewNeverExceedsContentHeightPlusFooter -- agent-tui#29's
// lesson applied to a brand-new pane before it ships, not found the same
// way agent-tui#29 was (a live pane silently overrunning its budget).
func TestViewNeverExceedsHeightBudget(t *testing.T) {
	m := New().WithSnapshot(fakeSnap(), time.Now(), nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)

	got := strings.Count(m.View(), "\n") + 1
	if got != 24 {
		t.Errorf("View() rendered %d lines, want exactly 24 (the height it was given)", got)
	}
}

// Test80x24Renders is agent-tui#64's own QA requirement: verify a small, realistic
// terminal size doesn't panic and produces a non-empty, bounded frame --
// the actual "capture the pane and read it" check happens in a live tmux
// pane (see the PR description), this only pins the same size in a test so
// a regression here fails fast without a human at a terminal.
func Test80x24Renders(t *testing.T) {
	m := New()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = next.(Model)
	m = m.WithSnapshot(fakeSnap(), time.Now(), nil)
	view := m.View()
	if view == "" {
		t.Fatal("View() returned empty at 80x24")
	}
	if strings.Count(view, "\n")+1 != 24 {
		t.Errorf("80x24 View() = %d lines, want 24", strings.Count(view, "\n")+1)
	}
}
