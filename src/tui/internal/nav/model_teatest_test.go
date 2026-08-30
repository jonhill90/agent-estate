package nav

// This file drives the real tea.Program via charmbracelet/x/exp/teatest --
// the same discipline internal/board and internal/shell's own
// model_teatest_test.go files use (see their doc comments): send tea.Msg
// through the actual event loop, then read the actual rendered output,
// rather than asserting against View()'s string in-process only.

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func run(t *testing.T, m Model) *teatest.TestModel {
	t.Helper()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(40, 24))
	t.Cleanup(func() { _ = tm.Quit() })
	return tm
}

// waitFor reads tm's output until it contains want, returning everything
// accumulated so far -- board/shell's own waitFor takes the same approach
// for the same reason: a failure needs the actual rendered frame, not just
// "condition never true."
func waitFor(t *testing.T, tm *teatest.TestModel, want string) []byte {
	t.Helper()
	var b bytes.Buffer
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		buf := make([]byte, 65536)
		n, _ := tm.Output().Read(buf)
		if n > 0 {
			b.Write(buf[:n])
		}
		if bytes.Contains(b.Bytes(), []byte(want)) {
			return b.Bytes()
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor %q: not seen after 8s. Output so far:\n%s", want, b.String())
	return nil
}

func mustNotContain(t *testing.T, out []byte, want string) {
	t.Helper()
	if bytes.Contains(out, []byte(want)) {
		t.Fatalf("expected %q absent, output:\n%s", want, out)
	}
}

// TestInitialFrameShowsTopLevelRailNotGroupChildren drives a plain New()
// against a real Program and asserts the first frame shows the top-level
// row (Home..Lanes) with every group collapsed (no "Skills") -- the
// default state before any active route has been set.
func TestInitialFrameShowsTopLevelRailNotGroupChildren(t *testing.T) {
	tm := run(t, New())
	out := waitFor(t, tm, "Home")
	for _, want := range []string{"Dashboard", "Agents", "Chat", "Tasks", "Knowledge", "Library", "Lanes", "Build", "Admin"} {
		if !bytes.Contains(out, []byte(want)) {
			t.Fatalf("missing %q in initial frame:\n%s", want, out)
		}
	}
	mustNotContain(t, out, "Skills")
}

// TestBKeyCollapsesToIconsOnly presses [b] against a real Program and
// asserts the labels actually disappear from the rendered frame -- proving
// the key through the real event loop, not just Update's return value
// (model_test.go's TestBKeyTogglesIconsOnly covers that cheaper path
// already; this is the pty-level proof the build loop asks for).
func TestBKeyCollapsesToIconsOnly(t *testing.T) {
	tm := run(t, New())
	waitFor(t, tm, "Home")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})

	deadline := time.Now().Add(8 * time.Second)
	var out []byte
	for time.Now().Before(deadline) {
		buf := make([]byte, 65536)
		n, _ := tm.Output().Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if bytes.Contains(out, []byte("⌂")) && !bytes.Contains(out, []byte("Home")) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("icons-only frame never arrived (want glyph \"⌂\" present, label \"Home\" absent). Output so far:\n%s", out)
}
