package rail

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-estate/tui/internal/lane"
	"github.com/jonhill90/agent-estate/tui/internal/session"
)

// fakeOps is a session.Interface with no MCP subprocess and no tmux
// involved at all -- exactly the boundary agent-tui#14's architectural rule
// draws: the rail package must never know how an operation reaches tmux,
// only that it calls session.Interface and renders what comes back.
type fakeOps struct {
	attachCalls []string
	attachErr   error

	detachCalls int
	detachErr   error

	addCalls []string
	addErr   error
	addOut   session.AddResult

	checkCalls []string
	checkErr   error
	checkOut   session.RemoveCheck

	removeCalls   []string
	removeConfirm []bool
	removeErr     error

	sendCalls []string
	sendErr   error
	sendOut   session.SendResult
}

func (f *fakeOps) Attach(s string) error {
	f.attachCalls = append(f.attachCalls, s)
	return f.attachErr
}

func (f *fakeOps) Detach() error {
	f.detachCalls++
	return f.detachErr
}

func (f *fakeOps) Add(s string, lanes int, agent, cwd string) (session.AddResult, error) {
	f.addCalls = append(f.addCalls, s)
	return f.addOut, f.addErr
}

// AddWithMode satisfies session.Interface's SPEC-shell.md S12 addition --
// nothing in this package calls it (rail's own "n" key stays on plain Add,
// see doAdd's own doc comment), so this just delegates to the same fake
// state Add already uses, matching Ops.AddWithMode's own
// ExecutionLocal-delegates-to-Add behaviour.
func (f *fakeOps) AddWithMode(s string, lanes int, agent, cwd string, mode session.ExecutionMode) (session.AddResult, error) {
	return f.Add(s, lanes, agent, cwd)
}

func (f *fakeOps) RemoveCheck(s string) (session.RemoveCheck, error) {
	f.checkCalls = append(f.checkCalls, s)
	return f.checkOut, f.checkErr
}

func (f *fakeOps) Remove(s string, confirm bool) (session.RemoveResult, error) {
	f.removeCalls = append(f.removeCalls, s)
	f.removeConfirm = append(f.removeConfirm, confirm)
	return session.RemoveResult{Session: s, Removed: f.removeErr == nil}, f.removeErr
}

// Send satisfies session.Interface's agent-supervisor#508 addition --
// nothing in this package calls it yet (rail has no composer; that is
// internal/chat's own S7 write path), so this just records the call the
// same way every other fake method here does.
func (f *fakeOps) Send(sessionID, message string) (session.SendResult, error) {
	f.sendCalls = append(f.sendCalls, sessionID)
	return f.sendOut, f.sendErr
}

var _ session.Interface = (*fakeOps)(nil)

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	panic("key: unmapped " + s)
}

func modelWithOps(ops session.Interface) Model {
	m := NewMultiSession(func() ([]lane.Session, error) { return threeSessions(), nil }, nil, nil, "director")
	m.sessions = threeSessions() // agent-supervisor session, index 2, supervised
	m.selected = 0
	m.width = RailWidth + 8 // the widest View() honors without clamping back to RailWidth -- see sessions_test.go
	return m.WithOps(ops)
}

// TestPlainModelIgnoresOpsKeys is the backward-compatibility contract every
// pre-agent-tui#14 rail test relies on implicitly: a Model with ops == nil (every
// constructor call that does not chain WithOps) must treat a/d/n/x exactly
// as it treated any other unmapped key before agent-tui#14 existed -- never a nil
// interface panic.
func TestPlainModelIgnoresOpsKeys(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	for _, k := range []string{"a", "d", "n", "x"} {
		updated, cmd := m.Update(key(k))
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("key %q on a Model with no ops issued a command", k)
		}
	}
}

