package chat

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fetched drives Init() and its returned Cmd synchronously, then delivers a
// realistic WindowSizeMsg -- the same pattern internal/gallery/model_test.go
// and internal/rail's own model tests use to get a Model past its first
// fetch without a real tea.Program, extended here so transcriptVP/listVP
// are actually sized (sync only runs from Update, never from View).
func fetched(t *testing.T, width, height int) Model {
	t.Helper()
	m := New(NewFixtureSource())
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned a nil Cmd")
	}
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	updated, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return updated.(Model)
}

func sendKey(t *testing.T, m Model, key string) Model {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return updated.(Model)
}

// TestFetchPrependsAllThread proves every render sees the unified feed
// (layouts.go's collapsed option [3]) at index 0, not as a separate mode.
func TestFetchPrependsAllThread(t *testing.T) {
	m := fetched(t, 100, 30)
	if len(m.threads) == 0 {
		t.Fatal("threads is empty after fetch")
	}
	if m.threads[0].ID != "all" {
		t.Errorf("threads[0].ID = %q, want \"all\"", m.threads[0].ID)
	}
}

// TestKeyVCyclesLayout is the seam the issue explicitly asks for, driven
// through a real Update the way rail.Model's own theme-cycle test drives
// theme.Cycle -- a real key delivered to a real Update, then a real View()
// actually differing, not a struct inspected in isolation.
func TestKeyVCyclesLayout(t *testing.T) {
	m := fetched(t, 100, 30)

	before := Layouts[m.layout].ID
	viewBefore := m.View()

	m = sendKey(t, m, "v")
	after := Layouts[m.layout].ID
	viewAfter := m.View()

	if before == after {
		t.Fatalf("layout did not change: still %q", after)
	}
	if viewBefore == viewAfter {
		t.Errorf("View() did not change after switching layout %q -> %q", before, after)
	}

	// Cycling len(Layouts) times must return to the start (wraps, never
	// runs off the end) -- same wraparound guarantee theme.Cycle documents.
	for i := 0; i < len(Layouts)-1; i++ {
		m = sendKey(t, m, "v")
	}
	if Layouts[m.layout].ID != before {
		t.Errorf("after a full cycle, layout = %q, want back to %q", Layouts[m.layout].ID, before)
	}
}

