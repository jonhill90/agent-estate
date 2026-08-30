// Package lanechat is agent-tui#115's shared ground for the "combine Lanes
// and Chat" variants (laneprimary, roomprimary, unifiedlist -- one
// subpackage each). The issue is explicit that the DIRECTION is settled
// (lanes and chat should become one surface) but the SHAPE is not, and asks
// for several real, working screens instead of a prose pick -- the same
// decide-by-variant discipline internal/chat/layouts.go already documents
// for two chat layouts, applied here a level up, across THREE structurally
// different renderers rather than two readings of one.
//
// This file is the one fixture every variant renders against -- FixtureLanes,
// FixtureThreads and FixtureParticipants, built once so a human comparing
// the three screenshots is comparing the SAME underlying agents and
// conversations laid out three different ways, never three different
// datasets that happen to differ in shape too. Every name is prefixed
// "fixture-" and every lane.Lane.Command carries "(fixture)" so nothing
// rendered here can be mistaken for a live lane or a live thread --
// agent-tui#20's Thread.Fixture convention (internal/chat/fixture.go)
// applied a second time, to lane.Lane data that field does not itself cover.
//
// No variant in this module talks to agent-supervisor's MCP surface, opens
// a real chat.Source, or calls a real chat.Sender -- every one of them is,
// like internal/gallery, a pane whose entire content is compiled into the
// binary. That is deliberate: the issue's own hard constraint is that fake
// data must be visibly fake, and the cheapest way to guarantee that is to
// give every variant no live seam to accidentally wire one day without
// changing this file.
package lanechat

import (
	"time"

	"github.com/jonhill90/agent-estate/tui/internal/chat"
	"github.com/jonhill90/agent-estate/tui/internal/lane"
)

// FixtureLanes returns the same five lanes every call -- deterministic by
// design, the same reason internal/chat.FixtureSource.Threads() never reads
// time.Now(). One state per lane spans the range a reader actually needs to
// judge a layout by eye: an active worker (busy), a lane that needs a human
// (hung), an idle one (free), a gone one (dead), and one waiting on a menu
// (menu-blocked) -- deliberately not all thirteen lane.AllStates entries
// (internal/gallery already owns "every state against every glyph set";
// this fixture owns "a believable small estate," the shape these three
// variants actually have to render).
func FixtureLanes() []lane.Lane {
	return []lane.Lane{
		{Window: 1, WindowID: "@101", Name: "fixture-atlas", Command: "claude (fixture)", State: "busy", IdleSeconds: 12, Model: "opus"},
		{Window: 2, WindowID: "@102", Name: "fixture-borealis", Command: "codex (fixture)", State: "hung", IdleSeconds: 930, Model: "unknown"},
		{Window: 3, WindowID: "@103", Name: "fixture-cascade", Command: "claude (fixture)", State: "free", IdleSeconds: 4, Model: "sonnet"},
		{Window: 4, WindowID: "@104", Name: "fixture-delta", Command: "claude (fixture)", State: "dead", IdleSeconds: 0, Model: "unknown"},
		{Window: 5, WindowID: "@105", Name: "fixture-ember", Command: "pi (fixture)", State: "menu-blocked", IdleSeconds: 300, Model: "unknown"},
	}
}

