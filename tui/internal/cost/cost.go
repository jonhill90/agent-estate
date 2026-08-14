// Package cost projects per-harness spend and quota pressure from ccusage's
// already-parsed usage data (agent-tui#4). Same discipline internal/board's
// doc comment states for its own three sources: this package does no I/O of
// its own and reimplements none of ccusage's per-harness usage-log parsing
// -- cmd/agent-tui shells `ccusage` out (a Runner, mirroring
// board.ExecRunner) and hands this package only bytes already in ccusage's
// own JSON shape. Turning that shape into a Snapshot is the whole of what
// lives here.
package cost

import "sort"

// Figure is one usage number that may not be known. ccusage failing to run
// -- or, for Limit specifically, ccusage having no local way to see a
// harness's real quota at all -- must never collapse to a bare 0: issue
// #4's own postmortem is explicit that "a zero reads as nothing spent, and
// this estate has been bitten by exactly that silent-blindness three
// times." Known is the only field a caller may branch display logic on;
// Value is meaningless when Known is false and must not be printed.
type Figure struct {
	Known bool
	Value float64
}

// KnownFigure wraps a value ccusage actually reported.
func KnownFigure(v float64) Figure { return Figure{Known: true, Value: v} }

// Limit is a harness's distance to whatever cap ccusage can compute
// locally. Not every harness has one: codex's ChatGPT plan quota -- the
// exact bucket issue #4 says went dark -- is not something ccusage's local
// usage-log parse can see at all (verified against `ccusage codex --help`:
// daily/monthly/session only, no blocks or token-limit concept; same for
// `ccusage pi --help`). Limit.Known is false for those, always, and
// Percent must never be read as "0% used" just because there was nothing
// to compute -- that is the exact silent-blindness issue #4 warns against,
// applied to quota instead of spend.
type Limit struct {
	Known   bool
	Percent float64 // ccusage's own tokenLimitStatus.percentUsed, unmodified
	Label   string  // what Percent is measured against, e.g. "active 5h block vs 200,000 tokens"
	Warn    bool    // ccusage's own tokenLimitStatus.status != "ok", when Known
}

// Harness is one coding-agent CLI's figures for the fetched window (today).
type Harness struct {
	Name      string // ccusage's own agent id: "claude", "codex", "pi", ...
	Cost      Figure // totalCost, USD
	Tokens    Figure // totalTokens -- includes cache read/creation, NOT just input+output
	CacheRead Figure // cacheReadTokens, broken out per issue #4's #2, never folded into Tokens
	Limit     Limit
}

// Snapshot is one fetch's worth of cost data across every harness ccusage
// detected today. Known is false only when the fetch itself failed --
// ccusage unreadable, non-zero exit, unparsable output (see
// cmd/agent-tui/cost.go). An empty Harnesses with Known true means ccusage
// ran and genuinely found no usage yet today: a real answer, not
// blindness, and the panel must be able to tell the two apart.
type Snapshot struct {
	Known     bool
	Harnesses []Harness
}

// Unknown is the Snapshot cmd/agent-tui returns when the ccusage fetch
// itself failed. Every figure the panel renders from this must read
// "unknown", never 0 -- this is the blindness-test acceptance case
// (agent-tui#4 item 2).
func Unknown() Snapshot { return Snapshot{Known: false} }

// harnessOrder ranks the harnesses issue #4 named explicitly first --
// Claude carries ~94% of spend, codex is the one that went dark, pi is the
// long-tail baseline -- so the panel always leads with the two the issue is
// actually about. Anything else ccusage detects sorts alphabetically after
// them, so a fifth harness showing up someday needs no edit here to be
// seen (same "data behind an interface" discipline as lane.Variants).
var harnessOrder = map[string]int{"claude": 0, "codex": 1, "pi": 2}

func sortHarnesses(hs []Harness) {
	sort.SliceStable(hs, func(i, j int) bool {
		oi, iok := harnessOrder[hs[i].Name]
		oj, jok := harnessOrder[hs[j].Name]
		switch {
		case iok && jok:
			return oi < oj
		case iok:
			return true
		case jok:
			return false
		default:
			return hs[i].Name < hs[j].Name
		}
	})
}

// Compose builds one Snapshot from a day's per-harness figures (ParseDaily)
// and, when available, Claude's block-limit pressure (ParseActiveBlockLimit)
// -- the only place the two ccusage calls cmd/agent-tui makes are joined,
// so both parse functions stay pure and independently testable.
func Compose(harnesses []Harness, claudeLimit Limit) Snapshot {
	out := make([]Harness, len(harnesses))
	copy(out, harnesses)
	for i := range out {
		if out[i].Name == "claude" {
			out[i].Limit = claudeLimit
		}
	}
	sortHarnesses(out)
	return Snapshot{Known: true, Harnesses: out}
}
