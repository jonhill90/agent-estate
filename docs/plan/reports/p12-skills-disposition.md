# P12 Phase 1 — docs/ disposition table, jonhill90/skills

Author: lane-c (agent-estate:3). Judgment only — **zero file moves made**.
Verified against `origin/main` at `36b820661bc44bbeb66a1a0243757321932b17ac`
(fetched fresh, confirmed via `git rev-parse HEAD origin/main` matching
after a hard reset — this run had an earlier reactive pass that made and
then fully reverted a set of `git mv`s on a throwaway local branch; nothing
from that pass was ever committed or pushed, and the branch was deleted).
Consumer-check cross-repo target: agent-estate at
`8ba75ac4e626e6872c69b333355fb7f1c16f0465` (fresh clone, same session).
Consumer-check vault target: `$AGENT_MEMORY_VAULT`, read-only grep, no
writes.

**Repo is PUBLIC.** No private skill name, path, or content appears
anywhere below — every citation here is either a public skill already
listed in this same public repo, or a public repo path/line number.
Counts only where a private surface would otherwise be implied (none
were; this repo has no private skills of its own — see the third-party /
authorship section).

## Method

`find docs -maxdepth 2 -type f` against the verified origin/main checkout
→ 58 files (14 `.md`, 3 `.json`, 41 `.jsonl` under `docs/eval-log/`). For
every file's basename, one combined `grep -rnF --exclude-dir=.git` pass
(not 58 separate tree walks — the first attempt at per-file grep loops
stalled/timed out against the iCloud-synced vault; a single multi-pattern
pass across each target tree is what actually completed) against: this
repo, the agent-estate clone above, and the vault. Every hit reported
below was read at its cited line to confirm it is a real reference, not
a coincidental substring collision (none turned out to be).

## Disposition legend

`canonical` (current standing reference) · `historical` (dated
investigation/decision record, superseded-in-place or settled) ·
`research` (open proposal, not yet decided) · `corpus` (raw, per-unit
append-only evidence records — still needs to leave `docs/`, labeled
separately from `relocate-out-of-docs` only to keep "generated snapshot"
distinct from "raw log") · `relocate-out-of-docs` (derived/generated
state, must leave `docs/` per the standing rule) · `DELETE-CANDIDATE`
(listed for Jon, never executed by this table or Phase 2).

## The 14 `.md` files

