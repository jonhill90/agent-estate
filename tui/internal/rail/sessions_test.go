package rail

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jonhill90/keelson/internal/lane"
	"github.com/jonhill90/keelson/internal/theme"
)

func threeSessions() []lane.Session {
	return []lane.Session{
		{
			Name:       "Hill90",
			Supervised: false,
			Lanes:      []lane.Lane{{Window: 1, WindowID: "@1", Name: "architecture", Command: "claude.exe", State: "supervisor"}},
		},
		{
			Name:       "director",
			Supervised: false,
			Lanes:      []lane.Lane{{Window: 1, WindowID: "@5", Name: "director", Command: "claude.exe", State: "supervisor"}},
		},
		{
			Name:       "agent-supervisor",
			Supervised: true,
			Lanes: []lane.Lane{
				{Window: 1, WindowID: "@10", Name: "supervisor", Command: "claude.exe", State: "supervisor"},
				{Window: 2, WindowID: "@11", Name: "at13-multi-session-rail", Command: "claude.exe", State: "busy"},
			},
		},
	}
}

// TestNewMultiSessionRendersEverySession is agent-tui#13's core acceptance
// item 1: every session renders, grouped, and none goes missing -- the
// regression the issue traces was a rail that showed exactly one.
func TestNewMultiSessionRendersEverySession(t *testing.T) {
	m := NewMultiSession(func() ([]lane.Session, error) { return threeSessions(), nil }, nil, nil, "director")
	m.sessions = threeSessions()
	m.width = RailWidth + 8 // the widest View() honors without clamping back to RailWidth
	out := m.View()
	for _, want := range []string{"Hill90", "director", "agent-supervisor", "architecture", "at13-multi-session-rail"} {
		if !strings.Contains(out, want) {
			t.Errorf("View() missing %q:\n%s", want, out)
		}
	}
}

// TestDirectorIsVisiblyDistinct is agent-tui#13's other named requirement:
// "the director must appear" AND "make it look different -- something to
// make it special". This checks the marker survives rendering, not the
// exact color -- the color is a design call the PR names, not a contract.
func TestDirectorIsVisiblyDistinct(t *testing.T) {
	m := NewMultiSession(func() ([]lane.Session, error) { return threeSessions(), nil }, nil, nil, "director")
	m.sessions = threeSessions()
	out := m.View()

	var directorLine, ordinaryLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "director") && directorLine == "" {
			directorLine = line
		}
		if strings.Contains(line, "Hill90") {
			ordinaryLine = line
		}
	}
	if directorLine == "" {
		t.Fatalf("no rendered line found for the director session:\n%s", out)
	}
	if !strings.Contains(directorLine, "★") {
		t.Errorf("director's header row has no distinct marker: %q", directorLine)
	}
	if ordinaryLine != "" && strings.Contains(ordinaryLine, "★") {
		t.Errorf("a non-director session's header carries the director marker: %q", ordinaryLine)
	}
}

// TestUnsupervisedSessionsAreMarked is the safety-critical case #13 and its
// traced comment both call out: Jon's own session (or any session the
// estate has no ledger evidence for) must never render indistinguishably
// from one the estate manages -- his sessions were destroyed three times by
// exactly that confusion.
func TestUnsupervisedSessionsAreMarked(t *testing.T) {
	m := NewMultiSession(func() ([]lane.Session, error) { return threeSessions(), nil }, nil, nil, "director")
	m.sessions = threeSessions()
	out := m.View()

	var hillLine, supervisorLine string
	for _, line := range strings.Split(out, "\n") {
		// First match only: "session: Hill90" in the trailing legend would
		// otherwise overwrite the header line this test actually means to
		// check, and that legend line never carries "(unsupervised)".
		if hillLine == "" && strings.Contains(line, "Hill90") {
			hillLine = line
		}
		if supervisorLine == "" && strings.Contains(line, "agent-supervisor") {
			supervisorLine = line
		}
	}
	if !strings.Contains(hillLine, "unsupervised") {
		t.Errorf("Jon's own (unsupervised) session is not marked unsupervised: %q", hillLine)
	}
	if strings.Contains(supervisorLine, "unsupervised") {
		t.Errorf("a ledger-known session is wrongly marked unsupervised: %q", supervisorLine)
	}
}