// FixtureThreads returns one chat.Thread per FixtureLanes entry, Thread.Lane
// set to the owning lane's own Name -- the 1:1 join every variant in this
// module keys its "which conversation belongs to which lane" logic on
// (roomprimary's own doc comment names this explicitly: it is the variant
// that makes that 1:1-ness the whole point). Every thread is tagged
// Fixture: true, chat.Thread's own "never let this render as though it
// were real" field (internal/chat/thread.go). fixture-borealis (the hung
// lane) carries the long thread -- deliberately, since a hung lane is
// exactly the one a human is most likely to open and scroll through
// looking for what it was doing when it stopped.
func FixtureThreads() []chat.Thread {
	base := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return base.Add(time.Duration(s) * time.Second) }

	threads := []chat.Thread{
		{
			ID:    "fixture-atlas",
			Title: "fixture-atlas",
			Lane:  "fixture-atlas",
			Messages: []chat.Message{
				{ID: "a1", Kind: chat.KindUserText, At: at(0), Text: "keep going on the render budget fix"},
				{ID: "a2", Kind: chat.KindToolCall, At: at(4), ToolName: "Read(view.go)", ToolStatus: chat.ToolDone},
				{ID: "a3", Kind: chat.KindAgentText, At: at(6), Text: "found the off-by-one, patching now"},
			},
			LastActivity: at(6),
			Unread:       true,
		},
		{
			ID:           "fixture-borealis",
			Title:        "fixture-borealis",
			Lane:         "fixture-borealis",
			Messages:     longFixtureThread(at),
			LastActivity: at(19 * 3),
			Unread:       true,
		},
		{
			ID:    "fixture-cascade",
			Title: "fixture-cascade",
			Lane:  "fixture-cascade",
			Messages: []chat.Message{
				{ID: "c1", Kind: chat.KindUserText, At: at(1), Text: "any update?"},
				{ID: "c2", Kind: chat.KindAgentText, At: at(3), Text: "idle -- last task finished and accepted, nothing queued"},
			},
			LastActivity: at(3),
			Unread:       false,
		},
		{
			ID:    "fixture-delta",
			Title: "fixture-delta",
			Lane:  "fixture-delta",
			Messages: []chat.Message{
				{ID: "d1", Kind: chat.KindUserText, At: at(0), Text: "status?"},
				{ID: "d2", Kind: chat.KindAgentText, At: at(2), Text: "last known message before the process died"},
			},
			LastActivity: at(2),
			Unread:       false,
		},
		{
			ID:    "fixture-ember",
			Title: "fixture-ember",
			Lane:  "fixture-ember",
			Messages: []chat.Message{
				{ID: "e1", Kind: chat.KindUserText, At: at(0), Text: "@fixture-atlas can you review this once you're free"},
				{ID: "e2", Kind: chat.KindPermission, At: at(5), Text: "waiting on a menu selection -- needs a human"},
			},
			LastActivity: at(5),
			Unread:       true,
		},
	}
	for i := range threads {
		threads[i].Fixture = true
	}
	return threads
}

// longFixtureThread manufactures enough messages to overflow any realistic
// terminal -- the same "force the scroll path, don't just fit" discipline
// internal/chat/fixture.go's own longThread documents (agent-tui#29's
// regression is what that rule exists to catch, and it applies here just
// as much as it did to internal/chat itself).
func longFixtureThread(at func(int) time.Time) []chat.Message {
	var out []chat.Message
	for i := 0; i < 18; i++ {
		kind, text := chat.KindAgentText, "still investigating the hang"
		switch i % 3 {
		case 0:
			kind, text = chat.KindUserText, "any progress?"
		case 1:
			kind, text = chat.KindThought, "checking whether the tool call ever returned"
		}
		out = append(out, chat.Message{ID: "b" + itoa(i), Kind: kind, At: at(i * 3), Text: text})
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}

// FixtureParticipants projects FixtureLanes into chat.Participant the exact
// same way cmd/estate/chat.go's buildParticipantsFetch projects real
// lane.Session data: Running is false only for "dead"/"stale" (the same
// evidence internal/agents' modeFor and buildParticipantsFetch both already
// use), true otherwise. Reused rather than re-derived so
// chat.ValidateMentions behaves identically here and against the real
// estate -- an @-mention refusal in one of these variants is proof the
// SAME gate agent-tui#114 built, not a second copy of it.
func FixtureParticipants() []chat.Participant {
	lanes := FixtureLanes()
	out := make([]chat.Participant, 0, len(lanes))
	for _, l := range lanes {
		out = append(out, chat.Participant{
			Name:    l.Name,
			Session: "fixture-session",
			Running: l.State != "dead" && l.State != "stale",
		})
	}
	return out
}
