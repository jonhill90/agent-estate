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
			"~/corpus/ledger.sqlite3 (renamed corpus.sqlite3, agent-estate#P6; re-verify: sqlite3 -readonly ~/corpus/corpus.sqlite3 " +
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
		Evidence: "PR #1241 (merged, main@2a04cf8's ancestor a879206) widened Hard() to " +
			"kind IN (parameter, directive, correction), not parameter alone -- but that " +
			"widening filtered weight and kind only, never status, so 'dropped' (retired) " +
			"and 'needs_review' (unconfirmed) hard rows were still injected as law. Fixed " +
			"in agent-estate#1139: Hard() now excludes status IN (dropped, needs_review) " +
			"and returns what it excluded so the omission is reported, never silent " +
			"(Grounding() renders an 'excluded as not-currently-law' line with per-status " +
			"counts). Re-verified 2026-09-06: sqlite3 -readonly ~/corpus/corpus.sqlite3 " +
			"\"select status, count(*) from items where weight='hard' and kind in " +
			"('parameter','directive','correction') group by status\" -> acted 1843, " +
			"acknowledged 299, resolved 236, open 52, dropped 32, needs_review 10 " +
			"(total 2472); eligible-as-law count is 2472 - 32 - 10 = 2430, confirmed by " +
			"both a direct SQL count and Hard()'s own live-corpus run. " +
			"src/estate/internal/corpus/corpus.go's Hard() and Grounding().",
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
		Evidence: "PR #1242 (main@2a04cf8) shipped CoverageSourceMissing and the " +
			"\"*** SOURCE GONE ***\" banner, but a post-merge review (comment " +
			"5556852578) found indexDependsOn in main.go matched the vault by the " +
			"literal \"vault-fact\" while vaultSource()'s real SourceResult.Name is " +
			"\"vault-facts\" (plural) -- the vault, the PR's own headline source, " +
			"silently never triggered the banner in production; loops-research and " +
			"corpus-db were unaffected. The fixtures behind " +
			"TestKnowledgeQueryProseNamesSourceGoneLoudly and " +
			"TestKnowledgeQueryJSONCoverageNamesMissingSource had independently " +
			"typed \"vault-fact\" as SourceResult.Name too, so they agreed with the " +
			"bug instead of catching it. Follow-up (this dispatch, agent-estate#1139): " +
			"indexDependsOn now shares knowledge.SourceNameMatches (the same " +
			"trailing-\"s\"-tolerant comparison failedSourceForTag already used) " +
			"instead of a second literal; vaultSource's Name/Source strings are now " +
			"exported constants (knowledge.VaultSourceName, " +
			"knowledge.VaultItemSourceTag) and the test fixtures derive from them " +
			"instead of retyping the literals. Re-verify: go test ./src/estate -run " +
			"'TestKnowledgeQueryProseNamesSourceGoneLoudly|" +
			"TestKnowledgeQueryJSONCoverageNamesMissingSource' -v (both PASS, re-run " +
			"2026-09-06, now against a fixture Name of \"vault-facts\" matching " +
			"production) and a real built binary: build a vault fixture, `estate " +
			"knowledge` to index it, move the vault directory away, `estate " +
			"knowledge query <question>` -- the SOURCE GONE banner names " +
			"agent-memory-vault (re-run 2026-09-06).",
	},
	{
		ID:     "agent-memory-v0",
		Name:   "Agent Memory v0 (durable per-agent storage format)",
		Status: NotStarted,
		Caveats: "Storage format is reserved to Jon -- do not build. Distinct from " +
			"agent-memory-v0-layout below (a visible directory/routing surface over " +
			"the existing vault) and from the standing-law injection mechanism " +
			"(agent-estate#1255, PRs #1260/#1261): that mechanism injects a small, " +
			"human-declared set of Agent Memory facts into every dispatch preamble as " +
			"law -- it is a retrieval/grounding feature, not a storage format, and its " +
			"being delivered does not advance this row.",
	},
	{
		ID:     "agent-memory-v0-layout",
		Name:   "Agent Memory v0 -- visible layout and routing surface",
		Status: Delivered,
		Evidence: "Verified 2026-09-06 against the live vault at $AGENT_MEMORY_VAULT " +
			"(an iCloud Obsidian vault, not tracked by git -- evidence is filesystem " +
			"state plus a dispatched-agent run, not a PR diff): " +
			"`agent/00 - Inbox/README.md` exists and is empty of candidates by design " +
			"(it explains that unreviewed candidates live in the corpus's " +
			"knowledge_candidates table, not as vault files); `agent/ROUTING.md` is 34 " +
			"lines (re-verify: wc -l); `agent/LIFECYCLE.md` is 42 lines (re-verify: " +
			"wc -l) and names the candidate -> accepted -> superseded -> rejected " +
			"lifecycle plus which stages are not automated; `agent/index.md` gained " +
			"exactly one additive pointer line at line 10 (\"New here? Start at " +
			"ROUTING.md ...\"), otherwise unchanged (118 facts before and after, " +
			"118 bullets in index.md, re-verify: grep -c '^- ' agent/index.md and ls " +
			"agent/facts | wc -l), and `tools/validate_index.py` (re-verify: `cd " +
			"agent && python3 tools/validate_index.py`) passes with 14 pre-existing " +
			"soft frontmatter warnings and no hard violations. Fresh-agent navigation " +
			"proof (run by the Director through a real `estate dispatch`, task naming " +
			"no paths and not mentioning ROUTING.md): the agent discovered ROUTING.md " +
			"and `00 - Inbox/README.md` unaided, described the layout, and located an " +
			"accepted fact by title and path (facts/python-package-manager-uv.md), " +
			"read-only, no writes.",
		Caveats: "Scope limit, stated plainly: this delivers a visible, governed " +
			"layout and routing surface only. It does not deliver candidate " +
			"review-and-promotion as an operator workflow (see " +
			"candidate-knowledge-inbox below, still InProgress), and it does not make " +
			"retrieval reliable (see knowledge-query's own Caveats). Distinct from the " +
			"standing-law injection mechanism (agent-estate#1255, PRs #1260/#1261): " +
			"that PR pair injects one declared class of facts into the dispatch " +
			"preamble as unconditional law -- a retrieval/grounding change, not a " +
			"layout or lifecycle change -- and does not by itself constitute organized " +
			"Agent Memory. Neither this row nor those PRs should be read as having " +
			"delivered organized Agent Memory as a whole; that remains the sum of " +
			"several still-partial rows in this ledger.",
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
			"status='candidate'. This dispatch (agent-estate#1139) adds the review " +
			"surface #1244 deliberately left unbuilt: `estate candidates list` " +
			"(filters by source file and pages in ingestion order; measured " +
			"2026-09-06 against a corpus copy: 4,360 candidates over 472 distinct " +
			"source files), `estate candidates show <id>` (resolves the cited " +
			"prompt through prompt_id/provenance_id on demand, never a copy), and " +
			"`estate candidates decide <id> promote|discard`, gated exactly like " +
			"cmd/codexingest's own -apply/-authorized-live-write/banner/SameFile " +
			"guard shape.",
		Caveats: "Quarantine and citation only. A promote/discard decision marks a " +
			"candidate reviewed; it never writes status (still always 'candidate') " +
			"and never moves anything into a durable knowledge store -- that " +
			"storage format is reserved to Jon and remains open. Do not read this " +
			"row as promotion (moving data into durable knowledge) working. Stays " +
			"InProgress, not Delivered: the non-standing retrieval gate " +
			"(agent-estate#1255), measured 2026-09-06 by the Director through real " +
			"`estate dispatch` runs, scored 2 of 3 -- one of three fresh agents never " +
			"consulted the knowledge base at all. \"Demonstrably usable by a fresh " +
			"agent\" is not true at 2-of-3, and this row's own review surface " +
			"(`estate candidates list/show/decide`) has not been separately gated by " +
			"that measurement.",
	},
	{
		ID:     "source-backed-candidates",
		Name:   "Source-backed candidates: catalogue citations, generalized proposal shape, repo-publication receipts",
		Status: InProgress,
		Evidence: "This dispatch (run/execution-plan.md's knowledge-architecture run, Lane C): " +
			"internal/candidates now derives TWO citation shapes into the same knowledge_candidates " +
			"queue -- 'conversation' (the original prompt_id/provenance_id citation, unchanged) and " +
			"'catalogue' (a new RegisterCatalogueSource entry point, wired as `estate candidates " +
			"register-source`) -- without either fabricating a row it doesn't have: a catalogue row's " +
			"prompt_id/provenance_id stay empty rather than pointing at an invented prompts row, which " +
			"required rebuilding the table's inline UNIQUE(prompt_id) constraint into two partial " +
			"unique indexes (SQLite cannot loosen an inline NOT NULL/UNIQUE via ALTER TABLE); a lazy " +
			"migration (migrateToSourceKindSchema) upgrades a live corpus's existing old-shape table on " +
			"first catalogue use, proven by TestRegisterCatalogueSourceMigratesOldShapeTable. The " +
			"Proposal shape gained required destination_kind/destination/reason fields (a proposal now " +
			"names WHERE it lands and WHY, not just what it says); acceptance either publishes through " +
			"the existing Agent Memory mechanism (Publish, unchanged for destination_kind=memory) or " +
			"records a repo/docs/skill patch receipt (new PublishRepo, destination_kind=repo) -- the " +
			"receipt requires an exact path match against what was proposed and only records AFTER " +
			"integration, never writing the repo file itself. Integrated fixture: " +
			"TestIntegratedSourceBackedWorkflow proposes a conversation candidate (A) and a catalogue " +
			"candidate (B), accepts A, rejects B, revises/supersedes A, and asserts retrieval returns " +
			"only A's current revision with zero duplicates on rerun -- entirely against a t.TempDir() " +
			"corpus/vault/index, never the real vault. Mutation check performed and reverted (not " +
			"committed): disabling internal/knowledge's currentMemoryItem staleness gate entirely " +
			"turned this fixture RED (`stale index (generated before the supersede) served A's " +
			"superseded revision`); restoring the gate returned it to PASS -- see " +
			"docs/knowledge-workflow-evidence.md for the full command transcript. Re-verify: `go test " +
			"./internal/candidates/... . -count=1` from src/estate.",
		Caveats: "Lane B's catalogue API was not yet frozen/merged when this shipped: CatalogueSource " +
			"is this package's own stub shape (id/title/location/content_hash), populated by " +
			"main.go from operator-supplied flags, not from a real catalogue lookup -- integrating " +
			"Lane B's actual exported functions once merged is follow-up work, expected to change only " +
			"the register-source call site, not internal/candidates itself. PublishRepo records a " +
			"receipt only; it has not yet been exercised against a REAL repo integration (only the " +
			"isolated fixture in TestPublishRepoRecordsReceiptOnlyOnExactDestinationMatch and the CLI " +
			"round-trip in TestCandidatesMemoryRepoDestinationCLI). Does not change or depend on " +
			"candidate-knowledge-inbox's own remaining gaps below.",
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