// TestSelectionSpansSessions is requirement 4: up/down must move across the
// WHOLE tree, not reset or clamp at a single session's boundary.
func TestSelectionSpansSessions(t *testing.T) {
	m := NewMultiSession(func() ([]lane.Session, error) { return threeSessions(), nil }, nil, nil, "director")
	m.sessions = threeSessions()
	total := len(m.sessionsFlat())
	if total != 4 {
		t.Fatalf("setup: expected 4 lanes across all sessions, got %d", total)
	}

	for i := 0; i < total-1; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	if m.selected != total-1 {
		t.Fatalf("pressing down %d times should reach the last row (%d), got selected=%d", total-1, total-1, m.selected)
	}
	// One more must not overshoot.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.selected != total-1 {
		t.Fatalf("down past the last row should clamp, got selected=%d", m.selected)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.selected != total-2 {
		t.Fatalf("up should move back across the session boundary, got selected=%d", m.selected)
	}
}

// TestGroupStyleKeyCyclesLiveAgainstRealData is requirement 5's picker rule,
// applied to grouping the way the existing digit-key picker applies it to
// glyphs: 'g' must change what is actually drawn, not just an internal
// counter nothing reads.
func TestGroupStyleKeyCyclesLiveAgainstRealData(t *testing.T) {
	if len(groupStyles) < 2 {
		t.Fatalf("setup: need at least 2 grouping styles to prove the picker cycles, got %d", len(groupStyles))
	}
	m := NewMultiSession(func() ([]lane.Session, error) { return threeSessions(), nil }, nil, nil, "director")
	m.sessions = threeSessions()

	before := m.View()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	m = updated.(Model)
	if m.groupStyle != 1 {
		t.Fatalf("pressing 'g' once should select groupStyles[1], got %d", m.groupStyle)
	}
	after := m.View()
	if before == after {
		t.Errorf("'g' changed groupStyle but View() output is byte-identical -- the picker is not live")
	}
}

// TestSessionsFetchErrorIsShownNeverBlank mirrors the flat view's own
// blindness discipline: a failed sessions fetch must render visibly, never
// a blank rail that looks like a quiet, healthy estate.
func TestSessionsFetchErrorIsShownNeverBlank(t *testing.T) {
	m := NewMultiSession(func() ([]lane.Session, error) { return nil, nil }, nil, nil, "director")
	model, _ := m.Update(sessionsFetchResultMsg{err: errCostFetchFailed})
	m = model.(Model)
	out := m.View()
	if !strings.Contains(out, "unavailable") {
		t.Errorf("a failed sessions fetch did not render as unavailable:\n%s", out)
	}
}

// TestBoardStillGetsFlatSingleSessionModel is the non-regression check for
// board.go and every pre-#13 rail test: New/NewWithCost must render exactly
// the flat single-session list they always did, untouched by sessions.go
// existing.
func TestBoardStillGetsFlatSingleSessionModel(t *testing.T) {
	m := New(func() ([]lane.Lane, error) {
		return []lane.Lane{{Window: 1, WindowID: "@0", Name: "free-1", Command: "claude", State: "free"}}, nil
	})
	model, _ := m.Update(fetchResultMsg{lanes: []lane.Lane{
		{Window: 1, WindowID: "@0", Name: "free-1", Command: "claude", State: "free"},
	}})
	m = model.(Model)
	out := m.View()
	if !strings.Contains(out, "free-1") {
		t.Fatalf("flat single-session View() lost its own lane row:\n%s", out)
	}
	if strings.Contains(out, "group ") {
		t.Errorf("the grouping picker leaked into the flat (non-multi-session) view:\n%s", out)
	}
}

// errSessionsToolUnavailable mirrors the exact shape the review's live
// reproduction reported: an agent-supervisor checkout that predates
// agent-supervisor#158 has no "sessions" tool at all, and mcp.Client.CallTool
// (internal/mcp/client.go) surfaces that as an error carrying the JSON-RPC
// message verbatim.
var errSessionsToolUnavailable = errors.New(`mcp: tools/call: "unknown tool: sessions" (code -32601)`)

