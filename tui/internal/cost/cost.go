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
	Quota     Quota // codexbar's session/weekly usage window via quota.sh (agent-tui#49 item 3); zero value is Known false
}

// Snapshot is one fetch's worth of cost data across every harness ccusage
// detected today. Known is false only when the ccusage fetch itself failed
// -- ccusage unreadable, non-zero exit, unparsable output (see
// cmd/agent-tui/cost.go). An empty Harnesses with Known true means ccusage
// ran and genuinely found no usage yet today: a real answer, not
// blindness, and the panel must be able to tell the two apart.
//
// Quotas is deliberately a SEPARATE source from Known/Harnesses -- quota.sh
// (agent-tui#49 item 3) and ccusage are two independent subprocess calls,
// and one failing must never blank out the other. A machine with no
// working ccusage but a working quota.sh (the review that reopened #49)
// must still show real session/weekly percentages; Quotas carries that
// even when Known is false and Harnesses is empty. When Known is true,
// this same data has ALSO already been merged onto each Harness.Quota by
// name (cmd/estate/cost.go) -- Quotas here exists so the quota-only
// render path (RenderBars/RenderNumeric's !Known branch) has something to
// read without needing a Harness to hang it off of.
type Snapshot struct {
	Known     bool
	Harnesses []Harness
	Quotas    map[string]Quota
}

// Unknown is the Snapshot cmd/agent-tui returns when the ccusage fetch
// itself failed and no quota.sh data is available either. Every figure the
// panel renders from this must read "unknown", never 0 -- this is the
// blindness-test acceptance case (agent-tui#4 item 2).
func Unknown() Snapshot { return Snapshot{Known: false} }

// UnknownWithQuota is Unknown, but for when ccusage failed and quota.sh
// still produced real data (agent-tui#49's reopened item 3) -- the cost
// figures read "unknown" exactly as Unknown() renders them, but the quota
// line(s) still show whatever quotas holds, keyed by provider name the
// same way Harness.Name/ParseQuotaSummary already agree on.
func UnknownWithQuota(quotas map[string]Quota) Snapshot {
	return Snapshot{Known: false, Quotas: quotas}
}

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

	// Quotas mirrors whatever's already on each Harness.Quota (the caller,
	// cmd/estate/cost.go, merges quota.sh's data onto Harnesses by name
	// before calling Compose) -- kept in sync here too so a reader of
	// Snapshot.Quotas sees the same answer whether Known is true or false
	// (UnknownWithQuota's doc comment).
	quotas := map[string]Quota{}
	for _, h := range out {
		if h.Quota.Known {
			quotas[h.Name] = h.Quota
		}
	}
	return Snapshot{Known: true, Harnesses: out, Quotas: quotas}
}
