#!/usr/bin/env python3
"""agent-estate#1362: merge the judgements orphaned by itemize_prompts.py's
default-path bug into the corpus.

WHY THIS EXISTS. `itemize_prompts.py` (before this issue's fix) defaulted its
`Ledger` root to `~/.local/state/agent-dotfiles-supervisor` -- the dead
database agent-estate#942 says nothing may use -- the same defect PR #1360
fixed for `prompt_capture_hook.py` and `mine_prompts.py`. Unlike those two,
`itemize_prompts.py --load` writes `items`: judgements carrying `kind`,
`weight`, `status` -- a claim about what a prompt MEANS, not just a raw
record that it was typed. 741 distinct prompts (748 items -- a handful of
prompts carry two) were judged only in the dead database, zero overlap with
the corpus, 65 of them `weight=hard, status=acted` law that
`internal/corpus.Hard()` (every dispatch's own grounding banner) has never
seen, because it reads only the corpus.

THE DECISION THIS SCRIPT MAKES, ARGUED (per agent-estate#1362's own brief --
"I do not know the answer and am not going to guess it for you"):

MIGRATE EVERY ORPHANED JUDGEMENT, not just the hard/acted subset. Measured
before deciding: of the 741 distinct dead-db-judged prompts, 721 (97.3%)
ALREADY have a prompt row in the corpus -- migrated wholesale by PR #1360's
own prompt-only recovery, which predates this script and did not know these
741 prompts had already been judged. If this script migrated only the
"interesting" 65 and left the remaining ~676 (mostly kind=thought,
weight=retracted, status=dropped -- noise the judging pass already decided
was noise) unmigrated, those ~676 prompts would sit in the corpus as
UNJUDGED (they have a prompt row, no item row) and resurface at the head of
every future `itemize_prompts.py --extract` batch -- paying a model to
re-derive the identical "nothing here" conclusion a human or model already
reached, or worse, reaching a DIFFERENT conclusion the second time and
creating a silent disagreement between two judgements of the same prompt.
The dropped/retracted bucket carries no promotion risk either way:
`internal/corpus.Hard()` and the `unacknowledged`/`live_parameters` views
already exclude `dropped`/`retracted` rows structurally (core.py's own view
definitions), so migrating them changes nothing about what a dispatch sees
except making the corpus's own itemisation queue accurate. This mirrors
#1360's own "recovery is a superset, not a curated subset" precedent for
prompts, applied to items for the same reason: a partial recovery here is
exactly the shape of silent gap this repo keeps re-discovering.

THE MERGE RULE, id-keyed and idempotent, id preserved exactly:

  - Item ids are content-derived: `it-` + sha1(prompt_id|index|body)[:16]
    (itemize_prompts.py's own `_item_id`). The SAME judgement produces the
    SAME id regardless of which database it was written from -- there is
    nothing to invent, only to carry forward. A dead-db item id already
    present in the corpus is SKIPPED, never overwritten or re-derived.
  - A dead-db-judged prompt whose id has NO row in the corpus yet (20 of
    741, measured -- prompts judged in the dead db after PR #1360's own
    prompt migration already ran, per this issue's own drift warning) gets
    its PROMPT row migrated FIRST, via the exact same rule #1360 already
    established for prompts (id-keyed, skip-if-exists, `project` left NULL
    -- `record_prompt`'s own INSERT does not name that column, the same
    honest-absence value every other writer through this API already
    produces). `items.prompt_id REFERENCES prompts(id)` is a real,
    enforced foreign key (core.py's own `_connect(foreign_keys=True)`), so
    writing an item before its prompt exists would fail closed, not
    silently corrupt anything -- this script does the prerequisite write
    itself rather than relying on that failure.
  - Schemas checked, not assumed: both databases' `items` tables have the
    IDENTICAL column set (id, prompt_id, kind, body, weight, status,
    status_reason, resolved_to, acked_at) -- no `project`-shaped mismatch
    here, unlike #1360's `prompts` migration. `add_item`'s own signature
    already covers every column; nothing is invented or dropped.
  - `links` (conflicts_with/supersedes/depends_on): the dead database has
    ZERO rows in its own `links` table (measured) -- nothing to migrate,
    and this script does not create any (agent-supervisor#303's own rule:
    links are recorded by hand, never inferred).

READ-ONLY on the dead database throughout -- this script never opens it for
write. WRITES to the corpus route exclusively through `Ledger.record_prompt`/
`Ledger.add_item` (core.py), never a direct `sqlite3.connect(..., mode="rw")`
-- the `ledger-write-guard` hook is correct and this script does not attempt
to route around it.

Run: python3 migrate_dead_items_1362.py [--dry-run] [--dead-db PATH] [--corpus-dir PATH]
"""
from __future__ import annotations

