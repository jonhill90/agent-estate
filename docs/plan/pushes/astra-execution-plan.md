# Astra one-shot execution plan — knowledge architecture, push 2

You are Astra. This is a ONE-SHOT BUILD: implement everything below on
branches, open PRs, merge nothing. Jon iterates afterward with Fable
(review), the director (opus, run management), and sonnet workers. Decisions
are already made — do not relitigate them, do not redesign, do not add
scope. Where this plan and a referenced spec conflict, the spec wins; where
anything is genuinely ambiguous, implement the smallest reading and note it
in the PR body.

## Read these first, in order (all in this directory)

1. `inmaps-spec.md` — the vault layout/flow/naming/frontmatter spec. BINDING.
2. `next-plan-inputs.md` — evidence and sequencing from push 1 (esp. items
   5, 6: INMAPS decision, migration pilot result).
3. `knowledge-structure-research.md` — why each rule exists; cite it, don't
   re-research.
4. `handoff-lane-a.md` §"Fact migration pilot" — the proven
   backup/move/alias/restore pattern and the validator gap.

Repo state: all push-1 PRs merged (#1263 navigation/contract, #1264
catalogue, #1265 source-backed candidates + CLI wiring). The live vault
($AGENT_MEMORY_VAULT) is already relaid out to INMAPS. K2's machinery is
merged, but the ledger still records candidate-knowledge-inbox as
InProgress and the retrieval gate at 2-of-3 — verify current state with
`estate features`, don't assume. K3's gate (#1255) is NOT passed.

## Workstreams, in dependency order

### W1 — Validator speaks INMAPS, then migrate all 119 facts

**Standing authorization, read before objecting:** Jon explicitly ordered
this migration on 2026-09-06 ("we should be able to start moving facts and
renaming them"). For THIS operation only, that order supersedes spec §6's
"one at a time" and §7's bulk-migration deferral — spec §8 (added later,
same day) is the operative rule, and §6/§7 now carry pointers saying so.
Rule 17 (tool-only writes) governs agents writing memory in ORDINARY
OPERATION; this scripted, checksummed, validator-gated migration is the
sanctioned mechanical exception and does not wait for W2's tool. Do not
conclude the spec forbids W1 — it does not.

**Evidence artifact (the vault is not a repo, so W1 produces no PR):**
write `run/w1-migration-report.md` — per-batch validator output (verbatim),
per-batch checksum manifests, and the full 119-row ID mapping table. That
report IS W1's deliverable for the definition of done.

**On the pilot's ID table:** it was COMPUTED for 10 and validated by real
execution for exactly 1 (202607120001). Recompute the mapping yourself for
all 119 from the §8 rule; use the pilot table as a cross-check for the
first 10, not as gospel.

1. Extend the vault's `tools/validate_index.py` so index links may target
   `facts/` OR `../01 - Notes/<12-digit-id>.md` (URL-encoded space form
   included). EXTEND, never weaken: existence must still be verified in
   whichever location; a dangling link still fails; add a test that proves
   the validator still rejects a broken link in each location (mutation
   check: break it, watch it fail, restore).
2. Migrate every file under `agent/facts/` (count verified at YOUR start —
   119 as of 2026-09-06 18:40; if you count differently, use your count
   and say so) → `01 - Notes/<ID>.md` batch-wise
   (10-20 per batch, validator after each) per spec §8: ID = YYYYMMDD +
   4-digit same-day sequence (full timestamp order, then slug-alphabetical
   on ties); `aliases: [old-slug]`; `id:` field; move, never copy;
   checksummed backups per batch; update `agent/index.md`, `ROUTING.md`,
   `LIFECYCLE.md` paths. Lane A's pilot table (handoff) gives the first 10
   IDs — reuse them exactly.
3. Update instruction surfaces that name `agent/facts/`: the
   memory-conventions skill and vault docs. List every surface you find
   and every one you changed in the report; global CLAUDE.md/rules text is
   W4 item 5's job (assigned there explicitly).

**Failure rule (binding, no improvisation on the memory store):** a batch
has FAILED if the validator exits non-zero, any checksum mismatches, or
the batch is partially moved. On failure: restore THAT batch from its
backup, re-hash to confirm byte-identical, re-run the validator to
confirm exit 0, then HALT W1 entirely and write the report with what
happened. Never continue past a failed batch.

Acceptance: validator exit 0 with all facts migrated; a spot-check that 3
old `[[slug]]` wikilinks resolve in Obsidian (aliases working); zero files
under `agent/facts/` — the REST of `agent/` (corpus/, intent/,
parameters/, index.md, …) remains in place; spec §8 supersedes §1/§6's
"over time" phrasing for facts/ specifically.

### W2 — The memory tool (rule 17: no hand-written memory)

Build the write path in Go in `src/estate` (extend existing packages;
`internal/knowledge` + `internal/candidates` are the seams):

- `estate memory add|accept|reject|supersede|source-register|refresh` (or
  extend the existing `candidates`/`sourcecatalogue` verbs — prefer
  extending what exists; do NOT create a parallel command family).
- Every write: schema-validated frontmatter (spec §3 — required core +
  OKF trust fields when known, never fabricated), tag checked against
  `99 - Meta` vocabulary, `updated` auto-stamped ISO 8601 with UTC offset,
  correct lifecycle transitions (draft→stable→deprecated; supersede keeps
  old ID, adds pointer).
- Intake lands in `00 - Inbox` as `status: draft` with provenance;
  acceptance moves to `01 - Notes` with a fresh ID; source registration
  writes `05 - Sources/SRC-YYYY-MM-DD-NNN.md` records via the catalogue.
- Fix push-1's known conflict while you are here: the staged-views
  generator writes old-layout `01 - Sources` — retarget to
  `05 - Sources` (see next-plan-inputs; one constant + its test).
- MOC proposal: when ≥8 notes share a tag with no hub linking them,
  `estate memory moc-propose` emits a proposal (through review, never
  auto-published). Regeneration preserves a curated overview section.

Acceptance: focused Go tests incl. one mutation check per guard
(schema rejection, vocabulary rejection, supersede-hides-old, AND the
MOC threshold — prove ≥8-with-no-hub fires and 7-with-no-hub does not,
then break the counter and watch the test fail); an
end-to-end fixture run in an isolated vault copy; NO writes to the live
vault from tests.

### W3 — Consultation reliability (the #1 gap: knowledge must be USED)

Order matters — the gate re-run and the fixture fix are SEQUENCED, not
merged (re-wording the fixture changes the fact, which would destroy
comparability with the recorded 2-of-3 baseline):

1. Inject the read protocol into every dispatch grounding by EXTENDING the
   existing `knowledgeGrounding()` paragraph in `src/estate/main.go` —
   that is the sanctioned surface. Do NOT touch the standing-law path:
   #1254's rule is that knowledge content never enters `Hard()`/law, and
   the standing-law caps from #1260/#1261 (`MaxStandingLawMembers`,
   `MaxStandingLawBytes`) fail the build if exceeded — the read protocol
   is grounding prose, not law, and counts against no law budget.
   (Pattern: Anthropic's own memory-tool injection — research file §Q5.)
2. FIRST re-run #1255's K3 gate fully UNCHANGED — same fact, same task
   wording, same three pre-stated criteria. This is the run comparable to
   the 2-of-3 baseline and the one #1255's acceptance names. Record
   PASS/FAIL honestly with the transcript.
3. THEN fix the fixture wording flaw ("Synthetic gate fixture" tell —
   re-author neutral, per next-plan-inputs item 2b) for all FUTURE runs,
   and note that the next measurement starts a new baseline. Do not
   average or compare across the wording change.

Acceptance: grounding diff + the unchanged-gate transcript, verbatim, in
the PR; the fixture fix as a separate commit clearly after the re-run.

### W4 — agent-dotfiles knowledge treatment (K4's other half)

In the agent-dotfiles repo, knowledge surfaces ONLY (config/overlays/
scripts untouched):

1. Reorganize `docs/` (24 flat files, measured) to the estate standard:
   AGENTS.md becomes a routing index (not a context blob); living specs
   (memory.md, PRD.md, SPEC.md, loop-engineering.md) separated from dated
   one-shot studies and issue-numbered workdocs; every doc marked
   canonical / historical / superseded with a pointer to what superseded
   it. Mark and move — DELETE NOTHING; anything that looks deletable gets
   listed for Jon instead.
2. Create the canonical agent roster doc (who exists on this estate, role,
   definition location) — the target `03 - Agents/index.md` already points
   at; then tighten that vault pointer to it.
3. Add an AGENTS.md routing line to agent-estate's
   `docs/knowledge-workflow.md`.
4. Register agent-dotfiles' living docs as catalogue sources so drift
   detection watches them — use the push-1 catalogue verbs
   (`cmd/sourcecatalogue` register/list/show/refresh); they are merged and
   exist. (Nothing merges during this one-shot, so never plan around W2
   being merged.)
5. Update the global instruction text (`~/.claude/CLAUDE.md` /
   `~/.claude/rules/` copies maintained by this repo) wherever it names
   `agent/facts/` — this is the surface W1.3 hands to you.

Acceptance: a stranger can open dotfiles' AGENTS.md and reach any living
spec in ≤2 hops; the roster exists; sources registered with hashes; zero
remaining `agent/facts/` references in dotfiles-managed instruction text.

### W5 — Skills index in git (K5 start, small)

One source-controlled skills index in
`~/source/repos/Personal/Skills` (jonhill90/skills — THE skills repo; no
fallback needed, it exists; if a push fails, the branch+PR still counts):
name, purpose, canonical location, evaluated/unassessed status —
inventory-of-git-things lives in git (the rule that killed the vault
Skills folder). Private skills get a one-line count reference, never
content, since that index is public. Register the index as a catalogue
source. Do NOT evaluate or install anything (K5's evaluation half is a
later push).

Acceptance: the index PR lists every skill found under `~/.claude/skills`
and the Skills repo's own tree (state the count and how you counted);
each row has name/purpose/location/status; catalogue registration shown
with hash; zero installs, zero evaluations.

## Constraints (binding)

- Branches + PRs only; NEVER merge; never push to main. One PR per
  workstream (W1 may be several batch PRs if cleaner).
- Vault writes: backups + checksums first, validator after, restore on
  failure — the Lane A pattern, exactly. Never bulk without batches.
- App code Go; helper scripts in shell/Python are fine (Jon 2026-09-06).
- Never touch: Second Brain (reference only), Keychain, scheduler/model/
  capacity machinery, the protected shared knowledge index, per-agent
  memory format (reserved to Jon), graphs/vectors (deferred).
- Missing metadata is never invented. "Could not measure" is a real
  verdict — report it over a guess.
- Quote Jon only via text_clean, never text_raw (corpus rule).
- Evidence discipline: every "works" claim carries actual command output
  in the PR body; private transcript content stays out of Git.

## Definition of done for the one-shot

Repo workstreams (W2–W5) as open PRs with green checks; W1 as
`run/w1-migration-report.md` (the vault is not a repo — that report is
W1's deliverable). Each PR body carries: what
it does, evidence, what it deliberately did not do, and what a reviewer
should attack first. A final summary comment in this directory
(`astra-oneshot-report.md`): per-workstream status, including anything
UNRUN, honestly labeled. The iteration crew takes it from there.

## Repo scope map (binding — do not wander outside it)

**Edit in this push:** agent-estate (W1-W3), agent-dotfiles (W4, knowledge
surfaces only), Skills and/or skills-private (W5 index file only).

**Register as catalogue sources, change nothing in them:** Loops-Research,
inmpara (canonical INMPARA methodology), and any prior-art checkout a spec
actually cites (hve-core, basic-memory). Registration = a source record
with hash/freshness, that is all.

**Explicitly OUT of this push, do not touch:**
- Hill90 / hill90-app / hill90-docs — Hill90 has its own knowledge service
  (AKM); its relationship to this architecture (consumer of it, later) is
  Jon's call in a future push, not yours.
- AgentBox — standing decision: AKM/knowledge is never added to AgentBox.
- agent-evals — eval-methodology home; the K gates may move there someday,
  not now.
- Graph-Research, tui-research — K7/deferred material.
- Notebook-MCP — reference signal source only.
- agent-tui, agent-supervisor-worktrees, Temp, backup — legacy/scratch.
- Second Brain vault — reference only, NEVER edited (standing rule).
- Everything else in ~/source/repos/Personal not named above: out.