| File | Disposition | Reason | Consumers found (repo / estate / vault) |
|---|---|---|---|
| `docs/audit-install-parity-270.md` | historical | Dated audit (#270) of a settled question; no open recommendation. | None in any of the three trees. |
| `docs/could-not-measure-vocabulary-296.md` | historical | States its own disposition: "landed" (#296), a completed relabeling PR record. | None. |
| `docs/eval-arm-wiring-retrofit-gap-287.md` | historical | Frontmatter `type: Diagnosis`; a diagnosis of a past gap, not an ongoing contract. | Cites `docs/eval-instrument-diagnosis-2026-08-23.md` at its own line 39 (repo, cross-doc link only — no code consumer). |
| `docs/eval-ask-a-council-266.md` | historical | States "Disposition: landed" up top. | Repo: `skills/ask-a-council/references/eval-result.md:23` cites this doc by path as its own evidence citation. |
| `docs/eval-cost-axis-principle.md` | **canonical** | The one file whose own text states a standing scoring principle "independent of that one case" for how ANY future counting-measurement eval scenario must pick its cost axis — not a report of what happened, a rule for what happens next. Only canonical candidate found. | None. |
| `docs/eval-cost-delta-recount.md` | historical | Dated recount (#266) answering a specific past question. | Repo: cites `docs/eval-harness-findings.md:90` and `docs/eval-status.json:202` (cross-doc/data links only). |
| `docs/eval-harness-adopt-or-build.md` | historical | Dated build-vs-adopt decision record ("This check was never run before now" — a one-time historical determination). | Repo: cites `docs/eval-harness-findings.md:7`, `docs/eval-longitudinal-design.md:75`, `docs/eval-status.json:23` (cross-doc links). |
| `docs/eval-harness-findings.md` | historical | Explicitly "supersedes its own earlier 2026-08-23 version" — a dated findings report, self-superseded in place, not two files. | **Real, load-bearing, multi-consumer path.** Repo: `tests/test_skill_read_confirmed.py:192`, `scripts/skill_read_confirmed.py:10` (production code + its test — both cite the path in a docstring/comment, not a runtime file-open — confirmed by reading both lines), `docs/eval-pass15-remaining-four.md:18`, `docs/eval-instrument-diagnosis-2026-08-23.md:24`, `docs/eval-longitudinal-design.md:3`, `docs/eval-cost-delta-recount.md:90`, `docs/eval-harness-adopt-or-build.md:7`, and four skills' own `eval-result.md` citations (`skills/linear`, `skills/tmux`, `skills/github-cli`, `skills/obsidian`). **A path this widely cross-referenced needs a tombstone if moved** (Phase 2). |
| `docs/eval-instrument-diagnosis-2026-08-23.md` | historical | Frontmatter `type: Diagnosis`; dated, answers "why 29 of 41 evals read could_not_measure" as of that date. | Repo: cited by `docs/eval-arm-wiring-retrofit-gap-287.md:39` and cites `docs/eval-harness-findings.md:24` itself (cross-doc, mutual). |
| `docs/eval-longitudinal-design.md` | **research** | Explicitly proposes something "recommended, but never built" — an open design, not a decision or a report of what happened. | Repo: cites `docs/eval-harness-adopt-or-build.md`, is cited BY `docs/eval-harness-findings.md:252` and `docs/eval-status.json:202`. |
| `docs/eval-pass15-remaining-four.md` | historical | Dated pass report ("Recorded 2026-08-23... This pass picked zero new skills"). | Repo: cited by three skills' `eval-blocked.md` files (`linear`, `github-cli`, `obsidian`), each at line 4. |
| `docs/loop-tick-placement-160.md` | historical | States "decision landed" up top (verified 2026-08-16, #160 closed with the "do not move" recommendation as the last word). | Repo: cited by `docs/skills-docs-proposal-161.md:13`. |
| `docs/skills-docs-proposal-161.md` | **research** | States "still an open proposal — no recorded decision from Jon accepts or rejects any of its four recommendations," verified 2026-08-16. | Repo: cites `docs/loop-tick-placement-160.md`. |
| `docs/stale-truth-pass-2026-08-23.md` | historical | Dated truth-pass report with an inline self-correction, a record of a specific pass. | None. |

## The 3 top-level JSON files (all `relocate-out-of-docs`)

| File | Disposition | Reason | Consumers found (repo / estate / vault) |
|---|---|---|---|
| `docs/eval-status.json` | relocate-out-of-docs | Generated/derived state: `scripts/eval_status.py`'s own docstring states it is rebuilt entirely from `docs/eval-log/*.jsonl` and "kept... through regeneration" — never hand-edited. This is exactly the "jsonl/json state OUT of docs/ entirely" class the standing rule names. | **The widest, highest-risk consumer set found in this whole sweep.** Repo: hardcoded in `scripts/eval_status.py:2` (`RECORD_PATH = REPO / "docs" / "eval-status.json"`, confirmed by reading the constant directly, not just the docstring hits), cited in `.gitattributes:3`, `tests/test_eval_status.py` (extensive, builds its own synthetic `docs/` fixture dirs — does not depend on the production default path, confirmed by reading the test setup), and cited by name in 24 skills' own `eval-result.md` files. **Cross-repo: agent-estate's TUI hardcodes this exact relative path as a Go constant** — `src/tui/cmd/estate/skills.go:11`: `const skillsEvalStatusRelPath = "docs/eval-status.json"` (confirmed by reading the constant, not inferred from the doc comment above it) — consumed by `src/tui/internal/skills/evalstatus.go`, `src/tui/cmd/estate/skills_test.go`, `src/tui/cmd/skillinvocations/main.go`, plus a VHS test tape and a nav-walk observation fixture. **Moving this file without updating that Go constant breaks agent-estate's skills-eval TUI pane.** Vault: none. |
| `docs/skills-environment.json` | relocate-out-of-docs | Push-4 generated snapshot input to `reconcile_skills.py`'s `build()` — read-only observation data, not documentation. | Repo: hardcoded in `scripts/reconcile_skills.py:297` (`observation=REPO/'docs/skills-environment.json'`). No estate or vault hit. |
| `docs/skills-reconciliation.json` | relocate-out-of-docs | **This is the Push-4 generated manifest constraint (3) names explicitly.** Its own generator (`scripts/reconcile_skills.py`) must reproduce its output byte-for-byte; I independently re-verified this in PR #304's review (`python3 scripts/reconcile_skills.py --check` → `Verified 41 public skill records; changed=0`, and separately a real write-mode regenerate + `git diff --exit-code` → clean). **Phase 2 must re-run the generator after relocating this file (and updating the generator's own hardcoded output path + the README render string that names `docs/skills-reconciliation.json` verbatim), then prove `--check` is green again at the new path — a byte-identical claim can only mean "self-consistent and reproducible at its new location," since the file's own rendered content includes its own path string.** | Repo: hardcoded in `scripts/reconcile_skills.py:256,276` and `README.md:49` (`The [machine-readable manifest](docs/skills-reconciliation.json)...`). No estate or vault hit. |

## The 41 `docs/eval-log/*.jsonl` files — `corpus`

One disposition for the whole set: **`corpus`** — each is a per-skill,
append-only raw observation log (`scripts/eval_status.py`'s own
`EVAL_LOG_DIR` target), the source-of-truth `docs/eval-status.json` is
regenerated FROM. Distinguished from `relocate-out-of-docs` above only to
keep "raw per-unit evidence" and "derived summary/manifest" separately
labeled for Phase 2 — **both dispositions mean the same mechanical
action: leave `docs/` entirely.**

Every one of the 41 basenames was checked individually (single combined
grep pass, not sampled) against all three trees. Result:

- **40 of 41** have no consumer beyond the two already-covered, generic
  facts: (a) `docs/eval-status.json` itself references every log file by
  name in its own generation code path (`scripts/eval_status.py`,
  already covered above — this is the SAME dependency already listed,
  not a new one per file), and (b) `.gitattributes:17` applies
  `merge=union` to the whole glob `docs/eval-log/*.jsonl`, not any one
  file by name.
- **`docs/eval-log/tdd.jsonl`** has one additional, file-specific hit:
  `tests/test_eval_status.py:649` — a comment describing PR #245's own
  historical migration bug ("`tdd.jsonl` was never..."), narrating a past
  event, not a live path dependency. Confirmed by reading the line.
- No `.jsonl` filename appears in any `skills/*/references/eval-result.md`
  file individually by name (they cite `docs/eval-status.json` or
  `docs/eval-harness-findings.md`, never a specific per-skill log file
  directly) — confirmed by the same combined grep, zero false negatives
  possible since every one of the 41 exact basenames was in the pattern
  set.
- No estate or vault hit for any of the 41.

Full file list (all `corpus`, same reason, same consumer profile except
`tdd.jsonl` as noted): `adopt-or-build.jsonl`, `ask-a-council.jsonl`,
`close-the-loop.jsonl`, `create-skill.jsonl`, `decide-by-variant.jsonl`,
`derive-independently-then-compare.jsonl`, `determine-intent.jsonl`,
`determine-signals.jsonl`, `devils-advocate.jsonl`,
`dispatch-brief.jsonl`, `dispatching-subagents.jsonl`, `distill.jsonl`,
`durable-fact-before-label.jsonl`, `failing-test-first.jsonl`,
`github-cli.jsonl`, `keep-me-honest.jsonl`, `linear.jsonl`,
`loop-contract.jsonl`, `loop-memory.jsonl`, `mechanize.jsonl`,
`memory-conventions.jsonl`, `mine-transcripts.jsonl`, `notify.jsonl`,
`obsidian.jsonl`, `plan-parallel-execution.jsonl`, `prd.jsonl`,
`primer.jsonl`, `progressive-disclosure.jsonl`, `prompt-corpus.jsonl`,
`refuse-invented-identity.jsonl`, `research-the-limit.jsonl`,
`safe-deletion.jsonl`, `sanity-check.jsonl`,
`spec-driven-development.jsonl`, `spec.jsonl`,
`supervised-lane-loop.jsonl`, `tdd.jsonl` (see above), `test-in-the-
consumer-context.jsonl`, `tmux.jsonl`, `verify-the-instrument.jsonl`,
`wire-it-when-you-write-it.jsonl`.

## DELETE-CANDIDATE

**None found.** P11's refutation covered exact duplicates; I independently
re-ran a full-repo checksum pass (`sha256sum` over every `.md`/`.py` file,
tracked at origin/main) and confirm zero duplicate checksums exist today.
`docs/eval-harness-findings.md`'s "supersedes its own earlier 2026-08-23
version" language describes an in-place edit to the same file (git
history), not two files — there is no second file to list. No stale
copy, no orphaned duplicate found in this sweep.

## Third-party skills (constraint 4 — LIST for Jon, never delete)

**None found.** Checked two independent ways: (1) `docs/skills-
reconciliation.json`'s own committed `authorship` field — all 41 rows
read `"class": "jon-or-agent-attributed"`, zero `"unknown"`/`"third-
party"`; (2) independently, for every `skills/*/SKILL.md`, `git log
--reverse --diff-filter=A --format=%an` on its own add-commit — all 41
resolve to author "Jon Hill", zero others. This repo currently carries no
third-party-committed skill; nothing to list.

## SKILLS-INDEX.md

Confirmed dead and staying dead: `git log --oneline -- SKILLS-INDEX.md`
on this checkout shows it deleted by #303 ("stale-copy anti-pattern"),
predating the reconciliation manifest (#304) that replaced it with a
generated, link-only table. Nothing in this table proposes reviving it
under any name — the classification above sorts existing prose docs, it
does not add a hand-maintained skill inventory.

## What Phase 2 must not get wrong (spine of this table, repeated for visibility)

1. `docs/eval-status.json`'s move requires an accompanying, same-PR edit
   to agent-estate's `src/tui/cmd/estate/skills.go:11` constant, or the
   estate TUI's skills-eval pane silently breaks against a path that no
   longer exists. This is a CROSS-REPO change; Phase 2 for skills alone
   cannot land it without a coordinating estate PR (or the constant made
   configurable) — flagging this dependency explicitly rather than
   letting Phase 2 discover it mid-move.
2. `docs/skills-reconciliation.json` and `docs/skills-environment.json`
   both need their generator's own hardcoded output paths updated in the
   same commit that moves the files, and `--check` must be proven green
   at the new location before merge (see the row above).
3. `docs/eval-harness-findings.md` has enough real cross-references
   (11 distinct citing files) that a tombstone at its old path is not
   optional if it moves — several citations are inside OTHER now-
   historical docs that themselves won't be re-edited lightly.

---
Reply DONE; lane-b reviews this table. I review lane-b's estate
disposition table.
