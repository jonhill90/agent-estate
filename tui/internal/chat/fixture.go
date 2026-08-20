package chat

import (
	"strconv"
	"time"
)

// FixtureSource is the only Source this drop ships. It is NOT a stand-in
// for a live ACP transport dressed up to look real -- every field below is
// deliberately, visibly synthetic (see the sample data's own text) so
// nothing can mistake a fixture render for a live one. It exists to prove
// the seam and every layout against the full shape session/update can
// produce (user/agent text, a thought, a running and a failed tool call, a
// plan, a pending permission request, and a fourth thread long enough to
// force scrolling) -- something no lane in this estate can hand back
// today.
//
// Why not a live Source yet: agent-supervisor's MCP surface -- "lanes",
// "sessions", the five internal/session write tools -- has no tool that
// returns message content of any kind, only lane state
// (window/name/command/state/idle_seconds). A screen-scraped transcript
// (reading a pane's text) was considered and rejected for the same reason
// the issue itself gives for routing a lane onto a structured transport
// first: send-keys panes are free-text terminal output with no message
// boundaries, kinds, or tool-call structure to recover, so a scraper
// produces exactly the "screen-scraper wearing a chat interface" the issue
// warns against, not this package's Message shape. A real Source needs
// one of: (a) a lane whose transport is 'acp' or 'pi-rpc' (today: zero of
// 16, per the issue) plus a client that speaks session/new+session/update
// over that lane's stdio, wired the same way internal/mcp.Client wires
// MCP; or (b) a new agent-supervisor MCP tool that surfaces recorded
// session/update events for a lane already on a structured transport.
// Either way, the new type is a second implementation of the Source
// interface above -- nothing in model.go, render.go or layouts.go changes.
type FixtureSource struct{}

// NewFixtureSource builds a FixtureSource. Takes no arguments today -- kept
// as a constructor rather than a bare struct literal so a future fixture
// (e.g. seeded, or sized differently) is a signature change here, not a
// call-site change everywhere New is used.
func NewFixtureSource() FixtureSource { return FixtureSource{} }

// Threads returns the same four threads every call -- deterministic by
// design (fixture.go's own point is a stable target for both layouts and
// their tests, not a moving one). Timestamps are relative to a fixed
// instant rather than time.Now() so a render captured today reads the same
// tomorrow. lane-d carries enough messages to overflow any realistic
// terminal height on its own -- the fixture this repo's own #29 regression
// (a board that could not scroll) says every scrollable view needs: a
// fixture that actually forces the scroll path, not one that happens to
// fit.
func (FixtureSource) Threads() ([]Thread, error) {
	base := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }

	threads := []Thread{
		{
			ID:    "lane-a",
			Title: "lane/20-chat-threads",
			Lane:  "director:1",
			Messages: []Message{
				{ID: "a1", Kind: KindUserText, At: at(0), Text: "read agentclientprotocol.com and confirm the agent-to-agent claim before anyone builds"},
				{ID: "a2", Kind: KindThought, At: at(2), Text: "the overview page is the primary source; the issue's summary is a starting point, not the citation"},
				{ID: "a3", Kind: KindToolCall, At: at(4), ToolName: "WebFetch(agentclientprotocol.com/protocol/overview)", ToolStatus: ToolDone},
				{ID: "a4", Kind: KindPlan, At: at(6), Plan: []PlanStep{
					{Text: "confirm ACP has no agent-to-agent channel", Done: true},
					{Text: "narrow four layouts to the ones that differ", Done: true},
					{Text: "ship the seam and one layout", Done: false},
				}},
				{ID: "a5", Kind: KindAgentText, At: at(8), Text: "confirmed -- client<->agent only, cited in thread.go's package doc. building the seam now."},
			},
			LastActivity: at(8),
			Unread:       true,
		},
		{
			ID:    "lane-b",
			Title: "lane/14-session-write",
			Lane:  "worker:3",
			Messages: []Message{
				{ID: "b1", Kind: KindUserText, At: at(1), Text: "wire session_remove_check into the rail before session_remove"},
				{ID: "b2", Kind: KindToolCall, At: at(3), ToolName: "session_remove_check(lane/9-old)", ToolStatus: ToolRunning},
				{ID: "b3", Kind: KindPermission, At: at(5), Text: "session_remove wants to kill tmux session lane/9-old -- worktree has unpushed commits"},
			},
			LastActivity: at(5),
			Unread:       true,
		},
		{
			ID:    "lane-c",
			Title: "lane/6-cost-panel",
			Lane:  "worker:5",
			Messages: []Message{
				{ID: "c1", Kind: KindUserText, At: at(0), Text: "ccusage blocks --token-limit needs a real cap or 'unknown', never a fabricated percentage"},
				{ID: "c2", Kind: KindToolCall, At: at(2), ToolName: "Bash(npx ccusage blocks --json)", ToolStatus: ToolFailed},
				{ID: "c3", Kind: KindAgentText, At: at(4), Text: "npx ccusage isn't on PATH in this sandbox -- rendering 'unknown' per the honesty rule, not 0."},
			},
			LastActivity: at(4),
			Unread:       false,
		},
		{
			ID:           "lane-d",
			Title:        "lane/29-board-scroll",
			Lane:         "worker:7",
			Messages:     longThread(at),
			LastActivity: at(19 * 2),
			Unread:       false,
		},
	}
	return threads, nil
}

// longThread manufactures 20 messages -- comfortably more than any
// realistic terminal (even a large one) shows at once -- so a Model built
// against this fixture always has at least one thread whose transcript
// overflows its pane. Verification (README, PR description) runs against
// exactly this thread.
func longThread(at func(int) time.Time) []Message {
	var out []Message
	for i := 0; i < 20; i++ {
		kind := KindAgentText
		text := "step"
		switch i % 4 {
		case 0:
			kind, text = KindUserText, "keep going"
		case 1:
			kind, text = KindThought, "checking the next file before touching it"
		case 2:
			kind, text = KindToolCall, ""
		}
		msg := Message{ID: strconv.Itoa(i), Kind: kind, At: at(i * 2)}
		switch kind {
		case KindToolCall:
			msg.ToolName = "Read(file_" + strconv.Itoa(i) + ".go)"
			msg.ToolStatus = ToolDone
		default:
			msg.Text = text + " " + strconv.Itoa(i)
		}
		out = append(out, msg)
	}
	return out
}

var _ Source = FixtureSource{}
