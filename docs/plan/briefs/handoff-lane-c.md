# Lane C handoff — source-backed candidates

## Status: MERGED

PR https://github.com/jonhill90/agent-estate/pull/1265
(`feat(estate/candidates): source-backed candidates, generalized proposal
shape, repo-publication receipts`), branch `knowledge/lane-c`, is
**merged into `main` as `7b039c5456a95f5b237a66152010187c44def089`**
(merged 2026-09-06T21:53:00Z). Reviewed head was
`300cc974262b60004dcfd48c23a80a0dfd1253ea` (rebased onto
`main@37c33e3` — Lane A's #1263 AND Lane B's #1264, both merged before
this one). A prior push, `744905561`, was rebased away before review and
is void; nobody reviewed it.

```
$ git log --oneline origin/main -3
7b039c5 feat(estate/candidates): source-backed candidates, generalized proposal shape, repo-publication receipts (#1265)
37c33e3 feat(estate/catalogue): persistent source register, extraction, retrieval integration (agent-estate#1139 lane B) (#1264)
24ca850 docs(estate/knowledge): knowledge-workflow guide + vault navigation + contract (Lane A, part 1) (#1263)
$ gh pr view 1265 --json state,mergeCommit,mergedAt
{"mergeCommit":{"oid":"7b039c5456a95f5b237a66152010187c44def089"},"mergedAt":"2026-09-06T21:53:00Z","state":"MERGED"}
```

**Independent review received: Lane A APPROVEd at the exact reviewed SHA**
(`Review-Lane: agent-estate:1`, `Reviewed-SHA: 300cc974262b60004dcfd48c23a80a0dfd1253ea`):
scope-checked the diff against `run/execution-plan.md`'s ownership table,
ran build/vet/test in an isolated clone rather than trusting the PR body,
and independently reproduced the mutation check (disabled
`currentMemoryItem` in `internal/knowledge/vault.go`, confirmed the
fixture goes RED for the claimed reason, restored, confirmed green) — no
findings, no requested changes. Full text on the PR.

Cross-lane review performed (the other half of my review duty this run):
Lane B's PR #1264 reviewed and APPROVEd
(comment: https://github.com/jonhill90/agent-estate/pull/1264#issuecomment-5562018921,
`Reviewed-SHA: da8119179b88519b7d83adf08b6c75bd9210e097`) — build/vet/full
test suite reproduced from a fresh clone at the pinned SHA, an
independently-constructed mutation of `identityFor` (the PR's own pasted
mutation command didn't compile as written — noted as a minor,
non-blocking finding), and an ownership/duplication check against
`run/execution-plan.md`. Merged as `37c33e3`.

## What shipped (PR #1265, merged as 7b039c5)

1. **Source-backed candidates.** `knowledge_candidates` now carries
   `source_kind` (`conversation`|`catalogue`), `catalogue_source_id`,
   `catalogue_locator`. A catalogue-sourced row never fabricates a
   `prompts`/`codex_provenance` row (`prompt_id`/`provenance_id` stay
   empty) — the old inline `UNIQUE(prompt_id)` constraint (which would
   collide on two empty `prompt_id`s) is replaced by partial unique
   indexes. A pre-existing old-shape table (as the live corpus has today)
   is migrated in place, lazily, only under `-apply`
   (`migrateToSourceKindSchema`), proven by
   `TestRegisterCatalogueSourceMigratesOldShapeTable`.
