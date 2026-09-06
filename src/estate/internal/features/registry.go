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
		Caveats: "Defects A, B and C (dated 2026-09-05) are fixed as of PRs #1241 and " +
			"#1242 -- see the grounding-coverage-fix, disclosure-ladder-third-rung and " +
			"query-absence-reporting rows below for evidence and re-verification commands. " +
			"Remaining known gaps: github-stars and repo-docs keep tier2-only depth by " +
			"documented design (no deeper local material for github-stars; out of scope " +
			"for repo-docs) -- only vault-fact, loops-research and corpus-* items gained a " +
			"genuine tier3.",
	},
	{
		ID:     "grounding-coverage-fix",
		Name:   "Dispatch grounding coverage fix (defect A)",
		Status: Delivered,
		Evidence: "PR #1241 (merged, main@2a04cf8's ancestor a879206): grounding now " +
			"injects Hard() over kind IN (parameter, directive, correction), not " +
			"parameter alone. Re-verified 2026-09-06: sqlite3 -readonly " +
			"~/corpus/ledger.sqlite3 \"select kind, count(*) from items where " +
			"weight='hard' group by kind\" -> correction 173, directive 1341, " +
			"parameter 958 (958+1341+173 = 2472, up from 958); " +
			"src/estate/internal/corpus/corpus.go's Hard() reads all three kinds.",
	},
	{
		ID:     "disclosure-ladder-third-rung",
		Name:   "Disclosure ladder third rung (defect B)",
		Status: Delivered,
		Evidence: "PR #1242 (main@2a04cf8): tier3 for vault-fact and loops-research is " +
			"now the entire source file verbatim, and corpus-* tier3 is the item's " +
			"untruncated body plus weight/status/resolved_to metadata -- genuinely " +
			"deeper than tier2, not shorter. Re-verify: go test ./src/estate/... -run " +
			"'TestVaultSourceTier3DeepensPastTier2|TestLoopsSourceTier3DeepensPastTier2|" +
			"TestCorpusSourceTier3DeepensPastTier2' -v (all three PASS, re-run " +
			"2026-09-06). github-stars and repo-docs are documented exceptions left at " +
			"tier2 (see knowledge-query's Caveats).",
	},
	{
		ID:     "query-absence-reporting",
		Name:   "Query-time absence reporting (defect C)",
		Status: Delivered,
		Evidence: "PR #1242 (main@2a04cf8): a source that read successfully at index-" +
			"build time but is unreachable at query time now reports its own " +
			"CoverageSourceMissing state with a \"*** SOURCE GONE ***\" banner in " +
			"prose, and names the missing source in --json coverage, instead of " +
			"folding silently into 'unknown'. Re-verify: go test ./src/estate -run " +
			"'TestKnowledgeQueryProseNamesSourceGoneLoudly|" +
			"TestKnowledgeQueryJSONCoverageNamesMissingSource' -v (both PASS, re-run " +
			"2026-09-06).",
	},
	{
		ID:      "agent-memory-v0",
		Name:    "Agent Memory v0 (per-agent storage)",
		Status:  NotStarted,
		Caveats: "Storage format is reserved to Jon -- do not build.",
	},
	{
		ID:     "feature-completion-ledger",
		Name:   "Feature-completion ledger (this instrument)",
		Status: Delivered,
		Evidence: "PR #1243 (merged; re-verify with `gh pr view 1243`): introduces this " +
			"package and the `estate features` command, hand-maintained, never " +
			"inferred from git/gh activity.",
	},
	{
		ID:     "candidate-knowledge-inbox",
		Name:   "Candidate knowledge inbox (quarantined, from Codex provenance)",
		Status: InProgress,
		Evidence: "PR #1244 (merged; re-verify with `gh pr view 1244`): " +
			"internal/candidates derives a cited, reviewable queue of CANDIDATE " +
			"records from codex_provenance, permanently quarantined at " +
			"status='candidate'.",
		Caveats: "Quarantine and citation only. Promotion of a candidate into durable " +
			"knowledge -- or discarding one -- is a separate, later, reviewed act that " +
			"this package deliberately does not build; nothing in it ever writes any " +
			"status other than 'candidate'. Do not read this row as promotion working.",
	},
	{
		ID:     "agents-md-progressive-disclosure",
		Name:   "AGENTS.md progressive-disclosure split",
		Status: Delivered,
		Evidence: "PR #1245 (merged; re-verify with `gh pr view 1245`): AGENTS.md shrinks " +
			"631 -> 111 lines; detailed sections move verbatim to docs/orientation/" +
			"{daemon,invariants,conventions,tui-arrival,go-only}.md. Re-verify: " +
			"wc -l AGENTS.md (111, measured 2026-09-06) and " +
			"git show 5f0caab^:AGENTS.md | wc -l (631).",
	},
}
