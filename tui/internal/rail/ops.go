// agent-tui#14: the write path -- add and remove a tmux session, entirely
// through internal/session.Interface (an MCP tools/call each), never
// through tmux directly (see internal/session's own package doc for why
// that is non-negotiable). Kept in its own file for the same reason
// sessions.go is: model.go's non-write path (every pre-agent-tui#14 test, board.go's
// flat single-session render) stays a diff-free read, and nothing there
// changes shape because of what is here.
//
// agent-tui#23: 'a'ttach and 'd'etach are deliberately NOT wired here,
// though session.Interface still declares Attach/Detach and Ops still
// implements them (agent-supervisor#165's session_attach/session_detach
// tools exist and work). Live testing against a real mcp_server.py +
// isolated tmux server (see the PR description for the transcript) found
// that both tools operate on "the client attached to the invoking
// process's own controlling terminal" -- but mcp_server.py's own stdio is
// wired to pipes for JSON-RPC (internal/mcp.Client.Start), never a tty, so
// it has no controlling terminal of its own and therefore no way to know
// which tmux client issued the call. With exactly one client attached to
// the whole tmux server, tmux's own "fall back to the only attached
// client" heuristic happens to make this look like it works. With two or
// more attached (an ordinary multi-terminal dev setup), the same call
// reports success but silently moves tmux's own idea of "the most recently
// active client" -- which is not reliably the one running agent-tui, and
// there is no parameter on either tool to say which one is meant. That is
// worse than the "silent no-op" this issue was filed about: it can attach
// or detach a DIFFERENT terminal than the one the user pressed the key in,
// while reporting success. Fixing it needs agent-supervisor to thread a
// client identity through session_attach/session_detach and target it
// explicitly (TmuxTransport.switch_client/detach_client currently never
// pass -c/-t for a specific client) -- out of this repo's boundary, filed
// as jonhill90/agent-supervisor#189. Until that lands, this package
// declares the control unavailable rather than paint one that can silently
// do the wrong thing: see TestAttachKeyIsNoLongerWired and
// TestDetachKeyIsNoLongerWired below, and the footer text change in
// renderOpsStatus.
package rail

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/agent-tui/internal/session"
)

// opsMode is the write path's small state machine. Every value but
// opsModeIdle intercepts key handling in handleOpsKey before the read-only
// switch in model.go's Update ever sees the key.
type opsMode int

const (
	opsModeIdle opsMode = iota
	// opsModeAdding: typing a new session's name after 'n'. Every rune key
	// is appended to opsInput rather than falling through to a read-only
	// binding -- a session named "g" or "r" must not cycle the group style
	// or trigger a refresh while it is being typed.
	opsModeAdding
	// opsModeBusy: an attach/detach/add/remove/remove-check call is in
	// flight. Every key is swallowed (not just left unhandled) so a second
	// keypress cannot race the pending result -- e.g. pressing 'x' twice
	// before the first session_remove_check answers must not fire two
	// checks against two different rows if selection also changed between
	// them.
	opsModeBusy
	// opsModeConfirmRemove: session_remove_check answered and is on screen.
	// 'y' proceeds ONLY when the check reported SafeToRemove; every other
	// key -- including 'y' when it is NOT safe -- cancels back to idle
	// without ever calling Remove. See handleConfirmRemoveKey's own comment
	// for why that second, client-side guard exists alongside the
	// supervisor's authoritative one.
	opsModeConfirmRemove
)

type addResultMsg struct {
	session string
	result  session.AddResult
	err     error
}

type removeCheckResultMsg struct {
	session string
	check   session.RemoveCheck
	err     error
}

type removeResultMsg struct {
	session string
	err     error
}

// doAdd never passes lanes/agent/cwd -- 'n' is deliberately the minimal
// "create me a normal session" entry point; a fuller form (picking a lane
// count or an explicit --agent) is a real feature of its own scope, not a
// silent drop -- see the PR description for what this issue's own text
// asks for versus what a follow-up would add.
func doAdd(ops session.Interface, target string) tea.Cmd {
	return func() tea.Msg {
		result, err := ops.Add(target, 0, "", "")
		return addResultMsg{session: target, result: result, err: err}
	}
}

func doRemoveCheck(ops session.Interface, target string) tea.Cmd {
	return func() tea.Msg {
		check, err := ops.RemoveCheck(target)
		return removeCheckResultMsg{session: target, check: check, err: err}
	}
}

// doRemove always passes confirm=true -- it is only ever reached from
// handleConfirmRemoveKey, which itself only fires 'y' when the last
// session_remove_check reported SafeToRemove. The supervisor re-checks
// everything fresh at this call regardless (never trusting that prior
// check -- see internal/session.Interface.Remove's own doc comment), so
// confirm=true here means exactly "I saw a check and chose to proceed", not
// "trust me".
func doRemove(ops session.Interface, target string) tea.Cmd {
	return func() tea.Msg {
		_, err := ops.Remove(target, true)
		return removeResultMsg{session: target, err: err}
	}
}

