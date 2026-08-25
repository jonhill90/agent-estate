// Layout is agent-tui#10's answer to "make it LOOK and BEHAVE like a
// board." view.go's Views (RenderByColumn/RenderByRepo) answer "which
// grouping" as plain text; Layout answers "what does it look like" --
// column style, density, card shape, colour theme, and the closed-items
// question Jon was asked once already (agent-tui#8) and must not be asked again.
// Every Layout is DATA (a struct literal in the Layouts slice below), never
// a new Render function hand-written per combination -- per agent-tui#10's own
// acceptance item 5, a layout set has to be data behind an interface, and
// this file's own layout_bench_test.go measures what it costs to prove
// that's true, not just claim it.
package board

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/jonhill90/agent-tui/internal/theme"
)

// ColumnStyle is how a column's boundary is drawn.
type ColumnStyle int

const (
	StyleBoxed      ColumnStyle = iota // full border around each column -- the "kanban board" look
	StyleRules                         // one vertical rule between columns, no full box
	StyleWhitespace                    // gutter spacing only, no border character at all
)

// Density controls vertical spacing between cards within a column.
type Density int

const (
	DensityCompact Density = iota // no blank line between cards
	DensityRoomy                  // one blank line between cards, padded column
)

// CardShape controls how much of a card's own metadata is visible.
type CardShape int

const (
	ShapeSingleLine CardShape = iota // marker+tag+number+title+age on one line (view.go's cardLine)
	ShapeMultiLine                   // title line, then a metadata line, then a blocked-reason line if any
)

// Theme controls colour. Restrained is one muted colour throughout;
// aged/blocked markers still warn in both themes -- theme is decoration,
// never the only signal for an ugly state (agent-tui#10: "every state renders,
// including the ugly ones").
type Theme int

const (
	ThemeRestrained Theme = iota
	ThemeVivid
)

// Grouping mirrors view.go's Views split: by-column across every repo, or
// by-repo with columns nested inside each (view.go's RenderByRepo,
// "evolved" here into real swimlanes -- see renderByRepo below).
type Grouping int

const (
	GroupByColumn Grouping = iota
	GroupByRepo
)

// ClosedFilter is agent-tui#8's still-open question ("should closed items show, and
// for how long?"), shipped as a variant per agent-tui#10's instruction rather than a
// second prompt.
type ClosedFilter int

const (
	ClosedHide   ClosedFilter = iota // Done column omitted from the render entirely
	ClosedRecent                     // Done cards shown only if Card.Age <= closedRecentWindow
	ClosedAll                        // every Done card, regardless of age
)

// closedRecentWindow is "last 24h" -- Card.Age for a Done card is
// now-CompletedAt (card.go), so this needs no wall-clock read of its own at
// render time; it is a pure function of the already-fetched Snapshot, same
// as every other Layout.Render below (agent-tui#10 item 4: "data fetching must stay
// off the render path").
const closedRecentWindow = 24 * 60 * 60 // seconds, compared against Card.Age.Seconds()

// Layout is one full board presentation: every axis agent-tui#10 asked for, bundled
// as one cycle-able unit, the same picker shape internal/rail's glyph sets
// and view.go's own Views already use. Adding or dropping a combination is
// a line in the Layouts slice below, not a new Render func.
type Layout struct {
	ID          string
	Name        string
	Description string
	Grouping    Grouping
	Column      ColumnStyle
	Density     Density
	Card        CardShape
	Theme       Theme
	Closed      ClosedFilter
}

