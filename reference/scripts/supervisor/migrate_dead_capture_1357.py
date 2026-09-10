#!/usr/bin/env python3
"""agent-estate#1357: merge the prompts orphaned by the capture-hook default
bug into the corpus.

WHY THIS EXISTS. `prompt_capture_hook.py` (before this issue's fix) defaulted
its `Ledger` root to `~/.local/state/agent-dotfiles-supervisor` -- the dead
database agent-estate#942 says nothing may use -- instead of the corpus. From
2026-09-02 to 2026-09-10 every live prompt capture landed there instead of
`~/corpus/corpus.sqlite3`. This script performs the one-time merge of the
rows that accumulated in the dead database's `prompts` table back into the
corpus, and ONLY that table -- it does not touch `items` (see the module
docstring's own "NOT IN SCOPE" note below for why).

THE MERGE RULE, id-keyed and idempotent:

  - A row whose id already exists in the corpus is SKIPPED, not overwritten.
    The corpus copy may have been cleaned (`text_clean`) or judged
    (`items` row) since; this script must never regress either.
  - A row whose id does not yet exist in the corpus is written via
    `Ledger.record_prompt` -- the corpus's own write API (core.py), which
    enforces the same invariants (`text_raw`/`context` required, one write
    per id ever) every other writer already relies on.
  - `record_prompt`'s own INSERT does not name the `project` column at all
    (corpus.prompts has one; the dead schema does not) -- every row this
    script writes gets `project=NULL`, the same honest "not derived" value
    every OTHER writer through this same API already produces. No value is
    invented here.
  - Idempotent by construction: re-running this script checks `get_prompt`
    fresh each time, so a second run skips everything the first run wrote
    and writes nothing new.

NOT IN SCOPE: the dead database ALSO carries 710 `items` rows (judgements --
27 hard parameters, 26 hard directives, both status=acted, among them) that
`itemize_prompts.py`'s OWN `--state-dir` default (a sibling instance of this
exact bug, not fixed by this PR) apparently wrote there. Those are LAW, not
raw prompts, and migrating a judgement carries different stakes and a
different merge question (does an already-`acted` decision in the dead DB
ever compete with a real one in the corpus?) that this brief does not
authorize this script to answer. Filed as a follow-up
(agent-estate#1357's own PR body); left untouched here, on purpose.

READ-ONLY on the dead database throughout -- this script never opens it for
write. WRITES to the corpus route exclusively through `Ledger.record_prompt`
(core.py), never a direct `sqlite3.connect(..., mode="rw")` -- the
`ledger-write-guard` hook is correct and this script does not attempt to
route around it.

Run: python3 migrate_dead_capture_1357.py [--dry-run] [--dead-db PATH] [--corpus-dir PATH]
"""
from __future__ import annotations

import argparse
import os
import sqlite3
import sys
from pathlib import Path

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # sibling core.py

DEFAULT_DEAD_DB = os.path.expanduser("~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3")
DEFAULT_CORPUS_DIR = os.environ.get("AGENT_CORPUS_DIR", os.path.expanduser("~/corpus"))

# The exact prompts-table columns this script reads from the dead DB and
# passes through to record_prompt -- explicitly NOT including `project`,
# which the dead schema does not have and record_prompt does not accept.
DEAD_ROW_COLUMNS = (
    "id", "at", "text_raw", "text_clean", "context", "session",
    "source_file", "tmux_pane", "tmux_pane_target",
)


def read_dead_rows(dead_db_path):
    """Read every `prompts` row from the dead database, read-only. Returns a
    list of dicts keyed by DEAD_ROW_COLUMNS."""
    uri = f"file:{dead_db_path}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    try:
        cur = conn.execute(f"SELECT {', '.join(DEAD_ROW_COLUMNS)} FROM prompts ORDER BY at")
        return [dict(zip(DEAD_ROW_COLUMNS, row)) for row in cur.fetchall()]
    finally:
        conn.close()


def read_corpus_ids(corpus_dir):
    """Read-only pass to count/report the corpus's current prompt ids,
    without going through the write-capable Ledger for this part."""
    db_path = os.path.join(corpus_dir, "corpus.sqlite3")
    uri = f"file:{db_path}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    try:
        cur = conn.execute("SELECT id FROM prompts")
        return {row[0] for row in cur.fetchall()}
    finally:
        conn.close()


def migrate(dead_db_path, corpus_dir, *, dry_run=False):
    from core import Ledger

    dead_rows = read_dead_rows(dead_db_path)
    corpus_ids_before = read_corpus_ids(corpus_dir)

    print(f"dead db:    {dead_db_path}  ({len(dead_rows)} prompt rows)")
    print(f"corpus dir: {corpus_dir}  ({len(corpus_ids_before)} prompt rows before)")

    to_write = [row for row in dead_rows if row["id"] not in corpus_ids_before]
    already_present = len(dead_rows) - len(to_write)
    print(f"already present in corpus (skip, never overwrite): {already_present}")
    print(f"candidates to migrate: {len(to_write)}")

    if dry_run:
        print("--dry-run: no writes performed")
        return

    ledger = Ledger(corpus_dir)
    written = 0
    skipped_race = 0
    for row in to_write:
        # Re-check immediately before writing -- belt-and-suspenders against
        # a row that landed in the corpus between the scan above and this
        # write (e.g. the live hook, now fixed, capturing the same id again
        # via a genuinely concurrent session). Never overwrite.
        if ledger.get_prompt(row["id"]) is not None:
            skipped_race += 1
            continue
        ledger.record_prompt(
            row["id"],
            at=row["at"],
            text_raw=row["text_raw"],
            text_clean=row["text_clean"],
            context=row["context"],
            session=row["session"],
            source_file=row["source_file"],
            tmux_pane=row["tmux_pane"],
            tmux_pane_target=row["tmux_pane_target"],
        )
        written += 1

    corpus_ids_after = read_corpus_ids(corpus_dir)
    print()
    print(f"written:              {written}")
    print(f"skipped (pre-existing): {already_present}")
    print(f"skipped (race, appeared mid-run): {skipped_race}")
    print(f"corpus prompt rows after: {len(corpus_ids_after)} (was {len(corpus_ids_before)}, delta {len(corpus_ids_after) - len(corpus_ids_before)})")


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--dead-db", default=DEFAULT_DEAD_DB, help="path to the dead ledger.sqlite3")
    ap.add_argument("--corpus-dir", default=DEFAULT_CORPUS_DIR,
                     help="corpus directory (must contain corpus.sqlite3; passed straight to Ledger())")
    ap.add_argument("--dry-run", action="store_true", help="scan and report only, write nothing")
    args = ap.parse_args()
    migrate(args.dead_db, args.corpus_dir, dry_run=args.dry_run)


if __name__ == "__main__":
    main()
