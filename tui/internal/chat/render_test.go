package chat

import (
	"strings"
	"testing"
	"time"
)

func TestAggregateAllSortsChronologically(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	threads := []Thread{
		{Title: "b", Messages: []Message{{ID: "b1", Kind: KindAgentText, At: base.Add(2 * time.Second), Text: "second"}}},
		{Title: "a", Messages: []Message{{ID: "a1", Kind: KindUserText, At: base, Text: "first"}}},
	}
	all := AggregateAll(threads)
	if all.ID != "all" {
		t.Fatalf("ID = %q, want \"all\"", all.ID)
	}
	if len(all.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(all.Messages))
	}
	if all.Messages[0].From != "a" || all.Messages[1].From != "b" {
		t.Errorf("messages not sorted chronologically: %+v", all.Messages)
	}
}

func TestAggregateAllUnreadIfAnySourceUnread(t *testing.T) {
	threads := []Thread{{Title: "a", Unread: false}, {Title: "b", Unread: true}}
	if !AggregateAll(threads).Unread {
		t.Error("Unread = false, want true when any source thread is unread")
	}
}

func TestRenderMessageEveryKind(t *testing.T) {
	cases := []struct {
		name string
		msg  Message
		want string // substring
	}{
		{"user", Message{Kind: KindUserText, Text: "hi"}, "you: hi"},
		{"agent", Message{Kind: KindAgentText, Text: "hi"}, "agent: hi"},
		{"thought", Message{Kind: KindThought, Text: "hmm"}, "thinking: hmm"},
		{"tool", Message{Kind: KindToolCall, ToolName: "Read(x)", ToolStatus: ToolRunning}, "Read(x)"},
		{"plan", Message{Kind: KindPlan, Plan: []PlanStep{{Text: "step", Done: true}}}, "[x] step"},
		{"permission", Message{Kind: KindPermission, Text: "ok?"}, "permission needed: ok?"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			lines := RenderMessage(c.msg)
			if len(lines) == 0 {
				t.Fatal("RenderMessage returned no lines")
			}
			joined := strings.Join(lines, "\n")
			if !strings.Contains(joined, c.want) {
				t.Errorf("RenderMessage(%+v) = %q, want substring %q", c.msg, joined, c.want)
			}
		})
	}
}

func TestRenderMessagePlanChecklist(t *testing.T) {
	msg := Message{Kind: KindPlan, Plan: []PlanStep{
		{Text: "done step", Done: true},
		{Text: "todo step", Done: false},
	}}
	lines := RenderMessage(msg)
	if len(lines) != 3 { // header + 2 steps
		t.Fatalf("len(lines) = %d, want 3", len(lines))
	}
	if !strings.Contains(lines[1], "[x] done step") {
		t.Errorf("lines[1] = %q, want done checkbox", lines[1])
	}
	if !strings.Contains(lines[2], "[ ] todo step") {
		t.Errorf("lines[2] = %q, want open checkbox", lines[2])
	}
}

func TestRenderTranscriptFlattensInOrder(t *testing.T) {
	msgs := []Message{
		{Kind: KindUserText, Text: "one"},
		{Kind: KindPlan, Plan: []PlanStep{{Text: "step"}}},
		{Kind: KindAgentText, Text: "two"},
	}
	lines := RenderTranscript(msgs)
	// user (1) + plan (2: header + step) + agent (1) = 4
	if len(lines) != 4 {
		t.Fatalf("len(lines) = %d, want 4: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "one") || !strings.Contains(lines[len(lines)-1], "two") {
		t.Errorf("lines out of order: %v", lines)
	}
}

func TestRenderThreadRowShowsUnreadMark(t *testing.T) {
	row := RenderThreadRow(Thread{Title: "t", Unread: true}, time.Now())
	if !strings.HasPrefix(row, "*") {
		t.Errorf("row = %q, want leading unread marker", row)
	}
}