// Layouts is every board presentation this drop ships, in picker order.
// Six, not two: "i like 2 more" is ambiguous between "I prefer variant 2"
// and "four/two was not enough range" (agent-tui#10's own text), and the fix for an
// ambiguous preference is to build both readings, not ask which. Variant
// 2's own grouping (by-repo) is kept and evolved (kanban-repo, compact-repo
// below, both real swimlanes now instead of a flat indented list), and the
// range is widened to six, spanning every axis agent-tui#10 named at least once:
// both groupings, all three column styles, both densities, both card
// shapes, both themes, and all three closed-item settings.
var Layouts = []Layout{
	{
		ID: "kanban-column", Name: "kanban", Grouping: GroupByColumn,
		Column: StyleBoxed, Density: DensityRoomy, Card: ShapeMultiLine, Theme: ThemeVivid, Closed: ClosedHide,
		Description: "bordered columns, one card per box, vivid per-column colour -- the eye-candy default",
	},
	{
		ID: "kanban-repo", Name: "kanban by repo", Grouping: GroupByRepo,
		Column: StyleBoxed, Density: DensityRoomy, Card: ShapeMultiLine, Theme: ThemeVivid, Closed: ClosedHide,
		Description: "evolves the old \"by repo\" grouping: one swimlane per repo, real boxed columns within it",
	},
	{
		ID: "compact-column", Name: "compact", Grouping: GroupByColumn,
		Column: StyleRules, Density: DensityCompact, Card: ShapeSingleLine, Theme: ThemeRestrained, Closed: ClosedHide,
		Description: "thin rules not boxes, one line per card, muted colour -- for speed and a small terminal",
	},
	{
		ID: "compact-repo", Name: "compact by repo", Grouping: GroupByRepo,
		Column: StyleRules, Density: DensityCompact, Card: ShapeSingleLine, Theme: ThemeRestrained, Closed: ClosedHide,
		Description: "compact's grouping, but swimlaned per repo -- \"what does one repo look like\", fast",
	},
	{
		ID: "kanban-recent", Name: "kanban + recent", Grouping: GroupByColumn,
		Column: StyleBoxed, Density: DensityRoomy, Card: ShapeMultiLine, Theme: ThemeVivid, Closed: ClosedRecent,
		Description: "kanban, plus Done cards completed in the last 24h -- closed items as a variant, not a question",
	},
	{
		ID: "whitespace-all", Name: "whitespace + all", Grouping: GroupByColumn,
		Column: StyleWhitespace, Density: DensityCompact, Card: ShapeSingleLine, Theme: ThemeRestrained, Closed: ClosedAll,
		Description: "no border characters at all, every Done card ever -- the full-history, lightest-render option",
	},
}

// columnColorRole maps a board Column to the theme.Role its vivid-theme
// accent uses -- Vivid gives every column its own colour (agent-tui#10: "eye
// candy"); Restrained collapses every column to theme.RoleNeutral so the
// vivid/restrained axis stays genuinely decorative -- see the warn role
// used by renderCard below for why an aged/blocked card still stands out
// in Restrained too. The concrete colour behind each role is agent-tui#27
// data (internal/theme/registry.go), not this file's business.
var columnColorRole = map[Column]theme.Role{
	Backlog:    theme.RoleBacklog,
	InProgress: theme.RoleInProgress,
	InReview:   theme.RoleInReview,
	Blocked:    theme.RoleBlocked,
	Done:       theme.RoleDone,
}

func columnColor(col Column, boardTheme Theme, th theme.Theme) lipgloss.Color {
	if boardTheme == ThemeRestrained {
		return th.Color(theme.RoleNeutral)
	}
	if role, ok := columnColorRole[col]; ok {
		return th.Color(role)
	}
	return th.Color(theme.RoleNeutral)
}

// filterClosed applies a Layout's ClosedFilter to cards -- pure, no I/O, no
// wall-clock read (see closedRecentWindow's doc comment). Only Column ==
// Done is ever filtered; every other column, including Blocked, is never
// touched here regardless of Closed, so this can never be the mechanism
// that hides an ugly state.
func filterClosed(cards []Card, mode ClosedFilter) []Card {
	if mode == ClosedAll {
		return cards
	}
	out := make([]Card, 0, len(cards))
	for _, c := range cards {
		if c.Column != Done {
			out = append(out, c)
			continue
		}
		switch mode {
		case ClosedHide:
			continue
		case ClosedRecent:
			if c.Age.Seconds() <= closedRecentWindow {
				out = append(out, c)
			}
		}
	}
	return out
}

