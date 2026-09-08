# P12 execution plan — docs standard, routing surfaces, live pointers
Fable, 2026-09-07 15:05. Supersedes the reactive queue append. Phased,
gated, one repo per lane, judgment separated from mechanics.

## Why this is phased (the risks a reactive sort trips)

- agent-estate `AGENTS.md` ⟷ `CLAUDE.md` are ONE FILE (symlink), and
  `src/estate/agents_md_test.go` + `internal/corpus/agents_md_test.go`
  FAIL THE BUILD if it names an estate subcommand or corpus path that
  doesn't exist. A wholesale rewrite breaks the build or silently drops
  binding rules (ask-Jon-last, the invariants, the historical guards
  record — which is deliberate history, not staleness).
- agent-dotfiles has the same AGENTS/CLAUDE symlink arrangement.
- W4 precedent: unreviewed classification produced the half-sort being
  cleaned now. Judgment BEFORE moves, reviewed, or it happens again.
- skills docs contain eval-log/ and JSON state that other tooling may
  read — moving before grepping consumers repeats the P0 failure.

## Phase 1 — Disposition tables (judgment, no moves) — 3 lanes, parallel

One lane per repo (estate / dotfiles / skills). Each produces
`run/p12-<repo>-disposition.md`: EVERY file under docs/ → one of
canonical | historical | research | corpus | relocate-out-of-docs (state
files, logs) | DELETE-CANDIDATE (listed for Jon, never executed), with a
one-line reason and a consumer check (`grep -rn` for each filename
across the repo + estate + vault; list every hit). Zero file moves in
this phase. Cross-lane review of each table (reviewer ≠ author).
GATE: three reviewed tables, every docs/ file dispositioned, every
consumer listed.

## Phase 2 — Mechanical moves + lint (tools, no judgment) — same lanes

Execute each reviewed table exactly: moves, tombstone stubs where any
consumer was found (3-line: what/why/where), consumers repointed in the
same PR. SHIP `scripts/docs-lint` (or equivalent) in the same PR, wired
into that repo's CI: no unclassified root files (allowlist for entry
points like PRD/SPEC/index), no state files in docs/, zero full-text
duplicates by checksum. Prove the lint fails before the sort and passes
after (red/green in the PR body).
GATE: three merged PRs, lint green in CI, deletables list posted for Jon.

## Phase 3 — Routing surfaces (surgical, not rewrite) — sequential per repo

README.md + AGENTS.md per repo: SURGICAL refresh — correct stale claims
(dated, per estate convention: verify each count/path before writing
it), add/update the routing index to the phase-2 layout, PRESERVE
binding rules and deliberate history. For estate: run
`go test ./src/estate/... -run AgentsMD` (and the corpus twin) locally
before pushing; the tests are the contract. Symlink pairs edited once,
not twice. Cross-lane review with an explicit lens: "did any binding
rule or test-enforced claim get dropped?"
GATE: three merged PRs, estate build green, a stranger-test walk (open
README → reach any canonical doc ≤2 hops) recorded per repo.

## Phase 4 — Live repo pointers (code, one lane)

Small spec first (half page in the PR): `estate catalogue ensure-local
<name>` (or smallest coherent verb) — records/updates local clone path
on a pointer record; disclosure guidance (knowledge-workflow.md) gains
the local-first rule: pointer → local clone if recorded → GitHub read →
clone-then-update-record. Concurrency: last-write-wins on refresh is
fine (records are observations, not locks — say so in the doc comment).
Tests + mutation check (missing-local state preserved, never guessed).
GATE: verb merged; workflow doc updated; one real walk demonstrated in
the PR (agent resolves a repo local-first after ensure-local).

## Sequencing & ownership

Phase 1 lanes run parallel (disjoint repos). Phase 2 follows each lane's
own reviewed table (no cross-repo barrier). Phase 3 sequential AFTER
that repo's phase 2 (routing must describe the sorted layout). Phase 4
independent — can run parallel to phases 2-3 (estate code only, no docs
overlap). MOC gate-removal work (already dispatched) is estate code +
vault — no collision with any of this except `docs/knowledge-workflow.md`,
which phase 4 also touches: phase 4 owns that file; MOC work must not
edit it.

## Explicitly out

No deletions executed (lists only). No wholesale AGENTS.md rewrites. No
Hill90. Second Brain untouched. The protected shared index untouched.
Mechanize items beyond docs-lint (batch wrapper, vault-census,
auto-merge, drift check) stay Push 5 — planned there, not smuggled here.