// selectedSessionName resolves the row under the cursor to a session name --
// remove acts on "whatever is selected", the same target up/down already
// navigates (agent-tui#23: attach used to share this too; it no longer
// calls into ops at all -- see this file's package doc). Returns ok ==
// false when there is nothing selectable (no sessions fetch wired, or an
// empty list), rather than acting on a stale or zero-value name.
func (m Model) selectedSessionName() (string, bool) {
	if m.sessionsFetch == nil {
		return "", false
	}
	flat := m.sessionsFlat()
	if m.selected < 0 || m.selected >= len(flat) {
		return "", false
	}
	idx := flat[m.selected].sessionIdx
	if idx < 0 || idx >= len(m.sessions) {
		return "", false
	}
	return m.sessions[idx].Name, true
}

// handleOpsKey is model.go's Update's first stop for every key when
// m.ops != nil. handled == false means "no opinion, fall through to the
// read-only switch" -- the only way out when m.ops == nil (every pre-agent-tui#14
// Model) or the idle mode's switch does not recognise the key.
func (m Model) handleOpsKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.ops == nil {
		return m, nil, false
	}

	switch m.opsMode {
	case opsModeAdding:
		return m.handleAddingKey(msg)
	case opsModeConfirmRemove:
		return m.handleConfirmRemoveKey(msg)
	case opsModeBusy:
		// at#22 review: quitting is not a write-path key and must never be
		// swallowed, even mid-operation -- a hung MCP call (see
		// internal/mcp.Client.call's deadline for the other half of this
		// fix) must never be able to lock Jon out of his own UI. Falling
		// through with handled == false lets the read-only switch below
		// (model.go's "q", "ctrl+c" case) run exactly as it would with no
		// ops in flight at all; every other key stays swallowed so a second
		// keypress cannot race the pending result.
		switch msg.String() {
		case "q", "ctrl+c":
			return m, nil, false
		}
		return m, nil, true
	}

	switch msg.String() {
	// agent-tui#23: 'a' and 'd' are deliberately absent here -- see this
	// file's package doc comment. Falling through with handled == false
	// (the final `return m, nil, false` below) means they behave exactly
	// like any other unbound key: read-only switch below ignores them too
	// (no lane list has an 'a' or 'd' binding), so pressing either is a
	// true no-op, not a swallow.
	case "n":
		m.opsMode = opsModeAdding
		m.opsInput = ""
		m.opsStatus = ""
		return m, nil, true
	case "x":
		target, ok := m.selectedSessionName()
		if !ok {
			return m, nil, true
		}
		m.opsMode = opsModeBusy
		m.opsTarget = target
		m.opsStatus = fmt.Sprintf("checking %s…", target)
		return m, doRemoveCheck(m.ops, target), true
	}
	return m, nil, false
}

// handleAddingKey builds opsInput one key at a time. There is no
// bubbles/textinput dependency in this module (go.mod carries none today) --
// this is deliberately the minimal rune accumulator this one field needs,
// not a general text-input widget.
func (m Model) handleAddingKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeyEsc:
		m.opsMode = opsModeIdle
		m.opsInput = ""
		m.opsStatus = "add cancelled"
		return m, nil, true
	case tea.KeyEnter:
		name := strings.TrimSpace(m.opsInput)
		if name == "" {
			m.opsStatus = "session name cannot be empty"
			return m, nil, true
		}
		m.opsMode = opsModeBusy
		m.opsTarget = name
		m.opsStatus = fmt.Sprintf("creating %s…", name)
		return m, doAdd(m.ops, name), true
	case tea.KeyBackspace:
		if r := []rune(m.opsInput); len(r) > 0 {
			m.opsInput = string(r[:len(r)-1])
		}
		return m, nil, true
	case tea.KeySpace:
		m.opsInput += " "
		return m, nil, true
	case tea.KeyRunes:
		m.opsInput += string(msg.Runes)
		return m, nil, true
	}
	// Every other key (arrows, function keys, ...) is swallowed rather than
	// left unhandled -- while typing a name, nothing should fall through to
	// a read-only binding underneath it.
	return m, nil, true
}

// handleConfirmRemoveKey is agent-tui#14 requirement 4 ("confirm
// explicitly, naming what will be destroyed") at the keystroke level. 'y'
// proceeds only when the CHECK ALREADY ON SCREEN says it is safe -- a
// second, client-side guard layered on top of the supervisor's own
// authoritative one (session_remove re-evaluates everything fresh
// regardless; see internal/session.Interface.Remove). This does not violate
// "safety rules live in one place": the supervisor is still the only place
// a removal can actually be AUTHORIZED, this only decides whether the UI
// bothers asking it to try.
func (m Model) handleConfirmRemoveKey(msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if msg.String() == "y" && m.opsCheck.SafeToRemove {
		target := m.opsTarget
		m.opsMode = opsModeBusy
		m.opsStatus = fmt.Sprintf("removing %s…", target)
		return m, doRemove(m.ops, target), true
	}
	m.opsMode = opsModeIdle
	m.opsStatus = "remove cancelled"
	return m, nil, true
}