// TestSelectionClearsUnread is the navigation ask's other half: reading a
// thread must clear its own unread marker.
func TestSelectionClearsUnread(t *testing.T) {
	m := fetched(t, 100, 30)
	idx := -1
	for i, th := range m.threads {
		if th.Unread {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatal("fixture has no unread thread to test against")
	}
	m.jumpTo(idx)
	if m.threads[idx].Unread {
		t.Errorf("thread %d still Unread after jumpTo", idx)
	}
}

// TestKeyFOnlyFocusesInGrid proves "f" is a no-op in listLayout -- focus is
// gridLayout's own state (layouts.go: "focus folded in"), not a global
// mode that could leak into the other layout's rendering.
func TestKeyFOnlyFocusesInGrid(t *testing.T) {
	m := fetched(t, 100, 30)
	if Layouts[m.layout].ID != listLayout.ID {
		t.Fatalf("test assumes listLayout is the default, got %q", Layouts[m.layout].ID)
	}
	m = sendKey(t, m, "f")
	if m.focused != -1 {
		t.Errorf("focused = %d after \"f\" in listLayout, want -1 (no-op)", m.focused)
	}
}

// TestQuitStopsProgram proves "q" issues tea.Quit -- the same baseline
// every other pane's model_test.go checks.
func TestQuitStopsProgram(t *testing.T) {
	m := fetched(t, 100, 30)
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = updated.(Model)
	if !m.quitting {
		t.Error("quitting = false after \"q\"")
	}
	if cmd == nil {
		t.Fatal("Update returned a nil Cmd for \"q\", want tea.Quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("cmd() = %v, want tea.Quit()", msg)
	}
}

// TestViewRendersEveryMessageKindWithoutPanicking is a smoke test over
// both layouts against the fixture's full kind coverage -- render.go's own
// unit tests check text shape; this checks the Model composes it without
// crashing at realistic terminal sizes, list and grid alike.
func TestViewRendersEveryMessageKindWithoutPanicking(t *testing.T) {
	for _, l := range Layouts {
		m := fetched(t, 120, 40)
		for m.layout < len(Layouts) && Layouts[m.layout].ID != l.ID {
			m = sendKey(t, m, "v")
		}
		out := m.View()
		if !strings.Contains(out, "chat") {
			t.Errorf("layout %q: View() missing title", l.ID)
		}
	}
}

// TestLongThreadIsScrollableNotTruncated is agent-tui#20's acceptance item
// this package exists to satisfy, and the exact shape of agent-tui#29's
// regression (a board that silently dropped rows past its height): the
// fixture's fourth thread has 20+ rendered lines against an 8-row pane, so
// this asserts (a) the model itself knows content is hidden
// (scrollIndicator is non-empty) and (b) scrolling actually moves the
// window rather than being a no-op -- "reachable", not just "flagged."
func TestLongThreadIsScrollableNotTruncated(t *testing.T) {
	m := fetched(t, 100, 12) // a deliberately small pane -- see mx.bodyHeight math
	longIdx := -1
	for i, th := range m.threads {
		if th.ID == "lane-d" {
			longIdx = i
		}
	}
	if longIdx < 0 {
		t.Fatal("fixture has no lane-d thread")
	}
	m.jumpTo(longIdx)
	m = m.sync()

	if m.transcriptVP.TotalLineCount() <= m.transcriptVP.Height {
		t.Fatalf("transcriptVP content (%d lines) does not exceed its height (%d) -- fixture no longer forces scrolling",
			m.transcriptVP.TotalLineCount(), m.transcriptVP.Height)
	}
	if scrollIndicator(m.transcriptVP, m.styles()) == "" {
		t.Error("scrollIndicator is empty for an overflowing transcript -- hidden content must be visibly flagged")
	}
	if !m.transcriptVP.AtBottom() {
		t.Fatal("test assumes a freshly selected thread starts scrolled to its latest message")
	}

	before := m.transcriptVP.YOffset
	m = sendKey(t, m, "home")
	if m.transcriptVP.YOffset >= before {
		t.Errorf("\"home\" did not scroll up: YOffset %d -> %d", before, m.transcriptVP.YOffset)
	}
	if !m.transcriptVP.AtTop() {
		t.Error("\"home\" did not reach the top of the transcript")
	}

	m = sendKey(t, m, "end")
	if !m.transcriptVP.AtBottom() {
		t.Error("\"end\" did not return to the bottom of the transcript")
	}
}

// TestGridTileFlagsHiddenMessages proves gridLayout's own reachability
// story: an overloaded tile must render a "more" marker rather than just
// quietly showing fewer lines than a thread with more history, and "f"
// must be the way to actually read the rest.
func TestGridTileFlagsHiddenMessages(t *testing.T) {
	m := fetched(t, 100, 20)
	m = sendKey(t, m, "v") // -> gridLayout
	if Layouts[m.layout].ID != gridLayout.ID {
		t.Fatalf("test assumes \"v\" reaches gridLayout, got %q", Layouts[m.layout].ID)
	}
	out := m.View()
	if !strings.Contains(out, "more -- [f] to focus") {
		t.Errorf("gridLayout View() has no hidden-content marker for the long thread:\n%s", out)
	}
}

// TestIKeyEntersComposeModeOnARealThread is S7's own composer, "[i]" the
// key -- fetched(t, ...) leaves selection on threads[0], the synthetic
// "All" thread (TestFetchPrependsAllThread), so this jumps to a real one
// first with "1" (the first real thread, threads[1]) before pressing "i".
func TestIKeyEntersComposeModeOnARealThread(t *testing.T) {
	m := fetched(t, 100, 30)
	m = sendKey(t, m, "1") // jumpTo(0) -- "1" jumps to threads[0], "2" to threads[1]... see jumpTo
	m = sendKey(t, m, "2")
	if m.threads[m.selected].ID == "all" {
		t.Fatalf("test setup: selection is still the \"All\" thread")
	}

	m = sendKey(t, m, "i")
	if !m.composing {
		t.Fatal("\"i\" did not enter compose mode on a real thread")
	}
	if !m.composer.Focused() {
		t.Fatal("composer is not focused after \"i\"")
	}
}

// TestIKeyRefusesOnTheAllThread guards Sender's own precondition: the
// synthetic "All" thread (AggregateAll, ID "all") has no single lane
// behind it to address -- composing against it would have nowhere real
// to send.
func TestIKeyRefusesOnTheAllThread(t *testing.T) {
	m := fetched(t, 100, 30)
	if m.threads[m.selected].ID != "all" {
		t.Fatalf("test setup: selection is not the \"All\" thread")
	}
	m = sendKey(t, m, "i")
	if m.composing {
		t.Fatal("\"i\" entered compose mode against the synthetic \"All\" thread")
	}
}

// TestIKeyRefusesInGridLayout matches composer/composing's own doc
// comment: the composer is scoped to listLayout.
func TestIKeyRefusesInGridLayout(t *testing.T) {
	m := fetched(t, 100, 30)
	m = sendKey(t, m, "2") // a real thread selected
	m = sendKey(t, m, "v") // -> gridLayout
	if Layouts[m.layout].ID != gridLayout.ID {
		t.Fatalf("test assumes \"v\" reaches gridLayout, got %q", Layouts[m.layout].ID)
	}
	m = sendKey(t, m, "i")
	if m.composing {
		t.Fatal("\"i\" entered compose mode in gridLayout")
	}
}

// TestEnterWithNoSenderShowsHonestError is Sender's own contract: no
// caller in this repo wires a non-nil Sender yet (no live transport
// exists), so [enter] must say so visibly rather than silently accepting
// the keypress and doing nothing -- AGENTS.md's "blind, not quiet."
func TestEnterWithNoSenderShowsHonestError(t *testing.T) {
	m := fetched(t, 100, 30)
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")
	m.composer.SetValue("hello")

	next, _ := m.handleComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)

	if m.sendErr == "" {
		t.Fatal("[enter] with no Sender wired produced no error")
	}
	if !strings.Contains(m.View(), "cannot send") {
		t.Fatalf("send error not rendered:\n%s", m.View())
	}
}

// runSend drives [enter] through the full async round trip: trySend's own
// Cmd (the send itself, running the fake Sender), then the sendMsg that
// Cmd produces, fed back into Update -- the same two-step shape a real
// bubbletea runtime drives, and the reason this cannot be a single
// handleComposerKey call the way the no-Sender path still is (that path
// never returns a Cmd at all). Returns the model immediately after
// trySend (still sendInFlight) and the model after sendMsg lands, so a
// caller can assert either point.
func runSend(t *testing.T, m Model, text string) (inFlight, resolved Model) {
	t.Helper()
	m.composer.SetValue(text)
	next, cmd := m.handleComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	inFlight = next.(Model)
	if cmd == nil {
		t.Fatal("trySend returned no Cmd -- a real Sender must run async, never inline in Update")
	}
	msg := cmd()
	next, _ = inFlight.Update(msg)
	resolved = next.(Model)
	return inFlight, resolved
}

// TestEnterWithSenderCallsItAndClearsComposer is the success/delivered
// path, driven against a fake Sender (adapter discipline, AGENTS.md) -- no
// real transport exists to test against, matching FixtureSource's own
// reason for existing. Also asserts the in-flight state trySend itself
// produces, before the fake Sender's result lands.
func TestEnterWithSenderCallsItAndClearsComposer(t *testing.T) {
	var gotThread, gotText string
	m := fetched(t, 100, 30).WithSender(func(threadID, text string) error {
		gotThread, gotText = threadID, text
		return nil
	})
	m = sendKey(t, m, "2")
	wantThread := m.threads[m.selected].ID
	m = sendKey(t, m, "i")

	inFlight, resolved := runSend(t, m, "hello agent")

	if inFlight.sendOutcome != sendInFlight {
		t.Fatalf("sendOutcome after trySend = %v, want sendInFlight -- SPEC-shell.md S7's own 'in flight' state", inFlight.sendOutcome)
	}
	if !inFlight.composing {
		t.Fatal("composer left compose mode before the send even resolved")
	}
	if !strings.Contains(inFlight.View(), "sending") {
		t.Fatalf("in-flight state not rendered:\n%s", inFlight.View())
	}

	if gotThread != wantThread || gotText != "hello agent" {
		t.Fatalf("Sender called with (%q, %q), want (%q, %q)", gotThread, gotText, wantThread, "hello agent")
	}
	if resolved.sendOutcome != sendIdle {
		t.Fatalf("sendOutcome after delivery = %v, want sendIdle", resolved.sendOutcome)
	}
	if resolved.composing {
		t.Fatal("still composing after a successful send")
	}
	if resolved.composer.Value() != "" {
		t.Fatalf("composer.Value() = %q after a successful send, want empty", resolved.composer.Value())
	}
	if resolved.sendErr != "" {
		t.Fatalf("sendErr = %q after a successful send, want empty", resolved.sendErr)
	}
}

// TestFailedSendRendersFailureWithItsError is the mutation-checked
// failure direction (agent-b3.md: "the failure direction matters more --
// a send that silently reports success is the defect this whole
// transport exists to eliminate"). A Sender returning a plain error (not
// ErrUnknown) must resolve to sendFailed, stay in compose mode (so the
// draft is not lost), and show the error text on screen.
func TestFailedSendRendersFailureWithItsError(t *testing.T) {
	m := fetched(t, 100, 30).WithSender(func(threadID, text string) error {
		return errors.New("daemon: turn reported is_error")
	})
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")

	_, resolved := runSend(t, m, "hello agent")

	if resolved.sendOutcome != sendFailed {
		t.Fatalf("sendOutcome = %v, want sendFailed", resolved.sendOutcome)
	}
	if !resolved.composing {
		t.Fatal("left compose mode after a failed send -- the draft should stay editable")
	}
	if !strings.Contains(resolved.View(), "is_error") {
		t.Fatalf("failure text not rendered:\n%s", resolved.View())
	}
}

// TestUnknownSendIsDistinguishableFromFailedAndDelivered is SPEC-shell.md
// S7's central requirement: errors.Is(err, ErrUnknown) must render as a
// THIRD state, never collapsed into "failed" (agent-supervisor#488's own
// lesson) and never silently treated as delivered.
func TestUnknownSendIsDistinguishableFromFailedAndDelivered(t *testing.T) {
	m := fetched(t, 100, 30).WithSender(func(threadID, text string) error {
		return fmt.Errorf("%w: mcp: session_send: no reply within 20m0s", ErrUnknown)
	})
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")

	_, resolved := runSend(t, m, "hello agent")

	if resolved.sendOutcome != sendUnknown {
		t.Fatalf("sendOutcome = %v, want sendUnknown", resolved.sendOutcome)
	}
	if resolved.sendOutcome == sendFailed {
		t.Fatal("an unconfirmed outcome must never be reported as sendFailed")
	}
	view := resolved.View()
	if !strings.Contains(view, "unknown") {
		t.Fatalf("unknown state not rendered:\n%s", view)
	}
	if strings.Contains(view, "! "+resolved.sendErr) {
		// renderComposer's failed branch renders exactly "! "+sendErr --
		// must not appear when the outcome is unknown, not failed (the
		// unknown branch renders "? unknown -- "+sendErr instead).
		t.Fatalf("unknown state rendered with the failed-state's own '! <err>' text:\n%s", view)
	}
}

// TestSecondEnterWhileInFlightIsANoOp guards against racing two sends to
// the same thread while the first has not resolved yet.
func TestSecondEnterWhileInFlightIsANoOp(t *testing.T) {
	calls := 0
	block := make(chan struct{})
	m := fetched(t, 100, 30).WithSender(func(threadID, text string) error {
		calls++
		<-block
		return nil
	})
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")
	m.composer.SetValue("hello agent")

	next, cmd := m.handleComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("first enter returned no Cmd")
	}

	next, secondCmd := m.handleComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	m = next.(Model)
	if secondCmd != nil {
		t.Fatal("a second [enter] while sendInFlight returned a Cmd -- must be a no-op")
	}
	close(block)
	_ = cmd() // drain the first Sender's goroutine-equivalent so it does not leak past the test
	if calls != 1 {
		t.Fatalf("Sender called %d times, want exactly 1", calls)
	}
}

