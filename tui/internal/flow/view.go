package flow

import (
	"fmt"
	"strings"
	"time"
)

// header renders the title, any theme/error notice, and the pipeline
// diagram -- see Model.headerHeight, which counts these same lines by hand
// so the viewport body below never overruns its budget (agent-tui#29's
// lesson: the line-count arithmetic and the actual render must agree, or
// content silently pushes the footer off screen).
func (m Model) header() []string {
	st := m.styles()
	var lines []string
	lines = append(lines, titleStyle.Render("flow -- work moving"))
	if m.themeNotice != "" {
		lines = append(lines, st.err.Render("! "+m.themeNotice))
	}
	if m.fetchErr != nil {
		lines = append(lines, st.err.Render("! unavailable"))
		lines = append(lines, st.dim.Render(m.fetchErr.Error()))
	} else if len(m.items) == 0 && m.lastFetched.IsZero() {
		lines = append(lines, st.dim.Render("(loading)"))
	}
	lines = append(lines, "")
	lines = append(lines, m.pipeline())
	lines = append(lines, "")
	return lines
}

// pipeline renders the aggregate flow diagram: queued -> working -> review
// -> done, each node counted, joined by a travelling marker (arrowTrack)
// that advances every motionMsg so the diagram reads as motion between
// fetches, not a static count that only changes once a minute (agent-tui#64's own
// acceptance test: "it will seem like watching a company work"). Blocked is
// drawn as its own line looping back into working/review, per agent-tui#64's "the
// loop edges" requirement -- review -> fix -> re-review is a cycle, and a
// side-by-side column (board.Model's own layout) hides that; this line
// makes it explicit.
func (m Model) pipeline() string {
	counts := StageCounts(m.items)
	st := m.styles()

	node := func(s Stage) string {
		return st.stage[s].Render(fmt.Sprintf("[%s %d]", s, counts[s]))
	}
	track := func(offset int) string {
		return st.dim.Render(" " + arrowTrack(6, m.animFrame+offset) + " ")
	}

	top := node(StageQueued) + track(0) + node(StageWorking) + track(2) + node(StageReview) + track(4) + node(StageDone)

	blocked := ""
	if counts[StageBlocked] > 0 {
		blocked = "\n" + st.stage[StageBlocked].Render(fmt.Sprintf("     +--[loop]--< blocked %d >--[fix]--+", counts[StageBlocked]))
	}
	return top + blocked
}

// arrowTrack is a fixed-width dash track with one travelling '>' -- the
// pipeline's own "position AND motion" element (agent-tui#64's own phrase for what
// a kanban card can't show: not just where something is, but that it is
// moving). width < 3 is clamped so the marker always has somewhere to be.
func arrowTrack(width, frame int) string {
	if width < 3 {
		width = 3
	}
	track := make([]byte, width)
	for i := range track {
		track[i] = '-'
	}
	pos := frame % width
	if pos < 0 {
		pos += width
	}
	track[pos] = '>'
	return string(track)
}

// bodyContent renders the scrollable item list -- InFlight-only by default,
// every stage (including Queued/Done) when 'a' toggles showAll. This is the
// per-item half of "position AND motion": each line names how long an item
// has sat in its CURRENT stage (Card.Age) and, once it has moved at all,
// how long since it was first dispatched (Card.CycleTime) -- a card in
// review for 90 minutes after three trips round the fix loop reads
// differently here than one that just arrived, which board.Model's kanban
// columns cannot show (agent-tui#64's own "position AND motion" example).
func (m Model) bodyContent() string {
	items := InFlight(m.items)
	if m.showAll {
		items = m.items
	}
	if len(items) == 0 {
		return m.styles().dim.Render("(nothing in flight)")
	}
	st := m.styles()
	var b strings.Builder
	for i, it := range items {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(itemLine(it, st))
	}
	return b.String()
}

func itemLine(it Item, st flowStyles) string {
	c := it.Card
	label := st.stage[it.Stage].Render(fmt.Sprintf("%-8s", it.Stage))
	ref := fmt.Sprintf("%s#%d", c.Repo.Label, c.Number)
	if c.Repo.Label == "" {
		ref = fmt.Sprintf("%s#%d", c.Repo.Name, c.Number)
	}
	title := c.Title
	if len(title) > 40 {
		title = title[:39] + "…"
	}

	age := formatDuration(c.Age)
	ageStyled := age
	if c.Aged() {
		ageStyled = st.warn.Render(age)
	}

	var extra string
	switch {
	case it.Stage == StageBlocked && c.BlockedReason != "":
		extra = st.warn.Render("  ! " + c.BlockedReason)
	case it.Stage == StageReview && c.PRNumber != 0:
		extra = st.dim.Render(fmt.Sprintf("  PR #%d", c.PRNumber))
	case it.Stage == StageDone && it.Terminal != "":
		extra = st.dim.Render("  " + it.Terminal)
	}

	cycle := ""
	if c.CycleTime > 0 {
		cycle = st.dim.Render("  cycle=" + formatDuration(c.CycleTime))
	}

	return fmt.Sprintf("%s %-16s %-40s age=%s%s%s", label, ref, title, ageStyled, cycle, extra)
}

// formatDuration renders a duration the same coarse granularity board
// already uses for "fetched N ago" -- minutes once past a minute, seconds
// below it, never a sub-second value nobody scanning a flow view needs.
func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d >= time.Minute {
		return d.Round(time.Minute).String()
	}
	return d.Round(time.Second).String()
}
