package shell

import (
	"strings"
	"testing"
)

// TestClampHeightTruncatesAndPads is clampHeight's own unit contract: over
// budget gets cut, under budget gets padded, and a not-yet-measured height
// (n <= 0, teatest's very first frame before any WindowSizeMsg) passes
// through untouched.
func TestClampHeightTruncatesAndPads(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		n     int
		lines int
	}{
		{"over budget by one trailing blank line", "a\nb\nc\n", 3, 3},
		{"under budget gets padded", "a\nb", 5, 5},
		{"exact match is untouched", "a\nb\nc", 3, 3},
		{"n<=0 passes through", "a\nb\nc\n", 0, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clampHeight(c.in, c.n)
			gotLines := strings.Count(got, "\n") + 1
			if gotLines != c.lines {
				t.Fatalf("clampHeight(%q, %d) = %q, %d lines, want %d", c.in, c.n, got, gotLines, c.lines)
			}
		})
	}
}

// TestViewNeverExceedsContentHeightPlusFooter is the regression test for
// the bug this package's teatest coverage found: gallery.Model.View() (via
// a trailing "\n" on its last line) rendered one line taller than the
// contentHeight budget resize() gave it, which pushed m.footer() past the
// bottom of a real altscreen terminal -- see TestWithStartOpensOnTheChosenPane's
// mutation-check note in the PR body for how this was caught. Every pane
// gets the same check: the composed View() must total EXACTLY
// contentHeight (rail + content, clamped to the same budget) + 1 footer
// line, never more, regardless of what any individual pane's own View()
// returns.
func TestViewNeverExceedsContentHeightPlusFooter(t *testing.T) {
	for _, pane := range []Pane{PaneHome, PaneBoard, PaneCost, PaneGallery} {
		m := testModel().WithStart(pane)
		m.width, m.height = 100, 30
		m.contentWidth, m.contentHeight = 73, 29 // mirrors resize()'s own arithmetic
		v := m.View()
		got := strings.Count(v, "\n") + 1
		want := m.contentHeight + footerHeight
		if got != want {
			t.Errorf("pane %d: View() rendered %d lines, want exactly %d (contentHeight=%d + footerHeight=%d) -- a pane exceeding its budget pushes the footer off screen", pane, got, want, m.contentHeight, footerHeight)
		}
	}
}
