# Inputs for the 19:15 EDT plan with Astra (accumulating)

1. **agent-dotfiles knowledge treatment** (Jon, 2026-09-06 ~16:30 EDT,
   intent confirmed by direct questions — this is the headline item):
   the knowledge system is machine-wide; agent-estate is one product using
   it. agent-dotfiles must get the SAME knowledge-architecture treatment,
   but ONLY the knowledge-affecting surfaces — config/scripts/overlays wait.
   - **Timing**: next plan (this session with Astra), NOT the current run.
   - **The docs directory itself is the job.** Jon: the repo has "random
     docs in the directory, it looks bad — it needs to follow the standard
     we came up with." Measured 2026-09-06: `docs/` is 24 flat entries —
     living specs (`memory.md`, `PRD.md`, `SPEC.md`, `loop-engineering.md`)
     mixed with dated one-shot studies (`okf-0.2-study-2026-08-23.md`,
     `inmpara-and-coleam-study-2026-08-23.md`) and issue-numbered workdocs
     (`hierarchy-naming-57.md`, `docs-layout-council-138.md`,
     `supervisor-extraction-plan-179.md`). Bring it to the estate standard:
     AGENTS.md as a routing index (not a context blob), progressive
     disclosure to scoped canonical docs, canonical vs. historical vs.
     superseded made explicit, OKF 0.2 for any newly managed markdown.
   - **Register it**: agent-dotfiles docs become catalogue sources so
     refresh/drift detection watches them (uses what Lane B built).
   - Also: routing link from agent-dotfiles AGENTS.md to the knowledge
     guide; decide whether the knowledge CLI ships as a PATH binary so any
     repo can call it without an agent-estate checkout.
2. Whatever the lane handoffs (`handoff-lane-{a,b,c}.md`) report as not-done,
   plus the demonstration result in the director's checkpoint summary.
3. Vault fact recorded: `agent/facts/knowledge-system-spans-repos.md`.
4. **Primary-checkout build residue** (Terra audit): `agent-estate/src/progress/progress`
   is an untracked, non-ignored 2.8 MB arm64 Mach-O binary (mtime 2026-09-02).
   It is not referenced by git history. Determine its owner before removal; handle under
   the existing cleanup/safe-deletion discipline rather than silently deleting it.
5. **Legacy Note metadata** (Terra audit): the vault validator has no hard failures,
   but reports 14 migrated `01 - Notes` records missing recommended `title` and/or
   `description` frontmatter. Treat this as a later, provenance-preserving normalization
   batch, not an excuse to bulk-rewrite during P0–P2.

2. **Held estate-CLI dispatches** (Director, 2026-09-06 ~20:20 EDT):
   queued rather than dispatched, per the run handoff — capacity belongs to
   the three lanes while the run is active.

   a. **Retrieval reliability — the substantive open gap.** The non-standing
      retrieval gate measured **2 of 3** (agent-estate#1255, three real
      `estate dispatch` runs, criteria predeclared before the fixture
      existed). The failure mode is **non-consultation**: one of three fresh
      agents never queried the knowledge base at all and answered from
      general reasoning. Retrieval itself works — a passing run found the
      fact unprompted and correctly used the superseding revision over the
      stale one. What is missing is that consulting is *optional*, so
      knowledge is present-but-sometimes-absent, which from the operator's
      side is indistinguishable from not having it. Bounded next item: make
      consultation reliable rather than optional. This outranks promoting
      more facts — a store agents check two-thirds of the time grows
      inventory, not capability.

   b. **Fixture wording flaw, cheap to fix.** The gate fixture's own
      description reads "Synthetic gate fixture"; both passing agents spotted
      it and discounted the number as a test value. That is a cue a real fact
      would never carry, so the runs may have been easier than reality.
      Re-author with neutral wording before the gate is re-run. Recorded
      rather than silently corrected.

   c. **#1262 post-merge review** (feature-ledger row for Agent Memory v0 +
      injection-scope corrections, merged `cce87a6`). Every other merge in
      this stretch has a returned post-merge review; this one does not yet.

   d. **Open hygiene, unstarted**: #1247 (sweep-worktrees deadlocks — refuses
      corpses whose dirty files are byte-identical to `origin/main`; blocked
      three dispatches in one stretch), #1248 (health watcher never re-arms
      itself; three consecutive backstops found it dead, plus it wakes on
      transitions the Director itself caused), #1254 (nothing pins the rule
      that published memory/knowledge can never enter `Hard()`/`Grounding()`
      as law — true today only by non-interaction, no test would fail).

3. **Brief-template lesson** (Director, 2026-09-06): stop asking lanes to run
   `estate dispatch`. Two consecutive lanes correctly refused nested dispatch
   as costly and outward-facing, and the second reported the acceptance item
   as *skipped rather than assumed passing* — the right disclosure. Dispatch-
   based proofs belong to the Director. This is now written into briefs
   rather than rediscovered per-lane.

4. **Language-rule scope clarified** (Jon, 2026-09-06 ~17:00 EDT): the
   Go-only rule covers the agent-estate APP only. Helper scripts/tools in
   shell or Python for knowledge work are fine in agent-estate AND
   agent-dotfiles — "it is just not to be an entire app written in shell or
   python." Vault fact `go-not-shell.md` updated with this scope. The
   agent-dotfiles knowledge treatment may therefore use small scripts where
   they're the right tool; no Go port required for tooling.