// TestSessionsFallbackRendersSingleSessionWithNote is at#18's core fix: a
// NewMultiSession Model built with a fallback Fetcher (cmd/agent-tui always
// wires one in) must not go blank when the sessions fetch fails -- it must
// fall back to that one session's own lanes AND say, visibly, that it is
// doing so. This is the exact scenario the review reproduced against a real,
// un-patched agent-supervisor checkout ("lanes / ! unavailable / mcp:
// tools/call: \"unknown...").
func TestSessionsFallbackRendersSingleSessionWithNote(t *testing.T) {
	fallbackLanes := []lane.Lane{{Window: 1, WindowID: "@0", Name: "solo-lane", Command: "claude", State: "busy"}}
	m := NewMultiSession(
		func() ([]lane.Session, error) { return nil, errSessionsToolUnavailable },
		func() ([]lane.Lane, error) { return fallbackLanes, nil },
		nil, "director",
	)
	m.width = RailWidth + 8 // wide enough that the note isn't truncated mid-word

	model, cmd := m.Update(sessionsFetchResultMsg{err: errSessionsToolUnavailable})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("a failed sessions fetch with a fallback Fetcher wired must issue the fallback fetch, got a nil command")
	}
	model, _ = m.Update(cmd())
	m = model.(Model)

	out := m.View()
	if !strings.Contains(out, "solo-lane") {
		t.Errorf("fallback did not render the single session's lane:\n%s", out)
	}
	// agent-tui#55: this used to assert "agent-supervisor#158" -- as#158
	// merged 2026-08-15, and a hardcoded reference to a closed issue is
	// exactly the stale-attribution defect #55 exists to fix. What must
	// stay true is that a genuinely unavailable tool (errSessionsToolUnavailable
	// above, a JSON-RPC "unknown tool" error, not a timeout) reads that way,
	// checked at runtime -- see isTimeoutErr/renderFallbackNote.
	if !strings.Contains(out, "sessions tool unavailable") {
		t.Errorf("fallback rendered with no visible, runtime-checked note that the sessions tool is unavailable:\n%s", out)
	}
	if strings.Contains(out, "agent-supervisor#158") || strings.Contains(out, "timed out") {
		t.Errorf("a tool-unavailable error must not read as a live blocker or a timeout:\n%s", out)
	}
	// Never silently blank: the rail must never look like a healthy, quiet
	// estate when it is actually degraded.
	if strings.Contains(out, "(no lanes)") {
		t.Errorf("fallback rendered as an empty estate instead of the real fallback data:\n%s", out)
	}
}

// fakeTimeoutErr implements the same Timeout() bool convention
// internal/mcp's real timeout error does (net.Error-style) without this
// test importing internal/mcp -- this package classifies by interface, not
// by importing MCP's own error type, so a fake proves the classification
// itself rather than one concrete implementation of it.
type fakeTimeoutErr struct{ msg string }

func (e fakeTimeoutErr) Error() string { return e.msg }
func (e fakeTimeoutErr) Timeout() bool { return true }

// TestSessionsFallbackDistinguishesTimeoutFromUnavailable is agent-tui#55's
// second required fix: "call timed out / no reply" and "tool not available"
// have different causes and must render different messages, not the one
// blanket note that made a live concurrency bug read as a stale
// supervisor-side gap.
func TestSessionsFallbackDistinguishesTimeoutFromUnavailable(t *testing.T) {
	timeoutErr := fakeTimeoutErr{msg: "mcp: tools/call: no reply within 10s"}
	fallbackLanes := []lane.Lane{{Window: 1, WindowID: "@0", Name: "solo-lane", Command: "claude", State: "busy"}}
	m := NewMultiSession(
		func() ([]lane.Session, error) { return nil, timeoutErr },
		func() ([]lane.Lane, error) { return fallbackLanes, nil },
		nil, "director",
	)
	m.width = RailWidth + 8

	model, cmd := m.Update(sessionsFetchResultMsg{err: timeoutErr})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("a failed sessions fetch with a fallback Fetcher wired must issue the fallback fetch, got a nil command")
	}
	model, _ = m.Update(cmd())
	m = model.(Model)

	out := m.View()
	if !strings.Contains(out, "sessions call timed out") {
		t.Errorf("a timeout error did not render as a timeout:\n%s", out)
	}
	if strings.Contains(out, "sessions tool unavailable") || strings.Contains(out, "agent-supervisor#158") {
		t.Errorf("a timeout must not read as a missing tool or name a stale issue:\n%s", out)
	}
}

