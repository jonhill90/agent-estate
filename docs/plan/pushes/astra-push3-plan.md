# Astra one-shot — Push 3: finish the memory foundation

ONE-SHOT BUILD: branches + PRs only, never merge, never push to main.
Decisions are made — do not relitigate. Spec wins on conflict; smallest
reading on ambiguity, noted in the PR body. Read first, in order:
`inmaps-spec.md` (esp. §3 tag standard, §7b dissolution map, §8 ID rule),
`iteration-queue.md` (P5–P8), `master-execution-plan.md` (scope + Push 3),
`w1-migration-report.md` (the proven batch pattern).

FIRST ACTION, before any branch: verify and RECORD current state (main
sha, `ls "$AGENT_MEMORY_VAULT/agent"`, note count) in your report — four
of five workstreams branch on it. State at write time (2026-09-06 23:57,
already stale — main moved to 9aaaf8f when P5 batch 1 merged): main
`e4fb7f4`; vault `agent/` still holds: `00 - Inbox` (signpost), `corpus/`,
`facts/` (empty), `index.md`, `INDEX-CONTRACT.md`, `parameters/` (6
generated view files). `01 - Notes` has 119 notes + index. Batch 1 of the
dissolution already removed ROUTING/LIFECYCLE/intent/sources/tools.

Repo scope (binding): agent-estate, agent-dotfiles, skills, skills-private
(if needed), agent-evals. Nothing else. No Hill90 anything. Second Brain:
read-only reference, never edited. tmux commands must not name protected
sessions (a guard enforces this).

## A1 — Parameters become notes (the big one)

Turn every item the 6 `agent/parameters/*.md` views render into an
INDIVIDUAL note, and retire the views.

- ID STABILITY (binding): the catalogue derives each source id from
  sha256 of Locator ALONE (`identityFor` in register.go) — 24 live
  records depend on it. Add `remote`/`local` as NEW fields ALONGSIDE the
  existing Locator; do NOT change identityFor's input. If an id change
  is ever needed it is an explicit migration with an old→new mapping —
  not this push. [applies to A5]
- Scope = the item set `estate vault-view`'s own selection produces (the
  same queries those views run). Report the count per kind at start; do
  not trust any number written here. Known gap #1020 (live_parameters
  hides hard directives): report what your selection covers vs the full
  `items where weight='hard'` count — cover what vault-view covers, STATE
  the remainder, do not silently expand scope.
- Each note: `01 - Notes/01p - Parameters/<ID>.md` — one subdir for ALL
  corpus-item kinds (kind in frontmatter; do NOT create five subdirs).
  ID per spec §8: YYYYMMDD from the source prompt's date + 4-digit
  same-day sequence. TIE-BREAK (binding, for determinism): order by full
  timestamp where present, then by CORPUS ITEM ID ascending — a rerun
  must produce identical IDs or regenerability is broken.
- **CRITICAL — the subdir is invisible until its readers learn it (P0
  class, 1,104× scale):** `internal/knowledge/vault.go` reads notes with
  a NON-RECURSIVE glob (`01 - Notes/*.md`) and the vault validator's
  link regex only accepts `01 - Notes/<12-digit>.md`. Extend BOTH to the
  subdir IN THE SAME CHANGE (glob + regex + tests proving a subdir note
  is indexed and a subdir link validates), or the entire migration is a
  retrieval blackout. Name every other `01 - Notes` glob you find.
