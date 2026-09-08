# Claude execution handoff — recover a usable knowledge system

2026-09-07. Start here. Read [PRD](PRD.md), [SPEC](SPEC.md), then the compact [evidence](EVIDENCE.md). Jon requested this plan and document preparation; this packet is not a record of implementation or a new blanket merge authorization.

## Integrate with the active crew first

**Post-update checkpoint:** read [DIRECTOR-HANDOFF.md](DIRECTOR-HANDOFF.md) first for the newer main/PR state, vanished scratch recovery copies and restarted worker sessions. Jon subsequently authorized sending that execution handoff to director:1 and the separate [Fable advisory update](FABLE-ADVISOR-UPDATE.md). The earlier lane snapshot below is historical context, not the current assignment state.

**Director:1 remains the run manager.** This packet supplies recovery work for that director to sequence into the existing run; it does not start a second run or replace the current lane assignments. No worker should take this document as an instruction to abandon, duplicate or overwrite in-flight work.

Snapshot from explicit read-only tmux captures on 2026-09-07, corroborated with GitHub where stated:

| Lane | Latest observed work | Coordination consequence |
|---|---|---|
| director:1 (`@1`) | Managing P12; reports retargeting dotfiles to the missing lint after discovering the sort already shipped. | Owns assignment, integration and existing merge decisions. |
| agent-estate:1 / lane-a (`@395`) | Reports posted APPROVE for estate #1285 at `538c6e449fbaccfebd65b6b40018d1a126dbeceb`. | Retain review ownership until the director completes that integration. Do not commission a duplicate review from this packet. |
| agent-estate:2 / lane-b (`@396`) | Authored/fixed #1285 at that head; latest output says holding for renewed review. GitHub confirms the PR remains OPEN at inspection. | Owns the estate docs changes; coordinate knowledge PRD/SPEC and routing integration through this owner. |
| agent-estate:3 / lane-c (`@397`) | Actively implementing/verifying dotfiles docs-lint against the already-sorted remote tree. | Preserve this work and its checkout; do not reassign or repeat the docs sort. |

GitHub independently confirms skills #305 merged at `80d9f8e8` and dotfiles #345 merged at `9648535c`. Their completed sorting is input, not new recovery work. Pane claims about tests/reviews are observations of reports, not this packet's independent test verification.

Before assigning each recovery slice, the director refreshes only the relevant lane/PR state, records its owner and overlapping files, and integrates current work where needed. Rebase dependent recovery work onto those landed changes. Reuse the same lanes as they become available; no new crew or competing supervisor. An idle-looking prompt does not release an assignment by itself. The time estimates below begin when the director assigns each slice.

## Goal / signal

Repair the actual notes and the path by which a fresh agent uses them. Demonstrate source → reviewed knowledge → current retrieval → correct task behavior, including a disputed answer and a changed source. Preserve the existing architecture and useful merged work.

Working diagnosis: unreadable generated projections, stale entry points/indexes, and incomplete integration let agents confidently act on an older or inappropriate picture. Fable’s “enforcement layer is the missing piece” is a hypothesis; hooks alone cannot repair these failures or prove understanding.

## Scope and operating budget

For each recovery slice, one implementation owner and one independent reviewer selected from the existing three lanes; preserve their current assignments until the director hands them off. No new council, recursive agents or broad audit restart. Use the already-available Claude capacity. No new Astra calls are required by this plan. Owners exchange file paths, changed-state summaries and exact failing evidence rather than full transcripts.

Target a 3–4 hour first delivery session, as a planning estimate, not a promise. Checkpoint after each working slice and at 60-minute intervals. If a slice exceeds its timebox, deliver the verified part and name the remainder; do not relabel full architecture completion. No recurring quota polling, new watcher, scheduler redesign, model experiment, or background automation is needed to execute this packet.

## Implementation steps

### 0. Establish one current working base — 15 minutes

Director checks current remote main and open PRs once. At inspection, estate main was `8ba75ac`, local checkout was three commits behind, and #1285 owned docs classification. The shared dotfiles checkout was ahead 1/behind 13 with uncommitted guard/lock work and an untracked doc. Preserve it; use an isolated current-main checkout for independent work. Do not turn preservation into another owner question unless the proposed change actually overlaps that work.

Use the existing estate branch/review protocol; verify whether a run-specific exception still applies rather than extending an old one. Implementation owner never self-merges. Do not disturb Fable's terminal or dispatch more lanes for orientation.

Pin a read-only corpus snapshot and source watermark; inventory affected vault files and hashes; verify a restore into a disposable location. Record where candidate state actually lives. Confirm no concurrent live vault writer. Keep the shared index protected. Reuse existing verified backup tooling where applicable.

### 1. Repair the projection producer and prove the result — 60 minutes