5. **INMAPS layout decision** (Jon + Fable, 2026-09-06 ~17:2x EDT, working
   decision — confirm at plan time): vault root becomes
   00 Inbox / 01 Notes / 02 MOCs / 03 Agents / 04 Projects / 05 Sources /
   99 Meta — the a-z tree spells INMAPS. Sources replaces Resources (Jon:
   his 05c Clippings were "just sources I added frontmatter to and tagged";
   123/132 measured). Notes = pure timestamp-ID filenames; MOCs/Projects =
   title-named; MOCs generated/threshold-born; 03 Agents reserved for
   personas/per-agent memory (parameter #20), index-only for now; types in
   frontmatter (Fact/Thought/Question/Parameter/Research), subtype folders
   only when earned; tool-only writes with schema-validated frontmatter.
   Full research basis: run/knowledge-structure-research.md. Supersedes the
   run's delivered 01 Sources/02 Projects/03 Shared Memory/04 Skills/05
   System naming — relayout is next-plan Lane A work.

---

# DIRECTOR CHECKPOINT for Astra — written 2026-09-06 ~17:58 EDT

## Run outcome: all three lane PRs merged, protocol held throughout

| PR | Lane | Reviewer | Merge commit |
|---|---|---|---|
| #1263 knowledge-workflow guide + vault navigation + contract | A | B (`agent-estate:2`) | `24ca850` |
| #1264 persistent source register, extraction, retrieval integration | B | C (`agent-estate:3`) | `37c33e3` |
| #1265 source-backed candidates, generalized proposal, publication receipts | C | A (`agent-estate:1`) | `7b039c5` |

`main` = `7b039c5`; `go build ./src/estate/...` and `go test ./src/estate/...` clean after each merge.

Every merge satisfied all four conditions, verified by the Director against
`gh` rather than taken from a lane's report: cross-lane `Verdict: APPROVE` +
`Review-Lane` + `Reviewed-SHA` at the EXACT current head, required checks green
at that SHA, reviewer lane != author lane, head unchanged at merge time. No
worker merged anything. No fabricated dispatch records.

**The reviews were real, not ceremonial** — worth noting because this run's
value depends on it:
- B on A: mutation-tested the golden-set guard (removed an entry →
  `TestLoadNaturalParsesEmbeddedCases` failed `len(cases) = 26, want 27` →
  reverted → passed), plus a duplication check showing the new guide
  *references* `knowledge-system.md` / `LIFECYCLE.md` / `contract.md` six
  times rather than restating them.
- C on B: **refused to re-run B's own pasted mutation command**, found it would
  not compile as written (`kind` out of scope at that call site), wrote its own
  compiling mutation, and confirmed the guard genuinely fails. Filed the
  transcription defect as non-blocking.
- A on C: isolated C's own commit (`git diff 37c33e3..300cc97`) for the scope
  check, and independently reproduced the mutation (disabled
  `currentMemoryItem`'s stale-index guard → `TestIntegratedSourceBackedWorkflow`
  FAIL → restore → green).

One sequencing catch worth keeping: #1265 was green but contained merged A and
NOT merged B. A review at that head would have been void the moment it rebased,
so the review was held until C rebased onto `37c33e3` (new head `300cc974`).
One ancestry check saved a full review cycle.

## Layout input: INMAPS supersedes the shipped 00-05 names

`run/inmaps-spec.md` (Jon-approved, basis in
`run/knowledge-structure-research.md`) is the layout input for the next plan.

**Live-vault state at checkpoint: still the SUPERSEDED tree.** Measured:
`00 - Inbox`, `01 - Sources`, `02 - Projects`, `03 - Shared Memory`,
`04 - Skills and Tools`, `05 - System`, `agent/`, `Start Here.md`.
INMAPS wants `01 - Notes`, `02 - MOCs`, `03 - Agents`, `04 - Projects`,
`05 - Sources`, `99 - Meta`. Lane A is mid-relayout; not complete at
checkpoint time.

## Demonstration: NOT RUN — recorded honestly, not skipped silently

Integration step 4 makes the Director the vault writer and requires lanes to
have stopped writing the vault first. Lane A's relayout was in flight at the
checkpoint, so the Director did **not** write the vault. Running the pinned
demo (`run/demo-task.md`) against a half-migrated vault would break the
single-writer rule and produce worthless evidence either way. A was told
explicitly not to rush the relayout to hit the checkpoint.

**Consequence:** the demonstration, the staged-view integration from
`run/views-staging/`, the one real publication and the refresh are all
UNRUN. They carry forward to Astra's plan. Nothing about them should be
reported as passed or attempted.

## Outstanding at checkpoint

- Lane A's FINAL guide/skill PR — not opened; merges last by sequence.
- Lane A's live-vault INMAPS relayout — in flight, backups-first per its task.
- `handoff-lane-{a,b,c}.md` — all three written.

## ESCALATION: staged source views conflict with INMAPS naming — NOT integrated

Integration step 4 was held on this, deliberately, per the handoff rule
"escalate any conflict between two lanes' changes to the same surface".

**The conflict.** `run/views-staging/` contains four source records at
`01 - Sources/src-<hash>.md`. INMAPS (`run/inmaps-spec.md` §1–2) puts sources
at `05 - Sources/` named `SRC-YYYY-MM-DD-NNN.md`.

**It is not a rename.** The id and the path are produced by MERGED code, not by
hand:
- `src/estate/internal/catalogue/register.go:292` — `return "src-" +
  hex.EncodeToString(sum[:8])`
- `src/estate/internal/catalogue/views_test.go:69` — asserts the view path
  `01 - Sources/src-abcdef0123456789.md`

So conforming to INMAPS requires changing Lane B's merged `internal/catalogue`
(id format, folder name, and its tests), not moving files. Hand-renaming the
staged files would desynchronise the vault from the code that writes and reads
them — the code would keep producing `01 - Sources/src-<hash>.md` on the next
run, and the renamed copies would be orphans.

**Director decision: did not integrate, did not rename.** A single writer
making an unreviewed structural change to another lane's merged surface, 40
minutes before a checkpoint, is exactly the shortcut this run's protocol
exists to prevent. Recorded for Astra rather than resolved unilaterally.

**For the next plan, this is a real, bounded work item:** reconcile
`internal/catalogue`'s source-view id/format/folder with INMAPS §1–2, with the
existing view tests updated, then integrate the staged views. Note INMAPS's
own rule that provenance stays stable even if titles change — whoever does this
must decide whether existing `src-<hash>` ids are migrated or preserved, and
say which.

## DEMONSTRATION: RUN, and PASSED — 2026-09-06 ~18:25 EDT

Executed by the Director through real `estate dispatch` to a fresh worker,
lane `1139-1788732159537716000-7811-1`, at `main` = `7b039c5`.

**Wording unchanged.** Verified by `diff` against the pinned blockquote in
`run/demo-task.md`: byte-identical. (An earlier ad-hoc sanity check reported a
mismatch; that was a bad line-range in the check itself, not an altered task.
The dispatched file was confirmed IDENTICAL before this result was judged.)

Judged against the three criteria pinned BEFORE any publication:

1. **Consults the surfaces rather than priors — MET.** The worker opened with
   "I've now confirmed this directly against the code and docs (not asked from
   memory)" and cited `docs/knowledge-workflow.md`, `docs/knowledge-system.md`,
   `run/contract.md`, `register.go`, `views.go`, `memory.go`, `main.go`.
