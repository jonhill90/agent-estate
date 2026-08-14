package board

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// View renders one board layout as plain text. Layout, grouping and
// density are exactly the kind of question agent-supervisor#107's addendum
// (and agent-tui#6, quoting it) says ships as variants rather than a
// question in prose -- ByColumn and ByRepo below are both real renderers
// over the SAME Cards/WIP, and Views lists them for a picker identical in
// spirit to the rail's glyph-set picker (internal/rail/model.go).
type View struct {
	ID          string
	Name        string
	Description string
	Render      func(cards []Card, wip []WIP, width int) string
}

// Views is every board layout this drop ships, in picker order. Adding a
// candidate or removing one Jon dislikes is a one-line change to this
// slice, matching internal/lane/variants.go's own convention.
var Views = []View{
	{ID: "by-column", Name: "by column", Description: "columns first, repo tag per card -- best for one WIP-wide question at a time", Render: RenderByColumn},
	{ID: "by-repo", Name: "by repo", Description: "repo first, columns within it -- best for 'what does agent-tui look like right now'", Render: RenderByRepo},
}

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

// RenderByColumn groups cards under each Column, in board order, with a
// repo tag on every line -- the one-screen answer to "what needs review
// right now, across every repo".
func RenderByColumn(cards []Card, wip []WIP, width int) string {
	var b strings.Builder
	writeWIP(&b, wip, width)

	for _, col := range Columns {
		var inCol []Card
		for _, c := range cards {
			if c.Column == col {
				inCol = append(inCol, c)
			}
		}
		fmt.Fprintf(&b, "\n%s (%d)\n", strings.ToUpper(string(col)), len(inCol))
		if len(inCol) == 0 {
			b.WriteString("  (empty)\n")
			continue
		}
		sort.SliceStable(inCol, func(i, j int) bool { return inCol[i].Age > inCol[j].Age })
		for _, c := range inCol {
			b.WriteString("  " + cardLine(c, width-2) + "\n")
			if c.Column == Blocked && c.BlockedReason != "" {
				b.WriteString("    -> " + c.BlockedReason + "\n")
			}
		}
	}
	return b.String()
}

// RenderByRepo groups cards by repo first, columns within -- the "what
// does this one repo look like" answer.
func RenderByRepo(cards []Card, wip []WIP, width int) string {
	var b strings.Builder
	writeWIP(&b, wip, width)

	var repos []string
	seen := map[string]bool{}
	for _, c := range cards {
		id := c.Repo.GitHubID()
		if !seen[id] {
			seen[id] = true
			repos = append(repos, id)
		}
	}
	sort.Strings(repos)

	for _, repoID := range repos {
		fmt.Fprintf(&b, "\n== %s ==\n", repoID)
		for _, col := range Columns {
			var inCol []Card
			for _, c := range cards {
				if c.Repo.GitHubID() == repoID && c.Column == col {
					inCol = append(inCol, c)
				}
			}
			if len(inCol) == 0 {
				continue
			}
			fmt.Fprintf(&b, "  %s (%d)\n", col, len(inCol))
			sort.SliceStable(inCol, func(i, j int) bool { return inCol[i].Age > inCol[j].Age })
			for _, c := range inCol {
				b.WriteString("    " + cardLine(c, width-4) + "\n")
				if c.Column == Blocked && c.BlockedReason != "" {
					b.WriteString("      -> " + c.BlockedReason + "\n")
				}
			}
		}
	}
	return b.String()
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
