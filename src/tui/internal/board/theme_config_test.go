package board

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// TestUserDefinedColorsDrivenThroughLoadAndRender is agent-tui#34's driven
// acceptance test, same discipline as theme_test.go's
// TestThemeSwitchChangesBoardRender: PHASES.md's QA gate ("Jon QAs look
// and feel. Agents QA function") means a control is proven by actually
// exercising it end to end -- write a real config.json to disk exactly as
// a user would, run it through the real theme.Load, wire the result
// through WithTheme exactly as cmd/agent-tui does, and inspect the real
// rendered ANSI -- not a unit test of theme.applyColorOverrides in
// isolation. Board, not rail, hosts this: rail's notice line truncates to
// its narrow column width (see rail/model.go's View, truncate(...,
// innerWidth)), so a long notice string is only ever partially present
// there, while board's header renders the notice in full (model.go:325).
//
// This covers agent-tui#34's acceptance list in one pass: a user defines a colour
// by editing only a config file (no rebuild), each of the three failure
// modes produces a visible notice, and a partial override behaves per the
// documented precedence (unmentioned roles keep the base theme's own
// colour).
func TestUserDefinedColorsDrivenThroughLoadAndRender(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	snap := Snapshot{
		Cards: []Card{
			{Repo: repoA, Number: 1, Title: "a card", Column: InProgress},
		},
		Repos: []Repo{repoA},
	}

	build := func(th theme.Theme, notice string) string {
		m := New(func() (Snapshot, error) { return snap, nil }).WithTheme(th, notice)
		next, _ := m.Update(fetchResultMsg{snap: snap})
		return next.(Model).View()
	}

	fgFrag := func(c lipgloss.Color) string {
		rendered := lipgloss.NewStyle().Foreground(c).Render("X")
		i := strings.Index(rendered, "38;2;")
		if i < 0 {
			t.Fatalf("no truecolor fragment in probe render %q", rendered)
		}
		j := strings.Index(rendered[i:], "m")
		return rendered[i : i+j]
	}

	// --- acceptance: a user defines their own colour, no rebuild -----
	t.Run("user-defined colour overrides the rendered role", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "theme.json")
		writeThemeConfig(t, path, `{"colors": {"in_progress": "#00ff99"}}`)

		th, notice := theme.Load(path)
		if notice != "" {
			t.Fatalf("notice = %q, want empty for a well-formed override", notice)
		}
		out := build(th, notice)

		overrideFrag := fgFrag(lipgloss.Color("#00ff99"))
		if !strings.Contains(out, overrideFrag) {
			t.Fatalf("render does not contain the user-defined in_progress colour -- got:\n%s", out)
		}
		defaultFrag := fgFrag(theme.Default.Color(theme.RoleInProgress))
		if strings.Contains(out, defaultFrag) {
			t.Fatal("render still contains Default's own in_progress colour -- the override did not take effect")
		}
	})

	// --- acceptance: a partial override keeps every other role -------
	t.Run("partial override leaves unmentioned roles on the base theme", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "theme.json")
		writeThemeConfig(t, path, `{"theme": "mono-contrast", "colors": {"in_progress": "#00ff99"}}`)

		th, notice := theme.Load(path)
		if notice != "" {
			t.Fatalf("notice = %q, want empty", notice)
		}
		out := build(th, notice)

		overrideFrag := fgFrag(lipgloss.Color("#00ff99"))
		if !strings.Contains(out, overrideFrag) {
			t.Fatalf("render does not contain the overridden in_progress colour -- got:\n%s", out)
		}
		// blocked was NOT mentioned in colors -- it must still show
		// Mono's own colour (the named base theme), not Default's --
		// proving an unmentioned role falls back to the base theme, not
		// a silent reset, i.e. the override is genuinely partial.
		monoBlockedFrag := fgFrag(theme.Mono.Color(theme.RoleBlocked))
		if !strings.Contains(out, monoBlockedFrag) {
			t.Fatalf("render does not contain Mono's own blocked colour for an unmentioned role -- partial override behaved like a full one:\n%s", out)
		}
	})

	// --- acceptance: each of the three failure modes is visible ------
	t.Run("missing role name produces a visible notice", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "theme.json")
		writeThemeConfig(t, path, `{"colors": {"": "#ff00ff"}}`)

		th, notice := theme.Load(path)
		if notice == "" {
			t.Fatal("theme.Load returned an empty notice for a colour entry with no role name")
		}
		out := build(th, notice)
		if !strings.Contains(out, notice) {
			t.Fatalf("rendered board does not contain the missing-role-name notice %q:\n%s", notice, out)
		}
	})

	t.Run("unknown role produces a visible notice", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "theme.json")
		writeThemeConfig(t, path, `{"colors": {"not_a_real_role": "#ff00ff"}}`)

		th, notice := theme.Load(path)
		if notice == "" {
			t.Fatal("theme.Load returned an empty notice for an unknown role")
		}
		out := build(th, notice)
		if !strings.Contains(out, notice) {
			t.Fatalf("rendered board does not contain the unknown-role notice %q:\n%s", notice, out)
		}
	})

	t.Run("non-colour value produces a visible notice", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "theme.json")
		writeThemeConfig(t, path, `{"colors": {"error": "chartreuse"}}`)

		th, notice := theme.Load(path)
		if notice == "" {
			t.Fatal("theme.Load returned an empty notice for a value that is not a colour")
		}
		out := build(th, notice)
		if !strings.Contains(out, notice) {
			t.Fatalf("rendered board does not contain the not-a-colour notice %q:\n%s", notice, out)
		}
		// The rejected value must not have applied -- fetch a fresh
		// build with a genuine fetch error to confirm RoleError still
		// renders Default's own colour, not something derived from
		// "chartreuse".
		errModel := New(func() (Snapshot, error) { return Snapshot{}, os.ErrInvalid }).WithTheme(th, notice)
		next, _ := errModel.Update(fetchResultMsg{err: os.ErrInvalid})
		errOut := next.(Model).View()
		defFrag := fgFrag(theme.Default.Color(theme.RoleError))
		if !strings.Contains(errOut, defFrag) {
			t.Fatalf("render lost Default's error colour after a rejected override -- got:\n%s", errOut)
		}
	})
}

func writeThemeConfig(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeThemeConfig: %v", err)
	}
}