import argparse
import hashlib
import os
import sqlite3
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # sibling core.py

DEFAULT_DEAD_DB = os.path.expanduser("~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3")
DEFAULT_CORPUS_DIR = os.environ.get("AGENT_CORPUS_DIR", os.path.expanduser("~/corpus"))

# The exact prompts-table columns this script reads from the dead DB and
# passes through to record_prompt when a prerequisite prompt row is
# missing -- explicitly NOT including `project`, matching
# migrate_dead_capture_1357.py's own DEAD_ROW_COLUMNS exactly (same schema,
# same reasoning: the dead schema never had this column).
DEAD_PROMPT_COLUMNS = (
    "id", "at", "text_raw", "text_clean", "context", "session",
    "source_file", "tmux_pane", "tmux_pane_target",
)

DEAD_ITEM_COLUMNS = (
    "id", "prompt_id", "kind", "body", "weight", "status", "status_reason",
    "resolved_to", "acked_at",
)


def _item_id(prompt_id, index, body):
    """Identical to itemize_prompts.py's own _item_id -- reproduced here,
    not imported, so this script has no import-time dependency on
    itemize_prompts.py's own CLI/argparse setup. Used only to VERIFY a
    read row's own id matches what this formula would produce (a cheap
    corruption check), never to re-derive an id for writing -- every id
    written by this script is the dead db's own, carried forward exactly."""
    digest = hashlib.sha1(f"{prompt_id}|{index}|{body}".encode("utf-8", errors="ignore")).hexdigest()
    return f"it-{digest[:16]}"


def read_dead_prompts_by_id(dead_db_path, ids):
    """Read specific prompts rows from the dead database, read-only, keyed
    by id. Only ever called for the small set of prompt ids this script
    found missing from the corpus -- never a full-table scan, unlike
    migrate_dead_capture_1357.py's own prompt migration, which already
    covers the general case."""
    if not ids:
        return {}
    uri = f"file:{dead_db_path}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    try:
        placeholders = ",".join("?" * len(ids))
        cur = conn.execute(
            f"SELECT {', '.join(DEAD_PROMPT_COLUMNS)} FROM prompts WHERE id IN ({placeholders})",
            list(ids),
        )
        return {row[0]: dict(zip(DEAD_PROMPT_COLUMNS, row)) for row in cur.fetchall()}
    finally:
        conn.close()


def read_dead_items(dead_db_path):
    """Read every `items` row from the dead database, read-only. Returns a
    list of dicts keyed by DEAD_ITEM_COLUMNS, ordered by prompt_id then id
    so a prompt's items (when it carries more than one) migrate together
    and deterministically."""
    uri = f"file:{dead_db_path}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    try:
        cur = conn.execute(
            f"SELECT {', '.join(DEAD_ITEM_COLUMNS)} FROM items ORDER BY prompt_id, id"
        )
        return [dict(zip(DEAD_ITEM_COLUMNS, row)) for row in cur.fetchall()]
    finally:
        conn.close()


def read_corpus_ids(corpus_dir, table):
    """Read-only pass to count/report the corpus's current ids in `table`
    (prompts or items), without going through the write-capable Ledger for
    this part -- same pattern as migrate_dead_capture_1357.py's own
    read_corpus_ids."""
    db_path = os.path.join(corpus_dir, "corpus.sqlite3")
    uri = f"file:{db_path}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    try:
        cur = conn.execute(f"SELECT id FROM {table}")
        return {row[0] for row in cur.fetchall()}
    finally:
        conn.close()


def read_dead_links_count(dead_db_path):
    uri = f"file:{dead_db_path}?mode=ro"
    conn = sqlite3.connect(uri, uri=True)
    try:
        return conn.execute("SELECT COUNT(*) FROM links").fetchone()[0]
    finally:
        conn.close()


