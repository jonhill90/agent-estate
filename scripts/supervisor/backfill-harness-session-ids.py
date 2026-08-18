#!/usr/bin/env python3
"""Backfill `harness_session_id`/`harness_project_dir` for claude-print lanes
registered before agent-supervisor#302 (agent-supervisor#311).

#302 made `ClaudePrintAdapter.register_lane` write `harness_session_id` and
`harness_project_dir` on NEW registrations. It fixed nothing for lanes that
already existed: `restore.sh` refuses rather than invents (AGENTS.md
invariant 3), so a tmux death reported every one of them UNRECOVERABLE while
the conversation sat resumable on disk.

THE MAPPING (agent-supervisor#302's own description): for claude-print,
`harness_session_id` and `session_id` are the SAME uuid by construction -- it
is what `claude -p --session-id <uuid>` minted at registration and what
`claude -p --resume <uuid>` takes. The one thing that needs establishing per
row is not the id itself (it is already sitting in `session_id`) but whether
a transcript for that id still exists on disk, which is what makes it
genuinely resumable rather than a stale uuid pointing at nothing.

Recoverable = `session_id` is a well-formed uuid AND exactly one
`<session_id>.jsonl` exists somewhere under `~/.claude/projects` (searched by
filename, not by reconstructing Claude Code's own project-directory
sanitisation, which does not just replace `/` with `-` -- it resolves
symlinks first, i.e. `/var` -> `/private/var` on macOS, and collapses
consecutive separators to one dash, not one dash per character. Guessing the
directory name under-counts recoverable rows; searching by filename does
not).

Idempotent: only touches rows where `harness_session_id` is currently
null/empty. Running it twice changes nothing the second time.

Usage:
    python3 backfill-harness-session-ids.py <path-to-ledger.sqlite3>

Prints a JSON summary: total affected, recovered, permanently
unrecoverable (named), and rows actually updated. ALWAYS back up the ledger
before pointing this at a live one -- it writes in place.
"""
from __future__ import annotations

import json
import os
import sqlite3
import subprocess
import sys
import time

CLAUDE_PROJECTS = os.path.expanduser("~/.claude/projects")


def _looks_like_uuid(value):
    return len(value) == 36 and value.count("-") == 4


def find_transcript(session_id):
    """Search the whole `~/.claude/projects` tree by filename rather than
    reconstructing the project-directory name -- see module docstring for
    why the naive reconstruction under-counts."""
    result = subprocess.run(
        ["find", CLAUDE_PROJECTS, "-name", f"{session_id}.jsonl"],
        capture_output=True,
        text=True,
    )
    return [line for line in result.stdout.splitlines() if line.strip()]


def backfill(db_path):
    con = sqlite3.connect(db_path)
    con.row_factory = sqlite3.Row
    rows = con.execute(
        "select lane, session_id, repo from lanes where transport='claude-print' "
        "and (harness_session_id is null or harness_session_id='') order by lane"
    ).fetchall()

    recovered = []
    unrecoverable = []
    for row in rows:
        session_id = row["session_id"]
        matches = find_transcript(session_id) if _looks_like_uuid(session_id) else []
        if _looks_like_uuid(session_id) and len(matches) == 1:
            recovered.append({"lane": row["lane"], "session_id": session_id, "repo": row["repo"]})
        else:
            unrecoverable.append({"lane": row["lane"], "session_id": session_id, "matches": matches})

    now = int(time.time())
    cur = con.cursor()
    updated = 0
    for entry in recovered:
        cur.execute(
            "update lanes set harness_session_id=?, harness_project_dir=?, updated_at=? "
            "where lane=? and transport='claude-print' "
            "and (harness_session_id is null or harness_session_id='')",
            (entry["session_id"], entry["repo"], now, entry["lane"]),
        )
        updated += cur.rowcount
    con.commit()
    con.close()

    return {
        "total_affected_before": len(rows),
        "recovered": len(recovered),
        "unrecoverable": len(unrecoverable),
        "rows_updated": updated,
        "unrecoverable_lanes": [entry["lane"] for entry in unrecoverable],
    }


def main():
    if len(sys.argv) != 2:
        print("usage: backfill-harness-session-ids.py <path-to-ledger.sqlite3>", file=sys.stderr)
        return 1
    print(json.dumps(backfill(sys.argv[1]), indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
