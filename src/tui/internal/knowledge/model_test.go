package knowledge

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/src/tui/internal/memgraph"
)

func testEntries() []IndexEntry {
	return []IndexEntry{
		{Slug: "zebra-fact", Title: "Zebra fact", Description: "z"},
		{Slug: "alpha-fact", Title: "Alpha fact", Description: "a"},
	}
}

// TestFetchResultPopulatesRows drives Update directly (cheaper than a
// full teatest.Program, the same two-tier discipline every other package
// in this module uses).
func TestFetchResultPopulatesRows(t *testing.T) {
	m := New(nil, nil)
	next, _ := m.Update(fetchResultMsg{entries: testEntries()})
	m = next.(Model)

	rows := m.Rows()
	if len(rows) != 2 || rows[0].Slug != "zebra-fact" {
		t.Fatalf("Rows() = %+v, want index order preserved (zebra-fact first)", rows)
	}
	if rows[0].Type != nil || rows[0].Created != nil {
		t.Fatalf("rows[0] = %+v, want Type/Created nil for a never-opened row", rows[0])
	}
}

// TestFetchErrorRendersVisibly is this pane's own "blind, not quiet"
// case, matching every other package's identical test.
func TestFetchErrorRendersVisibly(t *testing.T) {
	m := New(nil, nil)
	next, _ := m.Update(fetchResultMsg{err: errors.New("$AGENT_MEMORY_VAULT is not set")})
	m = next.(Model)
	out := m.View()
	if !strings.Contains(out, "$AGENT_MEMORY_VAULT is not set") {
		t.Fatalf("fetch error not rendered:\n%s", out)
	}
}

// TestSKeyTogglesSort covers the one sortable dimension available
// without opening anything: title.
func TestSKeyTogglesSort(t *testing.T) {
	m := New(nil, nil)
	next, _ := m.Update(fetchResultMsg{entries: testEntries()})
	m = next.(Model)

	if m.Rows()[0].Slug != "zebra-fact" {
		t.Fatalf("index order = %+v, want zebra-fact first", m.Rows())
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	m = next.(Model)
	if m.Rows()[0].Slug != "alpha-fact" {
		t.Fatalf("after [s], Rows() = %+v, want alpha-fact first (alphabetical by title)", m.Rows())
	}
}

// TestEnterOpensAFactAndCachesIt is the progressive-disclosure contract
// at the Model level: [enter] triggers exactly one FactLoader call for
// the selected row, and a second [enter] on the SAME row (after it is
// cached) does not call it again.
func TestEnterOpensAFactAndCachesIt(t *testing.T) {
	calls := 0
	loadFact := func(slug string) (Fact, error) {
		calls++
		return Fact{Slug: slug, Type: "project", Title: "T", Body: "body text"}, nil
	}
	m := New(nil, loadFact)
	next, _ := m.Update(fetchResultMsg{entries: testEntries()})
	m = next.(Model)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	if m.mode != modeReading {
		t.Fatal("[enter] did not switch to reading mode")
	}
	if cmd == nil {
		t.Fatal("[enter] on an unopened row returned a nil cmd, want a fact load")
	}
	msg := cmd()
	next, _ = m.Update(msg)
	m = next.(Model)
	if calls != 1 {
		t.Fatalf("loadFact called %d times, want 1", calls)
	}
	if !strings.Contains(m.View(), "body text") {
		t.Fatalf("View() missing the opened fact's body:\n%s", m.View())
	}

	// Back to the list, then open the SAME row again.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	if cmd != nil {
		t.Fatal("re-opening an already-cached row returned a non-nil cmd, want no re-read")
	}
	if calls != 1 {
		t.Fatalf("loadFact called %d times after re-opening the cached row, want still 1", calls)
	}
}

// TestFactLoadErrorRendersVisibly is LoadFact's own error path (a stale
// index link) surfaced through the Model -- never a silent fall-back to
// the list.
func TestFactLoadErrorRendersVisibly(t *testing.T) {
	loadFact := func(slug string) (Fact, error) { return Fact{}, errors.New("stale link") }
	m := New(nil, loadFact)
	next, _ := m.Update(fetchResultMsg{entries: testEntries()})
	m = next.(Model)

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	if !strings.Contains(m.View(), "stale link") {
		t.Fatalf("fact load error not rendered:\n%s", m.View())
	}
}