- Frontmatter per spec §3: `type:` Parameter|Correction|Directive|
  Question|Thought (+ add a `standing-rule` tag where the item is one —
  #330's missing category, expressed as a tag not a new type). FIELD
  NAMING (binding): `id:` = the 12-digit NOTE id (as W1 did); the corpus
  item id goes in its own `corpus_item:` field and the prompt id in
  `prompt_id:` — regeneration matches on `corpus_item`, never on `id:`.
  `generated: process:vault-view`, status/weight carried, flat tags only (§3 tag standard — zero
  namespaced, zero placeholders; escape any literal #N in body text).
- REGENERABILITY IS THE POINT: extend the estate Go tool (the vault-view
  path) so it writes/updates THESE notes in place (matched by corpus item
  id) instead of the 6 view files. The md files are the projection; the
  tool must be able to rebuild them all. Then delete `agent/parameters/`.
- Consumers repointed IN THE SAME CHANGE (spec §7b rule): whatever reads
  agent/parameters (knowledge index sources, docs, validator) — find them
  (`grep -rn "agent/parameters"` across estate + dotfiles + vault), list
  every one in the PR, repoint every one.
- Vault work follows the W1 pattern exactly: checksummed backups per
  batch, validator after each batch, halt-and-restore on any failure
  (never continue past a failed batch). Evidence report:
  `run/push3-a1-report.md` with per-batch validator output + full
  ID mapping.

## A2 — Finish the dissolution: agent/ ceases to exist

ORDERING (binding): A2 starts only after A1's last batch validates —
A2's gate cannot be met while `parameters/` exists, and deleting agent/
around a live migration is the exact failure §7b forbids.

- `index.md` + `INDEX-CONTRACT.md`: the capped-index DISCIPLINE survives,
  the files move — index content merges into `Start Here.md` (respect the
  cap; entries whose facts migrated in W1 already point at 01 - Notes),
  contract becomes `99 - Meta/index-contract.md`, validator repointed to
  validate Start Here against it (extend, never weaken).
- `corpus/` (read-only extraction record): it is LAYER-1 EVIDENCE, not
  knowledge — move it out of the vault to
  `~/.local/state/estate/corpus-extraction/` — if that path already
  exists, STOP and report (never overwrite; the estate ledger lives
  under the same parent). Leave one pointer note in `05 - Sources`
  recording what it is and where it went. If anything reads it in place
  (grep first), repoint in the same change.
- Evidence artifact: `run/push3-a2-report.md` — before/after tree
  listings, validator output, the corpus-extraction move receipt
  (file count + total bytes before/after).
- Remove the empty `facts/`, the `agent/00 - Inbox` signpost, and finally
  the `agent/` directory itself. GATE: `ls "$AGENT_MEMORY_VAULT/agent"`
  errors; validator green; a knowledge query still returns cited results.

## A3 — P6: kill the ledger name collision

Rename `~/corpus/ledger.sqlite3` → `~/corpus/corpus.sqlite3`; leave a
compat symlink `ledger.sqlite3 → corpus.sqlite3`. Repoint
`internal/corpus.Path()` and EVERY reference (grep estate + dotfiles
docs/CLAUDE.md + vault) in the same change. Write
`99 - Meta/ledgers-disambiguation.md` (corpus = what Jon said; estate
ledger.jsonl = what the estate did; the agent-dotfiles-supervisor db is
dead — #942). CONSUMERS INCLUDE HOOKS, NOT JUST DOCS: the write guard
`agent-dotfiles/hooks/ledger-write-guard.sh` matches on the literal name
`ledger.sqlite3` — repoint it AND prove it still refuses (attempt a
guarded write against the NEW name, paste the refusal; a guard that
silently lapsed looks identical to one that works). Evidence: sha256 of
the DB before and after the rename (byte-identical), Path() resolving
through both names, and each repointed reference exercised or listed as
not-exercisable.

## A4 — P7: skills repo routing, no copies

In jonhill90/skills: delete `SKILLS-INDEX.md`. Routing lives in the
repo's normal surfaces — README by DEFAULT; use docs/index.md only if
the generated listing would exceed ~100 lines in README (state the
count):
link-only per skill, any description text GENERATED from that SKILL.md's
own frontmatter (OKF §8 pattern), never hand-copied prose. Keep whatever
eval-status value the index carried IF it can be generated; drop it
otherwise (missing metadata is never invented). Repoint the catalogue
source registration and the vault Start Here pointer.

## A5 — P8: repo pointer records with dual locators

Extend the catalogue entry shape: locator carries `remote` (GitHub URL)
and `local` (path) as SEPARATE fields — never derived from each other; a
missing local checkout is a recorded state. Register/refresh remain the
only write path. Create/refresh pointer records for the 5 in-scope repos,
each noting its routing surface (README/AGENTS.md/docs index). Link them
from `Start Here.md`. Focused tests incl. one mutation check (break the
missing-local handling, watch it fail, restore).

## NOT in this one-shot

P3 branch/worktree cleanup (deletion work stays with the director's
lanes), K1 retrieval-quality debt, skills evaluation (Push 4), K6
(Push 5), anything touching scheduler/models/capacity, the protected
shared knowledge index (do not regenerate), per-agent memory format.

## Definition of done

One PR per workstream (A1 may be A1-tool + A1-migration-report; vault
operations deliver reports, not PRs). Each PR body: what it does, real
command output as evidence, what it deliberately did not do, what a
reviewer should attack first. Final `run/astra-push3-report.md`:
per-workstream status, honest FAILs and UNRUNs labeled, resume locations
for every branch/worktree. All worktrees preserved for iteration.
