# Knowledge architecture — execution run, 2026-09-06 16:03 EDT

Authorized by Jon 2026-09-06 (approval of the reconciled plan v2 at
`/tmp/knowledge-plan-review.hybPxD/knowledge-architecture-plan.md`, including
the scoped one-push merge exception). Review trail:
`/tmp/knowledge-plan-review.hybPxD/review-findings.md`.

**Hard checkpoint: Astra returns ~19:15 EDT (3h10m from start).** Whatever is
not merged by then is handed off via `run/handoff-<lane>.md` files and becomes
the next plan's input. Prefer a smaller merged-and-reviewed subset over a
larger unfinished one.

## Cast

- **Workers**: three Claude Sonnet lanes (`--model sonnet --effort medium`,
  launched via `cdsp`) in tmux session `agent-estate`, windows `lane-a`,
  `lane-b`, `lane-c`, one isolated worktree each under
  `~/source/repos/Personal/agent-estate-lanes/`.
- **Director** (codex, `director:2`): manages execution — watches lanes,
  unblocks, enforces sequence, merges reviewed PRs, pings Fable when
  judgement is needed. See `run/director-handoff.md`.
- **Fable** (interactive Claude session with Jon): feedback on ping only;
  does not edit worker files.

## Base state

All three worktrees branched from `origin/main` at `cce87a6`. Branches:
`knowledge/lane-a`, `knowledge/lane-b`, `knowledge/lane-c`. PRs target `main`
in `jonhill90/agent-estate`.

## Ownership (exclusive; from approved plan §3, with the main.go seam pinned)

| Lane | Owns | Must not touch |
|---|---|---|
| A | Vault root navigation (`Start Here.md`, `00`–`05` dirs), vault `agent/ROUTING.md`/`LIFECYCLE.md` corrections, `05 - System/tags.md` vocabulary; repo `docs/knowledge-workflow.md`, AGENTS.md routing link, `.claude/skills/knowledge-session/`, scoped merge decision doc | Vault fact bodies, `internal/catalogue`, `internal/candidates`, `internal/knowledge`, `src/estate/main.go`, Second Brain |
| B | `src/estate/internal/catalogue/`, `src/estate/cmd/sourcecatalogue/` (incl. its `main.go`), catalogue integration in `src/estate/internal/knowledge/`, private register + extraction caches, generated source views (staged in `run/views-staging/`, NOT the live vault) | `src/estate/main.go`, `internal/candidates`, vault (any file), A's docs/skill |
| C | `src/estate/internal/candidates/`, candidate handlers, **`src/estate/main.go` (all shared CLI wiring)**, integration tests, feature registry (`internal/features/registry.go`), `docs/knowledge-workflow-evidence.md` | B's packages' internals (call through B's exported API only), vault (any file), A's docs/skill |

Single-vault-writer rule: only Lane A writes the vault during development.
B's generated views land in `run/views-staging/` and are integrated by the
director at step 4.

## Sequence

1. **A first, immediately**: vault backup with checksums → additive root
   navigation visible in Obsidian → `run/contract.md` (source-record
   frontmatter fields, tag vocabulary, canonical-destination rule). B and C
   may scaffold packages/tests meanwhile but freeze public interfaces only
   after `contract.md` exists.
2. **B** freezes its source-reference API, documents it in
   `run/source-api.md`. **C** builds against that document, not B's code.
3. Integration order: **A's navigation/contract PR → B → C → A's final
   guide/skill PR**. Each later branch rebases on what merged before it.
   A changed head gets a renewed review.
4. Director integrates B's staged views + hands vault writing to itself,
   runs the integrated fixture, the one real publication, refresh, and the
   pre-stated ordinary-task demonstration (`run/demo-task.md`). C records
   results in a final evidence PR, merged last.

## Review and merge protocol (the scoped exception Jon approved)

- Cross-lane reviews: **A reviews C, B reviews A, C reviews B.** A review is
  a PR comment: `Verdict: APPROVE` or `REQUEST CHANGES` + specifics,
  `Review-Lane: <lane id>`, `Reviewed-SHA: <exact head sha>`.
- PR body must carry `Author-Lane: <lane id>` (lane id = `agent-estate:<window index>`,
  stated in each lane's kickoff message).
- **Workers never merge.** The director merges via `gh pr merge --squash`
  only when: an independent cross-lane APPROVE exists for the exact current
  head SHA, and all required checks are green at that SHA. No self-review,
  no fabricated dispatch records. One fix pass per PR; a second failed
  review closes the PR and files what remains.
- Lane A's merge decision doc records this exception as one-push-scoped.

## Demonstration task (pinned NOW, before any publication — R2)

Default wording, in `run/demo-task.md`; the director may replace it only
BEFORE Lane C's real publication happens, never after: the fresh worker gets
an ordinary task, no retrieval keywords, no fact contents fed. Judge: the
director, against pre-stated criteria (finds, cites, and uses relevant
current evidence). A failure blocks the "Delivered" claim, triggers
diagnosis, and feeds K6 inputs — it does not hide the artifacts.

## Standing constraints (bind all lanes)

- Go only for app code. New managed Markdown/lifecycle rules follow OKF 0.2
  (`github.com/GoogleCloudPlatform/open-knowledge-format/blob/main/SPEC.md`);
  report legacy 0.1 incompatibilities, do not bulk-rewrite old records.
- Originals stay authoritative; no copied fact bodies; no Second Brain work;
  no bulk migration; no scheduler/model/capacity changes.
- `estate knowledge` shared index: do NOT regenerate; use `--private`
  working index paths only.
- Private content (transcripts, extractions) never enters Git or PR bodies;
  public evidence is structural/synthetic only.
- Tests: focused Go tests + `go vet` for touched packages; vault validator
  for vault changes; keep actual command output in handoffs.
- Never run destructive tmux verbs. Never touch the Keychain.
