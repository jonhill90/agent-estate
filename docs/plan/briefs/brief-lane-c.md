# Lane C — reviewed distillation and publication

Read `run/execution-plan.md` first; it holds ownership, sequence,
review/merge protocol, and constraints. You are Lane C. Your worktree:
`~/source/repos/Personal/agent-estate-lanes/lane-c`, branch
`knowledge/lane-c`. You do NOT write the vault this run; live fact bodies
are untouchable during development — all lifecycle work runs against an
ISOLATED fixture vault/index you create under your worktree's test data.

## Mission

Generalize the candidates workflow (`src/estate/internal/candidates/`) so a
proposal can cite a catalogue source as well as conversation provenance, and
own all shared CLI wiring in `src/estate/main.go`.

## Deliverables

1. **Source-backed candidates**: a candidate references a stable catalogue
   source id (per `run/source-api.md`, Lane B's frozen API — code against
   the DOCUMENT; stub B's API until its PR merges, then integrate).
   External sources must NEVER require fabricated prompt rows — if the
   current schema demands a prompt row, extend the schema honestly.
2. **Proposal shape**: names the cited learning, intended canonical
   destination (per `run/contract.md`'s destination rule), and the reason.
   Conversation proposals distinguish operator statements from assistant
   context; assistant context is never treated as operator instruction.
3. **Separated states**: approval and publication are distinct. Acceptance
   either publishes a fact through the EXISTING memory mechanism
   (`estate candidates memory`) or produces a repo/docs/skill patch;
   repo publication is recorded only after integration, with path + commit
   receipt. Reject and supersede preserve history and remove obsolete
   content from current retrieval.
4. **`src/estate/main.go` wiring** (yours exclusively): new/changed
   `candidates` handling plus wiring Lane B's exported catalogue functions
   into `estate` subcommands. Coordinate timing: wire B's parts only after
   B's PR merges and you rebase.
5. **Feature registry** (`internal/features/registry.go`): update rows
   honestly for what this run actually delivers — including correcting
   `agent-memory-v0-layout`'s scope now that the root navigation supersedes
   part of it. No "Delivered" without its own evidence.
6. **Integrated fixture test** (in-repo, isolated): invent candidate A and
   B; propose both; accept A; reject B; revise/supersede A; retrieval
   returns ONLY A's current accepted revision; repeated
   registration/publication creates zero duplicates; superseded/rejected
   content does not return as current. Mutation check: break the
   supersede-hides-old-revision logic, show the test fail, restore.
   No fixture content may enter the real vault — assert on the paths.
7. **`docs/knowledge-workflow-evidence.md`**: the final evidence doc —
   sanitized commands/results, limitations, what was NOT proven. This is
   your last PR, merged after integration (the director runs the real
   publication and demonstration; you record results truthfully, including
   a failed demonstration if that is what happened).

## Tests and evidence

Focused `go test`/`go vet` on `internal/candidates` and `main.go` paths you
touch. Every "works" claim carries real command output; "could not measure"
is a legitimate verdict — report it rather than papering over. Private
transcript content stays out of Git and PR bodies. PR body: `Author-Lane:`
per the plan. You review Lane B's PR (verdict comment per protocol). Write
`run/handoff-lane-c.md` before 19:15 EDT.
