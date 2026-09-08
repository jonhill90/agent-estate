---
name: knowledge-session
description: Turn something worth knowing into a retrievable, cited fact (register a candidate, inspect it, propose a paraphrase, review, publish to the vault) or find one already recorded (retrieve, refresh the index) — the agent-estate knowledge loop. Use when deriving/reviewing knowledge candidates from the corpus, publishing a fact to Agent Memory, or querying `estate knowledge` for a cited answer.
---

# Knowledge session

This skill **references** `agent-estate`'s own canonical guide — it does
not restate it. Read [`docs/knowledge-workflow.md`](../../../docs/canonical/knowledge-workflow.md)
for the seven-step loop (register → inspect → propose → review →
publish/link → retrieve → refresh) with the exact, verified `estate`
commands for each step.

## When to reach for this

- Deriving or reviewing candidates from ingested conversation history
  (`estate candidates ...`).
- Drafting or publishing a paraphrased fact into the Agent Memory vault
  (`estate candidates memory ...`).
- Answering "what do we already know about X" with a cited pointer rather
  than a guess (`estate knowledge query|get`).
- Regenerating the compiled knowledge index after a doc or fact change.

## What this skill does not cover

- The mechanism `estate knowledge` uses to rank and disclose results
  (BM25 weighting, `coverage`/`contradictions`/`disclosure` states) — see
  `docs/canonical/knowledge-system.md`, linked from the workflow guide.
- Exactly where a candidate/fact lives at each lifecycle stage — see the
  vault's own `agent/LIFECYCLE.md` ($AGENT_MEMORY_VAULT), linked from the
  workflow guide's step 5.
- The source-record frontmatter contract for cataloguing a new source
  (repo, doc, transcript) — see `run/contract.md` from the knowledge
  architecture run this skill was added under, and the vault's
  `05 - System/tags.md` for the tag vocabulary it references.

## Guardrails (repeated here because they gate every step, not just one)

- Private content (raw prompts, transcript text) never enters a public PR
  body, a public doc, or a `--private`-free query result. `estate
  candidates show`/`memory` print structural metadata (harness, session,
  content hash) and paraphrased context, never raw corpus text.
- A proposal's `operator_context`/`assistant_context` must be an attributed
  paraphrase, not raw quotation — the CLI itself refuses an empty or
  missing pair rather than accepting an unattributed learning.
- `estate knowledge`'s index is derived and regenerable, never
  authoritative — safe to delete and rebuild; never treat it as a second
  source of truth for the vault, the corpus, or repo docs.
