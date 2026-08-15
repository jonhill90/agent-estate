package cost

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// TestThemeSwitchChangesCostRender is agent-tui#27's per-surface half of
// the driven acceptance test -- see internal/rail/theme_test.go's
// TestThemeSwitchChangesEverySurface for the full rationale. Driven through
// the fetch-error path (m.fetchErr), the same real state
// TestOnlyRefreshMsgAndKeyRTriggerAFetch already exercises in this package.
func TestThemeSwitchChangesCostRender(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	build := func(th theme.Theme) Model {
		m := New(func() (Snapshot, error) { return Snapshot{}, nil }).WithTheme(th, "")
		next, _ := m.Update(fetchResultMsg{err: errors.New("fixture: unavailable")})
		return next.(Model)
	}

	outDefault := build(theme.Default).View()
	outMono := build(theme.Mono).View()

	if outDefault == outMono {
		t.Fatal("cost render is byte-identical between theme.Default and theme.Mono")
	}

	fgFragment := func(c lipgloss.Color) string {
		rendered := lipgloss.NewStyle().Foreground(c).Render("X")
		i := strings.Index(rendered, "38;2;")
		j := strings.Index(rendered[i:], "m")
		return rendered[i : i+j]
	}

	defFrag := fgFragment(theme.Default.Color(theme.RoleError))
	monoFrag := fgFragment(theme.Mono.Color(theme.RoleError))
	if !strings.Contains(outDefault, defFrag) {
		t.Errorf("Default cost render missing Default's error colour fragment %q", defFrag)
	}
	if !strings.Contains(outMono, monoFrag) {
		t.Errorf("Mono cost render missing Mono's error colour fragment %q", monoFrag)
	}
	if strings.Contains(outMono, defFrag) {
		t.Errorf("Mono cost render still contains Default's error colour fragment %q -- literal not routed", defFrag)
	}
}