Owner: `internal/vaultview`, necessary publication metadata support in `internal/candidates`/`internal/notemeta`, and focused tests. No layout changes.

Red: reproduce boilerplate descriptions/footer, type-plus-ID titles, and an old task directive displayed as apparent standing law using synthetic examples with the real nested layout. Include an ambiguous fragment and a previously edited/tagged note.

Green: implement SPEC §2. Present readable note-specific titles/descriptions; keep provenance in metadata; preserve meaningful body content and all IDs/links. Correct misleading authority presentation using explicit scope, without changing `StandingLawSet` or silently weakening binding corpus rules. Context-dependent enrichment is reviewed and survives regeneration.

Pilot 12 real notes selected before editing: four fallback titles, four fragments/short commands, four already-useful notes, including overlap with associative tags/Relations and inactive/draft states where available. Read the whole before/after files. Reviewer checks meaning against cleaned source and context, not a length ratio. Do not force an ambiguous note into an invented fact to finish the batch.

After pilot approval, run mechanical repair over all 2,638 managed projections in checksummed batches. Apply only supported editorial changes; list every unresolved title/fragment separately. Validate every batch; second unchanged run must write zero files. Report mechanical completion separately from editorial completion. If the editorial tail is large, it remains an explicit next batch, not “all notes repaired.”

### 2. Make current knowledge reachable — 30–45 minutes

Owner: retrieval/grounding integration and the documentation owner for routing. Coordinate with #1285; do not run a parallel docs reorganization.

Correct the installed/session references to `agent/index.md`, `agent/facts` and the old lifecycle paths. Use root `index.md` and current INMAPS routes. Rewrite the current knowledge guide sections that still claim the catalogue has not landed; historical descriptions belong behind a historical link.

Build and select a private session index using current main plus the reviewed changes. Reuse the existing `ESTATE_KNOWLEDGE_INDEX` override and the registered repo locators. Verify from both an estate worktree and a dotfiles worktree. The user must not need to know which index happens to be stale. Report unavailable sources explicitly; never silently fall back to an old current-looking answer.

### 3. Close publication and correction behavior — 45–60 minutes

Owner: `main_candidates_register.go`, candidates/catalogue boundary, focused integration tests. Finish the source-ID lookup described in SPEC §3; verify current revision at acceptance; pin discard/revision transitions. Prove it first in isolated corpus/catalogue/vault/index fixtures.

Run one authorized real, clearly supported learning through the same path. Verify its persisted candidate/review state, canonical note or repo receipt, and retrieval. Then change an isolated source and prove dependent knowledge needs review. No fake source hashes, prompt rows, or “published” labels standing in for a real write.

After publication, rebuild/select the private index and record a new final source watermark. After the isolated drift transition, rebuild that fixture's own index too. Run the final demonstrations against these final revisions, never the index from step 2. Preserve the before/after watermarks rather than comparing results from different source states as if they were one snapshot.

Update the existing knowledge-session/correction instructions through their canonical deployment path. Add only the smallest evidence-recording seam needed for SPEC §4. Do not implement the proposed zero-tool Stop gate. Its counterexample is a `pwd` call followed by the same unsupported claim.

### 4. Demonstrate behavior and publish the handoff — 30 minutes

The independent reviewer uses fixed tasks, normal routing, and no seeded answer/keywords. Record exact task, code revision, private-index revision, retrieved evidence, response/action, expected behavior, pass/fail and elapsed usage where actually measurable.

1. A fresh estate agent obeys an applicable standing constraint during an ordinary task.
2. A fresh dotfiles agent finds the same cross-project knowledge and the correct local canonical repo source.
3. An expired task directive does not become a present global instruction; a genuinely binding rule still binds. Exercise both direct corpus results and vault projections of the same item, then the combined query. Applicability must survive either route; repairing only the Markdown presentation is insufficient.
4. A superseded or source-stale learning is not offered as unqualified current truth.
5. After a disputed factual assertion, the agent inspects the actual artifact and corrects its answer; irrelevant tool activity does not pass.

Run existing documented golden-query checks where relevant; preserve their old baseline separately. These five cases extend the earlier descriptive demo, which only established that a worker could describe the pipeline.

### 5. Re-triage and editorial backlog — begin after the operating path works

Use the fixed 7,311-ID manifest; preserve the 3,514/3,797 partition as the observed baseline, not as a judgment of correctness. Prioritize all 135 retracted/needs_review rows and the specifically disputed retractions. Review source/context, author, scope, duplication and reason; emit attributed proposed decisions. Inspect the rest in bounded contiguous batches with ID coverage receipts. No blanket restoration. Sensitive material stays private.

Track every unresolved projection and every unreviewed retraction. A census verifies coverage, not meaning. Do not claim this Astra pass independently read all item bodies, and do not inherit Fable's semantic verdict from a coverage report.

## TDD matrix — proposed tests, not existing/passed tests

