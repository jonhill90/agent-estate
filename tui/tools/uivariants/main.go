// Command uivariants is a THROWAWAY rendering harness for agent-tui#62.
// It exists to answer two QA questions Jon raised about the rail and the
// board without asking him to attach to anything: "too dense" (rail) and
// "structure right, looks wrong" (board). It renders three genuinely
// different fake-data variants of each, printed as real lipgloss/Bubble
// Tea output -- same renderer, same font, same colour depth as the shipped
// app -- so the images scripts/render-uivariants.sh produces from it show
// exactly what a terminal would show, no HTML-mock polish the terminal
// could never render.
//
// This follows internal/gallery's own pattern (--gallery renders every
// lane state against every glyph set with no supervisor connection)
// rather than inventing a new harness: hardcoded fake state, no MCP
// client, no ledger, no gh, no tea.NewProgram, no live wiring. It is NOT
// part of the shipped app -- nothing under cmd/ or internal/ imports this
// package, and it must never be wired to the real fetch/session/ledger
// seams. Delete this directory once Jon has picked a variant; whatever he
// picks gets rebuilt as a real Layout/rail change with live data, not by
// promoting this file.
//
// Usage: go run ./tools/uivariants <variant-id>, where variant-id is one
// of the names variants() below lists. Prints that one variant's rendered
// frame to stdout. See scripts/render-uivariants.sh for how the six
// images under docs/uivariants/ were produced (each a `freeze --execute`
// capture of one invocation).
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-estate/tui/internal/board"
	"github.com/jonhill90/agent-estate/tui/internal/lane"
	"github.com/jonhill90/agent-estate/tui/internal/theme"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: uivariants <variant-id>")
		fmt.Fprintln(os.Stderr, "variants:")
		for _, v := range variantList {
			fmt.Fprintf(os.Stderr, "  %-20s %s\n", v.id, v.implies)
		}
		os.Exit(1)
	}
	id := os.Args[1]
	for _, v := range variantList {
		if v.id == id {
			fmt.Print(v.render())
			return
		}
	}
	fmt.Fprintf(os.Stderr, "unknown variant %q\n", id)
	os.Exit(1)
}

type variant struct {
	id      string
	implies string // the one-line-on-implication the brief asks for
	render  func() string
}

var variantList = []variant{
	{"rail-thin", railThinImplies, func() string { return railThin(fakeSessions(), theme.Default) }},
	{"rail-roomy", railRoomyImplies, func() string { return railRoomy(fakeSessions(), theme.Default) }},
	{"rail-collapsed", railCollapsedImplies, func() string { return railCollapsed(fakeSessions(), theme.Default) }},
	{"board-boxed-rules", boardBoxedRulesImplies, func() string { return boardBoxedRules(fakeCards(), fakeWIP(), theme.Default) }},
	{"board-card-boxes", boardCardBoxesImplies, func() string { return boardCardBoxes(fakeCards(), fakeWIP(), theme.Default) }},
	{"board-chip", boardChipImplies, func() string { return boardChip(fakeCards(), fakeWIP(), theme.Default) }},
}

// -- fake data. Invented, not fetched -- see package doc comment. Picked to
// exercise a spread of states/columns/repos, not to model any real lane or
// issue.

func fakeSessions() []lane.Session {
	return []lane.Session{
		{Name: "director", Supervised: true, Lanes: []lane.Lane{
			{Name: "director:0", State: "busy", IdleSeconds: 8},
		}},
		{Name: "worker-1", Supervised: true, Lanes: []lane.Lane{
			{Name: "worker-1:0", State: "busy", IdleSeconds: 42},
			{Name: "worker-1:1", State: "text-blocked", IdleSeconds: 610},
			{Name: "worker-1:2", State: "free", IdleSeconds: 5},
		}},
		{Name: "worker-2", Supervised: true, Lanes: []lane.Lane{
			{Name: "worker-2:0", State: "hung", IdleSeconds: 1800},
			{Name: "worker-2:1", State: "stale", IdleSeconds: 5400},
		}},
		{Name: "worker-3", Supervised: false, Lanes: []lane.Lane{
			{Name: "worker-3:0", State: "unsent", IdleSeconds: 120},
		}},
	}
}

