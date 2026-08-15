package gallery

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-tui/internal/theme"
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

// TestKeyTCyclesThemeAtRuntime is agent-tui#25 scope item 3, driven -- see
// board's identical test for the full rationale.
func TestKeyTCyclesThemeAtRuntime(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	m := New().WithTheme(theme.Default, "")
	m.height = 200
	before := m.View()

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	m = next.(Model)
	after := m.View()

	if before == after {
		t.Fatal("pressing 't' did not change the gallery's render -- runtime theme switch not wired")
	}
	if m.theme.ID != theme.Cycle(theme.Default).ID {
		t.Fatalf("after 't', m.theme = %q, want %q", m.theme.ID, theme.Cycle(theme.Default).ID)
	}
}