2. **Finds and cites THIS RUN's canonical guidance — MET.** It cited
   `docs/knowledge-workflow.md` §1 and §5 (Lane A, #1263), the catalogue
   register and extraction kinds (Lane B, #1264), and source-backed candidates
   with `destination_kind` (Lane C, #1265). All three lanes' merged output.
3. **Consistent with that guidance — MET.** Its end-to-end picture (register →
   candidate queue → reviewed proposal → vault fact `agent/facts/<slug>.md` OR
   a repo-publication *receipt*, never a direct repo write → retrievable via
   `estate knowledge query`) matches the merged code.

**Director spot-checks of its citations** (not taken on trust):
- `main.go:575-580` does carry the `candidates register-source` subcommand.
- `PublishRepo` exists in `internal/candidates/memory.go` and is receipt-only.
- `views.go` writes to `stagingDir/"01 - Sources"/<id>.md` — which also
  independently confirms the staged-views escalation above.

**One immaterial inaccuracy, recorded rather than ignored:** the worker cited
`memory.go:424` for `PublishRepo`; it is at line 433. Line drift, not a wrong
claim — and this repo's own convention is to cite functions and behaviours,
never line numbers, precisely because of this.

**Scope of the claim.** This demonstrates that a fresh worker can find and
correctly describe the knowledge intake/review/publication path from the
merged guidance. It does NOT demonstrate the staged-view integration, the real
publication, or the refresh — all three remain UNRUN (see the escalation
above), and none should be reported as attempted.

## RUN CLOSED — 2026-09-06 18:10 EDT

All three lanes stood down cleanly. Panes left ALIVE and idle for Astra to
reuse (`agent-estate` windows 1-3); no window or session was killed.

Handoffs are final and each records its own merge outcome — verified, not
assumed:

| file | lines | updated | records |
|---|---|---|---|
| `handoff-lane-a.md` | 288 | 18:09 | `24ca850` + the completed INMAPS relayout |
| `handoff-lane-b.md` | 140 | 18:06 | `37c33e3` |
| `handoff-lane-c.md` | 152 | 18:07 | `7b039c5`, rebased head `300cc974` |

B's and C's were originally written at 16:29 and 16:50 — *before* their own PRs
merged — so both understated what shipped. They were asked to finalize as part
of standing down. Worth remembering as a run-management lesson: a handoff
written before the merge records the work but not the outcome, and the outcome
is what the next planner needs.

### State handed to Astra

- `main` = `7b039c5`; `go build ./src/estate/...` and `go test ./src/estate/...`
  clean.
- Live vault on the INMAPS tree, relayout complete with backups + validator
  (Lane A).
- Demonstration RUN and PASSED, pinned wording verified byte-identical.
- UNRUN and carried forward, none to be reported as attempted: staged-view
  integration, the real publication, the refresh — all blocked behind the
  `01 - Sources` vs `05 - Sources` reconciliation (Fable: next-plan work, one
  constant plus its test, properly reviewed; explicitly NOT hot-fixed tonight).
- Lane A's final guide/skill PR: not opened, carries forward.
- Held estate-CLI dispatches remain queued above (retrieval reliability, the
  fixture-wording flaw, #1262's post-merge review, and hygiene #1247/#1248/#1254).

6. **Fact-migration pilot result** (Lane A, 18:06-18:12 EDT): the ID
   mapping (spec §8) computes cleanly for all 10 oldest facts, but
   `tools/validate_index.py` structurally cannot validate a note outside
   `agent/facts/` — its link regex hardcodes the `facts/` prefix and its
   existence check listdirs `agent/facts/` only. Batch 1 was executed for
   real, rejected by the validator, restored byte-identical (re-hashed,
   contract holds again, exit 0). SEQUENCE FOR THE PLAN: (1) extend the
   validator to speak INMAPS paths — extend, never weaken: it must still
   verify target existence, now in either location; (2) then migrate all
   119 facts batch-wise per spec §8 using Lane A's proven
   backup/move/alias/restore pattern; (3) update instruction surfaces
   naming agent/facts/. Full report with ID table:
   run/handoff-lane-a.md '## Fact migration pilot'.

## Director review of astra plan

Adversarial read of `astra-execution-plan.md` (173 lines) against
`inmaps-spec.md`, 2026-09-06 ~18:40 EDT. Lens: **what wedges or misleads a
ONE-SHOT agent that cannot ask questions.** Findings only; no edits made to
the plan. Ordered by how likely each is to stop or misdirect the run.

1. **W1 is stopped dead by the plan's own conflict rule.** Plan line 7:
   "Where this plan and a referenced spec conflict, the spec wins." Spec §6
   says records migrate to `01 - Notes` "**via tool, one at a time, never
   bulk** (per Jon's standing rule)"; spec §7 explicitly defers "**bulk
   historical migration**". W1 (lines 36-42) orders "Migrate all 119 ...
   batch-wise (10-20 per batch)". A one-shot agent obeying line 7 must
   conclude the spec forbids what W1 instructs, and it cannot ask. It will
   either stall, silently do W1 anyway (violating a standing rule), or
   migrate one-at-a-time and blow its time budget. **This is the single most
   likely wedge in the plan** and needs an explicit ruling: either W1 is a
   sanctioned exception to §6/§7, or §6/§7 is amended. Saying which, in one
   sentence, removes the trap.

2. **Ordering trap: W1 needs the tool that W2 builds.** Workstreams are
   declared "in dependency order" (line 26) with W1 first. But spec §5
   (rule 17) says "An agent CANNOT hand-write a note file" and §6 says
   migration happens "via tool". The tool is W2 (lines 52-59). So W1 either
   precedes its own dependency or violates rule 17. Whichever is intended
   must be stated — a one-shot agent will improvise here, and both
   improvisations are bad.

3. **Vault work produces no PR, but "done" is defined in PRs.** Definition
   of done (line 146): "Five PRs ... with green checks". The vault is an
   iCloud Obsidian store, not a git repo — W1's migration and W2's writes
   leave no diff and no checks. This was measured last night: the Agent
   Memory v0 work produced zero repo diff for exactly this reason. As
   written, W1 has no defined evidence artifact. State what stands in for a
   PR on vault-side work (checksum listings, validator output before/after,
   file counts) or W1 will be reported as unevidenced or, worse, as passing
   because "the PR was green" about something else.

4. **W3.2 and W3.3 contradict each other on what "unchanged" covers.**
   W3.2 (lines 86-87) says re-author the K3 fixture's wording because its
   description reads "Synthetic gate fixture". W3.3 (lines 88-91) says
   re-run the gate "UNCHANGED (**same fact**, same task wording, same three
   pre-stated criteria)". Changing the fixture changes the fact. A one-shot
   agent can satisfy either but not obviously both, and the two readings
   produce results that are NOT comparable to the recorded 2-of-3 baseline.
   Specify: which artifact may change, which may not, and whether the re-run
   is a new baseline or a comparison. Getting this wrong silently invalidates
   the only gate number the estate has.

5. **W3.1 lands in a hard-capped surface with no budget stated.** "Inject
   the read protocol into every dispatch grounding" (lines 82-84) touches
   `corpus.Grounding`, which has enforced caps from #1260/#1261
   (`MaxStandingLawMembers = 5`, `MaxStandingLawBytes = 4096`) and a rule
   (#1254) that knowledge content must never become law. The plan does not
   say whether the read protocol counts against that byte budget, whether it
   is standing law or a separate section, or what to do if it does not fit.
   The cap fails the build when exceeded — so an agent that guesses wrong
   gets a hard failure late, in a surface it was not told it was entering.

6. **W4.4's conditional can never be false, and implies merging is
   possible.** Line 114: "if W2 is not merged yet, use the existing push-1
   catalogue verbs". The binding constraint (line 130) is "NEVER merge". So
   W2 is never merged during this one-shot and the conditional is always
   true — but its phrasing invites an agent to check merge state, or worse,
   to merge W2 to unblock W4. Delete the condition and name the push-1 verbs
   outright.

7. **W5 has no acceptance criteria at all.** W1-W4 each carry an explicit
   "Acceptance:" block (lines 48, 75, 93, 116). W5 (lines 119-126) has none.
   A one-shot agent has no way to know when W5 is done, and no reviewer has
   a bar to check it against.

8. **W5's target repo is chosen by an untestable condition.** "the skills
   repo (or dotfiles if the skills repo is not writable)" (lines 121-122) —
   "writable" is undefined (filesystem permission? push access? existence?),
   and the scope map (line 155) names "Skills and/or skills-private" without
   saying which is the skills repo. Three candidate destinations, no
   deciding rule. Name the repo and the fallback test, or an agent will pick
   one and the reviewer will not know whether it picked correctly.

9. **W1's acceptance contradicts INMAPS §1 and §6 on `agent/`.** Acceptance
   (line 50) requires "zero files under `agent/facts/`". Spec §1 says
   `agent/` holds "existing records; migrated by tool over time, **never
   bulk-moved**", and §6 says "`agent/` stays canonical **during
   transition**". "Over time" and "zero remaining today" cannot both hold.
   Same root as finding 1; listed separately because it is the acceptance
   line specifically that a one-shot agent will optimise toward.

10. **MOC proposal is specified but not verified.** W2 defines a concrete
    threshold — "when ≥8 notes share a tag with no hub linking them" (line
    71) — but W2's acceptance (lines 75-78) names only schema rejection,
    vocabulary rejection, and supersede-hides-old. The threshold behaviour
    has no required test, so it can ship unexercised. Given this estate's
    history of guards that pass while doing nothing, that is the defect
    class most worth a mutation check.

11. **W1.3 points at W4 for a surface W4 never covers.** Line 45-46: "global
    CLAUDE.md text belongs to agent-dotfiles — see W4." W4's four items
    (lines 100-114) cover `docs/` reorganisation, the agent roster, an
    AGENTS.md routing line in agent-estate, and source registration — none
    mentions updating global CLAUDE.md's `agent/facts/` references. As
    written, that instruction surface is assigned to nobody and will silently
    keep pointing at a path W1 empties.

12. **"K2 is effectively delivered" overstates the ledger.** Line 23-24.
    `estate features` records candidate-knowledge-inbox as `InProgress`, and
    the non-standing retrieval gate measured 2-of-3 with non-consultation as
    the failure mode. "Effectively" is load-bearing and unexplained; a
    one-shot agent may skip verification it should do. State the ledger
    status instead of a summary adjective.

13. **Restore-on-failure has no trigger and no verifier.** Constraint line
    133: "backups + checksums first, validator after, restore on failure".
    Undefined: what counts as failure (validator non-zero? checksum
    mismatch? a partially-moved batch?), who confirms the restore actually
    restored, and whether the run continues or halts after one. A one-shot
    agent mid-migration with a failing batch and no rule will improvise on
    the operator's memory store — the highest-consequence improvisation in
    this plan.

14. **Pilot-ID reuse rests on an unstated count.** W1.2 (lines 41-42): "Lane
    A's pilot table (handoff) gives the first 10 IDs — reuse them exactly."
    Measured now: `agent/facts/` holds **119** files and `01 - Notes/` holds
    **1**. So at most one of those ten IDs is actually in use, and the other
    nine are reserved-but-unmigrated. An agent expecting ten completed
    migrations will mis-sequence the remainder. Say explicitly: N migrated so
    far, IDs reserved for the rest, verify before reusing.

15. **Minor, but it will cost time:** the plan says "all 119 facts" (line 28)
    and separately "119" in spec §8; both are currently correct (verified
    2026-09-06 18:40). If W1 runs after anything else touches the vault, that
    number goes stale and an agent told "all 119" that finds a different
    count has no rule for what to do. Prefer "all facts under `agent/facts/`,
    count verified at start" over a frozen number.

**Not findings, deliberately:** the workstream *content* is sound and the
scope map is unusually clear (lines 152-173 are the best-specified part of
the plan). Nothing above argues for redesign — every item is a one-sentence
disambiguation that removes an improvisation opportunity from an agent that
cannot ask.

## Director review of push3 plan

Adversarial read of `astra-push3-plan.md` (117 lines) against `inmaps-spec.md`
and the live code, 2026-09-07 ~00:40. Lens: **what wedges or misleads a
ONE-SHOT agent that cannot ask.** Findings only; no edits to the plan. Ordered
by consequence. Claims below were reproduced against the tree, not inferred.

1. **A1's letter subdir makes every migrated note INVISIBLE to retrieval.
   This is the P0 failure class again, at 1,104× scale.** A1 (lines 32–36)
   writes notes to `01 - Notes/01p - Parameters/<ID>.md`. But the knowledge
   index reads notes with a NON-RECURSIVE glob —
   `internal/knowledge/vault.go:79`:
   `filepath.Glob(filepath.Join(vaultDir, "01 - Notes", "*.md"))`.
   A file at `01 - Notes/01p - Parameters/202609061745.md` does not match.
   The vault validator has the same shape: `validate_index.py` line 66 globs
   `(root/../01 - Notes").glob("*.md")`, and line 55 only accepts index links
   matching `\.\./01 - Notes/\d{12}\.md` — a subdir path fails that regex too.
   So as written, A1 migrates every corpus item into a directory the retriever
   cannot see and the validator will reject links to. Either the glob and the
   link regex are extended to the subdir **in the same change** (spec §7b's own
   rule, and the whole lesson of P0), or the notes stay flat in `01 - Notes/`.
   The plan names the validator as a consumer to repoint (line 48) but does not
   name `vault.go`'s glob, which is the one that silently swallows the content.

2. **A5 will silently re-identify all 24 live source records.** A5 (lines
   95–97) says "Extend the catalogue entry shape: locator carries `remote` and
   `local` as SEPARATE fields". `internal/catalogue/register.go`'s
   `identityFor(locator string)` derives every id as `"src-" + sha256(Locator)`
   — Locator ALONE, by explicit design (its comment: never time-based, never
   Kind, so `Register` stays idempotent). 24 records are live in `05 - Sources`
   right now. If the locator becomes a composite, every id changes and all 24
   are orphaned, with nothing in the code reporting it. The plan says nothing
   about id stability. It must say: add the fields ALONGSIDE `Locator` and leave
   `identityFor`'s input untouched — or treat an id change as an explicit
   migration with an old→new mapping and consumers repointed. (Recorded in
   `iteration-queue.md` under P8 as well.)

3. **A3 does not name the guard that protects the file it renames.** A3 (lines
   77–78) repoints "estate + dotfiles docs/CLAUDE.md + vault". The write guard
   for this database is `agent-dotfiles/hooks/ledger-write-guard.sh`, which
   matches on the name `ledger.sqlite3` — a hook, not a doc, so "docs/CLAUDE.md"
   does not cover it. Rename without repointing it and the protection on Jon's
   live corpus **silently lapses**; nothing fails, nothing warns. Worse, the
   compat symlink makes it look fine: writes addressed to the old name still
   trip the guard, while writes to `corpus.sqlite3` sail past. The plan should
   require the guard be repointed AND proven still refusing (attempt a guarded
   write, paste the refusal), not merely edited.

4. **A1→A2 ordering is a hard dependency but is only implicit.** A1 ends by
   deleting `agent/parameters/` (line 46); A2's gate is `ls .../agent` erroring
   (line 70). A one-shot that starts A2 before A1 completes cannot satisfy its
   own gate, and may delete `agent/` with `parameters/` still populated. State
   the ordering as binding, or make A2's gate explicitly conditional on A1.

5. **A1's ID tie-break is undefined for corpus items.** A1 (line 33) says "ID
   per spec §8". §8's rule is `YYYYMMDD` from `created:` plus a 4-digit
   same-day sequence, ordered "by full timestamp where present, then
   alphabetically **by slug**". Corpus items have ids and prompt_ids, not
   slugs. With 1,104 items and many sharing a date, the tie-break decides the
   sequence — and an undefined tie-break is non-deterministic, so a rerun
   produces different IDs for the same items. That breaks the regenerability
   A1 calls "the point" (line 43). Name the deterministic tie-break (corpus
   item id is the obvious candidate).

6. **"Regenerate in place, matched by corpus item id" needs the id stored, and
   the plan only implies it.** A1 line 44 matches notes by corpus item id on
   regeneration; line 40 lists `id`/`prompt_id` as provenance frontmatter. If
   the stored `id:` is the *note* ID (spec §3 requires an `id` field, and W1
   used the 12-digit note id there), then the corpus item id needs its own
   distinct field name or regeneration matches on the wrong key. Say which
   field carries which id.

7. **A2 moves `corpus/` out of the vault with no stated destination-collision
   check.** Line 66 sends it to `~/.local/state/estate/corpus-extraction/`.
   Nothing says what to do if that path exists. Given A3 is simultaneously
   renaming things under `~/corpus/`, and the estate ledger lives under
   `~/.local/state/estate/`, an existing-path collision should be a stop, not
   an overwrite.

8. **The state block is already stale, which is fine — but only one line says
   so.** Line 10 pins `main e4fb7f4`; main is now `9aaaf8f` (P5 batch 1 merged
   after the plan was written). The "VERIFY at your start" instruction covers
   it, and the `agent/` inventory on lines 11–13 is still accurate (verified:
   `00 - Inbox corpus facts INDEX-CONTRACT.md index.md parameters`). Worth
   making the verify step an explicit first action with a recorded result,
   since four of five workstreams branch on that state.

9. **A4's "if README would bloat" has no threshold.** Line 86 lets the agent
   choose README vs `docs/index.md` on an undefined criterion. Two agents get
   two answers; a reviewer has no bar. Name the default (README) and the
   condition for the alternative, or just name one.

10. **No acceptance criteria on A2 or A3 in the plan's own idiom.** A1, A4 and
    A5 carry explicit evidence requirements (report file, mutation check,
    tests). A2 has a gate but no evidence artifact; A3 says "Tests: Path()
    resolves through both names" but nothing about proving the 20+ repointed
    references actually work, or that the DB is byte-identical after the
    rename (sha256 before/after is the obvious proof for an operation on live
    operator data).

11. **Credit where due, and worth preserving:** "vault operations deliver
    reports, not PRs" (line 113) fixes the push-2 gap where vault work had no
    defined evidence artifact because the vault is not a git repo. The
    per-kind count instruction in A1 ("do not trust any number written here",
    line 27) and the #1020 disclosure requirement (lines 28–31) are exactly
    right — cover what vault-view covers, state the remainder, do not silently
    expand scope.

**Not findings:** the workstream content is sound, the scope block (lines
16–19) and "NOT in this one-shot" (lines 103–108) are unusually clear, and the
spec-wins/smallest-reading rule is correctly restated. Every item above is a
one-sentence disambiguation or a named consumer — none argues for redesign.

## Director review of push4 plan

Adversarial read of `astra-push4-plan.md` (95 lines) against
`push4-skills-parameters.md` (375 lines, the declared LAW), P7's merged state,
and the live corpus. 2026-09-07, ahead of the 05:30 deadline. Lens: **what
wedges a ONE-SHOT that cannot ask.** Findings only; no edits to the plan.

1. **FOUR of the plan's cited item ids are absent from its own declared law
   document — and the plan says the law document WINS.** Line 8: "Where this
   brief and that document conflict, that document wins." Measured: the law doc
   contains 31 distinct ids, all 16 hex chars (`it-ea42cf0d6f8a53b7` form). The
   plan cites four ids in 8-char form that appear nowhere in it, not even as
   prefixes:

   | cited in plan | where | in law doc | in corpus |
   |---|---|---|---|
   | `it-c10defd3` | B2, progressive npx install | **NO** | yes |
   | `it-ebe6f2ee` | B3, harness-agnostic/uploadable | **NO** | yes |
   | `it-377e6dbc` | B4, evals-or-named-gap | **NO** | yes |
   | `it-f61bb8e5` | B4, measured-roster rule | **NO** | yes |

   All four resolve in the corpus, so they are real items — the LAW DOC is
   incomplete, not the citations invented. But a one-shot told the law doc wins
   will look them up, find nothing, and have no rule for what to do. That is
   four separate workstream justifications with no retrievable basis: the whole
   of B3, half of B4, and B2's install mechanism. **Cheapest fix: add those four
   items to the law doc before dispatch** (they exist; it is a compile gap, not
   a research task). Second-cheapest: state in the brief that ids absent from
   the law doc are to be read from the corpus directly, with the query given.

2. **Id-format mismatch will cause silent lookup failures even for ids that ARE
   present.** The plan uses 8-char (`it-850afa9e`), the law doc 16-char
   (`it-850afa9e...`). A literal grep for the plan's string finds the law doc
   entry only because it is a prefix — but `it-33e6ad272` (9 chars) and
   `it-c10defd3` (8) are inconsistent even with each other. Pick one form.
   An agent that greps for an exact id and gets nothing concludes "no such law".

3. **B5 inherits the push-3 identity trap but states it as a one-liner.** Line
   71: "ID-stability rule from push 3 binds — locator input to identityFor
   unchanged." That is correct but under-specified for someone who did not live
   push 3: `identityFor(locator)` is `sha256(Locator)` alone, and there are live
   records in `05 - Sources` whose ids change if the locator input changes,
   silently orphaning every reference with nothing reporting it. B5 should
   require the same acceptance push 3 used: **list the existing record ids
   before and after and prove they are unchanged.** Otherwise "unchanged" is an
   assertion.

4. **B2's "converge to one canonical home, leaving no copies" collides with
   the plan's own no-deletion rule.** Line 32/79: "do not delete anything
   (deletions are a list for Jon, always)". Removing a duplicate copy IS a
   deletion. As written a one-shot must either violate the no-deletion
   constraint or leave the duplicate, and it cannot ask which. State explicitly
   whether removing a *verified byte-identical duplicate of a Jon-authored
   skill* counts as deletion (recommend: it does not, if the canonical copy is
   proven identical first — but the plan must say so, and require the identity
   proof).

5. **B1 asks for a "generated-not-hand-kept" manifest but names no generator
   and no regeneration proof.** P7 merged exactly this lesson: `SKILLS-INDEX.md`
   was deleted *because* per-skill descriptions duplicated `SKILL.md`
   frontmatter, and its replacement had to be generated from frontmatter or
   link-only. B1 risks recreating that artifact under a new name. Require: the
   generator is committed, rerunnable, and reproduces the committed output
   byte-for-byte — the acceptance P7's own review used.

6. **B4's "could-not-measure" is the right verdict but has no recording
   location.** The plan says say could-not-measure, and separately says the
   eval status is "GENERATED into the routing surface". If a skill's status is
   could-not-measure, does that string go in the generated surface, or only the
   report? An agent will guess, and guessing toward "omit it" turns an honest
   gap into a silent one — the exact failure `it-377e6dbc` exists to prevent.

7. **Scope-leak risk is handled well, with one hole.** Lines 18-20 correctly
   fence agent-dotfiles off (an active lane owns A3/P11) and route findings to
   a handoff item. But B5 line 72 says "Update the vault Start Here / Sources
   pointer records if the routing surface moved" — **vault writes are not
   fenced**, and a lane may be mid-flight in the vault (the conventions-note
   repair was live tonight). Add the same rule: vault changes go through the
   reviewed tool, and if another lane is writing the vault, it is a handoff
   item, not a commit.

8. **"FIRST ACTION: verify and record current state … Do not trust this brief's
   numbers" (lines 12-14) is the strongest line in the plan** — it is what
   saved P9/P10 from a stale-checkout error tonight, and what caught a false
   P11 report. Worth preserving verbatim in future briefs. One addition: it
   should name `origin/main` explicitly as the comparison point, because a
   local checkout being behind is precisely how tonight's false P11 "stale
   copies" report was generated (dotfiles local was 30+ commits behind).

9. **Minor: P7's state is asserted, not cited.** Line 10 says "P7 done". It is —
   Skills #303 merged `789dd7433`, and `SKILLS-INDEX.md` is gone from
   `origin/main`. Giving the merge sha lets the one-shot verify rather than
   trust, consistent with line 14's own instruction.

**Not findings:** the law-doc-wins hierarchy, the no-deletion/lists-for-Jon
posture, the employer-content flag-don't-move rule, the public-repo naming
constraint, and "no evals RUN" are all clear and correctly scoped. Nothing here
argues for redesign — every item is a one-sentence disambiguation, and finding
1 is a five-minute compile fix before dispatch.

### Director review of push4 plan — finding 1, CORRECTED after the fix landed

Finding 1 said the four plan-cited ids were "a compile gap, not invention". The
gap is now closed (law doc 375 → 460 lines, all four present). But completing it
revealed the more consequential fact, which changes what finding 1 means:

**All four are weight `preference`, not `hard`.** Measured:

```
it-c10defd3a1e55022   preference / parameter / acted          (B2, npx install)
it-ebe6f2eef9425eee   preference / parameter / acknowledged   (B3, harness-agnostic)
it-377e6dbc6abcd917   preference / parameter / acknowledged   (B4, evals-or-gap)
it-f61bb8e506b054b9   preference / parameter / acted          (B4, measured roster)
```

They were absent from the first compile because the sweep brief correctly said
`items where weight='hard'` (per #1020). The compile was RIGHT; the plan is
citing preference-weight items as though they were binding law.

**Why this matters before dispatch, not after.** The plan declares
`push4-skills-parameters.md` THE LAW and says it wins on conflict. A one-shot
that cannot ask will treat every entry as equally binding. But a `preference`
and a `hard` parameter do not bind the same way — that distinction is the whole
reason the corpus carries a weight column, and the reason the sweep brief asked
for parameter/directive/correction classification per entry.

Concretely: B3 (harness-agnostic packaging audit) and half of B4 (eval-status
honesty) now rest entirely on `preference`-weight items. That does not make
them wrong to do — Jon stated them, they are `acted`/`acknowledged` — but it
does mean a one-shot should not refuse or hard-fail work on their account, and
should not present findings against them as violations of law.

**Recommended one-line addition to the plan, before dispatch:** state that
entries in the law doc carry their corpus weight, that `hard` entries are
binding constraints and `preference` entries are stated preferences to honour
but not to hard-fail against, and that the doc's own per-entry classification is
authoritative on which is which. The law doc already records the weight per
entry, so this costs nothing to adopt.

**Not a reason to delay dispatch.** The gap is closed, the items are present and
correctly classified, and the plan is workable as written. This is a precision
fix to how a one-shot should weigh them.

### Push 4 law doc — full weight audit (Director, 04:47, ahead of dispatch)

My finding-1 correction checked only the four items I had flagged. Auditing
every id in the document, against the corpus:

```
law-doc ids: hard=31  preference=4  other=0  not-in-corpus=0
```

Three things this settles for the 05:45 dispatch:

1. **Every cited id resolves.** Zero not-in-corpus. The document is fully
   traceable — a one-shot can look up any entry and find it.
2. **The preference entries are exactly the four I flagged**, no others. So the
   weight-semantics clause I recommended covers the whole exposure: 31 hard
   constraints bind, 4 preferences are honoured but not hard-failed against.
   Nothing else in the document needs re-reading through that lens.
3. **The original compile was correct on its own terms.** It swept
   `weight='hard'` per #1020 and returned exactly the 31 hard items. The four
   preferences entered only because the plan cited them and I asked for them to
   be added — they are additions to a correct compile, not corrections of a
   faulty one. Worth stating plainly so the compile is not treated as
   unreliable at 05:45; it was right, and the gap was in the plan's citations.

No change recommended to the document. The single one-line clause already
proposed (entries carry their corpus weight; `hard` binds, `preference` is
honoured but not hard-failed) is sufficient and complete.

## Push 4.5 — MEASURED BASELINE (from the Push 3 gate, Part B, verbatim)

Plan-author ruling, 2026-09-07: Part B's tag-filter FAIL measures Push 4.5's
own deliverable, not a Push 3 regression — topical tags were sequenced into
Push 4.5 before the gate ran. **NOT waived.** The measurement is preserved
here as Push 4.5's starting baseline, so its completion can be judged against
a real number rather than an impression.

Measured 2026-09-07 by direct vault inspection (not by asking an agent),
at `main` = `ac7f1e5`, vault post-A2-completion:

```
notes carrying a `tags:` line
  01 - Notes/01p - Parameters : 2638 of 2638
  01 - Notes/01f - Facts      :    0 of  119

distinct tag values vault-wide : 5
  note · 06-2026 · 07-2026 · 08-2026 · standing-rule

topical tags                   : 0
  "azure" appears in no tag anywhere in the vault
```

**What works today:** filtering by an EXISTING tag succeeds — `standing-rule`
selects 2,430 notes. The tag mechanism is not broken; the vocabulary is
structural and temporal only.

**What fails today:** a topical tag-filter question ("show me the azure
parameters") is unanswerable, in Obsidian and in `estate knowledge query`
alike, because no topical tag exists to select on.

### Three constraints Push 4.5 must satisfy, measured before it starts

1. **Facts carry no tags at all** — 0 of 119. The associative pass covers
   `01p - Parameters`; it must also cover `01f - Facts` or half the vault stays
   untaggable.
2. **`vault.go` has no tag handling.** Grepping it for `tags` returns nothing.
   Vault-note frontmatter tags never reach the index today.
3. **The field already exists and is already searched.**
   `internal/knowledge/knowledge.go` defines `SynapticTags` ("#hashtag,
   associative"), and `bm25.go` folds it into searchable text. The ONLY
   populator is `stars.go` (GitHub stars).

**Consequence, stated so it is not discovered late:** the tagging pass could
tag all 2,638 notes correctly, the validator could pass, Obsidian could answer
perfectly — and `estate knowledge query` would still return nothing, because
the tags never enter the index. That presents as "tags are decorative", the
exact Second Brain failure mode Push 4.5 exists to avoid. **Wire `vault.go` →
`SynapticTags` BEFORE or WITH the pass, never after** — otherwise the pass is
unverifiable while it runs.

**Push 4.5 gate should re-measure these same numbers.** Success looks like:
topical tags present on both subdirs, a tag-filter question answering in BOTH
Obsidian and `estate knowledge query`, and the distinct-tag-value count risen
well above 5 with a governed vocabulary.

### Push 4 B1/B2 — Director pre-check, measured 05:14 (do not rediscover)

Measured read-only, so B1 starts from ground truth rather than discovery. This
does NOT do B1's work — it records the facts that would otherwise wedge it.

**The installed surface is a SYMLINK FARM, not a copy set.** This is the single
most important correction to B1/B2's model:

```
~/.claude/skills                 42 entries, ALL 42 are symlinks, 0 broken
                                 0 SKILL.md files of its own
  40 → ~/source/repos/Personal/Skills/skills/<name>   (the public repo)
   2 → ../../.agents/skills/<name>                    (diagram-design, tmux)

Skills (public repo)             41 skill dirs, 41 SKILL.md
skills-private                    1 SKILL.md
```

**Consequences B1/B2 must not get wrong:**

1. **A name present in both "installed" and "repo" is NOT a duplicate.** It is
   one artifact, symlinked. B1 asks for "any COPY found in more than one place"
   — a naive name-overlap check reports ~40 false duplicates here, and B2 would
   then "converge to one canonical home" on skills that already have exactly
   one. **Compare by resolved target, not by name.**
2. **B2's one-home enforcement is largely already satisfied** by this
   mechanism. The install path Jon's parameter describes
   (`it-c10defd3a1e55022`, npx-style rather than baked in) is currently realised
   as symlinks — worth stating in the report as the observed state, since it
   differs from the parameter's stated mechanism without contradicting its
   intent (nothing is baked in; the repo remains the single home).
3. **Two installed skills point OUTSIDE the public repo** — `diagram-design`
   and `tmux` → `.agents/skills/`. Those are the genuine one-home questions
   worth B1's attention: third home, unclear authorship, and exactly the shape
   `it-957502d135216f90` (third-party skills committed into Jon's repos) is
   about. **Do not delete** — Jon-list them with a stated disposition.
4. **Zero broken symlinks.** The farm is healthy; there is no cleanup task
   hiding here.

**Count reconciliation to expect:** 41 repo skills vs 42 installed entries vs
40 symlinks into the repo. That arithmetic (41 = 40 linked + 1 unlinked; 42 =
40 + 2 external) is the reconciliation manifest's first check, and any B1 output
that does not account for all three numbers has missed something.

## Push 4 — merged, and the report is a STUB. Queued items below.

**Skills #304 MERGED `36b820661`** — reconciliation manifest + evidence-aware
routing. Full protocol: lane-a REQUEST CHANGES at `8b54e3cfd` (aggregate
undercount), lane-b fix pass, lane-c APPROVE at exact head `38da45520`, all
three checks green, reviewer neither author nor fixer.

### FINDING: `run/astra-push4-report.md` is a pre-implementation stub

It contains only the verified initial-state block (05:41) and ends
*"Implementation pending. No merges authorized."* It carries **no Jon-list and
no dotfiles handoff items** — the artefacts that were supposed to be queued from
it do not exist in it. The report was written before the work and never updated
after #304 was produced.

This is worth recording rather than papering over: Astra's *definition of done*
required a final report with "the Jon-list (ambiguous dispositions + deletion
candidates), dotfiles handoff items, honest FAILs/UNRUNs". That deliverable is
outstanding even though the code deliverable merged.

### The Jon-list content DOES exist — in the merged artefact, not the report

Recovered from `docs/skills-reconciliation.json` on `origin/main`:

```
environment_counts:
  public_skills            41      installed_skills         42
  private_skills            1      installed_outside_public  2
  third_party_installed     1      repo_duplicate_names      0
  unresolved_installed      0
41 skill rows, each with authorship / canonical_home / eval / home_state /
installed / packaging / scope / scope_basis
```

**Ambiguity flagged for Jon, in the data:** `tmux` — authorship class
`jon-or-agent-attributed`, with the basis stated honestly as *"Earliest
available addition commit; git attribution does not prove absence of upstream
copying."* That is the right posture (uncertainty named, not resolved by
assertion) and it is exactly the `it-957502d135216f90` third-party question.
`third_party_installed: 1` corroborates it.

### QUEUED — not executed

1. **Astra's Push 4 report needs completing** — Jon-list, dotfiles handoff
   items, honest FAILs/UNRUNs. The data exists in the merged JSON; the narrative
   deliverable does not.
2. **`tmux` and `diagram-design` dispositions** — both install outside the
   public repo (`../../.agents/skills/`); `tmux` additionally carries unresolved
   authorship. Jon decides; nothing deleted, nothing moved.
3. **Dotfiles handoff items: NONE RECOVERABLE.** The report names none and I
   will not invent them. If Astra found dotfiles work, it is unrecorded — and
   the dotfiles checkout still holds unpushed guard work (backed up, untouched),
   so nothing should be executed there regardless.

## Director review of push45 plan

Adversarial read of `astra-push45-plan.md` (88 lines) against the live code, the
vault, and the recorded Part B baseline. Lens: **what wedges a ONE-SHOT that
cannot ask.** Findings only; no edits to the plan.

1. **C2's "generator must MERGE tags" is a real, unbuilt code change — and if it
   is skipped, the entire 2,757-note tagging pass is silently destroyed on the
   next regeneration.** The plan states the requirement (lines 45-48) but treats
   it as a property to test rather than a change to build. Measured:
   `internal/candidates/inmaps.go`'s `noteBytes()` REBUILDS the whole
   frontmatter block from the proposal struct —
   `fmt.Sprintf("---\ntype: %s\nid: %s\ntitle: %s\ndescription: %s\ntags: %s\n…")`
   with `tags, _ := json.Marshal(p.Tags)`. Anything already on disk is
   discarded; there is no read-merge-write anywhere in that path. So today
   regeneration overwrites tags, full stop. **This must be built and land with
   or before C2, and the plan should say "change `noteBytes` to merge" rather
   than "tags persist through regeneration", which reads as an assertion about
   existing behaviour.** This is the P9 failure class for this push: do the bulk
   work first and it evaporates.

2. **C1's ordering is right and is the plan's best decision — but its acceptance
   does not prove the thing that matters.** "A tagged fixture note is findable BY
   tag" (line 26) tests the fixture path. The recorded baseline failure was that
   REAL vault notes' tags never reach the index because `vault.go` has no tag
   handling at all. Require the end-to-end proof instead: tag one REAL note
   through the tool, rebuild a private index, and retrieve it BY TAG via
   `estate knowledge query` — the same shape the Part B gate question asks. A
   fixture-only test would pass while the live path stays broken, which is
   exactly how the original gate failed.

3. **The vocabulary target collides with what already exists.** C2 says "keep it
   SMALL (target ~30-60 values)" (line 38). `99 - Meta/tags.md` already carries
   **35** entries. So "extend to 30-60" is ambiguous — is 35 already at target,
   or is the topical set additional (making 65-95)? A one-shot cannot ask. State
   whether the target counts existing structural tags or only new topical ones.

4. **C4's gate question cannot be answered by the plan's own scope.** Line 70
   requires "show me the azure parameters" to answer in BOTH Obsidian and
   `estate knowledge query`. Obsidian's tag pane reads the vault directly, so
   that half works once tags exist. But `estate knowledge query` reads the
   COMPILED INDEX — and the standing constraint (repeated in this plan's own
   constraints, line 80) is that the protected shared index is never
   regenerated. So the query half can only be demonstrated against a private
   `ESTATE_KNOWLEDGE_INDEX` build. Say so explicitly, or the one-shot will
   either regenerate the protected index (violating a hard rule) or report the
   gate unanswerable.

5. **C3's "contradiction candidates" (line 55) is the one proposer input that
   can manufacture false knowledge.** Shared sources, shared rare tags and
   explicit textual reference are all mechanical. "Same subject, opposing
   statements" is a judgement, and a wrong `contradicts` relation asserts that
   one of Jon's recorded positions is false. Require that contradiction
   proposals carry both statements verbatim (via the law-doc's clean text rule,
   never `text_raw`) so a reviewer can see what is being claimed, and that
   `contradicts` is never auto-accepted even inside the bounded starter set.

6. **C3's bounded starter set has no stated selection rule.** "~50-100
   relations" (line 62) with no basis for which — highest-confidence? most
   central notes? A one-shot will pick, and the choice determines whether the
   accepted set is representative or merely convenient. Name the ordering
   (recommend: highest mechanical confidence first — shared source, then
   explicit textual reference — with `contradicts` excluded from auto-ordering
   per finding 5).

7. **"2,757 notes" (line 32) is correct today — verified: 2,638 + 119 — but the
   count is load-bearing and will drift.** C2 batches over it. Better to phrase
   as "every note under `01p - Parameters/` and `01f - Facts/`, count verified at
   start", consistent with the plan's own FIRST ACTION rule.

8. **Single-writer coordination is stated but not testable.** Line 18-19 says
   coordinate via report handoff, never concurrent writes. There is no way for a
   one-shot to DETECT another writer. Give it a concrete check — e.g. confirm no
   lane holds the vault by looking for recent mtimes or an agreed lock file —
   or state that the Director guarantees exclusivity for the window, which is
   the honest arrangement given the Director controls dispatch.

9. **Credit, and worth keeping:** C1-before-C2 is the correct sequencing and the
   plan says WHY ("tagging before wiring would repeat the decorative-tags
   failure Jon measured in his Second Brain"). The MOC-birth threshold firing
   naturally from tag density (C4) rather than being hand-created is the right
   mechanism. And "propose-at-scale without review is the anti-pattern"
   (line 63) is exactly the discipline that has held all night.

**Not findings:** scope fencing (dotfiles/skills/Hill90/Second Brain), the law
weights rule, tool-only vault writes, W1 batch discipline, and the
wind-down-at-20%-quota instruction are all clear.

## Jon-list — RESOLVED to two clean decisions, with evidence. 08:25.

Lane-a gathered the evidence (`run/jon-list-skills-evidence.md`, 15 KB); the
Director independently verified the decisive fact below. Both items turn out to
be simpler than the merged manifest could show.

### What `~/.agents/skills` actually is

**Not a git repo and not a checkout** — a plain directory of 16 skill dirs plus
`.skill-lock.json`, which records per-skill install provenance (source repo,
type, URL, exact `skillPath`, install-time folder hash, timestamps). It is a
neutral cross-harness skill MIRROR, populated by agent-dotfiles' own
`scripts/sync.py::ensure_neutral_skills()` against the Tier-A roster in
`settings/default-skills.txt`. So the two "installed outside the public repo"
entries are **an intentional install mechanism with provenance tracking**, not
stray copies to converge.

### `tmux` — authorship question ANSWERED. It is Jon's own.

The merged manifest flagged it `jon-or-agent-attributed` with the honest caveat
*"git attribution does not prove absence of upstream copying."* That caveat was
right, and the install lock settles it — Director-verified directly:

```
tmux -> sourceType github
        sourceUrl  https://github.com/jonhill90/skills.git
        skillPath  skills/tmux/SKILL.md
        installedAt 2026-08-23T04:54:24Z
```

**It was installed FROM Jon's own public repo.** Not third-party, no upstream
copying question. The manifest could not see this because it reasoned from git
attribution; `.skill-lock.json` records the actual install source. Worth noting
as a method lesson: provenance lived in the install record, not the history.

### `diagram-design` — genuinely third-party, cleanly licensed

```
diagram-design -> sourceUrl https://github.com/cathrynlavery/diagram-design.git
                  skillPath skills/diagram-design/SKILL.md
                  installedAt 2026-08-18T02:44:59Z
```

Third-party, MIT-licensed, external author, present in neither the public Skills
repo nor skills-private. `it-957502d135216f90` concerns third-party skills
**committed into Jon's repos** — this one is not committed anywhere of Jon's; it
is mirrored by the installer with its source recorded. That is arguably the rule
being satisfied, not violated.

### The two decisions, now decidable

1. **`tmux`** — no decision needed on authorship. If anything, the manifest's
   `third_party_installed: 1` count should be re-examined, since the one genuine
   third-party install is `diagram-design`, not `tmux`.
2. **`diagram-design`** — Jon's call, and it is a small one: keep the mirrored
   third-party skill as-is (provenance recorded, MIT, nothing committed to his
   repos), or stop mirroring it. **Nothing was moved, deleted, or repointed.**

**Still UNKNOWN and not guessed:** whether either skill is actually used in
practice, and whether any other machine or private harness config references
them. Lane-a stated both plainly rather than inferring.

### #1254 — Director pre-check, measured 11:10 (brief-ready, NOT dispatched)

#1254 says nothing pins the rule that published memory / knowledge content can
never enter `Hard()`/`Grounding()` as law. Measured against `main` = `754322c`:

**Partly pinned — better than the issue assumes, and narrower than it needs.**
`internal/corpus/context_leak_test.go` has
`TestDerivedContextCannotReachHardOrGrounding`, which does real work:

```
Hard() surfaced derived context as a hard item        -> fails
Grounding() rendered the derived context into the preamble -> fails
Grounding() dropped the real hard directive while guarding  -> fails
```

That third assertion is the good one — it catches the lazy fix where you block
the leak by breaking legitimate grounding.

**What is NOT pinned:** that test covers *derived prompt context* only. It says
nothing about the other two content classes that must never become law:

1. **Published vault facts** — anything under `01 - Notes/01f - Facts/`. Today
   they cannot reach `Grounding()` only because `internal/corpus` never reads
   the vault. Safety by non-interaction, not by assertion — and Push 4.5 is
   about to wire vault tags into retrieval, which moves knowledge closer to this
   boundary.
2. **Standing-law members** are the deliberate exception and ARE pinned
   separately (`standinglaw_test.go`, capped and declared-membership-only), so
   the rule is "declared members yes, everything else no" — a test must assert
   the *no* half for vault content, not just for derived context.

**Brief shape when dispatched:** extend `context_leak_test.go` (do not create a
parallel file — one home for this rule) with a case that publishes a vault fact
NOT in `StandingLawSet`, then asserts it appears in neither `Hard()` nor
`Grounding()`, while a declared member still does. Mutate the exclusion, confirm
failure, revert. Match on rendered preamble content, never on package imports —
a string copied across defeats an import-based test.

**Not dispatched:** `internal/corpus` is adjacent to Push 4.5's C1 (vault tags →
retrieval) and Astra's window is ~11:45. Starting now risks the collision class
that produced duplicate #1273. Queue for after Push 4.5 lands, when the tag
wiring's shape is known — the test should pin the boundary as it will then be,
not as it is now.
