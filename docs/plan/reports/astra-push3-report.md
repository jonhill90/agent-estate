# Astra Push 3 report

**Two implementation PRs delivered, CI green. A2 is incomplete. Nothing merged by Astra.**

| Workstream | Result | Evidence |
|---|---|---|
| A1 corpus projections | 2,638 individual notes in eight validated batches; old views retired; rerun changed=0. New generator/reader awaiting review in [#1272](https://github.com/jonhill90/agent-estate/pull/1272). | [A1 report](push3-a1-report.md), [ID mapping](push3-a1-id-mapping.json), `push3-a1-backups/` |
| A2 agent dissolution | **STOPPED / incomplete.** Additional hidden candidate backup directories were found. Preserved all; agent/index, contract and corpus remain. No extraction move attempted. Main still needs legacy reader compatibility. | [A2 inspection and remaining work](push3-a2-report.md) |
| A3 corpus rename | Existing paired PRs merged by iteration crew during this session; verified identical saved DB hashes, both symlink/real-name reads, both write refusals. Added requested Meta routing note. | [A3 report](push3-a3-report.md) |
| A4 Skills routing | Existing #303 verified; no duplicate PR. Superseded obsolete SKILLS-INDEX registration and linked current Skills repo route. | [A4 report](push3-a4-report.md) |
| A5 dual repo locators | Updated five existing records twice, all 29 existing ID/Locator pairs preserved. Source views regenerated and linked from Start Here. [#1273](https://github.com/jonhill90/agent-estate/pull/1273). | [A5 report](push3-a5-report.md), [records](push3-a5-evidence.json), `push3-a5-backup/` |

## Verification

Actual command results:
```
go test ./src/estate/...
all packages passed (full output: push3-a1-go-tests.txt)

go test ./src/estate/internal/candidates -run TestINMAPSLifecycle -count=1
ok github.com/jonhill90/agent-estate/estate/internal/candidates 0.658s

go test ./src/estate/internal/knowledge -run 'TestNestedNoteQuery|TestVaultSource' -count=1 -v
--- PASS: TestNestedNoteQueryExcludesRetiredAndDraft
PASS
ok github.com/jonhill90/agent-estate/estate/internal/knowledge 0.330s

python3 scripts/knowledge/vault/test_validate_index.py
Ran 8 tests
OK

python3 "$AGENT_MEMORY_VAULT/99 - Meta/tools/validate_index.py"
Checked 2758 fact file(s) against Agent Memory/agent/index.md
Contract holds: no hard violations.

run/astra-push3-bin vault-view
changed: 0
counts: correction=173 directive=1341 parameter=958 question=138 thought=28

go test ./src/estate/internal/catalogue -count=1
ok github.com/jonhill90/agent-estate/estate/internal/catalogue 7.890s

gh pr checks 1272 --repo jonhill90/agent-estate
estate pass 55s
estate pass 58s

gh pr checks 1273 --repo jonhill90/agent-estate
estate pass 54s
estate pass 57s
```

Both relevant `go vet` runs exited 0. Nested reading, nested validation and candidate ID collision each had a failing regression before the fix. Missing-local mutation exits 1; restored code passes (`push3-a5-mutation.txt`). The isolated query returns one current stable nested note with its canonical path, excluding draft/deprecated notes. Validator still reports 14 existing recommended-metadata warnings; no hard violations.

Protected shared index remains byte/stat identical: size 3821547; mtime_ns 1788601768273787859; SHA256 `3ca00988c3d041eeca7066e660d3a3b5a66d87ef67cdaf8547c84ea872f1037a`. No shared-index regeneration, Second Brain edits, tmux actions, scheduler/model/capacity changes, councils or subagents. Subscription allowance cannot be measured from this session; no percentage-usage claim is made.

## Cold-session handoff

- A1: `feat/astra-push3-parameters`, commit `b9414ef1d57327c74749bf8ee2503fd1d8171ff4`, worktree `/Users/jon/source/repos/Personal/agent-estate-lanes/astra-push3-parameters`.
- A5: `feat/astra-push3-repo-pointers`, commit `491921cc5815c6e07da4e84ac859d1354bf56ec5`, worktree `/Users/jon/source/repos/Personal/agent-estate-lanes/astra-push3-pointers`.
- Both worktrees clean, pushed and preserved. Independent PRs based on main including #1270; no mutual file overlap. Review either order. Do not merge under Astra's authorization.
- Review attack points: ID preservation across directories and regeneration; exclusion of retired/draft notes; unchanged Locator identity; missing-local handling. PRs contain no raw corpus bodies/transcripts.
- Open Obsidian `Start Here` → `01p - Parameters`, or one of the five canonical repo pointers. Regenerate using the explicit `run/astra-push3-bin` binary until reviewed code reaches the normal reader. Main alone does not yet see nested parameter notes; the shared index intentionally remains unchanged.
- A2 remains the only untouched workstream: preserve/move four hidden backup dirs plus lock, repoint ALL index/corpus/candidate/installed instruction readers, then migrate capped index and extraction with receipts. Inspection inventories are in A2 report. The safe-deletion rule requires stopping on content that contradicts the described deletion target; no unexpected backup was deleted. No A2 PR was fabricated.
- Migration helper assets and validator are reviewable in #1272; the deployed validator lives in `99 - Meta/tools/`. Every A1 batch includes generation JSON, checksum manifest and full validator output. Original six views and original index/validator/routing are in `push3-a1-backups/initial/`.

## Initial verified state



```json
{
  "at": "2026-09-07T00:44:27.096712-04:00",
  "main": {
    "command": [
      "git",
      "ls-remote",
      "origin",
      "refs/heads/main"
    ],
    "exit": 0,
    "stdout": "9aaaf8f59e5f524c5aeb3611e75e649611c794a6\trefs/heads/main",
    "stderr": ""
  },
  "local": {
    "command": [
      "git",
      "status",
      "--short",
      "--branch"
    ],
    "exit": 0,
    "stdout": "## main...origin/main\n?? src/progress/progress",
    "stderr": ""
  },
  "agent_listing": [
    ".candidate-memory.lock",
    ".inmaps-backup-4033725576",
    ".inmaps-backup-869119760",
    ".memory-backup-1729756205",
    ".memory-backup-2653422664",
    "00 - Inbox",
    "INDEX-CONTRACT.md",
    "corpus",
    "facts",
    "index.md",
    "parameters"
  ],
  "notes_recursive": 120,
  "corpus_extraction_destination_exists": false,
  "corpus.sqlite3": {
    "exists": true,
    "symlink": false,
    "resolved": "/Users/jon/corpus/corpus.sqlite3"
  },
  "ledger.sqlite3": {
    "exists": true,
    "symlink": true,
    "resolved": "/Users/jon/corpus/corpus.sqlite3"
  },
  "protected_index": {
    "size": 3821547,
    "mtime_ns": 1788601768273787859,
    "sha256": "3ca00988c3d041eeca7066e660d3a3b5a66d87ef67cdaf8547c84ea872f1037a"
  },
  "agent-estate#1270": {
    "command": [
      "gh",
      "pr",
      "view",
      "1270",
      "--repo",
      "jonhill90/agent-estate",
      "--json",
      "state,headRefOid,mergeCommit,url"
    ],
    "exit": 0,
    "stdout": "{\"headRefOid\":\"9f136bece805509067b7112523aafd96768e7d62\",\"mergeCommit\":null,\"state\":\"OPEN\",\"url\":\"https://github.com/jonhill90/agent-estate/pull/1270\"}",
    "stderr": ""
  },
  "agent-dotfiles#346": {
    "command": [
      "gh",
      "pr",
      "view",
      "346",
      "--repo",
      "jonhill90/agent-dotfiles",
      "--json",
      "state,headRefOid,mergeCommit,url"
    ],
    "exit": 0,
    "stdout": "{\"headRefOid\":\"011a64181d731f6052db6296efb86269cba8f38e\",\"mergeCommit\":null,\"state\":\"OPEN\",\"url\":\"https://github.com/jonhill90/agent-dotfiles/pull/346\"}",
    "stderr": ""
  },
  "skills#303": {
    "command": [
      "gh",
      "pr",
      "view",
      "303",
      "--repo",
      "jonhill90/skills",
      "--json",
      "state,headRefOid,mergeCommit,url"
    ],
    "exit": 0,
    "stdout": "{\"headRefOid\":\"9763852767c0331cb2e1e049fb5c67612a56e506\",\"mergeCommit\":{\"oid\":\"789dd74335bf62ae7e3ba08ad109ecc651e6834c\"},\"state\":\"MERGED\",\"url\":\"https://github.com/jonhill90/skills/pull/303\"}",
    "stderr": ""
  }
}
```

Initial state was recorded before branch creation. No merges authorized.

---

## DIRECTOR ADDENDUM — A2 is COMPLETE. Line 63 above is superseded.

Added 2026-09-07 05:33 by the Director. The report body is left unedited; this
addendum corrects it in place rather than rewriting someone else's record.

Line 63 states "A2 remains the only untouched workstream". That was true when
this report was written — Astra correctly stopped on unexpected hidden backup
directories rather than deleting through them, which was the right call. It was
completed afterwards by the lane crew.

**Ground truth, measured now:**

```
ls $AGENT_MEMORY_VAULT/agent   →  errors; the directory is GONE
agent-estate #1275             →  MERGED 46709fadb
```

A2-COMPLETION (#1275) retired `agent/` entirely: `corpus/` moved to
`~/.local/state/estate/corpus-extraction/` with a pointer record in
`05 - Sources`; `index.md` content merged into the bundle-root `index.md` and
`INDEX-CONTRACT.md` into `99 - Meta/index-contract.md`; the empty `facts/` and
the `agent/00 - Inbox` signpost removed. It took one REQUEST CHANGES and a fix
pass first — the initial version moved the `okf_version` carrier to
`Start Here.md`, which OKF §12 forbids (it names a bundle-root `index.md`
twice), and three consumer docs were left dangling.

**Why this addendum exists:** a future run reading this report would re-attempt
A2, find `agent/` already gone, and either stall or improvise. The stale line is
not Astra's error — the work simply continued after the report was final.

**Also settled since:** the Push 3 gate ran and Push 3 is CLOSED (see
`run/iteration-queue.md`): Part A passes on a pre-stated A3 re-run with zero
retired-path citations; Part B's tag-filter failure was reclassified by the plan
author as Push 4.5's measured baseline, recorded verbatim, not waived. #1273 was
closed as superseded by #1271 with the overlap credited.
