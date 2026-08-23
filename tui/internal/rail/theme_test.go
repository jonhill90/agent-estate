package rail

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jonhill90/agent-tui/internal/lane"
	"github.com/jonhill90/agent-tui/internal/theme"
)

// TestThemeSwitchChangesEverySurface is agent-tui#27's driven acceptance
// test: PHASES.md's QA gate ("Jon QAs look and feel. Agents QA function")
// requires a test that actually DRIVES the control being proven, not one
// that inspects the theme.Theme struct in isolation -- agent-tui#23
// shipped a painted-but-inert [a]ttach precisely because a delivery check
// never pressed a key. There is no in-app keypress for a theme switch (the
// brief is explicit: this is a per-user persisted setting, not a runtime
// cycler like the glyph-set picker) -- "driving" it here means going
// through the exact same construction path cmd/agent-tui uses
// (WithTheme(...)) and then calling the real View(), for the same live
// Model state, under both of this build's shipped themes, and asserting
// concrete, per-role differences in the rendered ANSI -- not just "the
// strings differ somewhere".
func TestThemeSwitchChangesEverySurface(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	build := func(th theme.Theme) Model {
		m := NewMultiSession(
			func() ([]lane.Session, error) {
				return []lane.Session{
					{
						Name:       "director",
						Supervised: true,
						Lanes: []lane.Lane{
							{Window: 1, WindowID: "@1", Name: "lane-one", Command: "claude", State: "busy"},
						},
					},
				}, nil
			},
			nil, nil, "director",
		).WithTheme(th, "")
		// Drive it exactly as a real session would: an Init-less fetch
		// result delivered, a row selected, a few animation ticks passed.
		next, _ := m.Update(sessionsFetchResultMsg{sessions: []lane.Session{
			{
				Name:       "director",
				Supervised: true,
				Lanes: []lane.Lane{
					{Window: 1, WindowID: "@1", Name: "lane-one", Command: "claude", State: "busy"},
				},
			},
		}})
		m = next.(Model)
		m.selected = 0
		m.tick = 3
		return m
	}

	def := build(theme.Default)
	mono := build(theme.Mono)

	outDefault := def.View()
	outMono := mono.View()

	if outDefault == outMono {
		t.Fatal("rail render is byte-identical between theme.Default and theme.Mono -- the theme switch changed nothing")
	}

	// Per-role assertions: each theme's colour for role must actually
	// appear in ITS OWN render, and must NOT appear in the other theme's
	// render (Default/Mono share no colour on any role -- see
	// theme.TestMonoDiffersFromDefaultOnEveryRole). A role whose literal
	// never got routed would still show Default's colour under Mono.
	assertRole := func(role theme.Role, defaultOut, monoOut string) {
		t.Helper()
		defFG := fgProbe(theme.Default.Color(role))
		monoFG := fgProbe(theme.Mono.Color(role))
		if !strings.Contains(defaultOut, defFG) {
			t.Errorf("role %q: Default's render does not contain Default's own colour (%q) -- got:\n%s", role, defFG, defaultOut)
		}
		if strings.Contains(defaultOut, monoFG) {
			t.Errorf("role %q: Default's render contains Mono's colour (%q) -- theme not actually switching", role, monoFG)
		}
		if !strings.Contains(monoOut, monoFG) {
			t.Errorf("role %q: Mono's render does not contain Mono's own colour (%q) -- got:\n%s", role, monoFG, monoOut)
		}
		if strings.Contains(monoOut, defFG) {
			t.Errorf("role %q: Mono's render contains Default's colour (%q) -- literal not routed through the theme", role, defFG)
		}
	}
	assertRole(theme.RoleBorder, outDefault, outMono)
	assertRole(theme.RoleDirector, outDefault, outMono)

	// selectionBG is a Background, not a Foreground -- probed separately.
	assertBG := func(role theme.Role, defaultOut, monoOut string) {
		t.Helper()
		defBG := bgProbe(theme.Default.Color(role))
		monoBG := bgProbe(theme.Mono.Color(role))
		if !strings.Contains(defaultOut, defBG) {
			t.Errorf("role %q: Default's render does not contain Default's own background (%q)", role, defBG)
		}
		if !strings.Contains(monoOut, monoBG) {
			t.Errorf("role %q: Mono's render does not contain Mono's own background (%q)", role, monoBG)
		}
	}
	assertBG(theme.RoleSelectedBG, outDefault, outMono)

	// The director glyph rune itself (theme.DirectorMark) must switch too
	// -- a glyph rune, not just a colour, per #27's own inventory category.
	if !strings.Contains(outDefault, theme.Default.DirectorMark) {
		t.Errorf("Default's render does not contain Default's own director mark %q", theme.Default.DirectorMark)
	}
	if !strings.Contains(outMono, theme.Mono.DirectorMark) {
		t.Errorf("Mono's render does not contain Mono's own director mark %q", theme.Mono.DirectorMark)
	}
	if strings.Contains(outMono, theme.Default.DirectorMark) {
		t.Errorf("Mono's render still contains Default's director mark %q -- glyph rune not routed through the theme", theme.Default.DirectorMark)
	}
}

