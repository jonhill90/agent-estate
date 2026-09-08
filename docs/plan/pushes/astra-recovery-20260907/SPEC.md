# Knowledge system — updated SPEC

2026-09-07. This document distinguishes inspected implementation from proposed contracts. Baseline: local `754322c`; remote main verified at `8ba75ac`. Proposed behavior below is not implemented merely because it appears here.

## Existing architecture

| Surface | Responsibility and observed implementation |
|---|---|
| `~/corpus/corpus.sqlite3` | Raw/cleaned prompts, context, provenance, judged items. Items use `kind`, `weight`, `status`, `status_reason`, `resolved_to`; there is no `lifecycle` column. |
| Candidate state | Go supports `knowledge_candidates` in a selected corpus database. The inspected live corpus has no such table; prior demonstrations used copies. Do not describe that queue as deployed live without a read-back. |
| `~/.local/state/agent-estate/catalogue/register.json` | Operational source register and extraction-cache references; 30 entries observed. Source originals remain authoritative. Implementation: `src/estate/internal/catalogue`, `src/estate/cmd/sourcecatalogue`. |
| Agent Memory | Root `index.md` is the capped session index; `Start Here.md` is navigation. INMAPS notes live under `01 - Notes`, including Facts and Parameters subdirectories. `agent/` is retired in this vault. |
| `internal/vaultview` | Generates hard-weight corpus projections, maps `corpus_item` to stable note IDs, and retains deprecated projections. Current generator emits repetitive descriptions/footer and fallback titles. |
| `internal/candidates` | Proposal/review/publication, INMAPS note lifecycle and repository publication receipts. Repo receipt records integration; it is not itself a repo edit. |
| `internal/knowledge` | Derived JSON index over stars, vault, corpus, research, repo docs and catalogue. Query/get provide ranked pointers and progressive detail. Catalogue indexing exposes metadata; it does not establish that extracted content was read. |
| `internal/corpus/standinglaw.go` | Explicit standing-law membership is distinct from publishing a vault fact. Preserve this boundary. A `standing-rule` tag is not proof of membership. |
| Estate ledger | `~/.local/state/estate/ledger.jsonl` records estate work. It is not the prompt corpus. |

## Proposed contracts for the recovery

### 1. Evidence and scope

Keep prompt/item IDs, source revision and speaker attribution. A judged item body is an interpretation, not a substitute for its source when meaning is disputed. Read cleaned prompt plus surrounding context; assistant context never becomes Jon's instruction. Missing cleaned text is an explicit evidence gap, not permission to quote raw text into reports.

Separate corpus weight/status, note lifecycle and instruction scope. A resolved one-time directive can remain historical evidence without becoming a current global rule. Preserve raw history. Semantic changes to scope, authority or retraction require a reviewed, attributable decision; no blanket restoration or reclassification.

Applicability checks apply to both corpus retrieval and vault projections, linked by corpus item identity. A label on one representation must not be bypassed by retrieving the other. This is separate from explicit standing-law membership; test both applicable and expired cases without silently changing the corpus's binding rules.

### 2. Readable corpus projections

Fix the producer, then regenerate through the writer. Preserve `corpus_item → note ID`, source links, body meaning, lifecycle, associative tags and Relations. Do not rename existing note IDs. Retain provenance once in structured metadata; remove the repetitive footer and generic description template.

A title names the subject; a description conveys the useful statement. A deterministic excerpt is acceptable only when it remains accurate and readable. Fragments needing context receive a reviewed editorial proposal, not an invented expansion. Persist accepted editorial title/description/enrichment in the managed publication mechanism and preserve it on regeneration. Do not create another ungoverned sidecar store. Unresolved fragments are explicitly labeled and excluded from authority-bearing use.

Distinguish an evidence projection from a distilled reusable learning. Existing projections remain addressable; a separate reviewed learning may cite several of them. Do not merge or delete evidence merely to reduce counts.

### 3. Source-backed publication

Replace caller-asserted catalogue locator/hash at the CLI boundary with a lookup of the registered stable source ID and pinned revision. At acceptance, resolve and verify the reviewed source revision again; drift/unavailability prevents an unqualified current publication. Test this across the real catalogue and candidate packages.

Candidate decision and publication state currently differ. Specify the transition explicitly: a discarded candidate cannot publish; an acceptance must name the exact reviewed proposal revision. Avoid adding a redundant human approval step merely to synchronize two labels.

Published Markdown is written before a database success receipt. Preserve retry/crash recovery and existing revision history. Before first live activation, inspect the actual queue location, back it up, prove the complete lifecycle in copies, then use the authorized tool path. Do not quietly substitute a scratch queue for operational persistence.

### 4. Retrieval and consultation

Use an explicit private session index built from the selected code revision and current sources. Record generation revision and per-source health. Leave the protected shared index unchanged under this run's constraint. New session routing must select the private index deliberately; an old shared-index match is not a current answer.

A consultation result records task scope, selected source IDs/paths and revisions, applicability, freshness and unresolved conflicts. Reuse query/get and existing grounding; add the smallest missing output support. Mechanically validate pointers and revisions. This proves which evidence was provided, not that the model understood it.

Behavioral evaluation proves use: the answer/action must obey the applicable source, reject a conflicting obsolete statement, and identify missing evidence. Use fixed expected outcomes and a reviewer who did not produce the answer. Test an irrelevant-tool-call counterexample. Tool count, file-open count and a self-written “I verified” flag are insufficient.

Use the existing agent-dotfiles deployment path for concise consultation/correction instructions. Any optional hook is a harness adapter, must have a bypass/recursion test and measured overhead, and cannot be described as enforcing truth. A generic Stop hook blocking every no-tool response is outside this recovery.

### 5. Refresh and re-triage

Source refresh compares revisions and marks dependent knowledge for review; it never silently rewrites its meaning. Mechanical projection refresh is idempotent. Vocabulary changes and navigation regeneration preserve legitimate associations and disclose skipped inputs.

Re-triage uses a pinned corpus snapshot and a complete ID manifest. Queue all 135 retracted/needs_review rows for contextual review; separately examine the other 3,662 retracted rows with explicit reason codes. “Emotional,” “critical,” or “uncomfortable” are not deletion/retraction reasons. Duplicate statements retain an evidence link. No raw corpus content enters public fixtures.

## Implementation homes and documentation integration

Keep application behavior in Go under agent-estate. Harness configuration belongs in agent-dotfiles; reusable skill content in its existing canonical skill repo; behavioral evaluations in agent-evals. Do not clone the knowledge implementation into dotfiles.

The active P12 documentation owner should integrate this scoped PRD/SPEC into the actual canonical layout, link them from the estate product PRD/SPEC, and update the knowledge guide and deployed memory instructions. Preserve the orchestration sections; do not replace the whole product spec with this knowledge slice. Current root product docs are dated August 30 and require their existing owner’s broader verification.
