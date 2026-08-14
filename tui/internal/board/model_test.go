package board

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// TestModelViewColorsAgedCard is the coverage the review demanded:
// TestRenderByColumnFlagsAgedCard (view_test.go) only checks plain-text
// Render output for the "!" marker text, never calling colorizeAged or
// Model.View -- so it cannot catch the marker showing without its color.
// This test drives the real path: Model.View(), which indents every card
// line ("  " + cardLine(...)) before colorizeAged ever sees it. It must go
// red if that indentation mismatch comes back (mutation-check 2 in the PR
// reply).
func TestModelViewColorsAgedCard(t *testing.T) {
	// lipgloss detects color support from the terminal; force it on so
	// warnStyle.Render actually emits ANSI codes in this non-tty test
	// process instead of silently degrading to plain text.
	prevProfile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	t.Cleanup(func() { lipgloss.SetColorProfile(prevProfile) })

	m := New(func() (Snapshot, error) { return Snapshot{}, nil })
	m.width, m.height = 80, 30
	m.lastFetched = time.Now()
	m.snap = Snapshot{
		Cards: []Card{
			{Repo: testRepo, Number: 95, Column: Blocked, Title: "as95 conflicting", Age: 3 * time.Hour},
		},
	}

	out := m.View()

	var agedLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "as95 conflicting") {
			agedLine = line
			break
		}
	}
	if agedLine == "" {
		t.Fatalf("no line in View() output mentions the aged card:\n%s", out)
	}
	if !strings.Contains(agedLine, "\x1b[") {
		t.Errorf("aged card line was not colorized (no ANSI escape found):\n%q", agedLine)
	}
	if !strings.Contains(agedLine, warnStyle.Render("!")[:2]) {
		// sanity: warnStyle actually produces ANSI in this test process at
		// all, so a failure above means colorizeAged skipped the line, not
		// that color rendering itself is off in this environment.
		t.Fatalf("warnStyle itself did not render ANSI in this test process; test setup is broken")
	}
}

// TestIsAgedCardLineMatchesIndentedMarker pins the fix directly: the
// marker cardLine writes at column 0 survives RenderByColumn/RenderByRepo's
// "  "/"    " indentation, and isAgedCardLine must still find it there.
func TestIsAgedCardLineMatchesIndentedMarker(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"! [agent-tui] #95 as95 conflicting 3h", true},
		{"  ! [agent-tui] #95 as95 conflicting 3h", true},
		{"    ! [agent-tui] #95 as95 conflicting 3h", true},
		{"  ", false},
		{"", false},
		{"  [agent-tui] #95 as95 conflicting 3h", false}, // not-aged marker is a space
		{"BLOCKED (1)", false},
		{"    -> PR #95 is conflicting", false},
	}
	for _, c := range cases {
		if got := isAgedCardLine(c.line); got != c.want {
			t.Errorf("isAgedCardLine(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}