def migrate(dead_db_path, corpus_dir, *, dry_run=False):
    from core import Ledger

    dead_items = read_dead_items(dead_db_path)
    corpus_prompt_ids_before = read_corpus_ids(corpus_dir, "prompts")
    corpus_item_ids_before = read_corpus_ids(corpus_dir, "items")

    dead_links = read_dead_links_count(dead_db_path)

    distinct_prompt_ids = sorted({row["prompt_id"] for row in dead_items})
    missing_prompt_ids = [pid for pid in distinct_prompt_ids if pid not in corpus_prompt_ids_before]

    print(f"dead db:              {dead_db_path}  ({len(dead_items)} item rows, "
          f"{len(distinct_prompt_ids)} distinct judged prompts)")
    print(f"corpus dir:           {corpus_dir}  "
          f"({len(corpus_prompt_ids_before)} prompt rows, {len(corpus_item_ids_before)} item rows before)")
    print(f"dead db links table:  {dead_links} row(s) -- nothing to migrate, none created here")
    print(f"prompt rows missing from corpus (need migrating first): {len(missing_prompt_ids)}")

    to_write_items = [row for row in dead_items if row["id"] not in corpus_item_ids_before]
    already_present = len(dead_items) - len(to_write_items)
    print(f"already present in corpus (skip, never overwrite): {already_present}")
    print(f"candidate items to migrate: {len(to_write_items)}")

    if dry_run:
        print("--dry-run: no writes performed")
        return

    ledger = Ledger(corpus_dir)

    prompts_written = 0
    prompts_skipped_race = 0
    if missing_prompt_ids:
        missing_rows = read_dead_prompts_by_id(dead_db_path, missing_prompt_ids)
        for pid in missing_prompt_ids:
            row = missing_rows.get(pid)
            if row is None:
                # The item's own prompt_id names no row in the dead db's
                # prompts table either -- a dangling reference the dead
                # db's own foreign key should never have allowed. Refuse
                # to guess; report and skip this prompt's items entirely
                # rather than writing an item for a prompt this script
                # cannot itself account for.
                print(f"  WARNING: prompt {pid} has an item in the dead db but no row in its own "
                      f"prompts table -- skipping every item for this prompt_id")
                continue
            if ledger.get_prompt(pid) is not None:
                prompts_skipped_race += 1
                continue
            ledger.record_prompt(
                row["id"], at=row["at"], text_raw=row["text_raw"], text_clean=row["text_clean"],
                context=row["context"], session=row["session"], source_file=row["source_file"],
                tmux_pane=row["tmux_pane"], tmux_pane_target=row["tmux_pane_target"],
            )
            prompts_written += 1

    written = 0
    skipped_race = 0
    skipped_no_prompt = 0
    for row in to_write_items:
        # Re-check immediately before writing -- belt-and-suspenders
        # against an id that landed in the corpus between the scan above
        # and this write. Never overwrite.
        if ledger.get_item(row["id"]) is not None:
            skipped_race += 1
            continue
        if ledger.get_prompt(row["prompt_id"]) is None:
            # Either the prerequisite prompt write above failed to find a
            # dead-db row for it (already warned above) or -- should not
            # happen given the scan above, but checked rather than
            # assumed -- something else left the prompt missing. Refuse
            # this one item rather than let the enforced foreign key
            # raise mid-batch and abandon everything after it.
            skipped_no_prompt += 1
            continue
        ledger.add_item(
            row["id"], prompt_id=row["prompt_id"], kind=row["kind"], body=row["body"],
            weight=row["weight"], status=row["status"], status_reason=row["status_reason"],
            resolved_to=row["resolved_to"], acked_at=row["acked_at"],
        )
        written += 1

    corpus_prompt_ids_after = read_corpus_ids(corpus_dir, "prompts")
    corpus_item_ids_after = read_corpus_ids(corpus_dir, "items")
    print()
    print(f"prompt rows written (prerequisite):  {prompts_written}")
    print(f"prompt rows skipped (race):          {prompts_skipped_race}")
    print(f"item rows written:                   {written}")
    print(f"item rows skipped (pre-existing):    {already_present}")
    print(f"item rows skipped (race):            {skipped_race}")
    print(f"item rows skipped (no prompt row):   {skipped_no_prompt}")
    print(f"corpus prompt rows after: {len(corpus_prompt_ids_after)} "
          f"(was {len(corpus_prompt_ids_before)}, delta {len(corpus_prompt_ids_after) - len(corpus_prompt_ids_before)})")
    print(f"corpus item rows after:   {len(corpus_item_ids_after)} "
          f"(was {len(corpus_item_ids_before)}, delta {len(corpus_item_ids_after) - len(corpus_item_ids_before)})")


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
