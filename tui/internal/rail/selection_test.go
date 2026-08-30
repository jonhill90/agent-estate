package rail

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-estate/tui/internal/lane"
)

// as#109: the selection used reverse video, which inverts whatever
// per-state foreground colour a row already carries -- a selected `busy`
// row highlighted blue, a selected `free` row highlighted green. These
// tests hold the fix to the same mechanical bar the issue's acceptance
// criteria set: no reverse video ever, a constant selection background
// regardless of state, and no lane name wrapping onto a second line.

var reverseVideoSeq = "\x1b[7m"

var bgSeqPattern = regexp.MustCompile(`\x1b\[48;2;\d+;\d+;\d+m`)

func renderSelectedRow(t *testing.T, state string) string {
	t.Helper()
	// Force a colour-capable profile: go test's stdout is not a tty, so
	// lipgloss would otherwise degrade to Ascii (no escape codes at all)
	// and every assertion below would pass vacuously.
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := New(func() ([]lane.Lane, error) { return nil, nil })
	m.lanes = []lane.Lane{{Window: 1, WindowID: "@0", Name: "lane-" + state, Command: "claude", State: state}}
	m.selected = 0
	out := m.View()

	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "lane-"+state) {
			return line
		}
	}
	t.Fatalf("no rendered line found for state %q in:\n%s", state, out)
	return ""
}

func TestSelectionNeverUsesReverseVideo(t *testing.T) {
	for _, state := range []string{"busy", "free", "unknown"} {
		row := renderSelectedRow(t, state)
		if strings.Contains(row, reverseVideoSeq) {
			t.Fatalf("selected %q row still contains reverse video %q: %q", state, reverseVideoSeq, row)
		}
	}
}

func TestSelectionBackgroundConstantAcrossStates(t *testing.T) {
	backgrounds := map[string]string{}
	for _, state := range []string{"busy", "free", "unknown"} {
		row := renderSelectedRow(t, state)
		matches := bgSeqPattern.FindAllString(row, -1)
		if len(matches) == 0 {
			t.Fatalf("selected %q row carries no explicit background sequence: %q", state, row)
		}
		for _, seq := range matches {
			if first, ok := backgrounds[state]; ok && first != seq {
				t.Fatalf("selected %q row uses more than one background sequence: %q vs %q", state, first, seq)
			}
			backgrounds[state] = seq
		}
	}
	var want string
	for _, seq := range backgrounds {
		if want == "" {
			want = seq
		} else if seq != want {
			t.Fatalf("selection background differs by state: %q vs %q (states compared: %v)", want, seq, backgrounds)
		}
	}
}

func TestSelectedRowNameDoesNotWrap(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := New(func() ([]lane.Lane, error) { return nil, nil })
	m.lanes = []lane.Lane{
		{Window: 1, WindowID: "@0", Name: "as89-as95-rebase-and-fix", Command: "claude", State: "busy"},
	}
	m.selected = 0
	out := m.View()

	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "as89") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("lane name spilled across %d lines, want 1 (row must not wrap): output=\n%s", count, out)
	}
}
