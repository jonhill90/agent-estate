#!/usr/bin/env python3
"""Resolve the HARNESS conversation id a lane is now running, or refuse.

agent-dotfiles#237. After a tmux server loss the only durable record of what
a lane was doing is the harness's own session file -- `claude --resume <id>`
brings the conversation back with full context, and nothing in tmux can. This
module is how `dispatch.sh` learns that id at the one moment it is knowable
and cheap: immediately after the brief has been submitted into the pane.

HOW THE ID IS FOUND, and what that costs (agent-dotfiles#237 asks for this to
be stated rather than assumed):

* This is an INFERENCE FROM A FILE LAYOUT, not a documented interface. Claude
  Code writes one JSONL transcript per conversation under
  `~/.claude/projects/<slug>/<session-id>.jsonl`; the file's stem is the id
  `--resume` takes. Nothing in the CLI promises that layout, so a future
  Claude Code that moves or renames those files breaks this resolver. It
  breaks it CLOSED: no candidate matches, nothing is recorded, and the lane
  is later reported unrecoverable rather than restarted from scratch.
* Three things were considered and rejected as the source, each for a reason
  measured on this machine on 2026-08-12:
  - The pane process's environment (`ps -Eww -p <pid>` does expose
    `CLAUDE_CODE_SESSION_ID` for a same-user process). It is the id the
    process was LAUNCHED with, and `dispatch.sh` sends `/clear` before every
    brief, which starts a new conversation with a new id. Measured on a live
    lane: the process env said `c5aa6462-...`, for which no session file
    exists at all, while the live conversation was `7cef6d59-...`. Resuming
    the process-env id would have restored nothing.
  - `lsof` on the pane process. Claude Code appends and closes; the
    transcript was not in the process's open files.
  - The `<slug>` directory name, derived from the pane's cwd. Every lane in
    this estate is bootstrapped in the same shared checkout, so the slug is
    identical across lanes and separates nothing. This module never computes
    a slug; it scans `projects/*/` and discriminates on file CONTENT instead.

WHAT MAKES A CANDIDATE, all three required (any one alone is ambiguous, and
that was checked, not assumed -- grepping the shared project directory for a
brief path alone returned FOUR files: the lane, the supervisor that printed
the path, and two other agents that had read it):

1. The transcript BEGAN during this dispatch -- its first timestamped entry
   is at or after `since`. `dispatch.sh` sends `/clear` immediately before
   the brief, which starts a fresh transcript, so the lane's file is new and
   every bystander's file is old.
2. It carries the dispatch `marker` -- the lane's own worktree path, which is
   unique per dispatch.
3. Its `sessionId` field agrees with its filename, and the name is a uuid.

EXACTLY ONE candidate resolves. Zero or several REFUSE, and refusing is the
point: a wrong id restores a fresh agent wearing a working lane's name, which
agent-dotfiles#237 names as the worst outcome available.

KNOWN LIMIT, stated because it is real: an agent that runs `/clear` itself
mid-task starts a new transcript, and the recorded id then names the
conversation as it was before that `/clear`. Restore brings back a real
conversation of that lane, not a fabricated one, but not its latest state.
Nothing here can see that happen -- closing it needs the harness to publish
its live id (a `SessionStart`/`UserPromptSubmit` hook is the documented
channel), which is a change to Jon's deployed `~/.claude` and out of scope
here.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
from datetime import datetime, timezone
from pathlib import Path

UUID_RE = re.compile(r"^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$")
# How far back a transcript's own first entry may sit and still count as
# "began during this dispatch". Not zero: `since` is read by the shell before
# the send, and the two clocks (this process's and the file's) are the same
# clock but the entry is written a moment later, never earlier. The slack is
# for the opposite direction -- a transcript opened a second or two before the
# dispatcher recorded `since` -- and it is small enough that a conversation
# from a previous dispatch, minutes or hours old, can never qualify.
BEGAN_SLACK_SECONDS = 5


def _first_timestamp(path):
    """The epoch of a transcript's first timestamped entry, or None.

    Reads line by line and stops at the first hit: the opening `mode` and
    `file-history-snapshot` entries carry no timestamp, and a transcript can
    be megabytes.
    """
    try:
        with path.open("r", encoding="utf-8", errors="replace") as handle:
            for line in handle:
                if '"timestamp"' not in line:
                    continue
                try:
                    stamp = json.loads(line).get("timestamp")
                except (ValueError, TypeError):
                    continue
                if not stamp:
                    continue
                try:
                    parsed = datetime.fromisoformat(str(stamp).replace("Z", "+00:00"))
                except ValueError:
                    continue
                if parsed.tzinfo is None:
                    parsed = parsed.replace(tzinfo=timezone.utc)
                return parsed.timestamp()
    except OSError:
        return None
    return None


def _declares_own_id(path, session_id):
    """Does the transcript's own `sessionId` agree with its filename?

    The filename is what `--resume` takes, so a file whose content disagrees
    with its name is not a thing this module will hand to a restore.
    """
    try:
        with path.open("r", encoding="utf-8", errors="replace") as handle:
            for line in handle:
                if '"sessionId"' not in line:
                    continue
                try:
                    declared = json.loads(line).get("sessionId")
                except (ValueError, TypeError):
                    continue
                if declared:
                    return declared == session_id
    except OSError:
        return False
    return False


def _carries(path, marker):
    try:
        with path.open("r", encoding="utf-8", errors="replace") as handle:
            for line in handle:
                if marker in line:
                    return True
    except OSError:
        return False
    return False


def candidates(*, home, marker, since):
    """Every transcript that satisfies all three tests above. Never guesses."""
    projects = Path(home) / ".claude" / "projects"
    if not projects.is_dir():
        return []
    found = []
    for project in sorted(projects.iterdir()):
        if not project.is_dir():
            continue
        for transcript in sorted(project.glob("*.jsonl")):
            session_id = transcript.stem
            if not UUID_RE.match(session_id):
                continue
            try:
                if transcript.stat().st_mtime < since - BEGAN_SLACK_SECONDS:
                    continue
            except OSError:
                continue
            began = _first_timestamp(transcript)
            if began is None or began < since - BEGAN_SLACK_SECONDS:
                continue
            if not _carries(transcript, marker):
                continue
            if not _declares_own_id(transcript, session_id):
                continue
            found.append(session_id)
    return found


def resolve(*, harness, marker, since, home=None, timeout=0.0, sleep=time.sleep, clock=time.time):
    """Resolve one session id, or raise LookupError naming why it refused.

    `timeout` polls: the transcript is written when the harness accepts the
    prompt, which is not instantaneous after `send-keys Enter`. Returns as
    soon as exactly one candidate exists; an AMBIGUOUS result is returned
    immediately rather than waited on, because more time can only add
    candidates.
    """
    if harness != "claude":
        raise LookupError(f"no session resolver for harness {harness!r} -- only claude is implemented")
    home = home or os.environ.get("HOME") or str(Path.home())
    deadline = clock() + timeout
    while True:
        found = candidates(home=home, marker=marker, since=since)
        if len(found) == 1:
            return found[0]
        if len(found) > 1:
            raise LookupError(
                "refusing: " + str(len(found)) + " transcripts match this dispatch (" + ", ".join(found) + ")"
            )
        if clock() >= deadline:
            raise LookupError("refusing: no transcript carries this dispatch marker")
        sleep(0.5)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--harness", required=True)
    parser.add_argument("--marker", required=True, help="a string unique to this dispatch (the lane's worktree path)")
    parser.add_argument("--since", type=float, required=True, help="epoch seconds, read before the brief was sent")
    parser.add_argument("--home", default=None)
    parser.add_argument("--timeout", type=float, default=20.0)
    args = parser.parse_args(argv)
    try:
        print(resolve(harness=args.harness, marker=args.marker, since=args.since, home=args.home, timeout=args.timeout))
    except LookupError as error:
        # Exit 3, not 1: the caller must be able to tell "could not resolve"
        # -- an ordinary, expected outcome that records an empty id -- from a
        # crash in this script.
        print(f"harness-session: {error}", file=sys.stderr)
        return 3
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