func fakeCards() []board.Card {
	tui := board.Repo{Label: "agent-tui", Owner: "jonhill90", Name: "agent-tui"}
	sup := board.Repo{Label: "agent-supervisor", Owner: "jonhill90", Name: "agent-supervisor"}
	return []board.Card{
		{Repo: tui, Number: 62, Title: "Three fake-data variants each of the rail and the board", Column: board.InProgress, Session: "worker-1", Age: 40 * time.Minute},
		{Repo: tui, Number: 42, Title: "Naming shortlist -- steading vs loom, nothing picked yet", Column: board.InProgress, Session: "director", Age: 3 * time.Hour},
		{Repo: tui, Number: 51, Title: "shell.Model owns the theme value, persists via theme.Save", Column: board.InReview, PRNumber: 59, Age: 20 * time.Minute},
		{Repo: tui, Number: 49, Title: "Degrade instead of fail closed on bare launch, dead board, quota", Column: board.Blocked, BlockedReason: "lane worker-2:0 is hung", Session: "worker-2", Age: 5 * time.Hour},
		{Repo: tui, Number: 55, Title: "Rail's self-inflicted MCP request pile-up", Column: board.Done, Age: 3 * 24 * time.Hour},
		{Repo: sup, Number: 158, Title: "sessions tool: fan out to lanes.sh per tmux session", Column: board.Backlog, Age: 2 * 24 * time.Hour},
		{Repo: sup, Number: 189, Title: "client-identity fix for switch-client/detach-client", Column: board.Backlog, Age: 6 * 24 * time.Hour},
	}
}

func fakeWIP() []board.WIP {
	return []board.WIP{
		{Session: "worker-1", InProgress: 1, Capacity: 2},
		{Session: "worker-2", InProgress: 1, Capacity: 1, OverCapacity: false},
	}
}

// glyphSet is fixed to lane.Default (the "signal" set, index 0) for every
// rail variant -- these images are about density/structure, not a second
// glyph-set comparison (that is what --gallery is for).
var glyphSet = lane.Default

const railWidth = 32

// -- rail variants: the axis is density (Jon: "too dense" -- Q1=2). --

const railThinImplies = "removes fields -- fastest scan, but a lane's WHY needs a keypress, not a glance"

// railThin drops everything but glyph + a short per-lane label: no state
// word, no idle age, no picker legends, no cost line. This is the "remove
// information" reading of Q1 -- literally fewer fields per lane, not the
// same fields drawn smaller.
func railThin(sessions []lane.Session, th theme.Theme) string {
	title := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	dim := lipgloss.NewStyle().Faint(true).Padding(0, 1)
	var b []string
	b = append(b, title.Render("lanes"))
	for _, s := range sessions {
		b = append(b, dim.Render(s.Name))
		for _, l := range s.Lanes {
			style := lane.StyleFor(glyphSet, l.State)
			g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Render(lane.Frame(glyphSet, l.State, 0))
			b = append(b, fmt.Sprintf("  %s  %s", g, laneShortLabel(l.Name)))
		}
	}
	content := lipgloss.JoinVertical(lipgloss.Left, b...)
	return lipgloss.NewStyle().
		Border(th.Border, false, true, false, false).BorderForeground(th.Color(theme.RoleBorder)).
		Width(railWidth).Render(content)
}

const railRoomyImplies = "keeps every field, spends whitespace instead of borders -- same knowledge, fewer lanes fit without scrolling"

// railRoomy keeps every field the shipped rail shows (glyph, name, state
// word, idle age) but removes the row-to-row density: a blank line between
// sessions, no rule between rows, wider left padding. This is the
// "keep the information but space it" reading of Q1.
func railRoomy(sessions []lane.Session, th theme.Theme) string {
	title := lipgloss.NewStyle().Bold(true).Padding(0, 2)
	head := lipgloss.NewStyle().Bold(true).Faint(true).Padding(0, 2)
	dim := lipgloss.NewStyle().Faint(true).Padding(0, 2)
	var b []string
	b = append(b, title.Render("lanes"), "")
	for _, s := range sessions {
		label := s.Name
		if !s.Supervised {
			label += "  (unsupervised)"
		}
		b = append(b, head.Render(label))
		for _, l := range s.Lanes {
			style := lane.StyleFor(glyphSet, l.State)
			g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Render(lane.Frame(glyphSet, l.State, 0))
			row := lipgloss.NewStyle().Padding(0, 2).Render(fmt.Sprintf("%s  %s", g, laneShortLabel(l.Name)))
			b = append(b, row)
			b = append(b, dim.Render(fmt.Sprintf("state: %-13s idle: %s", style.Label, formatIdle(l.IdleSeconds))))
		}
		b = append(b, "")
	}
	content := lipgloss.JoinVertical(lipgloss.Left, b...)
	return lipgloss.NewStyle().Width(railWidth + 6).Render(content)
}

