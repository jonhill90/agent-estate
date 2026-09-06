# Candidate memory workflow handoff

Started from main `30a97e7`, in a separate worktree. Direct implementation was
authorized by Jon while Claude was quota-paused; no other lane checkout was edited.

`candidates memory` supports propose/show/accept/reject. Proposal JSON is reviewed
paraphrase with separate operator and assistant context. The existing candidate
row records the proposal and publication state; Agent Memory remains canonical.
Legacy `decide promote` is still approval only and cannot bypass managed review.

Facts use a stable slug and revision hash. Revisions name the version they
supersede. Existing unmanaged facts require an inspected SHA-256 to adopt.
Publication checks ownership and external edits, backs up files, updates the
fact/index/log, then records publication. Rejection removes active fact/index
entries after backup. Old compiled vault snapshots cannot resurrect them.

## Evidence

Run from `src/estate`:

```text
go test ./internal/candidates ./internal/knowledge .
ok  github.com/jonhill90/agent-estate/estate/internal/candidates 3.258s
ok  github.com/jonhill90/agent-estate/estate/internal/knowledge 0.604s
ok  github.com/jonhill90/agent-estate/estate 49.666s

go vet ./internal/candidates ./internal/knowledge .
(exit 0, no output)

go test ./internal/candidates -run TestMemory -count=1 -v
accept=promoted reject=withdrawn
derive_rerun_new=0 publish_rerun_changed=false current_only=true
--- PASS: TestMemoryWorkflow (0.36s)
--- PASS: TestMemorySafety (1.10s)
PASS

go test . -run TestCandidatesMemoryCLI -count=1 -v
--- PASS: TestCandidatesMemoryCLI (0.66s)
PASS
```

The lifecycle output above is excerpted; it also prints exact fixture revision,
stable retrieval ID and prompt/provenance IDs. Safety cases cover adoption,
pre-adoption stale indexes, missing evidence, collision, stale revision, external
edit, dry run, rejected retry, interrupted-write repair and symlinks.

A real existing Agent Memory fact was adopted with its body preserved and its
source exchange contextualized privately. No duplicate fact was added. Its
publication returned `state=promoted, changed=true`, then `changed=false` on
repeat. The private corpus snapshot's derivation rerun inserted zero candidates.
Vault validation before and after: `Contract holds: no hard violations.`
Existing knowledge generation and private retrieval both exited 0.
Private source IDs, backups, proposal, corpus snapshot and retrieval evidence
remain outside this repository in the local estate state directory.

## Remaining boundaries

- No automatic interpretation/mining: the reviewer must recover the source
  exchange and supply accurate paraphrases; assistant context is not instruction.
- Files plus SQLite are not one atomic transaction. On interrupted publication,
  retry the same action; inspect the private backup when recovery is uncertain.
- Advisory locks coordinate workflow writers, not external editors or sync clients.
- Older pointer-only index formats need regeneration for current-fact checking.
  This change covers the Estate CLI query/get path, not every external consumer.
- No scheduler/deployment changes. Independent review and CI remain before merge;
  the author must not self-merge or forge dispatch identity for the gate.
