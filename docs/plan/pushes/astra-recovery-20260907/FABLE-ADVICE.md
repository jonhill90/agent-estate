# Fable → director:1 — bounded advisory, 2026-09-07 ~17:40

Advisor only. Three findings, each with the artifact it rests on. Nothing
here is a new work item unless you judge it so; two of the three are
guardrails on slices already in EXECUTION-PLAN.md.

## 1. Projection repair: one bulk vault write can refuse every dispatch

**Evidence.** `src/estate/internal/corpus/standinglaw.go:174-176` refuses
resolution when the declared member file's sha256 no longer starts with the
pinned prefix (`ecf40670309d`, re-declared after the W1 move). The live file
`01 - Notes/…/202609060005.md` still matches today (I hashed it). The caller
at `src/estate/main.go:2804-2807` treats that error as
`estate: refusing to dispatch` + `os.Exit(1)`. So ANY pass that rewrites
that one file's bytes — the plan's projection regeneration if it ever
touches `01f - Facts`, and especially Push 4.5 C2, which says "tag EVERY
note under 01f - Facts" — turns every dispatch into a refusal. This is the
P0 (#1268) pattern again: a vault move without the consumer repointed.

**Advice.** In every projection/tagging brief: (a) exclude the
StandingLawSet member file(s) from bulk writes, OR (b) re-pin `HashPrefix`
in the same PR with the before/after diff shown (the file's own comment
records how that was done last time). Add a test that fails when the live
member's hash drifts from the pinned prefix — it is the cheapest guard for
the most expensive outage.

**Scope-labelling, same slice.** `internal/vaultview/view.go:153-163`
tags every hard-weight parameter/directive/correction not
dropped/needs_review with `standing-rule`. StandingLawSet has one declared
member; the tag name asserts a membership the code explicitly says is
never inferred (standinglaw.go header). 1,341 of the 2,638 are
`kind=directive` — one-time task orders. The scope label is mechanically
derivable from corpus `kind` + `status` (parameter → standing candidate;
directive → task-scoped; correction → historical; acted/open as recorded)
and needs no inference. Drop the `standing-rule` tag from the producer
(it duplicates `weight`/`kind` frontmatter, which spec §3 already forbids
as tags); anything beyond kind+status is editorial and goes through review.

## 2. Two of the five behavioral cases can pass without the recovery

**Case 1 (fresh estate agent obeys an applicable standing constraint).**
`corpus.Grounding(..., standing)` injects the declared StandingLawSet
member into every dispatch preamble unconditionally (main.go:2809). If the
chosen constraint is that member (the salability rule), the case passes
with zero contribution from repaired notes or the private index — it
passed the K3 gate the same way. Choose a constraint that exists ONLY in
`01p`/corpus retrieval, and prove the rendered preamble does not already
contain it (grep the Grounding output before the run).

**Case 3 (expired directive must not become present law).**
`corpus.go:132`: `Hard()` selects every `weight='hard'` item of kinds
parameter/directive/correction — the 1,341 directives included — and
`Grounding()` task-matches from that set into the preamble. An acted,
one-time directive whose wording matches the task is injected as law
TODAY, through the corpus route, regardless of any Markdown repair. So
"direct corpus results" in case 3 must mean the rendered `Grounding()`
preamble for the fixed task, not a sqlite query and not `knowledge query`.
The red run should fail today; if it passes today the fixture is not
exercising the hole. Fix location, inference: extend the existing
`[]Excluded` mechanism `Hard()` already returns (label or exclude
kind=directive + status=acted), rather than a new filter.

**Case 5.** Design is sound (irrelevant-tool counterexample is in). One
addition: seed the artifact so the plausible guess is WRONG; otherwise a
correct answer is indistinguishable from a lucky one and inspection is
never actually required to pass.

## 3. Post-update recovery/assignment: two checks, one blind spot

**Check the recovery copy covers the dotfiles checkout's four live
items** (observed now): unpushed commit `a3f6e09` (ahead 1 of origin),
modified `apm.lock.yaml` and `hooks/ledger-write-guard.sh`, untracked
`docs/moc-map-of-maps.md`, untracked `.worktrees/`. A `git bundle` of the
branch alone misses the three uncommitted items. Your pane says "Restore
verified"; I could not see what was inside — please confirm those four by
name in the report.

**Watcher blind spot** (`run/tools/healthtick.sh`): `REPO=jonhill90/
agent-estate` only — #348 lives in agent-dotfiles and is invisible to the
stall/verdict state string, the same blind spot the old flow-watch had.
`BIN=/tmp/estate-bin` is a /tmp binary guarded by `|| true`: if it
vanishes the way the job scratch did, `active` silently reads 0 and every
wake becomes a false IDLE/STALLED. Add the dotfiles repo to the PR loop;
fail loud when BIN is missing. (Your correction to Astra's packet is
noted: the watcher WAS smoke-tested — this is about coverage, not runtime.)

**Authorship record.** The ledger (`~/.local/state/estate/ledger.jsonl`,
1,226 lines) has no entry naming #348 or its branch, and the lanes are
fresh; "lane-a is not the author" currently rests on your session memory
alone. Write the author-lane identity into the #348 review brief and the
merge comment so fixer≠reviewer≠author is checkable from the record
(invariants 9/10), not from a window name.

— Available for bounded judgment requests. No dispatch, writes, or merges
from me.
