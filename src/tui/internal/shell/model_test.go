package shell

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

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

// Regression for a council finding: removing the function keys orphaned the
// three decide-by-variant lane-chat panes. They have no sidebar route by
// design, so the fuzzy finder cannot reach them either -- an explicit leader
// binding is the only thing that keeps them alive.
//
// This asserts reachability for EVERY pane the leader is responsible for, so
// the next person to add a pane finds out here rather than from a user.
func TestEveryLeaderBoundPaneIsReachable(t *testing.T) {
	seen := map[Pane]string{}
	for _, b := range leaderBindings {
		if prev, dup := seen[b.Pane]; dup {
			t.Errorf("pane %v bound twice: %q and %q", b.Pane, prev, b.Key)
		}
		seen[b.Pane] = b.Key
	}
	for _, want := range []Pane{
		PaneLaneChatLanePrimary, PaneLaneChatRoomPrimary, PaneLaneChatUnifiedList,
	} {
		if _, ok := seen[want]; !ok {
			t.Errorf("pane %v has no sidebar route and no leader binding -- it is unreachable", want)
		}
	}
	keys := map[string]bool{}
	for _, b := range leaderBindings {
		if keys[b.Key] {
			t.Errorf("leader key %q is bound twice", b.Key)
		}
		keys[b.Key] = true
		if b.Key == FinderKey {
			t.Errorf("leader key %q collides with the finder", b.Key)
		}
	}
}

// Regression: the leader must not eat the space bar while the rail is taking
// a session name. Fixed by inspection in the previous round with no test; a
// council seat pointed out that a fix nobody can re-break safely is not
// finished.
func TestLeaderYieldsToTheRailComposer(t *testing.T) {
	m := testModel()
	m.focus = focusRail
	if !m.leaderTakesKeys() {
		t.Fatal("precondition: with an idle rail the leader is active")
	}
	m.rail = m.rail.EnterAddForTest()
	if m.leaderTakesKeys() {
		t.Fatal("while the rail is capturing text the leader must not claim keys")
	}
}

// Regression: tab was matched before the leader/finder checks, so a pending
// chord survived into the next keystroke and hijacked it.
func TestTabDoesNotHijackAPendingChord(t *testing.T) {
	m := testModel()
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	base := sized.(Model)
	beforeFocus := base.focus

	pending, _ := base.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	p := pending.(Model)
	if !p.leaderPending {
		t.Fatal("precondition: the leader is pending")
	}
	after, _ := p.Update(tea.KeyMsg{Type: tea.KeyTab})
	a := after.(Model)
	if a.leaderPending {
		t.Error("tab must be consumed by the pending chord, not left dangling")
	}
	if a.focus != beforeFocus {
		t.Error("tab must not also toggle focus while a chord is pending")
	}
	if a.leaderMiss != "tab" {
		t.Errorf("an unbound chord key must be reported, got %q", a.leaderMiss)
	}
}
