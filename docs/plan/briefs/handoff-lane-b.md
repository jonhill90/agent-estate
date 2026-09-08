# Lane B handoff — source library and retrieval

**MERGED.** PR: https://github.com/jonhill90/agent-estate/pull/1264
(branch `knowledge/lane-b` → `main`), merge commit `37c33e3`
(`37c33e30f321e1e85066d65d2346af5eaa036e14`), merged 2026-09-06T20:45:38Z.
`Author-Lane: agent-estate:2` is in the PR body. I did not merge it myself
— the Director merged after Lane C's independent cross-lane review at the
rebased head SHA `da81191`. This file was originally written at 16:29,
before the merge landed; this update (post-merge) is the accurate record
— read this version, not any cached copy of the pre-merge one.

Rebase note: this branch was rebased onto Lane A's merged `main`
(`24ca850`) before Lane C's review, per the Director's instruction that a
changed head voids a prior review — the head Lane C actually reviewed and
the head that merged are the same commit, `da81191` → `37c33e3` after
squash.

## Status: shipped and merged. Lane C's own PR (#1265) has since merged on top.

All eight deliverables from `run/brief-lane-b.md` shipped in the merged
PR, verified against real data on this machine (not just fixtures) — see
the PR body (linked above) for full command output. Summary:

1. **Persistent private register** — `~/.local/state/agent-estate/catalogue/register.json`.
   Fields realign onto `run/contract.md` (landed mid-run; I scaffolded
   before it existed, then refactored onto its exact field names once it
   landed — see `internal/catalogue/register.go`'s own doc comment for the
   two fields that are intentionally NOT contract fields, `ExtractionKind`
   and `Status`, and why).
2. **`register`/`list`/`show`/`refresh`** on `cmd/sourcecatalogue` — its
   own `main.go`, `src/estate/main.go` untouched. No-arg health invocation
   unchanged (verified: same JSON shape as before).
