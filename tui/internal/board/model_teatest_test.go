package board

// This file's tests DRIVE the real tea.Program via
// charmbracelet/x/exp/teatest -- sending tea.KeyMsg through the same
// event loop the compiled binary uses, then reading the program's actual
// rendered output -- rather than calling Model.Update directly the way
// model_test.go's older tests do. agent-tui#29's own text is explicit
// about why: "the regression you are closing... is 'nobody pressed the
// key,' so a test that does not press the key does not close it," citing
// agent-tui#23 as a prior painted, inert control that shipped because no
// delivery check ever pressed a key. TestScrollKeysMoveTheViewport below is
// that pressed key, for real, through teatest.

import (
	"bytes"
	"io"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// manyCardSnapshot builds a Snapshot with more Backlog cards than a small
// terminal can show at once -- the exact shape agent-tui#29 reported
// ("BACKLOG (15) holds more cards than fit... the column header row itself
// scrolls off the top"). Card numbers, not titles, are what the assertions
// below key on: a narrow column pane truncates a card's title long before
// it truncates the "#N " prefix renderCard writes ahead of the title
// (layout.go), so "#1 "/"#30 " stay reliable markers at any pane width a
// test picks, where a full title string would not.
func manyCardSnapshot(n int) Snapshot {
	cards := make([]Card, 0, n)
	for i := 0; i < n; i++ {
		cards = append(cards, Card{Repo: testRepo, Number: i + 1, Column: Backlog, Title: "x"})
	}
	return Snapshot{Cards: cards, Repos: []Repo{testRepo}}
}

// TestScrollKeysMoveTheViewport drives 'j' (down) against a real running
// Program at a terminal too small for the fetched content, and asserts the
// rendered output actually changes -- a card only visible after scrolling
// appears, and the scroll indicator's "more above" count moves off zero.
// Pressing a key that no handler exists for would leave the output
// byte-for-byte identical; this is what tells that apart from a real fix.
func TestScrollKeysMoveTheViewport(t *testing.T) {
	snap := manyCardSnapshot(30)
	m := New(func() (Snapshot, error) { return snap, nil })

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 15))
	t.Cleanup(func() { _ = tm.Quit() })

	before := drainUntil(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("#1 "))
	})
	if bytes.Contains(before, []byte("#30 ")) {
		t.Fatalf("card #30 already visible before scrolling -- this test needs content taller than the pane:\n%s", before)
	}
	if !bytes.Contains(before, []byte("more below")) {
		t.Fatalf("no scroll indicator before scrolling, but content overflows the pane -- off-screen content must be announced:\n%s", before)
	}

	for i := 0; i < 120; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}

	// after is everything written to the program's output SINCE before was
	// drained -- i.e. only the frames the 60 'j' presses actually produced,
	// not the cumulative stream (which would still contain "#1 " from the
	// very first frame forever, making a "no longer visible" assertion
	// meaningless against it).
	after := drainUntil(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("#30 "))
	})
	if bytes.Contains(after, []byte("#1 ")) {
		t.Errorf("card #1's line should have scrolled out of view after 60 'j' presses:\n%s", after)
	}
	if !bytes.Contains(after, []byte("more above")) {
		t.Errorf("no scroll indicator after scrolling down from the top -- off-screen content above must be announced too:\n%s", after)
	}
}

// drainUntil reads r (accumulating everything read, like teatest.WaitFor's
// own doWaitFor) until condition matches, and returns what it accumulated
// -- unlike teatest.WaitFor, which reports only pass/fail and discards the
// bytes. r is a Program's output stream: each Read drains it, so a second
// drainUntil call on the same reader sees only what was written since the
// first call returned, not the full history -- see TestScrollKeysMoveTheViewport's
// use of this for "after" for why that matters.
func drainUntil(tb testing.TB, r io.Reader, condition func([]byte) bool) []byte {
	tb.Helper()
	var b bytes.Buffer
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := io.ReadAll(io.TeeReader(r, &b)); err != nil {
			tb.Fatalf("drainUntil: %s", err)
		}
		if condition(b.Bytes()) {
			return b.Bytes()
		}
		time.Sleep(20 * time.Millisecond)
	}
	tb.Fatalf("drainUntil: condition not met after 8s. Last output:\n%s", b.String())
	return nil
}

// TestDefaultRefreshIntervalIsNotFiveSeconds is agent-tui#28's own
// acceptance item: "a test asserts the interval is configurable and that
// the default is not 5s."
func TestDefaultRefreshIntervalIsNotFiveSeconds(t *testing.T) {
	if DefaultRefreshInterval == 5*time.Second {
		t.Fatalf("DefaultRefreshInterval = %s, must not be the 5s that measured at ~8,160 GraphQL points/hr (agent-tui#28)", DefaultRefreshInterval)
	}
	if DefaultRefreshInterval <= 0 {
		t.Fatalf("DefaultRefreshInterval = %s, must be positive", DefaultRefreshInterval)
	}
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	if m.refreshInterval != DefaultRefreshInterval {
		t.Fatalf("New()'s refreshInterval = %s, want DefaultRefreshInterval (%s)", m.refreshInterval, DefaultRefreshInterval)
	}
}

// TestNewWithRefreshIntervalOverridesDefault is the "configurable" half of
// the same acceptance item -- cmd/agent-tui's -board-refresh flag calls
// NewWithRefreshInterval directly, so this is that call site's own
// contract test.
func TestNewWithRefreshIntervalOverridesDefault(t *testing.T) {
	m := NewWithRefreshInterval(func() (Snapshot, error) { return Snapshot{}, nil }, 30*time.Second)
	if m.refreshInterval != 30*time.Second {
		t.Fatalf("refreshInterval = %s, want 30s", m.refreshInterval)
	}
}

// TestRefreshTicksAtTheConfiguredInterval drives the real program with a
// short, non-default interval and counts fetches over a measured window,
// asserting the observed rate matches the configured interval rather than
// the old hardcoded 5s -- this is the same "measure it running" discipline
// agent-tui#28's own issue used (points/min, board running vs stopped),
// applied to fetch count instead of GraphQL points since this test has no
// real `gh` to meter.
func TestRefreshTicksAtTheConfiguredInterval(t *testing.T) {
	fetchCount := 0
	fetch := func() (Snapshot, error) {
		fetchCount++
		return Snapshot{}, nil
	}
	m := NewWithRefreshInterval(fetch, 40*time.Millisecond)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	t.Cleanup(func() { _ = tm.Quit() })

	time.Sleep(250 * time.Millisecond)
	_ = tm.Quit()
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))

	// One immediate fetch from Init, plus roughly one per tick over 250ms
	// at a 40ms interval (~6). A 5s-interval build would show 1 (the
	// initial fetch only, no tick landing inside 250ms) -- see this test's
	// mutation check in the PR body.
	if fetchCount < 3 {
		t.Fatalf("fetchCount = %d over 250ms at a 40ms interval, want >= 3 -- refreshInterval is not being honored", fetchCount)
	}
}
