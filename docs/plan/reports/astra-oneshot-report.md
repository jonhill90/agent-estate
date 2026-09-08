# Astra one-shot report — 2026-09-06

Four PRs opened; nothing merged. Implementation and evidence are ready for the
iteration crew. This is NOT a claim that all architecture acceptance criteria pass.
No scheduler/model/capacity changes, no Second Brain edits, no new memory backend.
Astra subscription usage was not measured; no remaining-percentage claim is made.

## Results and handoff

| Workstream | Result | Artifact / PR | Commit |
|---|---|---|---|
| W1 migration | 119 facts moved in 8 validated batches; 0 files left in agent/facts. Alias acceptance FAIL; legacy consumers unresolved. | [Migration report](w1-migration-report.md), backups/manifests under astra-w1/ | Vault operation, no PR |
| W2 memory tool | Isolated lifecycle, schema/vocabulary, source views, drift and reviewed MOC workflow implemented; Go checks pass. | [Estate PR 1267](https://github.com/jonhill90/agent-estate/pull/1267) | a712805 |
| W3 consultation | Read protocol injected; unchanged gate FAIL 2/3 overall, consultation 3/3. Separate neutral fixture created after rerun; new baseline UNRUN. | [Estate PR 1266](https://github.com/jonhill90/agent-estate/pull/1266), [private results](astra-w3/report.md) | c277530, then 48bf356 |
| W4 dotfiles | 20 docs classified/moved with old routing paths preserved; canonical roster and memory instructions updated. | [Dotfiles PR 345](https://github.com/jonhill90/agent-dotfiles/pull/345) | b5eb9f0 |
| W5 skills | 41 repo definitions + 42 installed definitions enumerated; 42 public names. No installs/evaluations. | [Skills PR 302](https://github.com/jonhill90/skills/pull/302) | f477a27 |

## Inspect now

- Agent Memory: Start Here → 01 - Notes (119 notes) / 05 - Sources (22 records).
- 03 - Agents/index.md links to the proposed dotfiles roster; no definitions copied.
- Dotfiles branch: docs/index.md and docs/canonical/agent-roster.md.
- Skills branch: SKILLS-INDEX.md.
- [Source registration evidence](source-registration-evidence.json): 18 selected
  sources registered through the already-merged push-1 catalogue, then repeated:
  **zero new rows**, total register size 22. Originals were read only. Includes
  living dotfiles docs, skills index, and entry documents from Loops-Research,
  inmpara, hve-core and basic-memory.
- Generated source views and roster pointer were written by the compiled W2 tool,
  with backups, before any merge. Review-branch source paths are marked pending
  integration; retarget/re-register after the relevant PRs merge. This is not
  deployment of the pending branch as the machine-wide estate binary.

## Exact checks and outputs

W1:
```
python3 run/astra-w1/test_validate_index.py
Before: FAILED (failures=3)
After: Ran 7 tests ... OK
python3 "$AGENT_MEMORY_VAULT/agent/tools/validate_index.py"
Checked 119 fact file(s) ...
Contract holds: no hard violations.
```
The same validator passed after each batch and after navigation/instruction updates.
Existence-guard mutation failed the broken-link tests, then was restored.
Full outputs: astra-w1/red.txt, green.txt, mutation.txt, final-validation.txt;
per-batch outputs and all 119 ID mappings are in w1-migration-report.md.

W2, from `astra-push2`:
```
go test ./src/estate/...
PASS (all packages; complete output: astra-w2-full-tests.txt)
go vet ./src/estate/...
exit 0 (no output)
```
Focused `TestINMAPSLifecycle`: accept A, reject B, repeat rejection/publication
without duplication, revise A into a new ID, preserve/deprecate its old note,
retrieve only current A, reject A, and refuse stale-index resurrection. All paths
are isolated fixtures. `TestINMAPSGuardsAndMOC` proves 7 versus 8 and preserves a
curated overview after refresh. `TestSourceDriftInvalidatesWithoutRewritingMeaning`
proves changed-source dependents leave current retrieval. Four compiling mutants
(schema, vocabulary, supersession, threshold) each fail; exact output is in
astra-w2-mutations.txt. The CLI test was corrected to explicitly isolate its vault.

W3, from `astra-consultation`:
```
go test ./src/estate -run TestKnowledgeReadProtocolIsMandatory -count=1
Before: FAIL — read protocol missing "Before reasoning or implementation"
After: ok github.com/jonhill90/agent-estate/estate
go test ./src/estate/...
PASS (astra-w3/tests.txt)
go vet ./src/estate/...
exit 0
```
Three real dispatch commands, exact recovered task and private verbatim results
are under astra-w3/. Runs 1/3 used 120; run 2 found the fact but declined its
synthetic authority. No task rewording or retrospective criterion adjustment.
Neutral fixture generation was a separate later commit; it has not been scored.

W4, from `astra-dotfiles`:
```
python3 scripts/validate_repository.py
Validated 0 skill(s): 0 error(s), 2 warning(s)
python3 -m unittest discover -s tests -v
Ran 457 tests in 13.758s
OK
apm lock -v
apm.lock.yaml unchanged -- skipping write
```
Warnings describe external skill ownership. CI caught four relative links in a
new archived doc that the initial local git-tracked-file scan had not included.
Fixed them; targeted link tests passed, and final remote CI is green.

W5, from `astra-skills`:
```
python3 scripts/validate_repository.py
Validated 41 skill(s): 0 error(s), 0 warning(s)
python3 -m unittest discover -s tests -v
Ran 206 tests in 0.592s
OK
npx skills add . --list
completed listing only; no install
```
Full outputs are the corresponding astra-w4-* / astra-w5-* files in this directory.

## Gaps that must not disappear in the next session

1. **Live legacy consumers are not migration-compatible.** Main's standing-law
   loader still reads its declared slug under agent/facts and pins original bytes.
   Migration moved that file and added ID/alias metadata. This push leaves law code,
   membership, hash pins and caps untouched as instructed. A separately reviewed
   compatibility change is required before normal live-vault dispatch works again.
   Main's legacy knowledge reader also needs the pending W2 change to read Notes.
   Do not interpret green fixture tests as live deployment evidence.
2. **Obsidian bare wikilinks do not resolve through aliases in the measured check.**
   getFirstLinkpathDest returned null for all three old slugs, including a repeat
   after indexing time. ID-path reads work. Existing wikilinks were not bulk rewritten.
   W1's alias criterion remains FAIL; backups and mappings allow a reviewed repair.
3. **K3 remains FAIL 2/3.** Consultation occurred in all runs but one rejected the
   synthetic authority. Neutral-baseline measurement remains UNRUN. No K6 claim.
4. The installed memory-conventions skill and backed-up live Claude instruction
   copies were updated; the canonical dotfiles source is in W4. Skills repository
   changes remain index-only as instructed. Future skill deployment must preserve
   the revised convention rather than restoring the old slug writer.
5. No turn-start checksum was captured for the protected shared knowledge index.
   Current stat: 3,821,547 bytes, mtime 2026-09-05T09:49:28.273788Z; no command in this
   push requested its regeneration. Current stat is not a full before/after proof.
6. Independent review is pending. Attack W2's crash/retry and adoption boundaries,
   generated-view identity, schema coverage and remaining legacy-consumer assumptions.
   No reviewer identity was fabricated and no PR was merged.

## Resume locations

All are owned isolated worktrees; preserve them and their unmerged branches:
- W2: ../astra-push2 — feat/astra-inmaps-memory
- W3: ../astra-consultation — fix/astra-knowledge-consultation
- W4: ../astra-dotfiles — docs/astra-knowledge-surfaces
- W5: ../astra-skills — docs/astra-skills-index

User checkouts and their pre-existing edits were left in place. Raw evidence,
transcripts, catalogue state and vault backups remain private outside Git.

## Final PR state

All four PRs are OPEN with green remote checks at their recorded heads. Exact GitHub responses: `pr-final-status.json`. All four owned worktrees are clean. No merges performed.