// Render produces this Layout's full, already-coloured output. cards/wip
// are the SAME Snapshot fields every other renderer in this package reads;
// Render does no fetching and no filtering beyond its own ClosedFilter --
// repo selection (agent-tui#10 item 2) is the caller's job (model.go),
// applied to cards before Render ever sees them, so this stays a pure
// function of what it's given, same discipline card.go's Derive documents
// for itself.
func (l Layout) Render(cards []Card, wip []WIP, width int, th theme.Theme) string {
	cards = filterClosed(cards, l.Closed)

	var b strings.Builder
	writeWIP(&b, wip, width)

	switch l.Grouping {
	case GroupByRepo:
		b.WriteString(renderSwimlanes(cards, l, width, th))
	default:
		b.WriteString(renderColumnRow(cards, l, width, true, th))
	}
	return b.String()
}

// renderSwimlanes is GroupByRepo: one repo per row, that repo's own columns
// joined horizontally beneath its header -- real swimlanes, not a flat
// indented list, over the exact grouping view.go's RenderByRepo already
// answers (agent-tui#10: "keep and evolve what variant 2 was").
func renderSwimlanes(cards []Card, l Layout, width int, th theme.Theme) string {
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

	var b strings.Builder
	for _, repoID := range repos {
		var inRepo []Card
		for _, c := range cards {
			if c.Repo.GitHubID() == repoID {
				inRepo = append(inRepo, c)
			}
		}
		fmt.Fprintf(&b, "\n== %s ==\n", repoID)
		b.WriteString(renderColumnRow(inRepo, l, width, false, th))
	}
	return b.String()
}

// renderColumnRow renders Columns side by side as real panes -- the "real
// columns with cards" core of agent-tui#10 item 1. showRepoTag is false inside a
// swimlane (the repo is already the row's own header, per-card repeating
// it would be noise); true for the by-column grouping, where cards from
// every repo sit in the same column and the tag is the only thing telling
// them apart.
func renderColumnRow(cards []Card, l Layout, width int, showRepoTag bool, th theme.Theme) string {
	n := len(Columns)
	paneWidth := width / n
	if paneWidth < 16 {
		paneWidth = 16
	}

	panes := make([]string, 0, n)
	for _, col := range Columns {
		var inCol []Card
		for _, c := range cards {
			if c.Column == col {
				inCol = append(inCol, c)
			}
		}
		// Oldest-in-this-column first, same ordering view.go's retired
		// RenderByColumn/RenderByRepo used -- the card that has looked this
		// way longest is the one most worth seeing without scrolling.
		sort.SliceStable(inCol, func(i, j int) bool { return inCol[i].Age > inCol[j].Age })
		panes = append(panes, renderColumnPane(col, inCol, l, paneWidth, showRepoTag, th))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, panes...) + "\n"
}

