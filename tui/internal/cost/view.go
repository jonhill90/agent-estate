package cost

import (
	"fmt"
	"sort"
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
		return "cost: unknown (ccusage unreadable)\n" + renderQuotaOnly(snap.Quotas)
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
		fmt.Fprintf(&b, "  quota:  %s\n", renderQuota(h.Quota))
	}
	return b.String()
}

// RenderNumeric is the plain-figures layout: no meter, no bar -- every
// number as a number, limit as a bare percentage. Same data as RenderBars,
// laid out for a narrower terminal or a preference for reading digits over
// a glyph.
func RenderNumeric(snap Snapshot, width int) string {
	if !snap.Known {
		return "cost: unknown (ccusage unreadable)\n" + renderQuotaOnly(snap.Quotas)
	}
	if len(snap.Harnesses) == 0 {
		return "cost: no usage recorded today\n"
	}

	var b strings.Builder
	for _, h := range snap.Harnesses {
		// agent-tui#56: matches renderLimitBar's own "not set" wording --
		// see its doc comment for why this must not say "unknown" the same
		// way renderQuota does for an actually-missing instrument.
		limit := "limit not set -- see quota"
		if h.Limit.Known {
			warn := ""
			if h.Limit.Warn {
				warn = " WARN"
			}
			limit = fmt.Sprintf("limit %.1f%% of %s%s", h.Limit.Percent, h.Limit.Label, warn)
		}
		fmt.Fprintf(&b, "%-8s $%-8s %-12s cache-read %-10s %-28s quota %s\n",
			h.Name,
			formatFigure(h.Cost, "%.2f"),
			formatCount(h.Tokens)+" tok",
			formatCount(h.CacheRead),
			limit,
			renderQuota(h.Quota),
		)
	}
	return b.String()
}

// renderLimitBar draws a fixed-width ascii meter for a Limit, or a
// not-set note when ccusage had no way to compute one (codex, pi, or
// Claude with no -claude-block-limit configured) -- never an empty or
// full-looking bar standing in for a percentage nobody measured.
//
// agent-tui#56: this used to say "unknown (no quota source)" -- the exact
// phrase renderQuota below uses for a GENUINELY missing instrument
// (quota.sh absent or failing). Limit is a different, always-optional
// field (ccusage's own local block-limit computation, which codex/pi never
// have and Claude only has with -claude-block-limit set); printing the
// same "no quota source" words for it, one line above a quota line that
// DOES have a real source and real numbers, read as the panel contradicting
// itself. "not set" names what this field actually is -- a configuration
// state -- and points at the line that answers the same underlying
// question ("how close to a cap") when quota.sh is wired in.
func renderLimitBar(l Limit) string {
	if !l.Known {
		return "not set -- see quota below"
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

// renderQuota renders codexbar's session/weekly usage window for a harness,
// read via quota.sh (agent-tui#49 item 3) -- "unknown" whenever quota.sh
// could not be read, never a guessed percentage, same discipline
// renderLimitBar already holds ccusage's own block-limit figure to.
func renderQuota(q Quota) string {
	if !q.Known {
		return "unknown (no quota source)"
	}
	session := formatFigure(q.SessionPercent, "%.0f%% used")
	weekly := formatFigure(q.WeeklyPercent, "%.0f%% used")
	out := fmt.Sprintf("session %s, weekly %s", session, weekly)
	if q.Note != "" {
		out += " -- " + q.Note
	}
	return out
}

// renderQuotaOnly renders every provider's quota line when ccusage itself
// is unknown (Snapshot.Known false) -- the fix for the reopened half of
// agent-tui#49 item 3: a machine with no working ccusage but a working
// quota.sh must still show real session/weekly percentages, not just
// "cost: unknown" with nothing else. Empty when quotas is empty/nil (no
// quota.sh configured either), so a caller with neither source still
// renders exactly the old one-line "cost: unknown".
func renderQuotaOnly(quotas map[string]Quota) string {
	if len(quotas) == 0 {
		return ""
	}
	names := make([]string, 0, len(quotas))
	for name := range quotas {
		names = append(names, name)
	}
	sortProviderNames(names)

	var b strings.Builder
	for _, name := range names {
		fmt.Fprintf(&b, "%-8s quota: %s\n", name, renderQuota(quotas[name]))
	}
	return b.String()
}

// sortProviderNames orders provider names the same way sortHarnesses
// already orders Harnesses -- claude/codex/pi first (issue #4's own
// reasoning), everything else alphabetically after -- so this render path
// and the per-Harness one never disagree about ordering.
func sortProviderNames(names []string) {
	sort.SliceStable(names, func(i, j int) bool {
		oi, iok := harnessOrder[names[i]]
		oj, jok := harnessOrder[names[j]]
		switch {
		case iok && jok:
			return oi < oj
		case iok:
			return true
		case jok:
			return false
		default:
			return names[i] < names[j]
		}
	})
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
