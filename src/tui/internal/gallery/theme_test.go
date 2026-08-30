package gallery

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// TestThemeSwitchChangesGalleryRender is agent-tui#27's per-surface half of
// the driven acceptance test -- see internal/rail/theme_test.go's
// TestThemeSwitchChangesEverySurface for the full rationale. Driven through
// the real View() with a real BuildRows() snapshot (New()'s own content),
// scrolled to a row that actually carries a [NF]/[emoji] flag so
// theme.RoleFlag renders.
func TestThemeSwitchChangesGalleryRender(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	build := func(th theme.Theme) Model {
		m := New().WithTheme(th, "")
		m.height = 200 // tall enough that every row (and its flags) renders in one View()
		return m
	}

	outDefault := build(theme.Default).View()
	outMono := build(theme.Mono).View()

	if outDefault == outMono {
		t.Fatal("gallery render is byte-identical between theme.Default and theme.Mono")
	}

	fgFragment := func(c lipgloss.Color) string {
		rendered := lipgloss.NewStyle().Foreground(c).Render("X")
		i := strings.Index(rendered, "38;2;")
		j := strings.Index(rendered[i:], "m")
		return rendered[i : i+j]
	}

	defFrag := fgFragment(theme.Default.Color(theme.RoleFlag))
	monoFrag := fgFragment(theme.Mono.Color(theme.RoleFlag))
	if !strings.Contains(outDefault, defFrag) {
		t.Errorf("Default gallery render missing Default's flag colour fragment %q -- got:\n%s", defFrag, outDefault)
	}
	if !strings.Contains(outMono, monoFrag) {
		t.Errorf("Mono gallery render missing Mono's flag colour fragment %q", monoFrag)
	}
	if strings.Contains(outMono, defFrag) {
		t.Errorf("Mono gallery render still contains Default's flag colour fragment %q -- literal not routed", defFrag)
	}
}

// TestKeyTRequestsThemeCycleWithoutMutatingLocalTheme is agent-tui#51's
// replacement for the old TestKeyTCyclesThemeAtRuntime -- see rail's
// identical test and theme.CycleRequestedMsg's doc comment for why this
// package must no longer own the theme value.
func TestKeyTRequestsThemeCycleWithoutMutatingLocalTheme(t *testing.T) {
	m := New().WithTheme(theme.Default, "")
	m.height = 200

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = next.(Model)

	if m.theme.ID != theme.Default.ID {
		t.Fatalf("pressing 't' mutated m.theme to %q -- this pane must no longer own the theme value (see theme.CycleRequestedMsg)", m.theme.ID)
	}
	if cmd == nil {
		t.Fatal("pressing 't' returned a nil Cmd -- no theme.CycleRequestedMsg was requested")
	}
	if _, ok := cmd().(theme.CycleRequestedMsg); !ok {
		t.Fatalf("pressing 't' returned a Cmd whose Msg is %T, want theme.CycleRequestedMsg", cmd())
	}
}