| Contract | Test to add before the fix |
|---|---|
| Useful rendering and explicit uncertainty | `TestProjectionUsesReadableMetadataWithoutBoilerplate`; `TestAmbiguousProjectionRemainsUnresolved` |
| Identity, editorial content and associations survive | `TestProjectionRegenerationPreservesIdentityAndEditorialFields`; extend existing regeneration/tag tests |
| Publication is not standing-law membership | Extend production-shaped INMAPS law-separation tests from #1280; include the opposite failure direction |
| Source revision is real | `TestCatalogueCandidateAcceptRejectsChangedRevision`; `TestDiscardedCandidateCannotPublish` |
| Selected private index has current routes | `TestKnowledgeConsultRejectsStaleEvidence`; nested-note/deprecated-note regression tests |
| Behavioral use, not tool count | The five fixed agent-evals cases above, including irrelevant-tool and legitimate no-tool response counterexamples |

## Verification matrix

Commands run from the implementation worktree. Save actual stdout/stderr and exit codes. The first two commands below exist now; proposed new test names only become meaningful after implementation.

```sh
go test ./src/estate/internal/vaultview ./src/estate/internal/candidates ./src/estate/internal/knowledge ./src/estate/internal/corpus -count=1
go vet ./src/estate/internal/vaultview ./src/estate/internal/candidates ./src/estate/internal/knowledge ./src/estate/internal/corpus
# Integration build, with an explicit disposable index path selected by the worker:
ESTATE_KNOWLEDGE_INDEX="$PRIVATE_INDEX" go run ./src/estate knowledge
ESTATE_KNOWLEDGE_INDEX="$PRIVATE_INDEX" go run ./src/estate knowledge query --private --json 'source:vault-fact knowledge'
```

Use a verified non-shared absolute path for `PRIVATE_INDEX`. Check returned paths exist and are current, not merely `state=matched`. Run the existing vault validator at `99 - Meta/tools/validate_index.py`; capture its real diagnostics. Writer dry-run/apply syntax must be confirmed against the changed CLI. Require unchanged-input rerun `changed=0`, unchanged ID map, preserved evidence hashes, and tested restore. Run the full affected module suite once after integration, with isolated homes/state; never write tests against live corpus/vault or default tmux.

## CI / drift gates

Keep focused regressions in the Go suite. Reuse the active P12 docs-lint and routing work; do not invent another lint project. Pin currently supported nested paths in fixtures and fail on reintroduced legacy current-entry links. Behavioral evidence belongs in agent-evals; a green Go suite alone cannot mark K1/K7 behavior delivered. Reviewer names mutations that actually failed, including over-restriction as well as missing checks.

## Risks and mitigations

| Risk | Response |
|---|---|
| Rewrite fabricates meaning or erases uncomfortable evidence | Source-context review; explicit unresolved state; no corpus deletion; preserve IDs and old evidence. |
| Migration overwrites another lane or accepted metadata | One vault writer, fixed watermark, before-hash check, durable editorial preservation, verified restore. |
| Ceremony consumes remaining usage | One owner/reviewer; one fix pass under existing protocol; changed-state updates; no all-corpus injection or extra council. |
| “Enforcement” produces false confidence | Evidence validity plus independently scored behavior; irrelevant-tool counterexample; no claim of mechanically proving understanding. |
| Pending docs/authority decisions get silently settled | Integrate only the knowledge slice with the active owner; keep Markdown-only operation/vault Git/native-memory override explicitly open. |

## Definition of done

- [ ] Readable pilot independently checked against sources; full mechanical projection repair verified; unresolved editorial count visible.
- [ ] Stable IDs, provenance, lifecycle and associations preserved; rerun writes zero files; restore demonstrated.
- [ ] Current private retrieval selected in both repos; stale/missing evidence disclosed; protected shared index unchanged.
- [ ] Catalogue-to-publication integration and real persisted write demonstrated with revision/drift checks.
- [ ] All five behavioral cases pass with evidence independent of the producing agent.
- [ ] PRD/SPEC and deployed entry points match shipped behavior; no stale “not merged” current guidance.
- [ ] Full semantic re-triage/editorial backlog remains enumerated until finished; completion labels match that scope.

## Stop conditions / exclusions

On backup mismatch, changed source watermark, unexpected vault content, or missing authority for a live write, stop that mutation, preserve evidence, and continue independent safe work. On failed behavioral evidence, fix the observed cause; do not change the task to make it pass. A second failed PR review follows the existing close/handoff rule, never a quiet third round.

No deletions, shared-index regeneration, credential changes, Second Brain edits, new backend, vault Git migration, harness-memory replacement, broad docs cleanup restart, supervisor redesign, or new recurring job. Existing unrelated PRs remain owned by their current lanes. This packet authorizes no terminal input or merges.