// TestAttachKeyIsNoLongerWired is agent-tui#23's fix, driven the same way
// TestAttachCallsOpsWithSelectedSession (this test's pre-agent-tui#23 self) drove
// 'a' -- through m.Update(), the real production dispatch entry point, not
// a direct call to handleOpsKey or any lower handler. Live testing against
// a real mcp_server.py and an isolated tmux server with more than one
// client attached (see ops.go's package doc comment and the PR description
// for the transcript) found that session_attach/session_detach silently
// move an ARBITRARY tmux client -- not necessarily the one running
// agent-tui -- because mcp_server.py's own stdio is piped for JSON-RPC and
// has no controlling terminal to identify "the invoking client" with. That
// is worse than the "does nothing" this issue was originally filed about,
// so 'a' (and 'd', below) are no longer wired to ops at all: this asserts
// the key press reaches Update() and produces neither a command nor a call
// to the fake, i.e. a genuine no-op, not a swallow with a hidden side
// effect.
func TestAttachKeyIsNoLongerWired(t *testing.T) {
	fake := &fakeOps{}
	m := modelWithOps(fake)
	m.selected = 1 // director, per threeSessions()

	updated, cmd := m.Update(key("a"))
	m = updated.(Model)

	if cmd != nil {
		t.Fatal("'a' issued a command -- attach must not be wired (agent-tui#23)")
	}
	if len(fake.attachCalls) != 0 {
		t.Fatalf("attachCalls = %+v, want none -- attach must not be wired (agent-tui#23)", fake.attachCalls)
	}
	if m.opsMode != opsModeIdle {
		t.Fatalf("opsMode = %v, want idle -- 'a' must not start a busy op", m.opsMode)
	}
}

// TestDetachKeyIsNoLongerWired is TestAttachKeyIsNoLongerWired's detach
// half. See that test's comment and ops.go's package doc comment for why.
func TestDetachKeyIsNoLongerWired(t *testing.T) {
	fake := &fakeOps{}
	m := modelWithOps(fake)

	updated, cmd := m.Update(key("d"))
	m = updated.(Model)

	if cmd != nil {
		t.Fatal("'d' issued a command -- detach must not be wired (agent-tui#23)")
	}
	if fake.detachCalls != 0 {
		t.Fatalf("detachCalls = %d, want 0 -- detach must not be wired (agent-tui#23)", fake.detachCalls)
	}
	if m.opsMode != opsModeIdle {
		t.Fatalf("opsMode = %v, want idle -- 'd' must not start a busy op", m.opsMode)
	}
}

// TestFooterNoLongerOffersAttachDetach is the rendered half of agent-tui#23:
// "a painted control that cannot work is worse than an absent one, because
// it reads as available" -- the footer must say so, not just the dispatch
// table.
func TestFooterNoLongerOffersAttachDetach(t *testing.T) {
	m := modelWithOps(&fakeOps{})
	out := m.View()
	if strings.Contains(out, "[a]ttach") || strings.Contains(out, "[d]etach") {
		t.Fatalf("View() still offers attach/detach -- agent-tui#23 removed both:\n%s", out)
	}
	if !strings.Contains(out, "[n]ew") || !strings.Contains(out, "[x]remove") {
		t.Fatalf("View() lost [n]ew/[x]remove along with attach/detach -- want those to stay:\n%s", out)
	}
}

