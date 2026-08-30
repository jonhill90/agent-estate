#!/usr/bin/env python3
"""One-off (but re-runnable) backfill for the 2026-08-23 -> 2026-08-27
capture gap (agent-supervisor#687, #696).

WHY THIS EXISTS. `prompt_capture_hook.py` (#687/#693) makes capture a
byproduct of every prompt submission going forward, but it was dead for
four days before it existed -- #687's own measurement found the newest
prompt stuck at 2026-08-23 ~13:14 local while the corpus's live agents kept
working. Those four days of transcripts still exist under
`~/.claude/projects/`; this script recovers them into `prompts` the same
way `mine_prompts.py --store` always has, plus one thing that script does
NOT do: it also populates `prompts.project`.

WHY `project` NEEDS SPECIAL HANDLING. `mine_prompts.harvest()` only ever
records `source=os.path.basename(path)` -- a transcript's session UUID,
never its directory -- and `Ledger.record_prompt` has no `project`
parameter at all (`core.py` never declared that column; it exists on the
live table from earlier, undocumented history -- see
`itemize_prompts.py`'s own comment on `prompts.project`). Deriving project
from `source_file` after the fact is a dead end -- a UUID names a session,
not a repo. The only place a transcript's originating repo is recoverable
from is the PATH ITSELF: `~/.claude/projects/<mangled-cwd>/<uuid>.jsonl`.
This script walks that directory structure directly (rather than reusing
`mine_prompts.harvest`'s basename-only glob) so it can carry the parent
directory name through to a plain `UPDATE prompts SET project=...` after
each insert -- the same column shape existing rows already carry.

SCOPE, deliberately narrow (#696's brief): recover prompts only. Judging is
`itemize_prompts.py`'s job, run separately, later, by a model -- nothing
here writes an `items` row.

IDEMPOTENT, same construction as `mine_prompts.py --store`: `mp-` ids
derive from `(source, at, text)` (imported from `mine_prompts._prompt_id`,
not re-implemented, so the id namespace and future manual re-crawls of
this window agree). Re-running writes zero new rows the second time --
verify that after every run, per the brief; this script's own `--verify`
mode does not exist, run it twice and read the "written" count.

USAGE:
  backfill_prompt_gap.py --since EPOCH --until EPOCH [--dry-run]
  backfill_prompt_gap.py --since EPOCH --until EPOCH   # writes; back up first (see below)

Bounds are REQUIRED and taken as arguments, never guessed inside this file
-- the gap's own boundary (last capture before it died, first capture after
the hook went live) is measured once, from the ledger, by the caller, and
passed in explicitly so this script cannot silently drift onto a different
window on a future run.

BACKUP. This is a write path against the estate's one ledger. Callers are
expected to copy `ledger.sqlite3` before running with a real (non
--dry-run) window -- this script does not do that for you, the same way
`mine_prompts.py --store` never has; see the brief's own "back it up
before any write, never DELETE" rule.
"""
import argparse
import datetime
import glob
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # sibling core.py/mine_prompts.py

from mine_prompts import CONTEXT_UNDETERMINED, _prompt_id, harvest, load_site_excludes  # noqa: E402


def _project_for_path(path, root):
    """`~/.claude/projects/<mangled-cwd>/<uuid>.jsonl` -> the mangled-cwd
    directory name, exactly the shape existing `project` values already
    carry (e.g. `-Users-jon-source-repos-Personal-hill90-app`)."""
    rel = os.path.relpath(path, root)
    return rel.split(os.sep, 1)[0]


def collect(root, excludes, since, until):
    """One row per operator turn, `at` filtered to (since, until] exclusive-
    lower/inclusive-upper on the epoch bound, `project` attached per file
    rather than left to a later, lossy re-derivation from `source_file`."""
    rows = []
    for path in glob.glob(os.path.join(root, "*", "*.jsonl")):
        project = _project_for_path(path, root)
        for row in harvest([path], excludes):
            row["project"] = project
            rows.append(row)
    from mine_prompts import _epoch
    out = []
    for row in rows:
        at = _epoch(row["at"])
        if at is None:
            continue
        if since < at <= until:
            row["_epoch"] = at
            out.append(row)
    out.sort(key=lambda r: r["_epoch"])
    return out


def store(rows, ledger, dry_run):
    written = skipped = 0
    oldest = newest = None
    for row in rows:
        prompt_id = _prompt_id(row)
        at = row["_epoch"]
        if ledger.get_prompt(prompt_id) is not None:
            skipped += 1
            continue
        if dry_run:
            written += 1
            oldest = at if oldest is None else min(oldest, at)
            newest = at if newest is None else max(newest, at)
            continue
        ledger.record_prompt(
            prompt_id,
            at=at,
            text_raw=row["text"],
            context=row["context"],
            session=row["source"],
            source_file=row["source"],
        )
        # `record_prompt` has no `project` parameter (see module docstring) --
        # one explicit UPDATE, scoped to the row just inserted, using the
        # ledger's own connection so it participates in its lock/txn model.
        with ledger._locked(), ledger._transaction() as connection:
            connection.execute(
                "UPDATE prompts SET project=? WHERE id=?", (row["project"], prompt_id)
            )
        written += 1
        oldest = at if oldest is None else min(oldest, at)
        newest = at if newest is None else max(newest, at)
    return written, skipped, oldest, newest


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--root", default=os.path.expanduser("~/.claude/projects"))
    ap.add_argument("--exclude-file", default=os.path.join(
        os.environ.get("SUPERVISOR_STATE", os.path.expanduser("~/.local/state/agent-dotfiles-supervisor")),
        "mine-exclude.txt"))
    ap.add_argument("--state-dir", default=os.environ.get(
        "AGENT_SUPERVISOR_STATE_DIR", os.path.expanduser("~/.local/state/agent-dotfiles-supervisor")))
    ap.add_argument("--since", type=int, required=True, help="epoch seconds, exclusive lower bound")
    ap.add_argument("--until", type=int, required=True, help="epoch seconds, inclusive upper bound")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    excludes = load_site_excludes(args.exclude_file)
    rows = collect(args.root, excludes, args.since, args.until)
    if not rows:
        print("NO ROWS in that window -- verify the bounds before trusting an empty result.",
              file=sys.stderr)
        return 2

    from core import Ledger
    ledger = Ledger(args.state_dir)
    written, skipped, oldest, newest = store(rows, ledger, args.dry_run)

    def fmt(ts):
        return datetime.datetime.fromtimestamp(ts).isoformat(sep=" ") if ts is not None else "n/a"

    print(f"{'[dry-run] ' if args.dry_run else ''}window ({fmt(args.since)}, {fmt(args.until)}]: "
          f"{len(rows)} candidate rows, {written} written, {skipped} already present")
    print(f"backfilled range: oldest={fmt(oldest)} newest={fmt(newest)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
