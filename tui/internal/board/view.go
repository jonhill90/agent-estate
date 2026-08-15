package board

import (
	"fmt"
	"strings"
	"time"
)

// cardLine, formatAge and writeWIP are the low-level text primitives
// layout.go's Layouts build on -- ShapeSingleLine cards render through
// cardLine directly, every card shape uses formatAge, and every Layout
// opens with writeWIP. agent-tui#10 replaced this file's own by-column/
// by-repo picker (the two "Views" shipped in #6) with layout.go's wider
// Layouts picker, which supersedes it: #10's own text says to "keep and
// evolve what variant 2 [by-repo] was", and layout.go's GroupByRepo/
// renderSwimlanes is that evolution -- real boxed columns per repo,
// swimlaned, rather than a second parallel picker left with no caller.
func cardLine(c Card, width int) string {
	tag := "[" + c.Repo.Label + "]"
	if tag == "[]" {
		tag = "[" + c.Repo.Name + "]"
	}
	number := fmt.Sprintf("#%d", c.Number)
	age := formatAge(c.Age)
	marker := " "
	if c.Aged() {
		marker = "!"
	}

	// budget: marker(1) + space + tag + space + number + space + age(right-aligned-ish) + spaces
	fixed := len(marker) + 1 + len(tag) + 1 + len(number) + 1 + len(age) + 1
	titleWidth := width - fixed
	title := c.Title
	if titleWidth > 0 && len(title) > titleWidth {
		if titleWidth <= 1 {
			title = title[:titleWidth]
		} else {
			title = title[:titleWidth-1] + "…"
		}
	}

	line := fmt.Sprintf("%s %s %s %s %s", marker, tag, number, title, age)
	if len(line) > width && width > 0 {
		line = line[:width]
	}
	return line
}

func formatAge(d time.Duration) string {
	if d <= 0 {
		return "-"
	}
	total := int(d.Seconds())
	switch {
	case total < 3600:
		return fmt.Sprintf("%dm", total/60)
	case total < 86400:
		return fmt.Sprintf("%dh", total/3600)
	default:
		return fmt.Sprintf("%dd", total/86400)
	}
}

func writeWIP(b *strings.Builder, wip []WIP, width int) {
	if len(wip) == 0 {
		return
	}
	b.WriteString("WIP: ")
	parts := make([]string, 0, len(wip))
	for _, w := range wip {
		mark := ""
		if w.OverCapacity {
			mark = " OVER"
		}
		parts = append(parts, fmt.Sprintf("%s %d/%d%s", w.Session, w.InProgress, w.Capacity, mark))
	}
	b.WriteString(strings.Join(parts, "  |  "))
	b.WriteString("\n")
}
