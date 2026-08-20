// Package chat is agent-tui#20's answer to "TUI chat interface: threads as
// ACP sessions, live via session/update" -- a thread IS an ACP session
// (agentclientprotocol.com/protocol/overview: session/new, session/load,
// session/prompt; a live session streams session/update notifications
// carrying message chunks, thought content, tool calls and their updates,
// plans, available-command changes and mode changes) and this package
// renders that structure instead of flattening it to text, per the issue's
// own "what to render inside a thread" list.
//
// The one correction the issue makes before any of this gets built: ACP is
// client<->agent only. Nothing in agentclientprotocol.com/protocol/overview
// defines a channel, method, or capability for one agent to talk to
// another. "See the agents chatting with each other" can only honestly
// mean every agent's live stream rendered side by side (Source below, fed
// by many live sessions) or the estate's own dispatch/reply traffic
// rendered as a conversation -- never agents autonomously prompting each
// other, which nothing in this estate does by design.
//
// Source is the adapter seam every layout in layouts.go renders against.
// FixtureSource (fixture.go) is the only implementation shipped today --
// see its doc comment for exactly why. Nothing in model.go, render.go or
// layouts.go names FixtureSource; swapping in a live source once one
// exists is a constructor argument, the same seam internal/rail.Model's
// Fetcher already proves for lane data.
package chat

import "time"

// MessageKind is which session/update variant a Message came from. Kept
// distinct rather than flattened to a plain string of chat text -- the
// issue is explicit that thoughts, tool calls, plans and permission
// requests must render AS those things, not as prose.
type MessageKind string

const (
	KindUserText  MessageKind = "user_text"  // session/update: user message chunk
	KindAgentText MessageKind = "agent_text" // session/update: agent message chunk
	// KindThought is session/update's thought content -- "styled distinctly
	// from output" per the issue: "this is the interesting part to watch."
	KindThought MessageKind = "thought"
	// KindToolCall is a tool call and its updates, rendered as one
	// collapsible-shaped block with a status, never raw text (issue: "tool
	// calls as collapsible blocks with status, not raw text").
	KindToolCall MessageKind = "tool_call"
	// KindPlan is a plan update, rendered as a checklist (issue: "plans as
	// a checklist").
	KindPlan MessageKind = "plan"
	// KindPermission is session/request_permission -- visually urgent
	// because it blocks the lane it belongs to, and a blocked lane is
	// invisible today (the issue's own framing).
	KindPermission MessageKind = "permission_request"
)

// ToolCallStatus mirrors ACP's tool call status vocabulary closely enough
// to render (pending/in-progress vs. a terminal state), without importing
// ACP's own JSON shape -- this package has no ACP client dependency yet
// (see the package doc and fixture.go for why).
type ToolCallStatus string

const (
	ToolPending ToolCallStatus = "pending"
	ToolRunning ToolCallStatus = "running"
	ToolDone    ToolCallStatus = "done"
	ToolFailed  ToolCallStatus = "failed"
)

// PlanStep is one line of a KindPlan message's checklist.
type PlanStep struct {
	Text string
	Done bool
}

// Message is one session/update-shaped event inside a Thread. Which fields
// are meaningful depends on Kind -- Text carries user/agent/thought
// content, ToolName/ToolStatus carry a tool call, Plan carries a plan
// update. A Source is free to leave the others zero; render.go never reads
// a field Kind does not own.
type Message struct {
	ID   string
	Kind MessageKind
	At   time.Time

	Text string // KindUserText, KindAgentText, KindThought, KindPermission

	ToolName   string         // KindToolCall
	ToolStatus ToolCallStatus // KindToolCall

	Plan []PlanStep // KindPlan

	// From is the source thread's Title, set only on the copies
	// AggregateAll (render.go) builds for the synthetic "All" thread --
	// every real Source leaves it empty. It exists so the unified-feed
	// reading (layouts.go's listLayout doc comment, collapsed option [3])
	// can tag which lane a merged line came from without a second Message
	// type.
	From string
}

// Thread is one ACP session rendered as a conversation. ID is stable
// across a Source's snapshots (used for selection to survive a refetch);
// Lane names which tmux lane/session this session runs on, when a Source
// can say -- empty is a legitimate answer, not an error, for a Source that
// has no lane concept at all.
type Thread struct {
	ID       string
	Title    string
	Lane     string
	Messages []Message

	// LastActivity is the most recent Message.At -- a Source may report it
	// directly rather than requiring a caller to scan Messages.
	LastActivity time.Time

	// Unread is the issue's own navigation ask: "unread/activity markers
	// so a quiet lane that just moved is obvious." A Source (or the Model,
	// once a thread is viewed) owns clearing it; this package's Model
	// clears it on selection -- see model.go.
	Unread bool
}

// Source is the adapter seam: anything that can hand back a live snapshot
// of every thread this view should show. Threads is called on every
// refresh tick the same way internal/rail's Fetcher is -- a Source owns
// its own liveness, this package only owns rendering what it returns.
type Source interface {
	Threads() ([]Thread, error)
}