// TestThemeErrorRoleDriven exercises theme.RoleError specifically, through
// the fetch-failure path (m.fetchErr) rather than a synthetic style call --
// "driven against real state", same discipline as the fetchErr-path tests
// elsewhere in this package.
func TestThemeErrorRoleDriven(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	build := func(th theme.Theme) Model {
		m := New(func() ([]lane.Lane, error) { return nil, nil }).WithTheme(th, "")
		next, _ := m.Update(fetchResultMsg{err: errors.New("fixture: unavailable")})
		return next.(Model)
	}

	outDefault := build(theme.Default).View()
	outMono := build(theme.Mono).View()

	defFG := fgProbe(theme.Default.Color(theme.RoleError))
	monoFG := fgProbe(theme.Mono.Color(theme.RoleError))

	if !strings.Contains(outDefault, defFG) {
		t.Errorf("Default's fetch-error render does not contain Default's error colour")
	}
	if !strings.Contains(outMono, monoFG) {
		t.Errorf("Mono's fetch-error render does not contain Mono's error colour")
	}
	if strings.Contains(outMono, defFG) {
		t.Errorf("Mono's fetch-error render still contains Default's error colour -- literal not routed")
	}
}

// fgProbe/bgProbe return the truecolor SGR fragment lipgloss emits for c
// ("38;2;R;G;B" / "48;2;R;G;B"), extracted by rendering a probe cell and
// pulling the fragment out with a regexp rather than assuming the whole
// escape sequence -- the real render path combines this fragment with
// other codes (Bold, Faint, ...) into one escape (e.g.
// "\x1b[1;38;2;r;g;bm"), so asserting the full prefix a bare
// Foreground()-only style produces would false-negative on every styled
// call site that also sets Bold. Testing the colour fragment, not the
// whole escape, is what actually proves "this theme's colour reached the
// render", which is the only thing this test needs to prove.
func fgProbe(c lipgloss.Color) string {
	return colorFragment(lipgloss.NewStyle().Foreground(c).Render("X"))
}

func bgProbe(c lipgloss.Color) string {
	return colorFragment(lipgloss.NewStyle().Background(c).Render("X"))
}

var sgrFragmentRe = regexp.MustCompile(`\d{1,3};2;\d{1,3};\d{1,3};\d{1,3}`)

func colorFragment(rendered string) string {
	m := sgrFragmentRe.FindString(rendered)
	if m == "" {
		panic("colorFragment: no truecolor SGR fragment found in " + rendered)
	}
	return m
}

// TestThemeNoticeRendersVisibly closes the loop on #27 acceptance item 3
// end to end: theme.Load's own tests (internal/theme/config_test.go) prove
// it RETURNS a non-empty notice for a malformed/unknown config; this proves
// that notice, once wired through WithTheme exactly as cmd/agent-tui wires
// it, actually reaches the rendered screen rather than being computed and
// silently dropped somewhere in main().
func TestThemeNoticeRendersVisibly(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil }).WithTheme(theme.Default, "unknown theme \"nope\"; using default theme \"signal-dark\"")
	out := m.View()
	if !strings.Contains(out, "unknown theme") {
		t.Fatalf("a non-empty theme notice did not render visibly:\n%s", out)
	}

	// The missing-config case (empty notice) must NOT render a spurious
	// line -- rendering exactly as today for a plain missing config is
	// #27 acceptance item 3's other half.
	quiet := New(func() ([]lane.Lane, error) { return nil, nil }).WithTheme(theme.Default, "").View()
	if strings.Contains(quiet, "unknown theme") {
		t.Fatalf("an empty theme notice rendered a notice line anyway:\n%s", quiet)
	}
}

// TestKeyTRequestsThemeCycleWithoutMutatingLocalTheme is agent-tui#51's
// replacement for the old TestKeyTCyclesThemeAtRuntime: this package no
// longer owns the theme value (theme.CycleRequestedMsg's doc comment
// explains why four independent copies -- one per pane -- was the actual
// defect, not merely unpersisted). Pressing 't' must still be driven
// through a real Model's Update, but the only thing it may now do is ask,
// via the returned tea.Cmd's Msg, for shell.Model to cycle the ONE shared
// theme -- m.theme itself must be untouched, or this pane and shell's
// value could drift again the moment either one changes it.
func TestKeyTRequestsThemeCycleWithoutMutatingLocalTheme(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil }).WithTheme(theme.Default, "")

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
