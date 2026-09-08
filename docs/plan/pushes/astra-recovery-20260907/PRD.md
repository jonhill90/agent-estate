# Knowledge system — updated PRD

2026-09-07. Proposed knowledge requirements for the existing estate; not a claim of delivery. Read with [SPEC](SPEC.md), [execution plan](EXECUTION-PLAN.md), and [evidence](EVIDENCE.md). This replaces the knowledge scope of overlapping push plans once adopted. It does not replace the estate's orchestration/TUI requirements.

## Purpose

Jon should be able to state an intention once, add worthwhile material, and have agents use the resulting knowledge correctly across his repositories. He should see concise, understandable notes and trace any claim to its source. Agents own registration, maintenance, routing and verification. Jon supplies intent and judges usefulness.

The estate is personal engineering practice. Success is useful behavior within the available time and usage, not a commercial launch or a count of PRs, notes, tags or tool calls.

## User journeys and acceptance

| ID | Requirement | Observable acceptance |
|---|---|---|
| K1 | Remember a durable statement with its context and scope. | A fresh agent finds the applicable statement, distinguishes it from an old task instruction, and follows it without Jon repeating it. |
| K2 | Bring in a document, PDF, conversation or repository. | Source identity, locator, revision, provenance and extraction availability are inspectable. A reviewed learning links back to the exact evidence used. |
| K3 | Read useful knowledge in Agent Memory. | A note's title and description explain its subject; its body contains the useful content. Provenance is available without repetitive template prose. Unclear fragments remain explicitly unresolved. |
| K4 | Keep canonical knowledge in its proper home. | Repo behavior and project decisions stay with the owning repo; procedures stay in canonical guides/skills; cross-project facts belong in Agent Memory. Other surfaces link. |
| K5 | Find current knowledge cheaply. | From either estate or dotfiles, a fresh session reaches applicable current evidence through short routing and scoped retrieval. Missing, stale, withheld and unreadable are distinct results. |
| K6 | Revise knowledge without losing history. | Rejection/supersession preserves identity and provenance; obsolete revisions leave current results. Source drift visibly requires review. A retry produces no duplicates. |
| K7 | Correct an agent using evidence. | When Jon disputes a factual assertion, the agent inspects the named artifact, reports the observed result, and corrects unsupported conclusions. An irrelevant tool call does not count. |
| K8 | Preserve inconvenient or uncertain evidence. | Retraction, privacy, duplication, expired task scope and uncertain authorship are separate reasons. Distress or criticism is not a noise classifier. Unconfirmed items remain unconfirmed. |

## Product boundaries

- The knowledge system serves agent-estate, agent-dotfiles, skills, skills-private when needed, and agent-evals. It is machine-wide even though its Go implementation lives in agent-estate.
- Keep INMAPS: Inbox, Notes, MOCs, Agents, Projects, Sources, Meta. Folders describe purpose; topical connections use tags/links. Further folder redesign is unnecessary for this recovery.
- Evidence, reviewed knowledge, navigation and binding instructions are distinct. Publishing a note or giving it a tag does not make it a standing instruction.
- Short session entry points lead to task-relevant content. Do not inject the complete corpus into every turn.
- All managed vault writes use the reviewed writer, with one live vault writer and verified recovery copies. No deletions are part of this plan.
- Second Brain is reference-only. Other product families are outside this effort. Private conversations and account material stay private.
- MOCs are generated navigation, not facts. Their maintenance should not consume the knowledge-review queue. Implement their already-recorded automatic maintenance policy separately if still outstanding.

## Decisions retained and decisions still open

Retain the current hybrid: the corpus preserves conversation evidence and judged items; Markdown stores inspectable knowledge; indexes are derived. Do not interpret this as approval of a new database/vector backend.

“Markdown alone is sufficient to operate; SQLite is evidence only,” putting the vault under Git, and replacing harness-native memory are not established approvals in the reviewed record. Today’s repair can proceed without them. Do not silently implement them.

## Completion

Today’s target is a demonstrated operating path with readable projections, honest authority, current private retrieval, and a repeatable correction workflow. Each journey above needs evidence or an explicit incomplete status. Historical editorial work and full re-triage have separate totals; unfinished work cannot be hidden behind a declaration that the entire architecture is complete.
