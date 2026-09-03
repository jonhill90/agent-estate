package shell

// This file covers agent-tui#51's two defects, both against a real
// tea.Program via teatest (same discipline as model_teatest_test.go):
//
//  1. Persistence: pressing 't' never called theme.Save (agent-tui#48's diff fixed
//     this by duplicating a saveTheme(th) call into four packages).
//  2. Shared value: one 't' keypress must change every pane at once --
//     four independent in-memory copies cannot do this, which is why the
//     rail and the active content pane could show two different themes on
//     screen simultaneously. This is the defect agent-tui#48's diff did not fix and
//     a rebase would not fix either -- see theme.CycleRequestedMsg's own
//     doc comment for the full rationale.

import (
	"bytes"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/jonhill90/agent-estate/src/tui/internal/theme"
)

// runWide is run (model_teatest_test.go)'s own teatest.NewTestModel call,
// but at a wider terminal -- 100 columns is model_teatest_test.go's own
// deliberate choice for its navigation tests, but agent-tui#64's [5] flow
// addition to the footer legend pushed TestKeyTSurfacesASaveFailure's
// "! theme not saved: ..." suffix past truncate()'s 100-column budget, so
// that one assertion needs the room this gives it -- the footer's actual
// content and truncation behaviour are unchanged, only this test's own
// viewport is wider.
func runWide(t *testing.T, m Model) *teatest.TestModel {
	t.Helper()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(160, 30))
	t.Cleanup(func() { _ = tm.Quit() })
	return tm
}

// TestKeyTPersistsThemeChoice drives 't' against a real Program with a
// WithThemeSave fake standing in for theme.Save (adapter discipline,
// AGENTS.md -- this package must never touch the filesystem itself) and
// asserts the fake was actually called with the CYCLED theme, not the
// starting one. Before agent-tui#51, no package called theme.Save at all
// on a 't' press -- see the PR body for this test's failure output against
// pre-fix code.
func TestKeyTPersistsThemeChoice(t *testing.T) {
	var saved []theme.Theme
	m := testModel().WithTheme(theme.Default, "").WithThemeSave(func(th theme.Theme) error {
		saved = append(saved, th)
		return nil
	})

	tm := run(t, m)
	waitFor(t, tm, "[2] board")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	// The rail's own footer line ("theme: <name>") is the visible proof
	// the cycle actually reached a render, same marker
	// TestKeyTRepaintsRailAndContentPaneTogether below waits on.
	waitFor(t, tm, theme.Cycle(theme.Default).Name)

	if len(saved) == 0 {
		t.Fatal("pressing 't' never called the wired theme-save function -- the choice would not survive a restart")
	}
	got := saved[len(saved)-1]
	want := theme.Cycle(theme.Default)
	if got.ID != want.ID {
		t.Fatalf("saved theme = %q, want %q (theme.Cycle(theme.Default))", got.ID, want.ID)
	}
}

// TestKeyTRepaintsRailAndContentPaneTogether is defect 2 specifically:
// one keypress, every pane's rendered theme changes together. Focus starts
// on the rail (New's default) so the keypress is routed to rail.Model,
// exactly the case where agent-tui#48's four-owner design could show the rail on
// one theme and the content pane on another -- the fix must make the
// GALLERY pane (never focused, never directly sent the keypress) repaint
// too, in the same frame's worth of output.
func TestKeyTRepaintsRailAndContentPaneTogether(t *testing.T) {
	m := testModel().WithTheme(theme.Default, "").WithStart(PaneGallery)

	tm := run(t, m)
	waitFor(t, tm, "glyph gallery")

	// Sanity: focus is still rail (WithStart only picks the visible pane,
	// not focus) -- the keypress below is routed to rail.Model, not
	// gallery.Model, so gallery repainting proves the fan-out, not a
	// coincidence of both being sent the same key directly.
	if m.focus != focusRail {
		t.Fatalf("test setup: focus = %v, want focusRail so the keypress is routed away from gallery", m.focus)
	}

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	out := waitFor(t, tm, theme.Cycle(theme.Default).Name)

	// Both the rail's own "theme: <name>" line and the gallery's own
	// footer line render theme.Cycle(theme.Default).Name -- one owner, one
	// value, both surfaces. Before agent-tui#51, only the KEY-RECEIVING pane (rail)
	// would show this; gallery would still read Default.
	got := bytes.Count(out, []byte(theme.Cycle(theme.Default).Name))
	if got < 2 {
		t.Fatalf("theme name %q appears %d time(s) in the composed frame, want at least 2 (rail AND the active content pane) -- a keypress on the rail did not repaint gallery:\n%s",
			theme.Cycle(theme.Default).Name, got, out)
	}
}

// TestKeyTSurfacesASaveFailureWithoutBlockingTheCycle is the "absence is a
// typed value" half of persistence: a theme.Save error (e.g. an
// unwritable config directory) must be visible, not silently dropped, but
// must not stop the in-memory cycle every pane just repainted with.
func TestKeyTSurfacesASaveFailureWithoutBlockingTheCycle(t *testing.T) {
	m := testModel().WithTheme(theme.Default, "").WithThemeSave(func(theme.Theme) error {
		return errors.New("fixture: read-only config dir")
	})

	tm := runWide(t, m)
	waitFor(t, tm, "[2] board")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	out := waitFor(t, tm, theme.Cycle(theme.Default).Name)

	if !bytes.Contains(out, []byte("theme not saved")) {
		t.Fatalf("a theme.Save error was not rendered anywhere in the footer:\n%s", out)
	}
}