3. **Three source kinds proven live**: pdf (`pdftotext`, cross-checked
   against the seed arXiv PDF's recorded SHA-256 — matched exactly),
   repo-docs (file/directory manifest hash + content cache), conversation
   (health/unit-count only, extraction explicitly not applicable).
4. **Idempotent registration** — identity is `Locator` alone; re-
   registering never duplicates. Mutation-tested (see PR body): broke the
   identity hash to ignore locator, confirmed the differentiation test
   fails, reverted.
5. **Drift detection** — `refresh` flips to `needs_review` on any revision
   change, never touches operator-declared fields. `Acknowledge` is the
   only path back to `active`.
6. **Generated OKF 0.2 source views** — `run/views-staging/01 - Sources/`
   (this directory, sibling to this handoff, never the live vault). Four
   real views currently staged there from the verification run: AGENTS.md,
   `~/.codex/sessions`, `~/.claude/projects`, the seed arXiv PDF.
7. **Retrieval integration** — `catalogue-source` is `internal/knowledge`'s
   sixth `Generate` source, private by default, read-only over the
   register. Verified live against `ESTATE_KNOWLEDGE_INDEX` (never the
   shared index) — a `catalogue-source` item ranked and returned for
   `knowledge query --private "orientation doc"`.
8. **`run/source-api.md`** — frozen for Lane C, in this directory.

## Cross-lane review I owe (B reviews A)

Done. Lane A's PR #1263 — `Verdict: APPROVE`, `Review-Lane: agent-estate:2`,
`Reviewed-SHA: f1a680add7bb315736eb574f6563af01f73328a5`, posted as a plain
PR comment:
https://github.com/jonhill90/agent-estate/pull/1263#issuecomment-5561935149

Verification behind that verdict: cloned the PR at the exact head SHA,
ran its cited tests (all pass), ran `go vet ./...` clean, mutation-tested
its golden-set count guard (removed a case, confirmed
`TestLoadNaturalParsesEmbeddedCases` fails naming the exact count, reverted,
confirmed it passes again), and independently reproduced every CLI claim
in its PR body (`candidates derive` rejected, `candidates decide`'s
positional-args usage string, `vault-view` absent from the whole repo).

## What's unfinished, shaky, or open — for Astra's next-plan work, not fixed tonight

- **Staging path will need to move: `01 - Sources` vs. the new INMAPS
  `05 - Sources`.** `internal/catalogue/views.go`'s `WriteViewsStaging`
  hardcodes `"01 - Sources"` as the subdirectory under the staging root —
  this predates the INMAPS spec, which places sources at `05 - Sources`
  instead. The Director confirmed this reconciliation (one constant plus
  its test) is deliberately **not** hot-fixed tonight and is Astra's next-
  plan work. The four real views this run staged into
  `run/views-staging/01 - Sources/` (AGENTS.md, `~/.codex/sessions`,
  `~/.claude/projects`, the seed arXiv PDF) sit at the OLD path and will
  need to move (or be regenerated at the new one) once that constant
  changes — do not assume they're already at `05 - Sources`.
- **Contract realignment happened mid-implementation.** I scaffolded
  `RegisterEntry` before `run/contract.md` existed (per the plan's own
  timing), then refactored onto its exact field names once it landed. If a
  future reviewer spots a contract field mapped questionably, the three I
  added beyond the contract (`Scope`, `Owner`, `WhyIndexed` — all three
  predate the contract, kept because they answer questions the contract's
  own fields don't) are the ones most worth a second look.
- **`internal/knowledge`'s five→six source count** is updated and tested
  in the merged PR (`classify_test.go`'s `wantPublic` map,
  `generate_test.go`'s source-count assertion) — but I have not swept the
  rest of that package (or `src/tui`, if it reads the compiled index) for
  another place that assumed exactly five sources by number rather than by
  name. Worth a grep before the next person adds a seventh.
- **No `estate catalogue ...` / `estate sources ...` top-level subcommand
  exists.** Lane C's PR (#1265, merged after mine) owned `src/estate/
  main.go` wiring for candidates; I did not see whether it also wired a
  catalogue-facing subcommand there. Everything Lane B built is exported
  and documented in `run/source-api.md` for whoever does that wiring —
  today the only way to reach `register`/`list`/`show`/`refresh` is the
  standalone `cmd/sourcecatalogue` binary directly.
- **`run/views-staging/01 - Sources/`** currently holds four real, live-
  verified views (not synthetic fixtures) from my own verification run.
  Nothing has moved them into the actual vault — that's the Director's
  integration step (plan step 4), and I never wrote to the vault myself.
- **Only one of the two allowed third kinds was proven.** The brief asked
  for repo/docs OR skill/tool; I proved repo/docs (the smaller lift, given
  `run/contract.md`'s `kind/skill`/`kind/tool` tags already exist for
  whoever registers one of those later) and did not attempt skill/tool at
  all — it has no extraction path in `internal/catalogue/extract.go` today
  and would need one.
- **Register scale is untested beyond four real entries and a handful of
  fixtures.** `LoadRegister`/`SaveRegister` read and rewrite the entire
  `register.json` on every call — fine at this run's scale, unmeasured at
  whatever scale a later "register every repo doc" pass would produce.

## Cross-lane review on this PR (C reviewed B)

Lane C reviewed #1264 at the exact rebased head `da8119179b88519b7d83adf08b6c75bd9210e097`
— `Verdict: APPROVE`, `Review-Lane: agent-estate:3` — before the Director
merged it as `37c33e3`. One non-blocking finding worth carrying forward:
the mutation-check command I pasted into the PR body (`sed ...
sha256.Sum256([]byte(kind))`) doesn't actually compile against
`identityFor(locator string)` on the merged code — `kind` isn't in scope
there after the contract realignment moved identity to `Locator` alone. I
had run a real, compiling mutation before that realignment (mutating the
two-argument `identityFor(kind, locator)`) and transcribed the wrong
version into the PR body afterward. Lane C caught this, re-ran a mutation
that does compile against the merged code (`Sum256([]byte("constant"))`),
confirmed the guard still fails RED / passes GREEN correctly, and approved
on that basis. The guard itself is genuine; only my PR-body transcription
was stale. No code fix needed — noting it here so nobody re-pastes the
stale command from the PR body as if it were still accurate.

## Verification commands (paste-ready, already run — see PR body for full output)

```
go build ./... && go vet ./... && go test ./...   # whole module, all green, at merge time
```