func TestEscReturnsToList(t *testing.T) {
	loadFact := func(slug string) (Fact, error) { return Fact{Slug: slug}, nil }
	m := New(nil, loadFact)
	next, _ := m.Update(fetchResultMsg{entries: testEntries()})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)
	if m.mode != modeReading {
		t.Fatal("test setup: not in reading mode")
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("[esc] did not return to list mode")
	}
}

func TestJKMoveSelectionAndClampAtEnds(t *testing.T) {
	m := New(nil, nil)
	next, _ := m.Update(fetchResultMsg{entries: testEntries()})
	m = next.(Model)

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	m = next.(Model)
	if m.selected != 0 {
		t.Fatalf("\"k\" from row 0 moved to %d, want clamped at 0", m.selected)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	m = next.(Model)
	if m.selected != 1 {
		t.Fatalf("\"j\" x2 from a 2-row list landed on %d, want clamped at 1", m.selected)
	}
}

func TestRKeyRefetches(t *testing.T) {
	calls := 0
	fetch := func() ([]IndexEntry, error) { calls++; return nil, nil }
	m := New(fetch, nil)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd == nil {
		t.Fatal("[r] returned a nil cmd, want a fetch")
	}
	cmd()
	if calls != 1 {
		t.Fatalf("fetch called %d times after [r], want 1", calls)
	}
}

func TestQuittingRendersNothing(t *testing.T) {
	m := New(nil, nil)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("\"q\" did not return tea.Quit")
	}
	if m.View() != "" {
		t.Fatalf("View() after quitting = %q, want empty", m.View())
	}
}

// TestQuitsFromReadingModeToo matches this repo's own convention (every
// pane must quit on "q"/"ctrl+c" regardless of its own internal mode).
func TestQuitsFromReadingModeToo(t *testing.T) {
	loadFact := func(slug string) (Fact, error) { return Fact{Slug: slug}, nil }
	m := New(nil, loadFact)
	next, _ := m.Update(fetchResultMsg{entries: testEntries()})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	next, _ = m.Update(cmd())
	m = next.(Model)

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("\"q\" from reading mode did not return tea.Quit")
	}
}

// TestViewNeverExceedsHeightAtRealisticWidths is the regression test for
// the bug this package's own teatest coverage found: the column-header
// and legend lines were wide enough (116+ characters) to word-WRAP at a
// terminal's actual default width (100, teatest's own
// WithInitialTermSize) rather than fit on one line, silently adding
// physical rows past the fixed 4-line chrome budget metrics() assumes --
// which, in a real Program, scrolled the "knowledge" title itself off the
// top before TestQQuitsARealProgram (model_teatest_test.go) ever saw it.
// Checked at several widths, list mode and reading mode both, several
// entry counts (0, 1, many) -- a fixed height budget only proves itself
// by holding across the range a real terminal can actually be, not one
// width picked to happen to fit.
func TestViewNeverExceedsHeightAtRealisticWidths(t *testing.T) {
	entries := make([]IndexEntry, 70)
	for i := range entries {
		entries[i] = IndexEntry{Slug: fmt.Sprintf("fact-%02d", i), Title: fmt.Sprintf("Fact number %d", i), Description: "a description long enough to matter for wrapping math"}
	}

	for _, width := range []int{60, 80, 100, 160} {
		for _, height := range []int{20, 24, 40} {
			for _, n := range []int{0, 1, len(entries)} {
				m := New(nil, nil)
				next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
				m = next.(Model)
				next, _ = m.Update(fetchResultMsg{entries: entries[:n]})
				m = next.(Model)

				got := strings.Count(m.View(), "\n") + 1
				if got != height {
					t.Errorf("list mode, width=%d height=%d entries=%d: View() rendered %d lines, want exactly %d", width, height, n, got, height)
				}

				if n > 0 {
					loadFact := func(slug string) (Fact, error) {
						return Fact{Slug: slug, Body: "one line\nanother line\nand a third, long enough to matter for width math too"}, nil
					}
					mr := New(nil, loadFact)
					next, _ := mr.Update(tea.WindowSizeMsg{Width: width, Height: height})
					mr = next.(Model)
					next, _ = mr.Update(fetchResultMsg{entries: entries[:n]})
					mr = next.(Model)
					next, cmd := mr.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
					mr = next.(Model)
					next, _ = mr.Update(cmd())
					mr = next.(Model)

					got := strings.Count(mr.View(), "\n") + 1
					if got != height {
						t.Errorf("reading mode, width=%d height=%d entries=%d: View() rendered %d lines, want exactly %d", width, height, n, got, height)
					}
				}
			}
		}
	}
}

