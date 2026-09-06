# Reviewed candidate memory

Goal: one cited proposal becomes a current Agent Memory fact, with rejection
and revision observable through the existing knowledge query.

Scope: candidates, a CLI entry point, and current-fact filtering in knowledge.
No mining, new store, scheduler, model, tmux, or Second Brain changes.

Test matrix: `TestMemoryWorkflow` covers approval versus publication, rejection,
supersession, citation preservation, stale-index refusal and duplicate-free reruns.
`TestMemorySafety` covers missing evidence, slug collisions and stale revisions.

Steps: write the lifecycle test first; implement reviewed publication with backups;
wire the CLI and existing index; demonstrate a clear real learning privately.
Verification: `go test ./internal/candidates ./internal/knowledge .` from
`src/estate`, plus the isolated integration test with `-v`.

CI: estate-ci runs Go tests. Lifecycle and stale-index tests bind the new contract.
Risks: shared-file edits and interrupted publication. Back up affected files before
replacement; serialize writers; use stable revisions, refuse collisions, and make
retries repair partial publication. External editors must not write concurrently.

Done criteria: focused tests, fixture demonstration, private real fact,
PR evidence and handoff. Stop at independent review: author does not self-merge.

## Interface

`estate candidates memory -db <corpus> -id <candidate> -action propose
-proposal <private.json> -apply` records a paraphrased proposal. Omit `-apply`
for validation only. JSON fields are `slug`, `type`, `title`, `description`,
`learning`, `operator_context`, `assistant_context`, `reviewer`, `supersedes`.
Context is attributed paraphrase; assistant context is not operator instruction.
An existing unmanaged fact can be adopted only with `existing_fact_hash` equal
to its inspected SHA-256. Preserve its useful content in the reviewed proposal;
a mismatch refuses the overwrite. Another candidate's managed fact cannot be adopted.
`supersedes` is the previous published revision for an update (empty initially).

Use `-action accept` or `-action reject` with `-vault <Agent Memory root>`
(defaults to `AGENT_MEMORY_VAULT`) to publish or withdraw. `-action show` reads
the saved proposal and publication state. Live corpus writes additionally require
`-authorized-live-write`. Legacy `decide promote` remains approval only.

Regenerate with `estate knowledge` using `ESTATE_KNOWLEDGE_INDEX` pointing to
an isolated output, then `estate knowledge query --private <terms>`.
Full vault snapshots are withheld if canonical bytes changed or disappeared,
including snapshots compiled before adoption. Older pointer-only disclosure
formats retain their existing behavior. Backups remain private under
`agent/.memory-backup-*/`; rejection withdraws the canonical file after backing
it up. The corpus review and memory log retain the rejection. Raw source exchanges
stay in their original private files; the workflow stores only citations and
reviewer-supplied paraphrases. Recover the exchange around the cited prompt before
writing those paraphrases. Provenance `record` is a zero-based extracted-turn
ordinal, not a JSONL line number.

The lock serializes workflow writers, not arbitrary editors or sync clients.
Files and SQLite are not one atomic transaction: interruption can leave partial
publication. Retry the same action to repair it; backups preserve prior bytes.
Only a successfully returned `state=promoted` with `changed=true` reports a new
publication; a dry run returns the current state plus `would_state`.