const railCollapsedImplies = "collapsed sessions by default, expand one to see its lanes -- scales to many sessions, but a lane's real state is one keypress deep"

// railCollapsed is progressive disclosure: every session renders as one
// rolled-up line (its own worst state's glyph, plus a count) until
// expanded. Since a single static frame can't show a keypress, this
// prints two frames stacked with a divider -- fully collapsed, then
// worker-2 expanded -- so both ends of the interaction are visible in one
// image. This is the "expandable sessions" reading of Q1.
func railCollapsed(sessions []lane.Session, th theme.Theme) string {
	title := lipgloss.NewStyle().Bold(true).Padding(0, 1)
	dim := lipgloss.NewStyle().Faint(true).Padding(0, 1)
	frame := func(expand string) string {
		var b []string
		b = append(b, title.Render("lanes"))
		for _, s := range sessions {
			worst := worstState(s.Lanes)
			style := lane.StyleFor(glyphSet, worst)
			g := lipgloss.NewStyle().Foreground(lipgloss.Color(style.Color)).Render(lane.Frame(glyphSet, worst, 0))
			chevron := "▸"
			if s.Name == expand {
				chevron = "▾"
			}
			b = append(b, fmt.Sprintf(" %s %s %-10s %s  (%d)", chevron, g, s.Name, style.Label, len(s.Lanes)))
			if s.Name == expand {
				for _, l := range s.Lanes {
					lstyle := lane.StyleFor(glyphSet, l.State)
					lg := lipgloss.NewStyle().Foreground(lipgloss.Color(lstyle.Color)).Render(lane.Frame(glyphSet, l.State, 0))
					b = append(b, fmt.Sprintf("     %s %-2s %-13s idle %s", lg, laneShortLabel(l.Name), lstyle.Label, formatIdle(l.IdleSeconds)))
				}
			}
		}
		content := lipgloss.JoinVertical(lipgloss.Left, b...)
		return lipgloss.NewStyle().
			Border(th.Border, false, true, false, false).BorderForeground(th.Color(theme.RoleBorder)).
			Width(railWidth + 20).Render(content)
	}
	var out strings.Builder
	out.WriteString(dim.Render("-- collapsed (default) --") + "\n")
	out.WriteString(frame("") + "\n\n")
	out.WriteString(dim.Render("-- worker-2 expanded (enter/space on it) --") + "\n")
	out.WriteString(frame("worker-2") + "\n")
	return out.String()
}

// worstState picks the most-attention-needing state among a session's
// lanes, so the collapsed line shows the ONE thing worth knowing without
// expanding -- hung/broken/dead outrank busy, which outranks free. Fake
// severity order, good enough for a demo image; a real implementation
// would need Jon's actual ranking, not this one.
func worstState(lanes []lane.Lane) string {
	rank := map[string]int{
		"hung": 0, "broken": 0, "dead": 0,
		"stale": 1, "menu-blocked": 1, "text-blocked": 1, "unsent": 1, "never-busy": 1,
		"busy": 2, "scrolled": 2, "service": 2, "supervisor": 2,
		"free": 3, "unknown": 3,
	}
	best := "free"
	bestRank := 99
	for _, l := range lanes {
		r, ok := rank[l.State]
		if !ok {
			r = 3
		}
		if r < bestRank {
			bestRank, best = r, l.State
		}
	}
	return best
}

func laneShortLabel(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}