// TestEscCancelsComposing covers the other way out of compose mode.
func TestEscCancelsComposing(t *testing.T) {
	m := fetched(t, 100, 30)
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")
	m.composer.SetValue("unsent draft")

	next, _ := m.handleComposerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("esc")})
	m = next.(Model)

	if m.composing {
		t.Fatal("still composing after \"esc\"")
	}
	if m.composer.Value() != "" {
		t.Fatalf("composer.Value() = %q after \"esc\", want cleared", m.composer.Value())
	}
}

// TestCtrlCQuitsWhileComposing is agent-tui#22's own lesson, applied to
// this new mode: quitting must never be swallowed by a key-capturing state.
func TestCtrlCQuitsWhileComposing(t *testing.T) {
	m := fetched(t, 100, 30)
	m = sendKey(t, m, "2")
	m = sendKey(t, m, "i")

	next, cmd := m.handleComposerKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("ctrl+c while composing did not return tea.Quit")
	}
	if !m.quitting {
		t.Fatal("ctrl+c while composing did not set quitting")
	}
}

// TestComposerBudgetIsIdenticalComposingOrNot is this package's own "fixed
// budget in, fixed budget out" discipline (renderList's doc comment) made
// a test for the two rows this file's composer change added: View()'s
// total line count (measured at 29 for a 30-row terminal -- one under
// height, true before this change too; shell.Model's own clampHeight pads
// the difference, same as every other pane in this module) must be
// IDENTICAL whether or not compose mode is active, so toggling [i]/[esc]
// never shifts anything below it on screen -- the exact regression class
// agent-tui#29/#38 already found once (gallery's View() overrunning its
// own budget by one line, in the other direction).
func TestComposerBudgetIsIdenticalComposingOrNot(t *testing.T) {
	atRest := fetched(t, 100, 30)
	restLines := strings.Count(atRest.View(), "\n") + 1

	composing := sendKey(t, sendKey(t, atRest, "2"), "i")
	composingLines := strings.Count(composing.View(), "\n") + 1

	if restLines != composingLines {
		t.Fatalf("View() line count = %d at rest, %d while composing -- composerHeight's own budget is not being held constant", restLines, composingLines)
	}
}