// TestAddTypesAndSubmitsASessionName is acceptance item 2's UI half: 'n'
// enters adding mode, keys build the name, enter submits it, and the
// resulting state (from session_add's response) renders -- "add creates a
// session that reads supervised" only holds if this round-trip is intact.
func TestAddTypesAndSubmitsASessionName(t *testing.T) {
	fake := &fakeOps{addOut: session.AddResult{Session: "scratch-1", Created: true, State: "supervised"}}
	m := modelWithOps(fake)

	updated, _ := m.Update(key("n"))
	m = updated.(Model)
	if !strings.Contains(m.View(), "new session:") {
		t.Fatalf("entering add mode did not render the prompt:\n%s", m.View())
	}

	for _, r := range "scratch-1" {
		updated, _ = m.Update(key(string(r)))
		m = updated.(Model)
	}
	if !strings.Contains(m.View(), "scratch-1") {
		t.Fatalf("typed input did not render:\n%s", m.View())
	}

	updated, cmd := m.Update(key("enter"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("enter with a non-empty name issued no command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	if len(fake.addCalls) != 1 || fake.addCalls[0] != "scratch-1" {
		t.Fatalf("addCalls = %+v, want [scratch-1]", fake.addCalls)
	}
	if !strings.Contains(m.View(), "supervised") {
		t.Errorf("View() did not report the resulting supervised state:\n%s", m.View())
	}
}

// TestAddBackspaceEditsInput exercises the minimal rune editor.
func TestAddBackspaceEditsInput(t *testing.T) {
	m := modelWithOps(&fakeOps{})
	updated, _ := m.Update(key("n"))
	m = updated.(Model)
	updated, _ = m.Update(key("x"))
	m = updated.(Model)
	updated, _ = m.Update(key("backspace"))
	m = updated.(Model)
	if m.opsInput != "" {
		t.Fatalf("opsInput = %q, want empty after backspace", m.opsInput)
	}
}

// TestAddEscapeCancelsWithoutCalling proves 'esc' never reaches Ops.Add.
func TestAddEscapeCancelsWithoutCalling(t *testing.T) {
	fake := &fakeOps{}
	m := modelWithOps(fake)
	updated, _ := m.Update(key("n"))
	m = updated.(Model)
	updated, _ = m.Update(key("x"))
	m = updated.(Model)
	updated, cmd := m.Update(key("esc"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("esc issued a command")
	}
	if len(fake.addCalls) != 0 {
		t.Fatalf("addCalls = %+v, want none after esc", fake.addCalls)
	}
	if m.opsMode != opsModeIdle {
		t.Fatalf("opsMode = %v, want idle after esc", m.opsMode)
	}
}

// TestGKeyDuringAddDoesNotCycleGroupStyle is the reason handleOpsKey must
// run BEFORE the read-only switch: a session literally named with a 'g' or
// 'r' in it must not trigger unrelated bindings while being typed.
func TestGKeyDuringAddDoesNotCycleGroupStyle(t *testing.T) {
	m := modelWithOps(&fakeOps{})
	before := m.groupStyle
	updated, _ := m.Update(key("n"))
	m = updated.(Model)
	updated, _ = m.Update(key("g"))
	m = updated.(Model)
	if m.groupStyle != before {
		t.Fatalf("groupStyle changed from %d to %d while typing 'g' into a session name", before, m.groupStyle)
	}
	if m.opsInput != "g" {
		t.Fatalf("opsInput = %q, want %q", m.opsInput, "g")
	}
}

// --- at#22 review: quit must never be swallowed, even mid-operation ---

// TestBusyModeStillQuitsOnCtrlC is the fix for at#22's blocking finding: a
// live op (opsModeBusy) must not be able to strand Jon with no keyboard
// escape. Before the fix, `case opsModeBusy: return m, nil, true` swallowed
// every key unconditionally, so ctrl+c never reached model.go's "q",
// "ctrl+c" case and m.quitting never got set -- reproduced directly by the
// at#22 reviewer. Restoring that unconditional swallow should turn this red
// again (see the mutation check in the PR description / commit message).
func TestBusyModeStillQuitsOnCtrlC(t *testing.T) {
	fake := &fakeOps{}
	m := modelWithOps(fake)
	m.opsMode = opsModeBusy

	updated, cmd := m.Update(key("ctrl+c"))
	m = updated.(Model)
	if !m.quitting {
		t.Fatal("ctrl+c during opsModeBusy did not set quitting -- a hung op would lock the UI")
	}
	if cmd == nil {
		t.Fatal("ctrl+c during opsModeBusy issued no command -- want tea.Quit")
	}
}

// TestBusyModeStillQuitsOnQ is the same fix, the other quit key.
func TestBusyModeStillQuitsOnQ(t *testing.T) {
	fake := &fakeOps{}
	m := modelWithOps(fake)
	m.opsMode = opsModeBusy

	updated, cmd := m.Update(key("q"))
	m = updated.(Model)
	if !m.quitting {
		t.Fatal("q during opsModeBusy did not set quitting -- a hung op would lock the UI")
	}
	if cmd == nil {
		t.Fatal("q during opsModeBusy issued no command -- want tea.Quit")
	}
}

// TestBusyModeStillSwallowsOtherKeys proves the fix above is narrow: only
// the two quit keys escape opsModeBusy. Everything else stays swallowed --
// e.g. a second 'x' must not race the pending result (see opsModeBusy's own
// doc comment in ops.go).
func TestBusyModeStillSwallowsOtherKeys(t *testing.T) {
	fake := &fakeOps{}
	m := modelWithOps(fake)
	m.opsMode = opsModeBusy

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	if m.quitting {
		t.Fatal("'x' during opsModeBusy set quitting")
	}
	if cmd != nil {
		t.Fatal("'x' during opsModeBusy issued a command")
	}
	if m.opsMode != opsModeBusy {
		t.Fatalf("opsMode = %v, want still busy", m.opsMode)
	}
}

// --- remove: the refusals are the deliverable (agent-tui#14) ---

func supervisedIdleClean(sessionName string) session.RemoveCheck {
	return session.RemoveCheck{
		Session: sessionName, Exists: true, Supervision: "supervised",
		BusyLanes: nil, Worktrees: nil, SafeToRemove: true, Refusals: nil,
	}
}

// TestRemoveRefusesUnsupervisedSession is acceptance item 3: the check
// comes back unsafe, naming supervision as the reason, and 'y' must not
// call Remove at all -- the confirm prompt for an unsafe check never offers
// a path to it.
func TestRemoveRefusesUnsupervisedSession(t *testing.T) {
	fake := &fakeOps{checkOut: session.RemoveCheck{
		Session: "Hill90", Exists: true, Supervision: "unknown",
		SafeToRemove: false, Refusals: []string{"not supervised (as#153 marker reads unknown)"},
	}}
	m := modelWithOps(fake)
	m.selected = 0 // Hill90, per threeSessions()

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	out := m.View()
	if !strings.Contains(out, "not supervised") { // acceptance item 3
		t.Fatalf("View() did not name the refusal reason:\n%s", out)
	}

	updated, cmd = m.Update(key("y"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("'y' on an unsafe check issued a command")
	}
	if len(fake.removeCalls) != 0 {
		t.Fatalf("removeCalls = %+v, want none -- 'y' must not remove an unsafe session", fake.removeCalls)
	}
}

// TestRemoveRefusesBusyLane is acceptance item 4.
func TestRemoveRefusesBusyLane(t *testing.T) {
	fake := &fakeOps{checkOut: session.RemoveCheck{
		Session: "agent-supervisor", Exists: true, Supervision: "supervised",
		BusyLanes: []string{"at13-multi-session-rail"}, SafeToRemove: false,
		Refusals: []string{"busy lane: at13-multi-session-rail"},
	}}
	m := modelWithOps(fake)
	m.selected = 2 // agent-supervisor, per threeSessions()

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	out := m.View()
	if !strings.Contains(out, "busy lane") { // acceptance item 4
		t.Fatalf("View() did not name the busy-lane refusal:\n%s", out)
	}
	updated, cmd = m.Update(key("y"))
	if cmd != nil {
		t.Fatal("'y' on a busy session issued a command")
	}
	_ = updated
	if len(fake.removeCalls) != 0 {
		t.Fatalf("removeCalls = %+v, want none", fake.removeCalls)
	}
}

// TestRemoveRefusesUndeterminableWorktree is acceptance item 5: "cannot
// tell" must never render or behave as safe.
func TestRemoveRefusesUndeterminableWorktree(t *testing.T) {
	fake := &fakeOps{checkOut: session.RemoveCheck{
		Session: "agent-supervisor", Exists: true, Supervision: "supervised",
		Worktrees: []session.Worktree{{
			Path: "/work/agent-supervisor", Clean: nil, Unpushed: nil,
			Reason: "git status failed: exit 128",
		}},
		SafeToRemove: false,
		Refusals:     []string{"cannot determine: /work/agent-supervisor (git status failed: exit 128)"},
	}}
	m := modelWithOps(fake)
	m.selected = 2

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	out := m.View()
	if !strings.Contains(out, "cannot determine") { // acceptance item 5
		t.Fatalf("View() did not name the undeterminable-worktree refusal:\n%s", out)
	}
	updated, cmd = m.Update(key("y"))
	if cmd != nil {
		t.Fatal("'y' on an undeterminable session issued a command")
	}
	_ = updated
	if len(fake.removeCalls) != 0 {
		t.Fatalf("removeCalls = %+v, want none", fake.removeCalls)
	}
}

// TestRemoveSucceedsOnIdleSupervisedCleanSession is acceptance item 6.
func TestRemoveSucceedsOnIdleSupervisedCleanSession(t *testing.T) {
	fake := &fakeOps{checkOut: supervisedIdleClean("scratch-throwaway")}
	m := modelWithOps(fake)
	m.selected = 2

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	if !strings.Contains(m.View(), "confirm") {
		t.Fatalf("safe check did not offer a confirm prompt:\n%s", m.View())
	}

	updated, cmd = m.Update(key("y"))
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("'y' on a safe check issued no command")
	}
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	if len(fake.removeCalls) != 1 {
		t.Fatalf("removeCalls = %+v, want exactly one", fake.removeCalls)
	}
	if !fake.removeConfirm[0] {
		t.Fatal("Remove was called with confirm=false")
	}
	if !strings.Contains(m.View(), "removed") {
		t.Errorf("View() did not report the removal:\n%s", m.View())
	}
}

// TestRemoveAnyOtherKeyCancels proves the confirm prompt is not a general
// "press anything to continue" -- only 'y' proceeds, and only when safe.
func TestRemoveAnyOtherKeyCancels(t *testing.T) {
	fake := &fakeOps{checkOut: supervisedIdleClean("scratch-throwaway")}
	m := modelWithOps(fake)
	m.selected = 2

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	updated, cmd = m.Update(key("n"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("a non-'y' key issued a command")
	}
	if len(fake.removeCalls) != 0 {
		t.Fatalf("removeCalls = %+v, want none", fake.removeCalls)
	}
	if m.opsMode != opsModeIdle {
		t.Fatalf("opsMode = %v, want idle after cancelling", m.opsMode)
	}
}

// TestRemoveServerRefusalAfterConfirmIsShown covers the TOCTOU case: the
// check said safe, but the supervisor's own re-check at the moment of
// Remove disagreed (e.g. a lane went busy between check and confirm) --
// this must surface as a refusal, never as a silent success.
func TestRemoveServerRefusalAfterConfirmIsShown(t *testing.T) {
	fake := &fakeOps{
		checkOut:  supervisedIdleClean("scratch-throwaway"),
		removeErr: errors.New(`lane free-2 went busy`),
	}
	m := modelWithOps(fake)
	m.selected = 2

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)
	updated, cmd = m.Update(key("y"))
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	out := m.View()
	if !strings.Contains(out, "refused") {
		t.Fatalf("View() did not report the server-side refusal:\n%s", out)
	}
}

// --- agent-tui#153: the nil-ops guard at the top of handleOpsKey ---

// TestNilOpsGuardPreventsAddCommandPanic is agent-tui#153's fix: the guard
// `if m.ops == nil { return m, nil, false }` at the top of handleOpsKey is
// the only thing standing between a degraded launch (cmd/agent-tui skips
// WithOps when there is no client -- see ops.go's package doc and
// cmd/agent-tui's own comment) and a nil-interface panic. The issue's own
// measurement is the reason this test cannot stop at checking cmd == nil
// after one or two keys: pressing 'n' or 'x' alone never panics even with
// the guard deleted, because opsModeAdding/opsModeBusy only *record*
// intent -- the actual nil-interface call is inside the tea.Cmd that
// 'enter' returns, and that Cmd only panics once bubbletea (or this test)
// RUNS it. A test that reads "handled/cmd" after 'n' and 'x' and stops
// there is exactly the false negative the issue reports.
//
// So this drives the whole add sequence through m.Update -- the real
// production entry point, not handleOpsKey directly -- on a Model with
// ops == nil, then explicitly calls whatever tea.Cmd 'enter' returns:
//
//   - Guard PRESENT (this file as it ships): 'n' never enters
//     opsModeAdding (handleOpsKey returns handled == false at the guard,
//     same as any other unmapped key), so every subsequent typed key and
//     'enter' fall through the same way, and 'enter' returns cmd == nil.
//     Nothing to run; the test passes.
//   - Guard ABSENT: 'n' falls through the guard's spot straight into the
//     switch below it and sets opsModeAdding regardless of m.ops, typed
//     runes accumulate exactly as they would with a real ops wired in, and
//     'enter' reaches doAdd(m.ops, ...) -- returning a real tea.Cmd that
//     closes over the nil m.ops. Calling that Cmd here calls Add on a nil
//     session.Interface and panics, exactly as the issue's own transcript
//     shows ("PANIC: runtime error: invalid memory address or nil pointer
//     dereference"), failing this test the moment go test's runtime
//     reports the unrecovered panic.
//
// The typed name ("sample") is deliberately free of every letter this
// package's read-only switch binds (q/r/g/t/w/k/j) -- with the guard
// removed those letters are consumed by handleAddingKey same as any other
// rune, but proving that is not the point of this test, and a stray 'r' or
// 'q' landing in the read-only switch on a guard-present run would issue
// an unrelated fetch/quit command and make a failure here harder to read.
func TestNilOpsGuardPreventsAddCommandPanic(t *testing.T) {
	m := New(func() ([]lane.Lane, error) { return nil, nil })
	if m.ops != nil {
		t.Fatal("test setup: Model has ops wired -- this test's whole point is m.ops == nil")
	}

	updated, cmd := m.Update(key("n"))
	m = updated.(Model)
	if cmd != nil {
		t.Fatal("'n' with nil ops issued a command")
	}

	for _, r := range "sample" {
		updated, cmd = m.Update(key(string(r)))
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("typing %q with nil ops issued a command", string(r))
		}
	}

	updated, cmd = m.Update(key("enter"))
	m = updated.(Model)

	if cmd == nil {
		// This is the guard doing its job: 'n' never entered opsModeAdding,
		// so 'enter' had nothing queued to submit. Nothing to run.
		return
	}

	// The guard is absent. Running this is the whole point (see the doc
	// comment above) -- it calls session.Interface.Add on m.ops == nil and
	// panics, failing this test.
	cmd()
	t.Fatal("nil-ops guard did not prevent handleOpsKey from queuing an add command -- running the returned command should have panicked on the nil ops.Add call")
}

// --- mutation check: break one refusal, prove the test goes red, restore it ---
//
// agent-tui#14 acceptance item 7. This is expressed as a single test that
// re-derives the SafeToRemove gate handleConfirmRemoveKey depends on and
// asserts it independently of the production code's own copy -- if
// handleConfirmRemoveKey's guard is ever weakened (e.g. "proceed on 'y'
// regardless of SafeToRemove"), this test fails. The mutation itself is
// demonstrated in the PR description (temporarily removing the
// `&& m.opsCheck.SafeToRemove` clause in ops.go, running this file, showing
// red, then restoring it) rather than committed here, since a permanently
// broken guard cannot ship.
func TestRemoveGateRequiresSafeToRemoveNotJustY(t *testing.T) {
	fake := &fakeOps{checkOut: session.RemoveCheck{
		Session: "Hill90", SafeToRemove: false, Refusals: []string{"session is not supervised"},
	}}
	m := modelWithOps(fake)
	m.selected = 0

	updated, cmd := m.Update(key("x"))
	m = updated.(Model)
	updated, _ = m.Update(cmd())
	m = updated.(Model)

	updated, cmd = m.Update(key("y"))
	if cmd != nil {
		t.Fatal("the remove gate let 'y' through an unsafe check")
	}
	_ = updated
	if len(fake.removeCalls) != 0 {
		t.Fatalf("removeCalls = %+v, the remove gate must never call Remove on an unsafe check", fake.removeCalls)
	}
}
