package shell

import (
	"strings"
	"testing"

	"github.com/jonhill90/agent-estate/src/tui/internal/nav"
)

// TestRouteToPaneNeverLosesAWiredRoute is agent-b5.md's own regression
// guard: merging main into feat/three-stubs conflicted in exactly this map
// twice in a row (once for "knowledge" landing on main while this branch
// was open, once inside THIS same merge's own resolution, where a careless
// text-level conflict resolution silently dropped a `return` statement a
// few lines below this map -- caught only by go build failing, not by this
// test, which is why this test now exists). Every route id below was
// wired to a real Pane on main as of the SHA this branch merged
// (`git show origin/main:internal/shell/model.go`, verified by hand
// building this list, not copied from routeToPane itself) -- if a future
// merge resolution drops one, this fails loudly instead of silently
// regressing a route back to PaneStub the way the Knowledge route once did
// with no test to catch it.
func TestRouteToPaneNeverLosesAWiredRoute(t *testing.T) {
	wantWired := []string{
		"home", "tasks", "usage", "lanes", "chat", "agents", "skills",
		"mcp-servers", "connections", "admin-services", "admin-profiles",
		"admin-users", "dependencies", "settings", "dashboard", "library",
		"knowledge",
		// This branch's own two additions (w5f.md) -- included so this test
		// also guards against THIS PR's own routes being the ones a future
		// conflict drops.
		"monitoring", "workflows",
	}
	for _, id := range wantWired {
		if _, ok := routeToPane[id]; !ok {
			t.Errorf("routeToPane missing %q -- a route that used to render a real pane now falls through to PaneStub", id)
		}
	}
}

// TestEveryNavRouteHasAPaneOrIsAnHonestStub cross-checks the OTHER
// direction: every KindRoute leaf in nav.Build()'s current tree either has
// a routeToPane entry or is one of the destinations still expected to
// stub -- so a route this repo forgot to wire AND forgot to list as an
// expected stub cannot go unnoticed either.
func TestEveryNavRouteHasAPaneOrIsAnHonestStub(t *testing.T) {
	// Routes with no real pane as of this branch -- STUB's own honest
	// placeholder is the correct render for these, not a bug.
	expectedStub := map[string]bool{
		"storage":       true,
		"discord":       true,
		"secrets":       true,
		"api-docs":      true,
		"platform-docs": true,
	}

	tr := nav.Build()
	for _, n := range tr.Flatten() {
		if n.IsGroupHeader() || n.Item.Kind != nav.KindRoute {
			continue
		}
		id := n.Item.ID
		_, wired := routeToPane[id]
		if !wired && !expectedStub[id] {
			t.Errorf("nav route %q is neither in routeToPane nor in this test's expectedStub list -- "+
				"either wire it or add it to expectedStub so a silently-forgotten route is caught here, not by a human clicking through the sidebar", id)
		}
	}
}

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
	for _, pane := range []Pane{PaneHome, PaneBoard, PaneCost, PaneGallery, PaneFlow} {
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
