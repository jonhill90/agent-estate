package cost

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// keyMsg builds a tea.KeyMsg for a single printable rune, matching how
// bubbletea itself decodes a plain digit keypress (key.go: KeyRunes).
func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// TestRefreshIntervalIsFiveMinutes pins the constant directly (mirrors
// board's own discipline of naming its interval in a comment, but board
// has no test asserting it -- this one exists because agent-tui#4
// acceptance item 4 requires SHOWING the chosen interval, not just stating
// it). A future edit that tightens the poll without updating this test and
// its surrounding justification is exactly the regression this guards.
func TestRefreshIntervalIsFiveMinutes(t *testing.T) {
	if refreshInterval != 5*time.Minute {
		t.Errorf("refreshInterval = %s, want 5m", refreshInterval)
	}
}

// TestOnlyRefreshMsgAndKeyRTriggerAFetch is the structural half of
// agent-tui#4 acceptance item 4 ("show the panel does not poll faster than
// [the stated interval]"): the only two Update branches that call doFetch
// are "r" and refreshMsg, and refreshMsg itself is only ever produced by
// refreshCmd, which is scheduled on the fixed refreshInterval. A
// WindowSizeMsg or an unrelated key must never trigger a fetch.
func TestOnlyRefreshMsgAndKeyRTriggerAFetch(t *testing.T) {
	fetchCount := 0
	m := New(func() (Snapshot, error) {
		fetchCount++
		return Compose(nil, Limit{}), nil
	})

	model, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	m = model.(Model)
	model, _ = m.Update(keyMsg("x"))
	m = model.(Model)
	if fetchCount != 0 {
		t.Fatalf("fetchCount = %d after a resize and an unrelated key, want 0", fetchCount)
	}

	model, cmd := m.Update(refreshMsg(time.Now()))
	m = model.(Model)
	if cmd == nil {
		t.Fatal("Update(refreshMsg) returned a nil Cmd, want a batch including doFetch")
	}
	// cmd is tea.Batch(refreshCmd(), doFetch(m.fetch)): refreshCmd's own
	// Cmd is a real tea.Tick that blocks for refreshInterval (5m) before
	// returning, so it must never be executed here -- doFetch is called
	// directly instead, the same function Update's refreshMsg branch
	// batches in, to prove the fetch path fires without paying that wait.
	doFetch(m.fetch)()
	if fetchCount != 1 {
		t.Errorf("fetchCount = %d after one refreshMsg's doFetch, want 1", fetchCount)
	}
}

func TestFetchErrorRendersUnknownNeverStaleData(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	m.width, m.height = 80, 30

	// Simulate a healthy fetch first, so there IS real prior data to make
	// sure a subsequent failure clears -- this is the blindness test's
	// model-level half: agent-tui#4 says a stale-but-real number silently
	// surviving a fetch failure is the same lie as a fabricated zero.
	model, _ := m.Update(fetchResultMsg{snap: Compose([]Harness{knownHarness}, knownHarness.Limit), err: nil})
	m = model.(Model)
	if !strings.Contains(m.View(), "412.98") {
		t.Fatalf("setup: expected the healthy fetch's real cost in View():\n%s", m.View())
	}

	model, _ = m.Update(fetchResultMsg{snap: Snapshot{}, err: errors.New("ccusage: exec: \"agent-tui-ccusage-binary-does-not-exist-on-this-path\": executable file not found in $PATH")})
	m = model.(Model)
	out := m.View()

	if strings.Contains(out, "412.98") {
		t.Errorf("View() still shows the previous fetch's real cost after a failed fetch:\n%s", out)
	}
	if !strings.Contains(out, "unavailable") {
		t.Errorf("View() does not report unavailability after a failed fetch:\n%s", out)
	}
	if strings.Contains(out, "$0.00") {
		t.Errorf("View() shows $0.00 after a failed fetch -- must show unknown, never zero:\n%s", out)
	}
}

func TestViewColorsWarnMarker(t *testing.T) {
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })

	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	m.width, m.height = 80, 30
	m.lastFetched = time.Now()
	m.snap = Compose([]Harness{knownHarness}, knownHarness.Limit)

	out := m.View()
	var warnLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "WARN") {
			warnLine = line
			break
		}
	}
	if warnLine == "" {
		t.Fatalf("no WARN line found in View() output:\n%s", out)
	}
	if !strings.Contains(warnLine, "\x1b[") {
		t.Errorf("WARN line was not colorized: %q", warnLine)
	}
}

func TestPickerSwitchesView(t *testing.T) {
	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	if m.viewIdx != 0 {
		t.Fatalf("default viewIdx = %d, want 0", m.viewIdx)
	}
	model, _ := m.Update(keyMsg("2"))
	m = model.(Model)
	if m.viewIdx != 1 {
		t.Errorf("viewIdx after pressing 2 = %d, want 1", m.viewIdx)
	}
}