// handleOpsResult applies one of the three doXxx commands' results (agent-
// tui#23: attach/detach no longer have one -- see this file's package doc).
// Called from model.go's Update for the matching message types.
func (m Model) handleOpsResult(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case addResultMsg:
		m.opsMode = opsModeIdle
		m.opsInput = ""
		if msg.err != nil {
			m.opsStatus = fmt.Sprintf("add %s failed: %s", msg.session, msg.err)
			return m, nil
		}
		m.opsStatus = fmt.Sprintf("%s created (%s)", msg.result.Session, msg.result.State)
		// Refresh immediately rather than waiting up to refreshInterval --
		// agent-tui#14 acceptance item 2 ("add creates a session that reads
		// supervised") is meant to be visible right away, not after a poll.
		return m.doFetchAll()

	case removeCheckResultMsg:
		if msg.err != nil {
			m.opsMode = opsModeIdle
			m.opsStatus = fmt.Sprintf("remove check %s failed: %s", msg.session, msg.err)
			return m, nil
		}
		m.opsMode = opsModeConfirmRemove
		m.opsCheck = msg.check
		m.opsTarget = msg.session
		return m, nil

	case removeResultMsg:
		m.opsMode = opsModeIdle
		if msg.err != nil {
			// A refusal the supervisor caught even though the check on
			// screen said safe (state changed between check and confirm,
			// e.g. a lane went busy) surfaces exactly the same as any other
			// tool error -- never swallowed into a bare "failed".
			m.opsStatus = fmt.Sprintf("remove %s refused: %s", msg.session, msg.err)
			return m, nil
		}
		m.opsStatus = fmt.Sprintf("%s removed", msg.session)
		return m.doFetchAll()
	}
	return m, nil
}

// renderOpsStatus is View()'s entry point into the write path's own
// footer section -- the prompt for whichever mode is live, or the last
// completed operation's one-line result while idle.
func (m Model) renderOpsStatus(innerWidth int, st railStyles) []string {
	switch m.opsMode {
	case opsModeAdding:
		return []string{
			st.legend.Width(innerWidth).Render("new session:"),
			st.row.Width(innerWidth).Render(truncate(m.opsInput+"_", innerWidth)),
			st.legend.Width(innerWidth).Render("[enter] create  [esc] cancel"),
		}
	case opsModeConfirmRemove:
		return m.renderRemoveConfirm(innerWidth, st)
	case opsModeBusy:
		return []string{st.legend.Width(innerWidth).Render(truncate(m.opsStatus, innerWidth))}
	default:
		if m.opsStatus == "" {
			// agent-tui#23: was "[a]ttach [d]etach [n]ew [x]remove" -- attach
			// and detach are gone from this line for the same reason they are
			// gone from handleOpsKey's switch above; see this file's package
			// doc comment.
			return []string{st.legend.Width(innerWidth).Render("[n]ew [x]remove")}
		}
		return []string{st.legend.Width(innerWidth).Render(truncate(m.opsStatus, innerWidth))}
	}
}

// renderRemoveConfirm is agent-tui#14 requirement 4 rendered: what would be
// destroyed, named, or exactly why it is refused -- never a bare "no".
func (m Model) renderRemoveConfirm(innerWidth int, st railStyles) []string {
	c := m.opsCheck
	var lines []string
	lines = append(lines, st.err.Width(innerWidth).Render(truncate("remove "+c.Session+"?", innerWidth)))
	if c.SafeToRemove {
		lines = append(lines, st.legend.Width(innerWidth).Render("idle, supervised, clean"))
		lines = append(lines, st.legend.Width(innerWidth).Render("[y] confirm  [any] cancel"))
		return lines
	}
	lines = append(lines, st.dim.Width(innerWidth).Render("refused:"))
	for _, reason := range c.Refusals {
		lines = append(lines, st.dim.Width(innerWidth).Render(truncate("- "+reason, innerWidth)))
	}
	if len(c.Refusals) == 0 {
		// safe_to_remove was false with no named reason -- a contract
		// violation on the supervisor's side, not something to render as if
		// nothing were wrong (the same "never a bare no" rule applies to
		// this program's own bugs, not just the supervisor's refusals).
		lines = append(lines, st.dim.Width(innerWidth).Render("- refused, no reason given (bug: report this)"))
	}
	lines = append(lines, st.legend.Width(innerWidth).Render("[any key] dismiss"))
	return lines
}
