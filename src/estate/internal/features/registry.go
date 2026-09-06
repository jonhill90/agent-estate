// Package features holds the feature-completion ledger: a checked-in,
// hand-maintained record of which operator-visible capabilities have
// actually shipped, as of which merge, versus which are only in progress or
// not started. This is the fix for a specific, recorded failure: delivered
// work (codex ingestion, PR #1240) and undelivered work (Agent Memory v0,
// the disclosure-ladder fix, the grounding-coverage fix) were indistinguishable
// without reading history by hand, because status was being inferred from
// activity instead of recorded as fact.
//
// The registry below is DATA, not code -- the same convention this repo
// already applies to glyph sets and themes (internal/lane/variants.go,
// internal/theme/registry.go). Nothing in this package inspects git log,
// gh, or the ledger to compute a status: a human updates a Feature's Status
// field when, and only when, evidence exists that it shipped. Activity is
// not progress; automating that inference is the exact defect this ledger
// exists to prevent.
package features

// Status is the tri-state a Feature can be in. There is no fourth state
// ("mostly done", "blocked") on purpose -- a hand-maintained ledger with
// more than three buckets invites hedging instead of a plain claim.
type Status string

const (
	Delivered  Status = "delivered"
	InProgress Status = "in-progress"
	NotStarted Status = "not-started"
)

// Feature is one row of the ledger: one operator-visible capability, its
// current status, and -- for anything claimed Delivered -- the evidence a
// human or CI can independently follow to check that claim. Evidence is a
// free-text field naming a PR number and, where useful, a command to
// re-verify a figure; it is read by a human, never fetched by this package
// (see registry_test.go's TestRegistry_EveryDeliveredRowHasEvidence for the
// one rule this package enforces about it).
type Feature struct {
	// ID is a short, stable, unique slug -- referenced in commit messages
	// and PR bodies when a row's status changes, so it must not be renamed
	// casually once in use.
	ID string
	// Name is the operator-facing capability description.
	Name string
	// Status is hand-set by whoever last confirmed it -- never derived.
	Status Status
	// Evidence names a PR number (and, ideally, a re-verification command)
	// for a Delivered row. Required non-empty when Status == Delivered;
	// registry_test.go enforces this so the ledger cannot drift into
	// aspiration. May also be set (optionally) on other statuses to link
	// context, but is only checked for Delivered rows.
	Evidence string
	// Caveats records known gaps, measured defects, or scope limits in an
	// otherwise-delivered capability -- e.g. "estate knowledge" is
	// delivered, but its grounding coverage, disclosure ladder and
	// absence-reporting all have named, dated defects. Optional.
	Caveats string
}

// Registry is the ledger itself. Seeded 2026-09-05 from the dispatch brief
// for agent-estate#1139; see that brief and the PR that introduced this
// package for which claims were re-verified at seed time versus carried
// from an earlier measurement. Update a row's Status only when new
// evidence exists -- a merged PR, a re-run command -- and cite it in
// Evidence, never because time has passed or work "feels" further along.
var Registry = []Feature{
	{
		ID:     "codex-ingestion",
		Name:   "Codex transcript ingestion (cmd/codexingest)",
		Status: Delivered,
		Evidence: "PR #1240, merged and ran: 4,360 codex provenance rows live in " +
			"~/corpus/ledger.sqlite3 (re-verify: sqlite3 -readonly ~/corpus/ledger.sqlite3 " +
			"\"select count(*) from codex_provenance\")",
	},
	{
		ID:       "corpus-provenance-contract",
		Name:     "Corpus provenance contract",
		Status:   Delivered,
		Evidence: "PR #1228 (merged; re-verify with `gh pr view 1228`)",
	},
	{
		ID:       "seed-source-catalogue",
		Name:     "Seed-source catalogue",
		Status:   Delivered,
		Evidence: "PR #1239 (merged; re-verify with `gh pr view 1239`)",
	},
	{
		ID:       "knowledge-query",
		Name:     "estate knowledge query (cited, capped, progressive disclosure)",
		Status:   Delivered,
		Evidence: "issue #1019 work",
		Caveats: "Three measured defects, dated 2026-09-05: " +
			"(A) grounding injects only kind=parameter weight=hard -- 958 rows, excluding " +
			"1,341 hard directives + 173 hard corrections, 39% coverage (the injecting query " +
			"is Hard() in src/estate/internal/corpus/corpus.go; re-verify counts: " +
			"sqlite3 -readonly ~/corpus/ledger.sqlite3 \"select kind, count(*) from items " +
			"where weight='hard' group by kind\"); " +
			"(B) disclosure ladder has two real rungs, not three (tier3 median 38-148 chars -- " +
			"measured 2026-09-05, carried from the dispatching session; not cheaply " +
			"re-measurable, carried rather than re-verified); " +
			"(C) query-time absence is silent (vanished source => changed results, exit 0, " +
			"no report)",
	},
	{
		ID:      "grounding-coverage-fix",
		Name:    "Dispatch grounding coverage fix (defect A)",
		Status:  NotStarted,
		Caveats: "See knowledge-query's Caveats, defect (A).",
	},
	{
		ID:      "disclosure-ladder-third-rung",
		Name:    "Disclosure ladder third rung (defect B)",
		Status:  NotStarted,
		Caveats: "See knowledge-query's Caveats, defect (B).",
	},
	{
		ID:      "query-absence-reporting",
		Name:    "Query-time absence reporting (defect C)",
		Status:  NotStarted,
		Caveats: "See knowledge-query's Caveats, defect (C).",
	},
	{
		ID:      "agent-memory-v0",
		Name:    "Agent Memory v0 (per-agent storage)",
		Status:  NotStarted,
		Caveats: "Storage format is reserved to Jon -- do not build.",
	},
}
