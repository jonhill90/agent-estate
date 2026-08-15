package board

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// TestThemeSwitchChangesBoardRender is agent-tui#27's per-surface half of
// the driven acceptance test -- rail's own TestThemeSwitchChangesEverySurface
// (internal/rail/theme_test.go) documents the full "driven, not a struct
// unit test" rationale; this proves the SAME theme value changes the
// board's render too, "every surface, not just the one you were thinking
// about" (#27 acceptance item 2).
func TestThemeSwitchChangesBoardRender(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	snap := Snapshot{
		Cards: []Card{
			{Repo: repoA, Number: 1, Title: "a card", Column: InProgress},
			{Repo: repoA, Number: 2, Title: "a blocked card", Column: Blocked, BlockedReason: "waiting"},
		},
		Repos: []Repo{repoA},
	}

	build := func(th theme.Theme) Model {
		m := New(func() (Snapshot, error) { return snap, nil }).WithTheme(th, "")
		next, _ := m.Update(fetchResultMsg{snap: snap})
		return next.(Model)
	}

	outDefault := build(theme.Default).View()
	outMono := build(theme.Mono).View()

	if outDefault == outMono {
		t.Fatal("board render is byte-identical between theme.Default and theme.Mono")
	}

	fgFragment := func(c lipgloss.Color) string {
		rendered := lipgloss.NewStyle().Foreground(c).Render("X")
		i := strings.Index(rendered, "38;2;")
		if i < 0 {
			t.Fatalf("no truecolor fragment in probe render %q", rendered)
		}
		j := strings.Index(rendered[i:], "m")
		return rendered[i : i+j]
	}

	// InProgress column header colour (theme.RoleInProgress, vivid theme by
	// default in Layouts[0]) must be Default's under Default and Mono's
	// under Mono, never the other way -- the mutation this catches is
	// exactly board/layout.go's columnColor falling back to a literal.
	defFrag := fgFragment(theme.Default.Color(theme.RoleInProgress))
	monoFrag := fgFragment(theme.Mono.Color(theme.RoleInProgress))
	if !strings.Contains(outDefault, defFrag) {
		t.Errorf("Default board render missing Default's in-progress colour fragment %q", defFrag)
	}
	if strings.Contains(outDefault, monoFrag) {
		t.Errorf("Default board render contains Mono's in-progress colour fragment %q", monoFrag)
	}
	if !strings.Contains(outMono, monoFrag) {
		t.Errorf("Mono board render missing Mono's in-progress colour fragment %q", monoFrag)
	}
	if strings.Contains(outMono, defFrag) {
		t.Errorf("Mono board render still contains Default's in-progress colour fragment %q -- literal not routed", defFrag)
	}
}