// driveGraphInit runs m.graph's own Init command synchronously and pushes
// its resulting fetchResultMsg through Model.Update -- the same
// "construct, run Init, push its Cmd's Msg back through Update" sequence
// a real Program's runtime performs, used here so a test can get past the
// initial async load without a full teatest.Program.
func driveGraphInit(t *testing.T, m Model) Model {
	t.Helper()
	cmd := m.graph.Init()
	if cmd == nil {
		return m
	}
	next, _ := m.Update(cmd())
	return next.(Model)
}

// TestGKeyEntersGraphModeAndEscReturns is this pane's own reachability
// proof for internal/memgraph's pane: [g] from the list must switch to
// modeGraph and render the graph sub-model's own content, and [esc] must
// return to the list -- the same round-trip TestEscReturnsToList already
// proves for modeReading.
func TestGKeyEntersGraphModeAndEscReturns(t *testing.T) {
	m := New(nil, nil).WithGraph(func() (memgraph.Graph, error) {
		return memgraph.Graph{Nodes: []memgraph.Node{{ID: "n1", Label: "fact one", Type: "project"}}}, nil
	})
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	next, _ = m.Update(fetchResultMsg{entries: testEntries()})
	m = next.(Model)
	m = driveGraphInit(t, m)

	if !strings.Contains(m.View(), "[g] graph") {
		t.Fatalf("list legend does not advertise [g]:\n%s", m.View())
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = next.(Model)
	if m.mode != modeGraph {
		t.Fatal("\"g\" from list mode did not enter modeGraph")
	}

	out := m.View()
	if !strings.Contains(out, "drag to reposition") {
		t.Fatalf("modeGraph did not render memgraph's own frame:\n%s", out)
	}
	if strings.Contains(out, "[g] graph") {
		t.Fatalf("still rendering the list legend while in modeGraph:\n%s", out)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode != modeList {
		t.Fatal("[esc] from modeGraph did not return to modeList")
	}
}

// TestGraphModeDragMovesANode is the shell-of-the-shell drive: a mouse
// press/motion/release forwarded through knowledge.Model.Update (exactly
// how internal/shell.routeAll delivers a MouseMsg to every pane) must
// reach internal/memgraph.Model and move the grabbed node, proving the
// unconditional forward in Update actually wires the two together rather
// than only memgraph's own isolated tests proving the mechanism works.
func TestGraphModeDragMovesANode(t *testing.T) {
	fetch := func() (memgraph.Graph, error) {
		return memgraph.Graph{
			Nodes: []memgraph.Node{{ID: "n1", Type: "project"}, {ID: "n2", Type: "feedback"}},
			Edges: []memgraph.Edge{{From: "n1", To: "n2"}},
		}, nil
	}
	m := New(nil, nil).WithGraph(fetch)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	m = driveGraphInit(t, m)

	x0, y0, ok := m.graph.PositionOf("n1")
	if !ok {
		t.Fatalf("test setup: node n1 has no position after graph load")
	}

	press := tea.MouseMsg{X: x0 + 1, Y: y0 + 1, Action: tea.MouseActionPress}
	next, _ = m.Update(press)
	m = next.(Model)

	tx, ty := x0+1, y0
	if x0 == 0 {
		tx = x0 + 1
	} else {
		tx = x0 - 1
	}
	release := tea.MouseMsg{X: tx + 1, Y: ty + 1, Action: tea.MouseActionRelease}
	next, _ = m.Update(release)
	m = next.(Model)

	fx, fy, _ := m.graph.PositionOf("n1")
	if fx == x0 && fy == y0 {
		t.Fatalf("dragging n1 through knowledge.Model.Update did not move it: stayed at (%d,%d)", x0, y0)
	}
	if fx != tx || fy != ty {
		t.Fatalf("n1 landed at (%d,%d), want (%d,%d)", fx, fy, tx, ty)
	}
}