func formatIdle(sec int) string {
	d := time.Duration(sec) * time.Second
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", sec)
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

// -- board variants: the axis is decoration (Q2=2 -- structure right,
// looks wrong: colour per column, rounded cards, spacing). Grouping
// (by-column), the five Columns, and every Card field stay exactly what
// board.Layout already derives -- only how a card and a column boundary
// are drawn changes between these three.

var columnRole = map[board.Column]theme.Role{
	board.Backlog:    theme.RoleBacklog,
	board.InProgress: theme.RoleInProgress,
	board.InReview:   theme.RoleInReview,
	board.Blocked:    theme.RoleBlocked,
	board.Done:       theme.RoleDone,
}

const boardWidth = 150

const boardBoxedRulesImplies = "closest to what ships today -- a coloured rounded box per COLUMN, cards inside separated by a rule; still reads as five lists more than five stacks of cards"

// boardBoxedRules: one rounded, colour-per-column box (theme.Default's
// border is already RoundedBorder -- see internal/theme/registry.go), a
// thin rule between cards instead of blank space. This is the variant
// closest to today's shipped kanban-column Layout, kept as the anchor the
// other two are judged against.
func boardBoxedRules(cards []board.Card, wip []board.WIP, th theme.Theme) string {
	n := len(board.Columns)
	paneWidth := boardWidth / n
	panes := make([]string, 0, n)
	for _, col := range board.Columns {
		color := th.Color(columnRole[col])
		header := lipgloss.NewStyle().Bold(true).Foreground(color).Render(strings.ToUpper(string(col)))
		var body strings.Builder
		body.WriteString(header + "\n")
		inCol := cardsIn(cards, col)
		if len(inCol) == 0 {
			body.WriteString(lipgloss.NewStyle().Faint(true).Render("(empty)") + "\n")
		}
		for i, c := range inCol {
			if i > 0 {
				body.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3a3a")).Render(strings.Repeat("─", paneWidth-4)) + "\n")
			}
			body.WriteString(cardTextBlock(c, paneWidth-4, th))
		}
		content := strings.TrimRight(body.String(), "\n")
		panes = append(panes, lipgloss.NewStyle().
			Border(th.Border).BorderForeground(color).Padding(0, 1).Width(paneWidth-2).Render(content))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, panes...)
	return wipLine(wip) + "\n" + body
}

const boardCardBoxesImplies = "one rounded box PER CARD, colour-matched to its column -- reads instantly as a kanban card, costs vertical space so fewer cards fit per column at once"

// boardCardBoxes: no column border at all -- just a header word above a
// stack of individually-boxed cards, each card's own rounded border
// carrying the column's colour. This is the "rounded cards" reading of
// Q2 taken literally: the CARD is the boxed unit, not the column.
func boardCardBoxes(cards []board.Card, wip []board.WIP, th theme.Theme) string {
	n := len(board.Columns)
	paneWidth := boardWidth / n
	panes := make([]string, 0, n)
	for _, col := range board.Columns {
		color := th.Color(columnRole[col])
		header := lipgloss.NewStyle().Bold(true).Foreground(color).Padding(0, 1).Render(strings.ToUpper(string(col)) + fmt.Sprintf(" (%d)", len(cardsIn(cards, col))))
		var blocks []string
		blocks = append(blocks, header)
		for _, c := range cardsIn(cards, col) {
			inner := paneWidth - 6
			block := cardTextBlock(c, inner, th)
			blocks = append(blocks, lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).BorderForeground(color).
				Padding(0, 1).Width(paneWidth-4).Render(strings.TrimRight(block, "\n")))
		}
		if len(cardsIn(cards, col)) == 0 {
			blocks = append(blocks, lipgloss.NewStyle().Faint(true).Padding(0, 1).Render("(empty)"))
		}
		panes = append(panes, lipgloss.JoinVertical(lipgloss.Left, blocks...))
	}
	// Columns are independent-height stacks -- pad each to the tallest so
	// JoinHorizontal doesn't stairstep them. lipgloss.JoinHorizontal(Top,...)
	// already left-aligns tops, which is what a real kanban board does too
	// (columns don't have to end at the same row).
	body := lipgloss.JoinHorizontal(lipgloss.Top, panes...)
	return wipLine(wip) + "\n" + body
}

const boardChipImplies = "no border characters anywhere -- a coloured chip bar per card and blank-line spacing carry the whole board; quietest option, most cards visible at once, but nothing marks a column's edge except a header word"