// renderColumnPane is one column's box/rule/whitespace container -- the
// unit ColumnStyle actually varies. Header + count always render, even for
// an empty column (agent-tui#10's "every state renders" extends to "every column
// renders", the board-level equivalent of internal/lane's AllStates guard
// -- see layout_test.go's TestEveryLayoutRendersEveryColumn).
func renderColumnPane(col Column, cards []Card, l Layout, paneWidth int, showRepoTag bool, th theme.Theme) string {
	color := columnColor(col, l.Theme, th)
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(color)

	// innerWidth is the actual usable text width once each ColumnStyle's
	// own border/padding overhead is subtracted -- computed once, here,
	// and handed to every card line this pane writes, so a card is
	// TRUNCATED to what will fit rather than left for lipgloss's own
	// Width() to auto-wrap at render time. Auto-wrap breaks a card's text
	// across lines with no truncation marker and no way for a caller to
	// tell "wrapped" from "a second real line" -- layout_test.go's
	// TestLayoutRenderShowsBlockedReason and TestLetterKeyTogglesRepo
	// Selection (model_test.go) both failed against an earlier version of
	// this function that skipped this and let Width() wrap instead.
	//
	// pad is th.Padding (agent-tui#27: the horizontal chrome padding every
	// pane style applies is theme data, not a literal here); border chars
	// come from th.Border, but WHETHER a border draws at all -- and how
	// many sides -- stays l.Column's own axis, unchanged by theme.
	pad := th.Padding
	var frameWidth int
	switch l.Column {
	case StyleBoxed:
		frameWidth = 2 + 2*pad // border, both sides + padding, both sides
	case StyleRules:
		frameWidth = 1 + pad // border, one side + padding, one side
	default: // StyleWhitespace
		frameWidth = 2 * pad // padding only, both sides
	}
	innerWidth := paneWidth - frameWidth
	if innerWidth < 8 {
		innerWidth = 8
	}

	var body strings.Builder
	fmt.Fprintf(&body, "%s\n", headerStyle.Render(truncate(strings.ToUpper(string(col))+" ("+strconv.Itoa(len(cards))+")", innerWidth)))
	if len(cards) == 0 {
		body.WriteString(lipgloss.NewStyle().Faint(true).Render("(empty)") + "\n")
	}
	for i, c := range cards {
		if l.Density == DensityRoomy && i > 0 {
			body.WriteString("\n")
		}
		body.WriteString(renderCard(c, l, innerWidth, showRepoTag, th))
	}

	content := strings.TrimRight(body.String(), "\n")

	switch l.Column {
	case StyleBoxed:
		return lipgloss.NewStyle().
			Border(th.Border).BorderForeground(color).
			Padding(0, pad).Width(paneWidth - 2).Render(content)
	case StyleRules:
		return lipgloss.NewStyle().
			BorderStyle(th.Border).BorderLeft(true).BorderForeground(color).
			PaddingLeft(pad).Width(paneWidth - 1).Render(content)
	default: // StyleWhitespace
		return lipgloss.NewStyle().Padding(0, pad).Width(paneWidth).Render(content)
	}
}

// renderCard renders one card, single- or multi-line per l.Card. The aged
// marker and its warn colour (theme.RoleWarn) always apply regardless of
// l.Theme -- a pretty board that de-emphasizes an aged/blocked card by
// colour is the same failure agent-tui#10 already ruled out for omitting one
// entirely. Every line is explicitly truncated to innerWidth before it is
// styled -- see renderColumnPane's own doc comment for why this can't be
// left to lipgloss's automatic wrap.
func renderCard(c Card, l Layout, innerWidth int, showRepoTag bool, th theme.Theme) string {
	warn := c.Aged()
	warnColor := th.Color(theme.RoleWarn)
	style := lipgloss.NewStyle()
	if warn {
		style = style.Bold(true).Foreground(warnColor)
	}

	if l.Card == ShapeSingleLine {
		line := cardLine(c, innerWidth)
		return style.Render(line) + "\n"
	}

	marker := " "
	if warn {
		marker = "!"
	}
	titlePrefix := fmt.Sprintf("%s #%d ", marker, c.Number)
	title := truncate(c.Title, innerWidth-len(titlePrefix))

	var out strings.Builder
	out.WriteString(style.Render(truncate(titlePrefix+title, innerWidth)) + "\n")

	meta := []string{formatAge(c.Age)}
	if showRepoTag {
		tag := c.Repo.Label
		if tag == "" {
			tag = c.Repo.Name
		}
		meta = append([]string{"[" + tag + "]"}, meta...)
	}
	if c.Session != "" {
		meta = append(meta, c.Session)
	}
	out.WriteString(lipgloss.NewStyle().Faint(true).Render(truncate(strings.Join(meta, "  "), innerWidth)) + "\n")

	if c.Column == Blocked && c.BlockedReason != "" {
		out.WriteString(lipgloss.NewStyle().Foreground(warnColor).Render(truncate("-> "+c.BlockedReason, innerWidth)) + "\n")
	}
	return out.String()
}

// truncate shortens s to at most n runes, replacing the last one with "…"
// when it had to cut -- the same convention internal/rail's own truncate
// (model.go) and view.go's cardLine already use, kept as a rune-aware copy
// here rather than exported cross-package, matching how internal/lane's
// states.go is a copy of lanes.sh's list rather than an import (this repo
// avoids cross-package coupling for a four-line helper).
func truncate(s string, n int) string {
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
