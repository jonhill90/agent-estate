package chat

// This file drives the real tea.Program via charmbracelet/x/exp/teatest --
// the same discipline internal/board/internal/shell's own
// model_teatest_test.go files use. internal/chat had no teatest file of
// its own before S7 (SPEC-shell.md) -- internal/shell's own
// TestF6NavigatesToChatPane exercises this package only as far as
// mounting it; composer keys ([i]/[enter]/[esc]) are new with this file's
// own change and are proven here at the real event-loop level, not only
// via model_test.go's cheaper direct-Update calls.

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func run(t *testing.T, m Model) *teatest.TestModel {
	t.Helper()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))
	t.Cleanup(func() { _ = tm.Quit() })
	return tm
}

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

// TestIKeyOpensComposerAgainstARealProgram drives a real Program: select a
// real thread, press "i", type text, and assert the actual rendered frame
// shows it in the composer's own input line -- not just that Update's
// return value carries it (model_test.go's cheaper coverage).
func TestIKeyOpensComposerAgainstARealProgram(t *testing.T) {
	tm := run(t, New(NewFixtureSource()))
	waitFor(t, tm, "chat")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")}) // a real thread, not "All"
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	waitFor(t, tm, "[enter] send")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	waitFor(t, tm, "hello")
}

// TestEnterWithNoSenderShowsErrorAgainstARealProgram is
// TestEnterWithNoSenderShowsHonestError's pty-level twin.
func TestEnterWithNoSenderShowsErrorAgainstARealProgram(t *testing.T) {
	tm := run(t, New(NewFixtureSource()))
	waitFor(t, tm, "chat")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hi")})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	waitFor(t, tm, "cannot send")
}

// TestCtrlCQuitsARealProgramWhileComposing is agent-tui#22's lesson,
// proven at the real event-loop level: the program must actually
// terminate, not just return a Cmd model_test.go's fake harness trusts.
func TestCtrlCQuitsARealProgramWhileComposing(t *testing.T) {
	tm := run(t, New(NewFixtureSource()))
	waitFor(t, tm, "chat")

	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	tm.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i")})
	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
}
