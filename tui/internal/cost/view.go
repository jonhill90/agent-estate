package cost

import (
	"fmt"
	"strings"
)

// View renders one cost-panel layout as plain text over the same Snapshot.
// Layout and units are exactly the kind of question agent-supervisor#107's
// addendum (and agent-tui#6's Views, board/view.go) says ships as variants
// rather than a question in prose: bars-vs-numeric, and percentage-vs-raw,
// are both real renderers here, and Views lists them for a picker identical
// in spirit to the rail's glyph-set picker and the board's own view picker.
type View struct {
	ID          string
	Name        string
	Description string
	Render      func(snap Snapshot, width int) string
}

// Views is every cost-panel layout this drop ships, in picker order.
// Variants[0] is the default -- what renders with no selection made.
var Views = []View{
	{ID: "bars", Name: "bars", Description: "ascii meter for limit pressure; cache-read on its own line", Render: RenderBars},
	{ID: "numeric", Name: "numeric", Description: "plain figures only -- cost, tokens, cache-read, limit as a number", Render: RenderNumeric},
}

const meterWidth = 20

// RenderCompact renders one short line per harness for a narrow host --
// internal/rail's ~20-24 usable columns, where agent-tui#4 asked the panel
// to actually live ("glanceable, always there, no command to run"). Each
// line leads with limit pressure when ccusage can compute one (the number
// issue #4 says matters: quota exhaustion, not spend) and falls back to
// today's cost when it can't (codex, pi, or Claude with no
// -claude-block-limit configured), so a harness with no quota source still
// shows something live rather than a blank line. width is accepted for
// symmetry with the View.Render signature and future wrapping; every line
// here is already short enough not to need it today.
func RenderCompact(snap Snapshot, width int) []string {
	if !snap.Known {
		return []string{"unknown"}
	}
	if len(snap.Harnesses) == 0 {
		return []string{"none today"}
	}
	lines := make([]string, 0, len(snap.Harnesses))
	for _, h := range snap.Harnesses {
		figure := "$" + formatFigure(h.Cost, "%.2f")
		if h.Limit.Known {
			warn := ""
			if h.Limit.Warn {
				warn = "!"
			}
			figure = fmt.Sprintf("%.0f%%%s", h.Limit.Percent, warn)
		}
		lines = append(lines, fmt.Sprintf("%-7s %s", h.Name, figure))
	}
	return lines
}

// formatFigure renders a Figure, honoring Known so "unknown" can never
// regress into a printed 0. RenderBars and RenderNumeric each short-circuit
// on !snap.Known before reaching any per-harness figure, and ParseDaily's
// parse is all-or-nothing (see ccusage.go) -- so in real usage every
// Harness this package composes from ccusage output already has Known
// figures, and this guard does not currently fire from either View. It
// stays as defensive coverage for a Harness built some other way (a test,
// a future caller) rather than as "the one place both Views must go
// through" -- that claim was not true and is not repeated here.
func formatFigure(f Figure, format string) string {
	if !f.Known {
		return "unknown"
	}
	return fmt.Sprintf(format, f.Value)
}

// RenderBars is the CodexBar-style layout: a filled/empty ascii meter for
// limit pressure (when known), cache-read broken onto its own line per
// issue #4's #2 rather than folded into a token total.
func RenderBars(snap Snapshot, width int) string {
	if !snap.Known {
		return "cost: unknown (ccusage unreadable)\n"
	}
	if len(snap.Harnesses) == 0 {
		return "cost: no usage recorded today\n"
	}

	var b strings.Builder
	for _, h := range snap.Harnesses {
		fmt.Fprintf(&b, "%s\n", h.Name)
		fmt.Fprintf(&b, "  cost:   $%s today\n", formatFigure(h.Cost, "%.2f"))
		fmt.Fprintf(&b, "  tokens: %s (cache-read: %s)\n", formatCount(h.Tokens), formatCount(h.CacheRead))
		fmt.Fprintf(&b, "  limit:  %s\n", renderLimitBar(h.Limit))
	}
	return b.String()
}

// RenderNumeric is the plain-figures layout: no meter, no bar -- every
// number as a number, limit as a bare percentage. Same data as RenderBars,
// laid out for a narrower terminal or a preference for reading digits over
// a glyph.
func RenderNumeric(snap Snapshot, width int) string {
	if !snap.Known {
		return "cost: unknown (ccusage unreadable)\n"
	}
	if len(snap.Harnesses) == 0 {
		return "cost: no usage recorded today\n"
	}

	var b strings.Builder
	for _, h := range snap.Harnesses {
		limit := "limit unknown"
		if h.Limit.Known {
			warn := ""
			if h.Limit.Warn {
				warn = " WARN"
			}
			limit = fmt.Sprintf("limit %.1f%% of %s%s", h.Limit.Percent, h.Limit.Label, warn)
		}
		fmt.Fprintf(&b, "%-8s $%-8s %-12s cache-read %-10s %s\n",
			h.Name,
			formatFigure(h.Cost, "%.2f"),
			formatCount(h.Tokens)+" tok",
			formatCount(h.CacheRead),
			limit,
		)
	}
	return b.String()
}

// renderLimitBar draws a fixed-width ascii meter for a Limit, or the word
// "unknown" when ccusage had no way to compute one (codex, pi, or Claude
// with no -claude-block-limit configured) -- never an empty or full-looking
// bar standing in for a percentage nobody measured.
func renderLimitBar(l Limit) string {
	if !l.Known {
		return "unknown (no quota source)"
	}
	filled := int(l.Percent / 100 * meterWidth)
	if filled > meterWidth {
		filled = meterWidth
	}
	if filled < 0 {
		filled = 0
	}
	bar := strings.Repeat("#", filled) + strings.Repeat("-", meterWidth-filled)
	marker := ""
	if l.Warn {
		marker = " WARN"
	}
	return fmt.Sprintf("[%s] %.1f%% of %s%s", bar, l.Percent, l.Label, marker)
}

// formatCount renders a token-count Figure without decimals -- tokens are
// integers ccusage already reports as such; the float64 Figure type is
// shared with Cost only so both go through the same Known guard.
func formatCount(f Figure) string {
	if !f.Known {
		return "unknown"
	}
	return commaInt(int64(f.Value))
}

func commaInt(n int64) string {
	s := fmt.Sprintf("%d", n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}
