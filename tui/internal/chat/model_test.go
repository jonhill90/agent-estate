package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fetched drives Init() and its returned Cmd synchronously, then delivers a
// realistic WindowSizeMsg -- the same pattern internal/gallery/model_test.go
// and internal/rail's own model tests use to get a Model past its first
// fetch without a real tea.Program, extended here so transcriptVP/listVP
// are actually sized (sync only runs from Update, never from View).
func fetched(t *testing.T, width, height int) Model {
	t.Helper()
	m := New(NewFixtureSource())
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned a nil Cmd")
	}
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func sendKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return updated.(Model)
}

// TestFetchPrependsAllThread proves every render sees the unified feed
// (layouts.go's collapsed option [3]) at index 0, not as a separate mode.
func TestFetchPrependsAllThread(t *testing.T) {
	m := fetched(t, 100, 30)
	if len(m.threads) == 0 {
		t.Fatal("threads is empty after fetch")
	}
	if m.threads[0].ID != "all" {
		t.Errorf("threads[0].ID = %q, want \"all\"", m.threads[0].ID)
	}
}

// TestKeyVCyclesLayout is the seam the issue explicitly asks for, driven
// through a real Update the way rail.Model's own theme-cycle test drives
// theme.Cycle -- a real key delivered to a real Update, then a real View()
// actually differing, not a struct inspected in isolation.
func TestKeyVCyclesLayout(t *testing.T) {
	m := fetched(t, 100, 30)

	before := Layouts[m.layout].ID
	viewBefore := m.View()

	m = sendKey(t, m, "v")
	after := Layouts[m.layout].ID
	viewAfter := m.View()

	if before == after {
		t.Fatalf("layout did not change: still %q", after)
	}
	if viewBefore == viewAfter {
		t.Errorf("View() did not change after switching layout %q -> %q", before, after)
	}

	// Cycling len(Layouts) times must return to the start (wraps, never
	// runs off the end) -- same wraparound guarantee theme.Cycle documents.
	for i := 0; i < len(Layouts)-1; i++ {
		m = sendKey(t, m, "v")
	}
	if Layouts[m.layout].ID != before {
		t.Errorf("after a full cycle, layout = %q, want back to %q", Layouts[m.layout].ID, before)
	}
}

// TestSelectionClearsUnread is the navigation ask's other half: reading a
// thread must clear its own unread marker.
func TestSelectionClearsUnread(t *testing.T) {
	m := fetched(t, 100, 30)
	idx := -1
	for i, th := range m.threads {
		if th.Unread {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("fixture has no unread thread to test against")
	}
	m.jumpTo(idx)
	if m.threads[idx].Unread {
		t.Errorf("thread %d still Unread after jumpTo", idx)
	}
}

// TestKeyFOnlyFocusesInGrid proves "f" is a no-op in listLayout -- focus is
// gridLayout's own state (layouts.go: "focus folded in"), not a global
// mode that could leak into the other layout's rendering.
func TestKeyFOnlyFocusesInGrid(t *testing.T) {
	m := fetched(t, 100, 30)
	if Layouts[m.layout].ID != listLayout.ID {
		t.Fatalf("test assumes listLayout is the default, got %q", Layouts[m.layout].ID)
	}
	m = sendKey(t, m, "f")
	if m.focused != -1 {
		t.Errorf("focused = %d after \"f\" in listLayout, want -1 (no-op)", m.focused)
	}
}

// TestQuitStopsProgram proves "q" issues tea.Quit -- the same baseline
// every other pane's model_test.go checks.
func TestQuitStopsProgram(t *testing.T) {
	m := fetched(t, 100, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(Model)
	if !m.quitting {
		t.Error("quitting = false after \"q\"")
	}
	if cmd == nil {
		t.Fatal("Update returned a nil Cmd for \"q\", want tea.Quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("cmd() = %v, want tea.Quit()", msg)
	}
}

// TestViewRendersEveryMessageKindWithoutPanicking is a smoke test over
// both layouts against the fixture's full kind coverage -- render.go's own
// unit tests check text shape; this checks the Model composes it without
// crashing at realistic terminal sizes, list and grid alike.
func TestViewRendersEveryMessageKindWithoutPanicking(t *testing.T) {
	for _, l := range Layouts {
		m := fetched(t, 120, 40)
		for m.layout < len(Layouts) && Layouts[m.layout].ID != l.ID {
			m = sendKey(t, m, "v")
		}
		out := m.View()
		if !strings.Contains(out, "chat") {
			t.Errorf("layout %q: View() missing title", l.ID)
		}
	}
}

// TestLongThreadIsScrollableNotTruncated is agent-tui#20's acceptance item
// this package exists to satisfy, and the exact shape of agent-tui#29's
// regression (a board that silently dropped rows past its height): the
// fixture's fourth thread has 20+ rendered lines against an 8-row pane, so
// this asserts (a) the model itself knows content is hidden
// (scrollIndicator is non-empty) and (b) scrolling actually moves the
// window rather than being a no-op -- "reachable", not just "flagged."
func TestLongThreadIsScrollableNotTruncated(t *testing.T) {
	m := fetched(t, 100, 12) // a deliberately small pane -- see mx.bodyHeight math
	longIdx := -1
	for i, th := range m.threads {
		if th.ID == "lane-d" {
			longIdx = i
		}
	}
	if longIdx < 0 {
		t.Fatal("fixture has no lane-d thread")
	}
	m.jumpTo(longIdx)
	m = m.sync()

	if m.transcriptVP.TotalLineCount() <= m.transcriptVP.Height {
		t.Fatalf("transcriptVP content (%d lines) does not exceed its height (%d) -- fixture no longer forces scrolling",
			m.transcriptVP.TotalLineCount(), m.transcriptVP.Height)
	}
	if scrollIndicator(m.transcriptVP, m.styles()) == "" {
		t.Error("scrollIndicator is empty for an overflowing transcript -- hidden content must be visibly flagged")
	}
	if !m.transcriptVP.AtBottom() {
		t.Fatal("test assumes a freshly selected thread starts scrolled to its latest message")
	}

	before := m.transcriptVP.YOffset
	m = sendKey(t, m, "home")
	if m.transcriptVP.YOffset >= before {
		t.Errorf("\"home\" did not scroll up: YOffset %d -> %d", before, m.transcriptVP.YOffset)
	}
	if !m.transcriptVP.AtTop() {
		t.Error("\"home\" did not reach the top of the transcript")
	}

	m = sendKey(t, m, "end")
	if !m.transcriptVP.AtBottom() {
		t.Error("\"end\" did not return to the bottom of the transcript")
	}
}

// TestGridTileFlagsHiddenMessages proves gridLayout's own reachability
// story: an overloaded tile must render a "more" marker rather than just
// quietly showing fewer lines than a thread with more history, and "f"
// must be the way to actually read the rest.
func TestGridTileFlagsHiddenMessages(t *testing.T) {
	m := fetched(t, 100, 20)
	m = sendKey(t, m, "v") // -> gridLayout
	if Layouts[m.layout].ID != gridLayout.ID {
		t.Fatalf("test assumes \"v\" reaches gridLayout, got %q", Layouts[m.layout].ID)
	}
	out := m.View()
	if !strings.Contains(out, "more -- [f] to focus") {
		t.Errorf("gridLayout View() has no hidden-content marker for the long thread:\n%s", out)
	}
}
