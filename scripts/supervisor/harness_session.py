#!/usr/bin/env python3
"""Resolve the HARNESS conversation id a lane is now running, or refuse.

agent-dotfiles#237. After a tmux server loss the only durable record of what
a lane was doing is the harness's own session file -- `claude --resume <id>`
brings the conversation back with full context, and nothing in tmux can. This
module is how `dispatch.sh` learns that id at the one moment it is knowable
and cheap: immediately after the brief has been submitted into the pane.

agent-supervisor#(codex adapter gap, 2026-08-23): `resolve()` used to refuse
outright for every harness but `claude` ("no session resolver ... only claude
is implemented"), unconditionally, on every single codex dispatch -- not a
theoretical gap, a live one: `dispatch.sh` calls this after every dispatch
regardless of harness, and every codex lane hit this refusal, every time,
printing "no harness session id recorded" to dispatch's own stdout and
leaving that lane permanently `UNRECOVERABLE` to `restore.sh` (see its own
header) for the rest of its life. Codex has never been resumable in this
estate, not because `codex` lacks a resume dialect -- verified live,
`codex resume <SESSION_ID>` reopens the exact prior conversation, no picker,
2026-08-23 -- but because nothing here ever looked. `candidates_codex` below
closes that; see its own docstring for what codex's own on-disk layout gives
for free that Claude's did not.

HOW THE CLAUDE ID IS FOUND, and what that costs (agent-dotfiles#237 asks for
this to be stated rather than assumed):

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
    """Every Claude transcript that satisfies all three tests above. Never guesses."""
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


def _codex_session_meta(path):
    """Read a codex rollout file's own `session_meta` record: (id, cwd, epoch)
    or None.

    MEASURED, not inferred (isolated tmux socket, real codex-cli 0.149.0,
    2026-08-23): unlike Claude's transcript, which scatters `sessionId`
    across many lines and never states its own cwd at all, codex writes
    EXACTLY ONE `session_meta` event, always first in the file, carrying
    `session_id` (== the filename's own uuid), `cwd` (the directory codex was
    launched in -- a lane's worktree, unique per dispatch, so it is a better
    discriminator than Claude's substring-in-body `marker` check: an exact
    field match, not "appears somewhere in this file"), and `timestamp`
    (ISO-8601, matching `since` the same way `_first_timestamp` does for
    Claude). Reading only the first line is deliberate and cheap: a real
    rollout file was 75KB in this measurement, and everything this function
    needs is in the header a caller would otherwise have to scan for.
    """
    try:
        with path.open("r", encoding="utf-8", errors="replace") as handle:
            first = handle.readline()
    except OSError:
        return None
    try:
        rec = json.loads(first)
    except (ValueError, TypeError):
        return None
    if rec.get("type") != "session_meta":
        return None
    payload = rec.get("payload") or {}
    session_id = payload.get("session_id")
    cwd = payload.get("cwd")
    stamp = payload.get("timestamp")
    if not session_id or not cwd or not stamp:
        return None
    try:
        parsed = datetime.fromisoformat(str(stamp).replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None:
        parsed = parsed.replace(tzinfo=timezone.utc)
    return session_id, cwd, parsed.timestamp()


def candidates_codex(*, home, marker, since):
    """Every codex rollout that satisfies the codex analogue of the three
    Claude tests above. Never guesses.

    Codex writes one JSONL rollout per conversation under
    `~/.codex/sessions/<YYYY>/<MM>/<DD>/rollout-<timestamp>-<uuid>.jsonl` --
    an on-disk layout this module infers from the shipped CLI the same way
    it already does for Claude's, and just as unpromised by codex's own
    interface: a future codex that moves or renames these files breaks this
    resolver CLOSED, the same failure direction as Claude's.

    `marker` here is `$WORKTREE` (dispatch.sh's own caller), and codex's own
    `cwd` field is checked for EQUALITY against it, not substring containment
    -- `respawn-pane -c "$WORKTREE"` launches codex directly in that
    directory (harness/codex.sh: `HARNESS_LAUNCH_TAKES_PROMPT`, the prompt is
    codex's own launch argv, not a typed message), so cwd is exactly the
    worktree path codex was started in, not merely a string that might
    appear in conversation text somewhere.
    """
    sessions = Path(home) / ".codex" / "sessions"
    if not sessions.is_dir():
        return []
    found = []
    for rollout in sorted(sessions.glob("*/*/*/rollout-*.jsonl")):
        meta = _codex_session_meta(rollout)
        if meta is None:
            continue
        session_id, cwd, began = meta
        if not UUID_RE.match(session_id):
            continue
        if cwd != marker:
            continue
        if began < since - BEGAN_SLACK_SECONDS:
            continue
        if not rollout.stem.endswith(session_id):
            # The filename's own trailing uuid must agree with the payload's
            # `session_id` -- the same "content agrees with name" discipline
            # `_declares_own_id` applies for Claude, so a rollout whose
            # header was hand-edited or corrupted is never handed to a
            # restore. `endswith`, not a split on the last `-`: a uuid is
            # itself hyphenated (8-4-4-4-12), so splitting on one hyphen
            # truncates it to its final 8 hex digits, a bug this test caught
            # (session_id `01a0...037c` vs. the split's `4741037c`).
            continue
        found.append(session_id)
    return found


_RESOLVERS = {
    "claude": candidates,
    "codex": candidates_codex,
}


def resolve(*, harness, marker, since, home=None, timeout=0.0, sleep=time.sleep, clock=time.time):
    """Resolve one session id, or raise LookupError naming why it refused.

    `timeout` polls: the transcript is written when the harness accepts the
    prompt, which is not instantaneous after `send-keys Enter`. Returns as
    soon as exactly one candidate exists; an AMBIGUOUS result is returned
    immediately rather than waited on, because more time can only add
    candidates.
    """
    finder = _RESOLVERS.get(harness)
    if finder is None:
        raise LookupError(
            f"no session resolver for harness {harness!r} -- only {', '.join(sorted(_RESOLVERS))} implemented"
        )
    home = home or os.environ.get("HOME") or str(Path.home())
    deadline = clock() + timeout
    while True:
        found = finder(home=home, marker=marker, since=since)
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