// boardChip: whitespace-only columns (board.StyleWhitespace's axis), each
// card marked by a coloured vertical chip bar (▎) instead of any box.
// This is the lightest-ink reading of "colour per column, spacing" -- the
// opposite bet from boardCardBoxes: colour and gaps do the work borders
// did in the other two variants.
func boardChip(cards []board.Card, wip []board.WIP, th theme.Theme) string {
	n := len(board.Columns)
	paneWidth := boardWidth / n
	panes := make([]string, 0, n)
	for _, col := range board.Columns {
		color := th.Color(columnRole[col])
		var b []string
		b = append(b, lipgloss.NewStyle().Bold(true).Foreground(color).Render(strings.ToUpper(string(col))), "")
		inCol := cardsIn(cards, col)
		if len(inCol) == 0 {
			b = append(b, lipgloss.NewStyle().Faint(true).Render("(empty)"))
		}
		for _, c := range inCol {
			chip := lipgloss.NewStyle().Foreground(color).Render("▎")
			warn := c.Aged() || c.Column == board.Blocked
			titleStyle := lipgloss.NewStyle()
			if warn {
				titleStyle = titleStyle.Bold(true).Foreground(th.Color(theme.RoleWarn))
			}
			title := truncateStr(fmt.Sprintf("#%d %s", c.Number, c.Title), paneWidth-4)
			b = append(b, chip+" "+titleStyle.Render(title))
			meta := []string{c.Repo.Label, formatAge(c.Age)}
			if c.Session != "" {
				meta = append(meta, c.Session)
			}
			b = append(b, "  "+lipgloss.NewStyle().Faint(true).Render(strings.Join(meta, "  ")))
			if c.Column == board.Blocked && c.BlockedReason != "" {
				b = append(b, "  "+lipgloss.NewStyle().Foreground(th.Color(theme.RoleWarn)).Render("-> "+c.BlockedReason))
			}
			b = append(b, "")
		}
		panes = append(panes, lipgloss.NewStyle().Width(paneWidth).Render(lipgloss.JoinVertical(lipgloss.Left, b...)))
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, panes...)
	return wipLine(wip) + "\n" + body
}

func cardsIn(cards []board.Card, col board.Column) []board.Card {
	var out []board.Card
	for _, c := range cards {
		if c.Column == col {
			out = append(out, c)
		}
	}
	return out
}

// cardTextBlock is the two/three-line card body shared by boardBoxedRules
// and boardCardBoxes: title, then a metadata line, then a blocked-reason
// line if any -- the same shape board/layout.go's ShapeMultiLine already
// uses, reproduced here (not imported) since these are throwaway variants,
// not a caller of the shipped Layout renderer.
func cardTextBlock(c board.Card, width int, th theme.Theme) string {
	warn := c.Aged() || c.Column == board.Blocked
	style := lipgloss.NewStyle()
	if warn {
		style = style.Bold(true).Foreground(th.Color(theme.RoleWarn))
	}
	var b strings.Builder
	b.WriteString(style.Render(truncateStr(fmt.Sprintf("#%d %s", c.Number, c.Title), width)) + "\n")
	meta := []string{c.Repo.Label, formatAge(c.Age)}
	if c.Session != "" {
		meta = append(meta, c.Session)
	}
	b.WriteString(lipgloss.NewStyle().Faint(true).Render(truncateStr(strings.Join(meta, "  "), width)) + "\n")
	if c.Column == board.Blocked && c.BlockedReason != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(th.Color(theme.RoleWarn)).Render(truncateStr("-> "+c.BlockedReason, width)) + "\n")
	}
	return b.String()
}

func wipLine(wip []board.WIP) string {
	if len(wip) == 0 {
		return ""
	}
	parts := make([]string, 0, len(wip))
	for _, w := range wip {
		mark := ""
		if w.OverCapacity {
			mark = " OVER"
		}
		parts = append(parts, fmt.Sprintf("%s %d/%d%s", w.Session, w.InProgress, w.Capacity, mark))
	}
	return lipgloss.NewStyle().Faint(true).Render("WIP: " + strings.Join(parts, "  |  "))
}

func formatAge(d time.Duration) string {
	switch {
	case d <= 0:
		return "-"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func truncateStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}
