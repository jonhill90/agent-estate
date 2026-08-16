package board

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var repoA = Repo{Label: "agent-tui", Owner: "jonhill90", Name: "agent-tui"}
var repoB = Repo{Label: "agent-supervisor", Owner: "jonhill90", Name: "agent-supervisor"}

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestDigitKeySwitchesLayout mirrors internal/rail's own digit-key test:
// pressing a number selects Layouts[n-1] against the layout picker #10
// replaced view.go's Views with.
func TestDigitKeySwitchesLayout(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	if m.layoutIdx != 0 {
		t.Fatalf("default layoutIdx = %d, want 0", m.layoutIdx)
	}
	model, _ := m.Update(keyMsg("2"))
	m = model.(Model)
	if m.layoutIdx != 1 {
		t.Errorf("layoutIdx after pressing 2 = %d, want 1", m.layoutIdx)
	}
}

// TestScrollKeysAllMoveOffset closes a gap TestScrollKeysMoveTheViewport
// (model_teatest_test.go) left open: that test drives only "j" through a
// real Program. up/k/down/pgup/pgdown/pgdn share the same case block
// (Update's tea.KeyMsg switch) but were never individually sent through
// Update by any test -- agent-tui#38's QA gate ("drive every key... a
// control that is not pressed is not proven") applies to each one, not
// just the first.
func TestScrollKeysAllMoveOffset(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	m.height = 30

	cases := []struct {
		key  string
		want int
	}{
		{"down", 1},
		{"j", 2},
		{"up", 1},
		{"k", 0},
		{"pgdown", pageSize(30)},
	}
	for _, c := range cases {
		model, _ := m.Update(keyMsg(c.key))
		m = model.(Model)
		if m.scrollOffset != c.want {
			t.Fatalf("after %q, scrollOffset = %d, want %d", c.key, m.scrollOffset, c.want)
		}
	}

	before := m.scrollOffset
	model, _ := m.Update(keyMsg("pgup"))
	m = model.(Model)
	if m.scrollOffset != before-pageSize(30) {
		t.Errorf("after \"pgup\", scrollOffset = %d, want %d", m.scrollOffset, before-pageSize(30))
	}
}

// TestKeyRSentThroughUpdateActuallyFetches is "r"'s own driven proof --
// previously only asserted present in the footer legend, never sent
// through Update.
func TestKeyRSentThroughUpdateActuallyFetches(t *testing.T) {
	fetchCount := 0
	m := New(func() (Snapshot, error) { fetchCount++; return Snapshot{}, nil })

	model, cmd := m.Update(keyMsg("r"))
	m = model.(Model)
	if cmd == nil {
		t.Fatal("Update(keyMsg(\"r\")) returned a nil Cmd, want doFetch")
	}
	msg := cmd()
	if _, ok := msg.(fetchResultMsg); !ok {
		t.Fatalf("cmd() returned %T, want fetchResultMsg", msg)
	}
	model, _ = m.Update(msg)
	m = model.(Model)
	_ = m
	if fetchCount != 1 {
		t.Fatalf("fetchCount = %d after sending \"r\" through Update and running its Cmd, want 1", fetchCount)
	}
}

// TestQAndCtrlCSentThroughUpdateQuit drives board's own "q"/"ctrl+c" case
// directly -- neither was previously sent through Update by any test in
// this package.
func TestQAndCtrlCSentThroughUpdateQuit(t *testing.T) {
	for _, key := range []tea.KeyMsg{keyMsg("q"), {Type: tea.KeyCtrlC}} {
		m := New(func() (Snapshot, error) { return Snapshot{}, nil })
		model, cmd := m.Update(key)
		m = model.(Model)
		if !m.quitting {
			t.Errorf("Update(%v) did not set quitting", key)
		}
		if cmd == nil {
			t.Errorf("Update(%v) returned a nil Cmd, want tea.Quit", key)
		}
	}
}

func TestDigitKeyBeyondLayoutCountIsIgnored(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	tooHigh := len(Layouts) + 1
	if tooHigh > 9 {
		t.Skip("no unused digit key left to test with")
	}
	model, _ := m.Update(keyMsg(string(rune('0' + tooHigh))))
	m2 := model.(Model)
	if m2.layoutIdx != m.layoutIdx {
		t.Fatalf("a digit with no corresponding layout must not change layoutIdx, got %d", m2.layoutIdx)
	}
}

// TestLetterKeyTogglesRepoSelection is agent-tui#10 item 2's own test:
// toggling a repo off removes its cards from View(), toggling it back on
// (or pressing "0") restores them -- with no new fetch either time.
func TestLetterKeyTogglesRepoSelection(t *testing.T) {
	fetchCount := 0
	m := New(func() (Snapshot, error) {
		fetchCount++
		return Snapshot{}, nil
	})
	m.width, m.height = 220, 30
	m.snap = Snapshot{
		Repos: []Repo{repoA, repoB},
		Cards: []Card{
			{Repo: repoA, Number: 1, Column: Backlog, Title: "in agent-tui"},
			{Repo: repoB, Number: 2, Column: Backlog, Title: "in agent-supervisor"},
		},
	}
	m.lastFetched = time.Now()

	out := m.View()
	if !strings.Contains(out, "in agent-tui") || !strings.Contains(out, "in agent-supervisor") {
		t.Fatalf("both repos' cards should be visible with no toggle pressed:\n%s", out)
	}

	// 'b' is repoB (Repos[1]) -- toggle it off.
	model, _ := m.Update(keyMsg("b"))
	m = model.(Model)
	out = m.View()
	if strings.Contains(out, "in agent-supervisor") {
		t.Errorf("agent-supervisor's card still visible after deselecting it:\n%s", out)
	}
	if !strings.Contains(out, "in agent-tui") {
		t.Errorf("agent-tui's card should still be visible:\n%s", out)
	}

	// '0' resets to "show everything".
	model, _ = m.Update(keyMsg("0"))
	m = model.(Model)
	out = m.View()
	if !strings.Contains(out, "in agent-supervisor") {
		t.Errorf("agent-supervisor's card should be back after pressing 0:\n%s", out)
	}

	if fetchCount != 0 {
		t.Errorf("repo toggling triggered %d fetches, want 0 -- selection must not touch the fetch path", fetchCount)
	}
}

// TestLetterKeyBeyondFetchedRepoCountIsIgnored: a letter with no
// corresponding Repos entry (nothing fetched yet, or fewer repos than
// letters) must not panic or silently create a phantom selection.
func TestLetterKeyBeyondFetchedRepoCountIsIgnored(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	model, _ := m.Update(keyMsg("z"))
	m2 := model.(Model)
	if len(m2.deselected) != 0 {
		t.Fatalf("letter key with no matching repo created a selection: %+v", m2.deselected)
	}
}

func TestRepoLegendMarksSelectedRepos(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	m.snap = Snapshot{Repos: []Repo{repoA, repoB}}
	legend := m.repoLegend()
	if !strings.Contains(legend, "[a]*agent-tui") {
		t.Errorf("legend missing selected agent-tui: %q", legend)
	}
	model, _ := m.Update(keyMsg("b"))
	m = model.(Model)
	legend = m.repoLegend()
	if !strings.Contains(legend, "[b] agent-supervisor") {
		t.Errorf("legend did not un-mark deselected agent-supervisor: %q", legend)
	}
}
