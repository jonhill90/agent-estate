// Plain-text rendering, no lipgloss -- the same split internal/gallery's
// view.go/model.go and internal/cost's view.go document for their own
// Render functions: testable without a styled terminal, with colour
// layered on afterward by model.go's Model.View(). Nothing here reads
// m.theme; nothing in model.go duplicates this package's text layout.
package chat

import (
	"fmt"
	"sort"
	"time"
)

// AggregateAll merges every thread's messages into one synthetic Thread,
// sorted chronologically and tagged with each message's source thread
// title (Message.From) -- layouts.go's collapsed option [3], "unified
// activity feed." Returns a Thread with ID "all" regardless of input;
// callers place it at index 0 of whatever list they render (model.go).
func AggregateAll(threads []Thread) Thread {
	var all []Message
	var last time.Time
	for _, t := range threads {
		for _, m := range t.Messages {
			m.From = t.Title
			all = append(all, m)
			if m.At.After(last) {
				last = m.At
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.Before(all[j].At) })

	unread := false
	for _, t := range threads {
		if t.Unread {
			unread = true
			break
		}
	}

	return Thread{
		ID:           "all",
		Title:        "All (unified)",
		Messages:     all,
		LastActivity: last,
		Unread:       unread,
	}
}

// RenderThreadRow is one row of a thread list -- listLayout's left column
// and gridLayout's peripheral strip both use it, so the two layouts agree
// on how a thread reads when it is not the one being read in full.
func RenderThreadRow(t Thread, now time.Time) string {
	mark := " "
	if t.Unread {
		mark = "*"
	}
	age := "--"
	if !t.LastActivity.IsZero() {
		age = now.Sub(t.LastActivity).Round(time.Second).String()
	}
	last := "(no messages)"
	if n := len(t.Messages); n > 0 {
		last = summarize(t.Messages[n-1])
	}
	lane := t.Lane
	if lane == "" {
		lane = "--"
	}
	return fmt.Sprintf("%s %-24s %-10s %6s  %s", mark, truncate(t.Title, 24), lane, age, last)
}

// summarize renders one Message as a single line, prefixed by kind --
// used by RenderThreadRow (last-line preview) and gridLayout's compact
// pane body. RenderMessage below is the fuller, possibly multi-line form
// listLayout's transcript pane uses for the thread actually being read.
func summarize(m Message) string {
	switch m.Kind {
	case KindUserText:
		return "you: " + truncate(m.Text, 60)
	case KindAgentText:
		return "agent: " + truncate(m.Text, 60)
	case KindThought:
		return "· " + truncate(m.Text, 60) // "· " -- thinking, distinct from output
	case KindToolCall:
		return fmt.Sprintf("[tool] %s — %s", m.ToolName, m.ToolStatus) // em dash
	case KindPlan:
		done := 0
		for _, s := range m.Plan {
			if s.Done {
				done++
			}
		}
		return fmt.Sprintf("[plan] %d/%d steps done", done, len(m.Plan))
	case KindPermission:
		return "⚠ permission: " + truncate(m.Text, 55) // ⚠
	default:
		return truncate(m.Text, 60)
	}
}

// RenderMessage renders one Message as the (possibly multi-line) block
// listLayout's transcript pane shows for the thread being read in full --
// the issue's own list of what a thread must not flatten to text: thoughts
// distinct from output, tool calls as a status block not raw text, plans
// as a checklist, permission requests visually urgent. Model.go colours
// these lines by Kind; this function only decides their text and shape.
func RenderMessage(m Message) []string {
	prefix := ""
	if m.From != "" {
		prefix = "[" + m.From + "] "
	}

	switch m.Kind {
	case KindUserText:
		return []string{prefix + "you: " + m.Text}
	case KindAgentText:
		return []string{prefix + "agent: " + m.Text}
	case KindThought:
		return []string{prefix + "· thinking: " + m.Text}
	case KindToolCall:
		return []string{fmt.Sprintf("%s[tool] %s — %s", prefix, m.ToolName, m.ToolStatus)}
	case KindPlan:
		lines := []string{prefix + "[plan]"}
		for _, s := range m.Plan {
			box := "[ ]"
			if s.Done {
				box = "[x]"
			}
			lines = append(lines, "    "+box+" "+s.Text)
		}
		return lines
	case KindPermission:
		return []string{prefix + "⚠ permission needed: " + m.Text}
	default:
		return []string{prefix + m.Text}
	}
}

// RenderTranscript flattens every Message in msgs through RenderMessage,
// in order -- the shape both the list-layout transcript pane and the
// grid-layout focused pane feed into a viewport.Model (model.go). Kept
// here, not inline in model.go, so render_test.go can assert the full
// transcript's line count/shape without constructing a Bubble Tea model.
func RenderTranscript(msgs []Message) []string {
	var lines []string
	for _, msg := range msgs {
		lines = append(lines, RenderMessage(msg)...)
	}
	return lines
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