// TestSessionsFetchErrorWithNoFallbackStillShowsUnavailable is at#13's
// original behavior, unchanged: a Model built with lanesFetch == nil (no
// pre-#18 call site passes one) must render exactly what
// TestSessionsFetchErrorIsShownNeverBlank above already checks -- this test
// additionally checks that NO fallback single-session data appears, since
// there is none to show.
func TestSessionsFetchErrorWithNoFallbackStillShowsUnavailable(t *testing.T) {
	m := NewMultiSession(func() ([]lane.Session, error) { return nil, errSessionsToolUnavailable }, nil, nil, "director")
	model, cmd := m.Update(sessionsFetchResultMsg{err: errSessionsToolUnavailable})
	m = model.(Model)
	if cmd != nil {
		t.Errorf("a Model with no fallback Fetcher must not issue a fallback fetch command, got %T", cmd())
	}
	out := m.View()
	if !strings.Contains(out, "unavailable") {
		t.Errorf("a failed sessions fetch with no fallback did not render as unavailable:\n%s", out)
	}
}

// TestUnsupervisedAccentWinsOverDirectorAccent is agent-tui#18's mutation-
// checked fix for the review's own finding: renderSessionHeader documents,
// twice, that the unsupervised-amber accent must beat the director-gold
// accent when a session is both -- "safety over decoration" -- but nothing
// asserted it. Swapping headerAccent's case order (director checked first)
// makes this fail red; see the PR/commit description for the actual
// go test output from that mutation.
func TestUnsupervisedAccentWinsOverDirectorAccent(t *testing.T) {
	st := Model{theme: theme.Default}.styles()

	accent, ok := headerAccent(false /* unsupervised */, true /* also the director session */, st)
	if !ok {
		t.Fatalf("headerAccent(unsupervised, director) returned no accent at all")
	}
	if accent != st.unsupervisedAccent {
		t.Errorf("director accent must not win over unsupervised for a session that is both: got %v, want unsupervisedAccent %v", accent, st.unsupervisedAccent)
	}

	// The two single-condition cases, so a future edit can't satisfy the
	// combined case above by making both conditions always return the same
	// accent.
	if accent, ok := headerAccent(true, true, st); !ok || accent != st.directorAccent {
		t.Errorf("a supervised director session should get directorAccent, got %v, ok=%v", accent, ok)
	}
	if accent, ok := headerAccent(false, false, st); !ok || accent != st.unsupervisedAccent {
		t.Errorf("an unsupervised non-director session should get unsupervisedAccent, got %v, ok=%v", accent, ok)
	}
	if _, ok := headerAccent(true, false, st); ok {
		t.Errorf("a supervised non-director session should get no accent at all")
	}
}

func TestNewMultiSessionInitDoesNotCallANilFetch(t *testing.T) {
	// A regression here is a nil-pointer panic inside Init()'s tea.Cmd,
	// which only surfaces when the returned command actually runs -- so run
	// it, don't just call Init() and ignore the result.
	m := NewMultiSession(func() ([]lane.Session, error) { return threeSessions(), nil }, nil, nil, "director")
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned a nil command")
	}
	msg := cmd()
	if _, ok := msg.(tea.BatchMsg); !ok {
		t.Fatalf("Init() command did not return a batch, got %T", msg)
	}
	batch := msg.(tea.BatchMsg)
	for _, c := range batch {
		_ = c() // must not panic on a nil m.fetch
	}
}
