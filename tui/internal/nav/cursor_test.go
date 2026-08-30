package nav

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

// The cursor and the active route are DIFFERENT things and both must be
// visible. Driving the real binary showed pressing Down twice left the
// highlight on Home -- the cursor was rendered nowhere, so you could not tell
// what Enter would open.
func TestCursorIsRenderedDistinctlyFromActive(t *testing.T) {
	m := New().WithTheme(theme.Default, "").WithActive("home")

	atHome := m.WithCursor(0).View()
	atAgents := m.WithCursor(2).View()

	if atHome == atAgents {
		t.Fatal("moving the cursor changed nothing on screen -- the exact bug this pins")
	}
	if !strings.Contains(atAgents, "▌") {
		t.Fatal("cursor marker not rendered")
	}
}

// The cursor marker must land on the cursored row, not on the active one.
func TestCursorMarkerFollowsTheCursorNotTheActiveRoute(t *testing.T) {
	m := New().WithTheme(theme.Default, "").WithActive("home").WithCursor(2)
	for _, line := range strings.Split(m.View(), "\n") {
		if strings.Contains(line, "Agents") && !strings.Contains(line, "▌") {
			t.Fatal("cursored row (Agents) has no marker")
		}
		if strings.Contains(line, "Dashboard") && strings.Contains(line, "▌") {
			t.Fatal("marker on a row that is neither cursored nor active")
		}
	}
}