2. **Built against `run/source-api.md`, not Lane B's code.**
   `CatalogueSource{ID, Locator, ContentHash}` matches `run/contract.md`'s
   source-record fields (`id`/`locator`/`revision`-`hash`) verbatim.
   Real integration of `internal/catalogue.RegisterEntry` (Lane B's PR
   #1264) is follow-up work, gated on that PR merging — expected to be a
   rename at the `register-source` call site only.
3. **Generalized `Proposal` shape**: required `destination_kind`
   (`memory`|`repo`), `destination`, `reason`. `memory` accepts through
   the unchanged `Publish` (existing vault-fact mechanism). `repo` accepts
   through the new `PublishRepo`, which never writes the repo file
   itself — it only records a receipt (exact path match + commit SHA)
   after integration happened elsewhere.
4. **`main.go` wiring** (mine exclusively): `estate candidates
   register-source` (same live-corpus guard shape as `derive`/`decide`);
   `candidates memory -action accept/reject` routes to `Publish` or
   `PublishRepo` by the saved proposal's own `destination_kind`.
5. **`internal/features/registry.go`**: new `source-backed-candidates` row
   (`in-progress`), evidence cites the integrated fixture and the
   mutation check below.
6. **Integrated fixture** (`TestIntegratedSourceBackedWorkflow`):
   candidate A (conversation) + B (catalogue), A accepted, B rejected, A
   revised/superseded, retrieval returns only A's current revision
   (checked against a **stale pre-supersede compiled index**, not just a
   freshly rebuilt one), zero duplicates on rerun, superseded/rejected
   content never resurfaces. All against `t.TempDir()` — never the real
   vault, asserted on paths.
7. **Mutation check performed and reverted** (not committed):
   disabling `internal/knowledge`'s `currentMemoryItem` staleness gate
   entirely turned the integrated fixture RED
   (`stale index (generated before the supersede) served A's superseded
   revision`); restoring the gate returned it to PASS. `git status`
   confirms `internal/knowledge/vault.go` is untouched in the final diff.

Verification, from `src/estate` at merged `main` (`7b039c5`):
```
$ go build ./... && go vet ./... && go test ./... -count=1
ok  	github.com/jonhill90/agent-estate/estate	...
ok  	github.com/jonhill90/agent-estate/estate/internal/candidates	...
ok  	github.com/jonhill90/agent-estate/estate/internal/catalogue	...
ok  	github.com/jonhill90/agent-estate/estate/internal/knowledge	...
(every other package: ok, no failures -- last run before merge, at
rebased head 300cc974, with Lane B's internal/catalogue and this PR's
internal/candidates compiled and tested together for the first time)
```

## What's NOT done yet (by design, per the brief, or genuinely uncertain)

- **`docs/knowledge-workflow-evidence.md`** (deliverable 7) — deliberately
  held back. Per the brief this is Lane C's *last* PR, written after the
  director's integration, the real publication, and the pre-stated
  demonstration task run, so it reports what actually happened (including
  a failed demonstration, if that's what happens) rather than a plan.
- **Correcting `agent-memory-v0-layout`'s registry Caveats** now that
  Lane A's root navigation (#1263) landed — deferred to the same final
  evidence PR so it's written against the fully-integrated state.
- **Real Lane B integration** in `register-source` — no longer blocked
  (#1264 is merged and `knowledge/lane-c` is rebased onto it), but NOT
  done in this PR: `register-source` still constructs `CatalogueSource`
  from CLI flags rather than calling Lane B's real
  `internal/catalogue.Register`/`LoadRegister`/`Show`. Swapping the
  `main_candidates_register.go` call site to the real API (field names
  already match `run/source-api.md`, so this should be small) is the next
  piece of work on this branch, not yet started as of this handoff.

## If picked up cold (Astra's next plan)

Lane C's own worktree (`knowledge/lane-c`) is now behind `main` again by
one commit (its own #1265) plus whatever else has merged since — start
any new work from a fresh branch off current `main`, not this stale
worktree.

1. **Wire the real Lane B integration** — not done. `estate candidates
   register-source` (`src/estate/main_candidates_register.go`) still
   constructs `candidates.CatalogueSource{ID, Locator, ContentHash}` from
   CLI flags rather than calling Lane B's real
   `internal/catalogue.Register`/`LoadRegister`/`Show`. Field names were
   deliberately aligned to `run/contract.md`/`run/source-api.md` so this
   should be a small swap at that one call site, not a reshape of
   `internal/candidates` — but it has not been attempted, so treat "should
   be small" as unverified, not proven.
2. **`docs/knowledge-workflow-evidence.md`** (deliverable 7, still not
   written) — per Lane C's brief this is meant to be written AFTER the
   director's integration, the real publication, and the pre-stated
   demonstration task (`run/demo-task.md`) actually run, reporting what
   happened (including a failed demonstration, if that's what happened),
   not a plan. As of this handoff no demonstration run has been reported
   to Lane C.
3. **`internal/features/registry.go`'s `agent-memory-v0-layout` row**
   still needs its Caveats corrected to reflect Lane A's root navigation
   (#1263) superseding part of its original scope — Lane C's brief asked
   for this but it was deliberately deferred to the same final evidence
   PR above so it's written against the fully-integrated state, not a
   partial one. Still outstanding.
4. **Uncertain / not measured by Lane C:** whether the director's
   integrated fixture, real publication, and demonstration task (the
   items in `run/execution-plan.md` step 4) have run at all — that was
   explicitly the director's job, not Lane C's, and nothing in this
   handoff should be read as claiming they did.
