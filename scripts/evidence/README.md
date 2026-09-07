# Evidence-layer backup + restore test

`master-execution-plan.md`, Completeness audit #2: the corpus DB and the
estate ledger are the receipts for everything the estate has done. Losing
them loses provenance. This is a boring, verified backup routine for the
two of them -- and, more importantly, a **restore test**, since "a backup
exists" proves nothing on its own.

```
python3 scripts/evidence/backup_and_restore_test.py
```

## What it does, every run

1. **Backs up** `~/corpus/corpus.sqlite3` and
   `~/.local/state/estate/ledger.jsonl` into a fresh timestamped directory
   under `~/.local/state/estate-evidence-backups/`.
2. **Integrity-checks the backup at creation**, not just copies it: a real
   `PRAGMA integrity_check` (via SQLite's own `.backup` API) for the DB,
   and a per-line `json.loads()` pass for the ledger.
3. **Restores into a throwaway temp directory** (`tempfile.mkdtemp`,
   removed at the end unless `--keep-restore-dir`) and proves the restored
   copy is byte-identical to the backup (sha256 both sides) and
   queryable — a real `SELECT` against the restored DB, a full parse of
   the restored ledger.
4. **Proves the live stores were never touched**: size + mtime of each
   live source, snapshotted before and after the whole run, compared.

A missing or unreadable source is a typed status (`absent` /
`unreadable`), recorded and reported — never a silent skip, never an
abort of the other source's own backup.

## What is NOT covered, and why

Only the two files above. Everything else living alongside them is
listed, with a reason, in `EXCLUSIONS` in the script itself — run
`--report-exclusions` to print that list on its own. An unlisted
exclusion is exactly how a backup quietly fails to restore, so nothing
here is a silent gap: WAL/SHM sidecars, the `ledger.sqlite3` compat
symlink, pre-existing manual backup copies, empty directories, mirror
logs, a scratch demo directory, and a couple of files flagged as
candidates for a future pass rather than silently folded in.

## Hard constraints this script holds itself to

- Every read of the **live** `corpus.sqlite3` uses `sqlite3 -readonly` —
  never opened read-write, anywhere.
- The ledger-write-guard hook that blocks direct sqlite writes to the
  live corpus is correct and is never worked around; this script simply
  never attempts a write against the live corpus path.
- The live `ledger.jsonl` is only ever opened `rb` (read).
- Restore always targets a fresh temp directory, never a live store's own
  path.

## Why `-readonly` needs a flatten step first

SQLite cannot open a WAL-journaled database with `-readonly` unless its
`-wal`/`-shm` sidecars are already present alongside it — a bare
`.backup` copy does not carry them. Verified directly against the real
corpus before this script was written (see the PR for the reproduction).
The fix: flatten the **backup copy** (never the live source) to
`journal_mode=DELETE` right after `.backup` — a single self-contained
file, openable `-readonly` cleanly, with no sidecars required.

## Testing

```
python3 -m unittest scripts/evidence/test_backup_and_restore_test.py -v
```

Every fixture is synthetic; the suite never touches the real `~/corpus`
or `~/.local/state/estate`. Covers: typed absence (both kinds, both
formats), the happy path end to end (including a WAL-mode fixture that
exercises the flatten step for real), a mocked integrity-check failure
that proves `backup_sqlite`'s own gate refuses to report `ok` on bad
data, a real corrupted-source end-to-end case, a malformed-JSONL-line
case, and an unexpected-filesystem-error case (proving one source's
failure never crashes the whole run).

## Not included here

Scheduling (cron/launchd) — this PR is the backup + restore-test
mechanism itself; wiring it to run periodically is a follow-up, named so
its absence reads as a decision, not an oversight.
