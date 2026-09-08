# P12 Phase 1 — agent-dotfiles `docs/` disposition table

Lane: agent-estate:1 (lane-a). Judgment only — **zero file moves, zero
deletions, zero edits** to any file in `agent-dotfiles` in this pass. Every
consumer-check grep was run read-only against the working tree exactly as
Director measured it; no git command that mutates state was run anywhere
in `agent-dotfiles` (`git status`/`git log` only).

## Read before anything else moves in Phase 2

**The checkout is not clean and Phase 2 must not assume it is.**
Measured directly (`git status`, read-only, matches Director's own
measurement exactly):

- Branch `main`, 1 unpushed commit (`a3f6e09`, "fix(ledger-write-guard):
  match the ledger path as an open, not as a mention") ahead of
  `origin/main`.
- **`hooks/ledger-write-guard.sh` has uncommitted modifications** —
  work in flight, not touched by this table, not part of `docs/` at
  all, but Phase 2's own execution against this repo must not `git
  checkout`/`git restore`/`git clean` or otherwise assume a clean
  working tree going in. Whatever moves this table produces are moves
  Phase 2, not this pass, has to layer on top of that in-flight state.
- `apm.lock.yaml` also has uncommitted modifications (lockfile, not a
  docs file, same caveat).
- `docs/moc-map-of-maps.md` is untracked **in this local checkout only**
  — see its own row below, corrected after lane-b's review found this
  local checkout's own `git status` framing was itself the exact stale-
  checkout error this rule exists to catch.
- `.worktrees/` untracked (other lanes' worktrees; not this repo's own
  content, not dispositioned).

**This checkout is 1 commit ahead of `origin/main` and 13 commits
BEHIND it** — measured directly, read-only, after this table's first
version stated only the "1 ahead" half and missed the divergence
entirely (the same error Director independently made and corrected on
the row below):
```
$ git rev-list --left-right --count HEAD...origin/main
1	13
```
**Any claim in this table derived from this local working tree or its
own `git log`/`git status` may be stale relative to `origin/main`.**
Phase 2 must verify every fact this table states against `origin/main`
directly (`git ls-tree`/`git show origin/main:<path>`, not this
checkout's working tree) before executing anything against it — this is
the general form of the specific error corrected in the
`docs/moc-map-of-maps.md` row below, and it is not scoped to that one
row.

No destructive git command was run. A non-destructive backup already
exists at `~/.claude/jobs/8182f39f/tmp/dotfiles-at-risk/` per Director's
message and was not touched or relied on — the working tree itself was
read directly and stayed exactly as found.

## Prior art this table defers to, not repeats

**`docs/docs-layout-council-138.md`** (agent-dotfiles#138) already ran a
12-arm council on almost this exact question — "does each file sit in the
right place, is there noise to strip, is the flat-vs-subdirectory split
itself right" — against the *same* `docs/` tree, 2026-08-23. Its own
verdicts, read directly rather than re-derived:

- **Q1 (file placement):** no move needed for any of `PRD.md`, `SPEC.md`,
  `handoff.md`, `harness-engineering.md`, `loop-engineering.md`,
  `memory.md`, `provenance-manifest.md`, `work-tracking.md`. Two files
  (`migration-audit.md`, `agent-engineering-lineage.md`) have "a plausible
  future case for relocation" — not acted on, not urgent.
- **Q2 (noise):** none found — every file in `docs/` carries a dated
  status line or explicit historical banner; no undisclosed-stale record
  exists.
- **Q3 (`skills`/`skills-private` docs/):** correctly absent, N/A to this
  repo.
- **Q4 (flat vs. subdirectory):** stay flat at this repo's current size —
  a scoped call, not a portable rule, re-check due at the next spec
  iteration if the file count keeps growing.

This table's dispositions for the files #138 already covers **agree with
its verdict** rather than re-litigating it — #138 is cited per row below,
not silently repeated. Where this table's judgment differs (none do), that
would be flagged explicitly; none needed it.

## Table

Legend: **canonical** = current, authoritative, no move; **historical** =
deliberately retained past record, not live guidance; **research** = raw
evidence/study material, not living documentation; **corpus** = dated
pass/triage report about the prompt corpus; **relocate-out-of-docs** =
belongs somewhere else entirely; **DELETE-CANDIDATE** = listed for Jon,
never executed by this pass or any pass that follows without his say-so.

| File | Disposition | Reason | Consumer check (grep -rn across agent-dotfiles + agent-estate + vault) |
|---|---|---|---|
| `docs/.gitkeep` | DELETE-CANDIDATE | Vestigial empty-dir placeholder. `docs/` has held real content since before this file's own history; the one basename hit found is for a *different* file (`hooks/.gitkeep`, coincidental basename match). | 0 real hits for `docs/.gitkeep` itself in any of the three trees. (`supervisor-extraction-plan-179.md:415` mentions `hooks/.gitkeep`, a different file — not this one.) |
| `docs/agent-engineering-lineage.md` | canonical | Cited glossary of external agent-engineering vocabulary, actively linked from `provenance-manifest.md` (2 links) and cross-referenced by `docs-layout-council-138.md` §1 as a plausible-future-move candidate — not moved. | agent-dotfiles: 12 hits (`provenance-manifest.md` ×2 live links, `docs-layout-council-138.md` ×3, `corpus/b3-unacked-triage-2026-08-23.md` ×1, 6 research-batch citations). agent-estate: 0. vault: 1 (`05 - Sources/SRC-2026-09-06-007.md`, a catalogue source pointer, expected/harmless). |
| `docs/corpus/b2-corpus-refresh-2026-08-23.md` | corpus | Dated pass report for one specific corpus refresh sweep — `docs/corpus/index.md`'s own words: "dated pass reports... none of them is where a fresh agent should start." | agent-dotfiles: 0. agent-estate: 0. vault: 0. |
| `docs/corpus/b3-unacked-triage-2026-08-23.md` | corpus | Same as above — dated triage-pass report. | agent-dotfiles: 0. agent-estate: 0. vault: 0. |
| `docs/corpus/b4-delta-triage-2026-08-23.md` | corpus | Same as above — dated triage-pass report. | agent-dotfiles: 0. agent-estate: 0. vault: 0. |
| `docs/corpus/index.md` | canonical | The corpus's own live entry point/map ("Not a report — a map"), explicitly distinguishing itself from the dated `b2`/`b3`/`b4` reports beside it. Real, active consumers cite `agent/index.md`/`index.md` generically (the vault's own index, a different file with the same basename) — no direct link to *this* file found, but its role (living map, not a report) is stated in its own frontmatter and matches nothing that should move. | agent-dotfiles: 0 direct references to `docs/corpus/index.md` by path (the many `index.md` hits under other basenames in this batch are the *vault's* `index.md`/`agent/index.md`, a same-named but different file — see that row). agent-estate: 0. vault: 0. |
| `docs/docs-layout-council-138.md` | canonical | The prior authoritative disposition analysis this very table defers to (see "Prior art" above) — actively cited by `hierarchy-naming-57.md`, `supervisor-extraction-plan-179.md`, and its own research batch. | agent-dotfiles: 3 direct references (`hierarchy-naming-57.md:13`, `supervisor-extraction-plan-179.md`, plus internal self-references from its own research batch files). agent-estate: 0. vault: 0. |
| `docs/handoff.md` | canonical | Living "current state" doc, rewritten not appended to — #138 Q1: no arm proposed moving it; already flags its own staleness banner inline (the pattern Q2 asks whether other docs lack, already present here). | agent-dotfiles: 2 (`docs-layout-council-138.md` ×2, quoting the file's own staleness-banner practice). agent-estate: 0. vault: 2 (`01p - Parameters/202607300024.md`, `01f - Facts/202608100002.md`). |
| `docs/harness-engineering.md` | canonical | Deployment-model/harness-boundaries doc, actively linked from `PRD.md` and `SPEC.md`'s own Inputs list; #138 Q1: no move proposed. | agent-dotfiles: 6 live citations (`PRD.md`, `SPEC.md` ×3, `provenance-manifest.md` ×3, `memory.md`). agent-estate: 0. vault: 1 (`05 - Sources/SRC-2026-09-06-009.md`). |
| `docs/hierarchy-naming-57.md` | canonical | Active decision-support doc — #138 Q1 calls it "self-describing by filename already," and it is directly cited with section numbers by `supervisor-extraction-plan-179.md` and `docs-layout-council-138.md`. | agent-dotfiles: 8+ live citations across `docs-layout-council-138.md`, `supervisor-extraction-plan-179.md`, `loop-engineering.md`. agent-estate: **8 hits** (corrected — lane-b's review measured 8, verified myself: `reference/scripts/supervisor/digest.sh:12` and `reference/scripts/supervisor/director-route.sh:8` are the 2 real, current hits; the same pair repeats across 3 stale `.claude/worktrees/*/` snapshots = 6 more = 8 total). This is a real cross-repo dependency: agent-estate code comments cite this file's content directly, in the CURRENT `reference/` tree, not only stale copies. vault: 0. |
| `docs/inmpara-and-coleam-study-2026-08-23.md` | research | Dated external-methodology study (INMPARA + coleam00/skills); feeds `memory.md`'s living contract but is itself study material, not the contract. | agent-dotfiles: 0 files cite it by name (checked directly — no inbound references found in this batch). agent-estate: 0. vault: 0. |
| `docs/loop-engineering.md` | canonical | Working document on unattended-loop patterns; #138 Q1: no move proposed; cited by `supervisor-disposition.md`, `hierarchy-naming-57.md`, `loop-signals.md`, `okf-adoption-280.md`. | agent-dotfiles: 9 live citations. agent-estate: **2 hits** — `reference/scripts/estate-loop/check.sh:4` and its `.claude/worktrees/` copy cite "LOOP CONTRACT (agent-dotfiles docs/loop-engineering.md)" directly in a code comment. vault: 2 (`01p - Parameters/202608120025.md`, `05 - Sources/SRC-2026-09-06-010.md`). |
| `docs/loop-signals.md` | canonical | Companion to `loop-engineering.md`/`supervisor-disposition.md`/`supervisor-extraction-plan-179.md`; cited back by `supervisor-extraction-plan-179.md`. | agent-dotfiles: 2 (`supervisor-extraction-plan-179.md` ×2). agent-estate: 0. vault: 0. |
| `docs/memory-done-condition.md` | canonical | Contract-defining doc (session-start load cap, index-reachability check) with measured numbers currently cited. | agent-dotfiles: 0 inbound (it cites others, not cited itself in this batch). agent-estate: 0. vault: 2 (`99 - Meta/pending-links.md`, `05 - Sources/SRC-2026-09-06-011.md`). |
| `docs/memory-per-agent-map-contract.md` | canonical | Generalizes `memory.md`'s map-before-search contract (#280); actively cited by `memory.md` and `scripts/memory_lint.py`. | agent-dotfiles: 3 (`corpus/index.md`, `memory.md`, `scripts/memory_lint.py`). agent-estate: **1 hit** — a stale `.claude/worktrees/agent-a6ac84368bab686f4/docs/okf-bundle-replication-2026-08-23.md` cites it (`from agent-dotfiles#315`). vault: 1 (`05 - Sources/SRC-2026-09-06-012.md`). |
| `docs/memory.md` | canonical | The memory vault contract summary — #138 Q1: no move proposed; extensively linked from `PRD.md`, `SPEC.md`, `harness-engineering.md`, `okf-adoption-280.md`, `scripts/memory_lint.py`. | agent-dotfiles: 15+ live citations. agent-estate: 2 (`goldenset/cases.json` ×2, an unrelated same-named `09-memory.md` golden-set fixture — coincidental basename overlap, not this file). vault: 2 (`99 - Meta/log.md`, `05 - Sources/SRC-2026-09-06-013.md`). |
| `docs/migration-audit.md` | historical | Explicitly historical audit — #138 Q1's own words: successor is `provenance-manifest.md`, which names it directly in its opening line; #138 flags this as the strongest candidate for a future `docs/decisions/`-style move, not acted on. | agent-dotfiles: 5 live citations (`PRD.md`, `docs-layout-council-138.md` ×3, `provenance-manifest.md`, `README.md`). agent-estate: 0. vault: 0. |
| `docs/moc-map-of-maps.md` | **QUESTION FOR JON** | **Corrected after lane-b's review (blocking finding, confirmed independently): this file is NOT unowned, and git is not blind to it.** The first version of this row said "untracked, unowned — git does not know this file exists," on Director's own original framing — that framing was itself built on a stale local `git status` read, the identical class of error this table's own opening section now warns Phase 2 against. Verified directly, read-only, against `origin/main` (not this checkout's stale `main`, which sits 1 ahead / 13 behind it): `git ls-tree origin/main -- docs/moc-map-of-maps.md` returns a real tracked blob (`0f03ae69`); `git log --all --oneline -- docs/moc-map-of-maps.md` shows it added by #321, fixed by #328, and most recently edited **yesterday** by #345 ("organize canonical knowledge surfaces"); `git merge-base --is-ancestor` confirms the commit that first tracked it is not even an ancestor of this checkout's local `main` — this checkout's own lane never merged the history that tracks it. `origin/main`'s tracked version today is a 5-line tombstone: "Status: superseded routing location. Canonical content moved, not copied. Continue to `canonical/moc-map-of-maps.md`" — and `docs/canonical/moc-map-of-maps.md` is confirmed present on `origin/main` (blob `0f977921`) holding the real content. **This file's fate was already decided and executed upstream, days ago, by merged PRs — it only looks undecided from this one stale, unsynced clone.** So the real question for Jon is not the open-ended one the first version of this row posed ("nobody has decided this file's fate, judge it from nothing") — it is the narrow, nearly-answered one: this local checkout's untracked copy is an OLDER draft that predates the supersession; does it hold anything the relocation to `docs/canonical/moc-map-of-maps.md` did not carry forward, and is any of that worth preserving before this checkout finally syncs and one copy silently shadows the other? A direct read-only diff against `origin/main`'s current canonical version (run only to characterize the shape of the question, not to answer it) shows the two differ by frontmatter (`type`/`knowledge_status`/`updated`, present only in the canonical copy) and one row's wording (the vault row — the canonical copy already reflects agent-estate#1275's `agent/` retirement and a repo-relative link; this checkout's older draft still has the pre-#328 absolute machine-local path #328 was opened specifically to fix). Nothing beyond that surfaced in the diff, but **running and interpreting that comparison as a decision is Phase 2's or Jon's, not this table's** — this row states what the diff shows, not what should be done about it. | agent-dotfiles (origin/main, not this stale checkout): 1 real tracked reference — the tombstone at this exact path links forward to `docs/canonical/moc-map-of-maps.md`. This local checkout's untracked copy: 0 references from the tracked tree (expected — this checkout's `main` never merged the commit that tracks the real file at all). agent-estate: 0. vault: 1 (`05 - Sources/SRC-2026-09-06-014.md`, a catalogue source pointer against this checkout's untracked copy — predates the correction, not re-verified against `origin/main`'s tombstone). |
| `docs/okf-0.2-study-2026-08-23.md` | research | Dated study of OKF 0.2 spec adoption; companion/predecessor to `okf-adoption-280.md`; feeds the living memory contract but is study material itself. | agent-dotfiles: 2 (`okf-validators-pilot-2026-08-23.md` ×2 citing it as step-2 predecessor, `okf-adoption-280.md` ×2). agent-estate: 0. vault: 0. |
| `docs/okf-adoption-280.md` | research | Dated OKF-adoption decision study (#280), companion to `okf-0.2-study-2026-08-23.md`; feeds `memory.md`'s contract, not itself the contract. | agent-dotfiles: 5 (`memory-done-condition.md` ×2, `memory.md`, `okf-0.2-study-2026-08-23.md` ×2, `scripts/memory_lint.py`). agent-estate: 0. vault: 0. |
| `docs/okf-validators-pilot-2026-08-23.md` | research | Dated pilot report for the OKF validators, step 2 of `okf-0.2-study-2026-08-23.md`'s sequence — a point-in-time run record, not living guidance. | agent-dotfiles: 0 inbound (it cites `okf-0.2-study-2026-08-23.md`, not cited itself in this batch). agent-estate: 0. vault: 0. |
| `docs/PRD.md` | canonical | Product requirements — #138 Q1: no arm in any variant proposed moving it; extensively linked as the repo's own entry point from `README.md`, `AGENTS.md`, and every other doc in the tree. | agent-dotfiles: 30+ live citations, including `README.md` and `AGENTS.md` root-level routing. agent-estate: 0 real hits for *this* file — every "PRD.md" hit found is agent-estate's own, unrelated `docs/product/PRD.md`/`docs/tui/PRD.md` (same basename, different, unconnected documents; confirmed by reading surrounding context). vault: 11 (mostly `01 - Notes/01p - Parameters/*` corpus items and one `05 - Sources` pointer). |
| `docs/provenance-manifest.md` | canonical | Decision ledger (adopt/adapt/author/reject), successor to `migration-audit.md`; #138 Q1: no move proposed; `AGENTS.md` names it a decision-ledger that "stays here" explicitly. | agent-dotfiles: 20+ live citations including `AGENTS.md`, `README.md` ×2, `SPEC.md` ×2, `handoff.md` ×3, `scripts/validate_repository.py`, `tests/test_validate_repository.py`. agent-estate: 0. vault: 1 (`05 - Sources/SRC-2026-09-06-015.md`). |
| `docs/research/docs-layout-council-138/claude-adversarial.txt` | research | Raw arm-response transcript for the #138 council, cited by file name from `docs-layout-council-138.md` itself as an audit trail, not living documentation. Per that file's own words: "should be deleted [after Jon acts on this document's conclusions]... if it is still here at the next spec iteration's exit with nothing left citing it, that is the signal" — not yet, since `docs-layout-council-138.md` still cites it today. | agent-dotfiles: cited by `docs-layout-council-138.md` (indirectly, via the batch as a whole) and cross-referenced by sibling transcripts in the same directory. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/claude-blind.txt` | research | Same class as above (blind-variant transcript). | agent-dotfiles: cross-referenced by `README.md` in the same dir ("prompt-blind.txt" pairing). agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/claude-concrete.txt` | research | Same class (concrete-variant transcript). | agent-dotfiles: cross-referenced by sibling `prompt-concrete.txt`. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/claude-control.txt` | research | Same class (control-variant transcript). | agent-dotfiles: cross-referenced by sibling `prompt-control.txt`. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/codex-adversarial.txt` | research | Same class, Codex arm. | agent-dotfiles: 0 direct inbound beyond the batch's own internal structure. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/codex-blind.txt` | research | Same class, Codex arm. | agent-dotfiles: 0. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/codex-concrete.txt` | research | Same class, Codex arm. | agent-dotfiles: 0. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/codex-control.txt` | research | Same class, Codex arm. | agent-dotfiles: 0. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/copilot-adversarial.txt` | research | Same class, Copilot arm. | agent-dotfiles: 0. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/copilot-blind.txt` | research | Same class, Copilot arm. | agent-dotfiles: 0. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/copilot-concrete.txt` | research | The one dissenting arm `docs-layout-council-138.md` §1 cites by name and treats as "the most defensible dissent in the batch" — directly load-bearing evidence for the parent doc's own verdict. | agent-dotfiles: 2 (`docs-layout-council-138.md` ×2, direct named citation). agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/copilot-control.txt` | research | Same class, Copilot arm, also quoted at length by `docs-layout-council-138.md` for its "skills/skills-private have no docs/" reasoning. | agent-dotfiles: cited in substance (not by filename) by `docs-layout-council-138.md`'s Q3 discussion. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/prompt-adversarial.txt` | research | The adversarial prompt text itself, referenced by its paired `claude-adversarial.txt` ("Prompt: prompt-adversarial.txt"). | agent-dotfiles: 1 (paired transcript header). agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/prompt-blind.txt` | research | The blind prompt text; referenced by `README.md` and its paired transcript. | agent-dotfiles: 2. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/prompt-concrete.txt` | research | The concrete prompt text; referenced by its paired transcript. | agent-dotfiles: 1. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/prompt-control.txt` | research | The control prompt text; referenced by its paired transcript. | agent-dotfiles: 1. agent-estate: 0. vault: 0. |
| `docs/research/docs-layout-council-138/README.md` | research | The batch's own manifest, explaining what each raw file is (e.g. names `shared-corpus.txt` and `prompt-blind.txt` directly). Basename `README.md` is too generic for a bare grep to isolate from every other `README.md` in both repos — path-qualified search used instead (`docs-layout-council-138/README`); real self-contained references only, no external inbound found. | agent-dotfiles: self-referenced by sibling files in the same directory only; 0 external inbound beyond `docs-layout-council-138.md`'s general link to the directory. agent-estate: 0 (every bare "README.md" hit elsewhere in both repos is a *different* README, confirmed by path). vault: 0. |
| `docs/research/docs-layout-council-138/shared-corpus.txt` | research | The real repo/doc inventory handed to every council arm — cited by name from `docs-layout-council-138.md`'s own §1 file-count claims and the batch's own `README.md`. | agent-dotfiles: 1 (`README.md`, direct named citation). agent-estate: 0. vault: 0. |
| `docs/SPEC.md` | canonical | Technical design spec — #138 Q1: no move proposed, "carries a dated status line, updated 2026-07-29, not stale"; the repo's single most cross-referenced document (30+ inbound citations across every other file). | agent-dotfiles: 30+ live citations. agent-estate: 0 real hits for *this* file — every "SPEC.md" hit found is agent-estate's own unrelated `docs/product/SPEC.md`/`docs/tui/SPEC.md` (confirmed by reading context; same basename, different documents, own repo's own spec). vault: 12 (mostly `01 - Notes/01p - Parameters/*`). |
| `docs/supervisor-disposition.md` | canonical | Comparison doc, explicitly "does not pick a winner" per #138 — but it is a **live cross-repo dependency**: agent-estate's own supervisor scripts cite specific sections of it in code comments. | agent-dotfiles: 15+ live citations including `hierarchy-naming-57.md`, `loop-signals.md`, `supervisor-extraction-plan-179.md` (its own successor plan, citing it as measured input). agent-estate: **7 hits, all real** — but 2 of the original citations were drawn from stale `.claude/worktrees/` snapshots and are wrong for the CURRENT `reference/` tree (corrected after lane-b's review, verified myself): `reference/scripts/supervisor/adapter.py:16,83` and `reference/scripts/supervisor/watchdog-harness.sh:32` are current and correct as originally cited paths go, but the file this table originally cited as `scripts/supervisor/cli.py:850` has since been renamed — the live citation in the current tree is `reference/scripts/supervisor/cli_dispatch_record.py:97`, and `scripts/supervisor/watchdog.sh:96` is now `reference/scripts/supervisor/watchdog-harness.sh:32`. Anyone repointing these comments against the current tree using the ORIGINAL citations would search for files that no longer exist there. The dependency itself is real regardless; only the specific paths needed correcting. Moving this file without updating those comments would silently orphan them. vault: 1 (`01f - Facts/202608110007.md`). |
| `docs/supervisor-extraction-plan-179.md` | canonical | The active plan for extracting the shell supervisor, superseding/extending `supervisor-disposition.md`'s comparison with a concrete extraction proposal; cited by `hierarchy-naming-57.md`, `loop-signals.md`, `SPEC.md` (twice, by section). | agent-dotfiles: 5 live citations. agent-estate: 0. vault: 0. |
| `docs/work-tracking.md` | canonical | Defines issue-tracker scope and conventions — #138 Q1: no move proposed; cited by `PRD.md`, `provenance-manifest.md`, `SPEC.md` (×2), `handoff.md`, `AGENTS.md`. | agent-dotfiles: 9 live citations. agent-estate: 0. vault: 1 (`05 - Sources/SRC-2026-09-06-017.md`). |

## Totals

45 files under `docs/` (44 real files + `docs/.gitkeep`), every one
dispositioned exactly once:

- **canonical: 17** — `agent-engineering-lineage.md`, `corpus/index.md`,
  `docs-layout-council-138.md`, `handoff.md`, `harness-engineering.md`,
  `hierarchy-naming-57.md`, `loop-engineering.md`, `loop-signals.md`,
  `memory-done-condition.md`, `memory-per-agent-map-contract.md`,
  `memory.md`, `PRD.md`, `provenance-manifest.md`, `SPEC.md`,
  `supervisor-disposition.md`, `supervisor-extraction-plan-179.md`,
  `work-tracking.md`.
- **historical: 1** — `migration-audit.md`.
- **research: 22** — `inmpara-and-coleam-study-2026-08-23.md`,
  `okf-0.2-study-2026-08-23.md`, `okf-adoption-280.md`,
  `okf-validators-pilot-2026-08-23.md`, and the 18 files under
  `docs/research/docs-layout-council-138/` (16 arm transcripts +
  `README.md` + `shared-corpus.txt`).
- **corpus: 3** — `corpus/b2-corpus-refresh-2026-08-23.md`,
  `corpus/b3-unacked-triage-2026-08-23.md`,
  `corpus/b4-delta-triage-2026-08-23.md`.
- **relocate-out-of-docs: 0** — nothing found that belongs somewhere else
  entirely; every file here is genuinely documentation-shaped.
- **DELETE-CANDIDATE: 1** — `docs/.gitkeep` (listed for Jon; not removed
  by this pass or any pass that follows without his say-so).
- **QUESTION FOR JON: 1** — `docs/moc-map-of-maps.md` (tracked and
  already superseded on `origin/main`; this checkout's untracked copy is
  an older draft — whether it holds anything worth preserving before
  the checkout syncs is a one-diff comparison, not this pass's or the
  Director's call to make).

## What this table found that matters most for Phase 2

Two files carry **real, active cross-repo consumers in agent-estate code
comments**, not just prose cross-references within agent-dotfiles itself:

1. **`docs/hierarchy-naming-57.md`** — cited by path in
   `reference/scripts/supervisor/digest.sh` and
   `reference/scripts/supervisor/director-route.sh` (8 hits total once the
   3 stale `.claude/worktrees/` copies of the same two files are counted;
   the 2 real, current hits are the ones that matter for Phase 2).
2. **`docs/supervisor-disposition.md`** — cited by path *and specific
   section number* in `reference/scripts/supervisor/adapter.py`,
   `reference/scripts/supervisor/cli_dispatch_record.py`, and
   `reference/scripts/supervisor/watchdog-harness.sh` (current filenames,
   corrected after lane-b's review — the originally-cited `cli.py`/
   `watchdog.sh` paths were drawn from a stale worktree snapshot and no
   longer exist under those names in the live `reference/` tree).

Neither is proposed to move by this table (both are `canonical`), but if
Phase 2 ever does move either, the tombstone-stub rule in the execution
plan (§Phase 2: "consumers repointed in the same PR") must account for
comments in a *different repository*, which a same-repo lint pass will
never catch on its own.

`docs/loop-engineering.md` has one real agent-estate consumer too
(`reference/scripts/estate-loop/check.sh`), same caveat, lower stakes
(one file, one comment, not several call sites).
