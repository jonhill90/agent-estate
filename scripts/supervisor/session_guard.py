"""One place session removal is judged safe or refused -- agent-tui#14.

`session_remove_check` (a read source) and `session_remove` (the write that
actually kills the session) both need the exact same three-refusal logic,
and this codebase's own stated principle is that safety rules live in one
place: a caller who checked, got "safe", and then called a second
implementation that quietly disagrees is worse than no check at all. So
there is exactly one function here, `remove_guard`, and both call sites
below import it rather than each carrying their own copy.

Three refusals, agent-tui#14's own vocabulary:

  * the session is not agent-supervisor#153-supervised (see cli.py's
    `session_state` for the tri-state this collapses `unsupervised` into
    `unknown`, same convention).
  * a lane in the session is busy (`lanes.sh --json`, any row read `busy`).
  * a worktree the session's panes are sitting in is not provably clean and
    fully pushed.

The third one is the one worth being careful about. `git status`/`git
rev-list` can fail for reasons that have nothing to do with whether the
worktree is actually dirty -- the path was removed out from under it, git
itself is missing, the call timed out -- and every one of those is reported
as `clean: None` / `unpushed: None`, never guessed as clean. `safe_to_remove`
treats `None` exactly like a positive "unsafe": undeterminable must never
mean safe, the same "blind, not quiet" posture the rest of this directory
takes (see `supervisor_view.py`'s module docstring).

`remove_guard` itself never raises for a determinable-but-negative answer --
every one of the three checks above resolves to `False`/`None` plus a
`refusals` entry, not an exception. It raises `SupervisorUnavailable` only
when it could not even READ the evidence for a session that is supposed to
be readable (`lanes.sh --json` failing for a session tmux says exists, for
instance) -- the same fail-loud discipline every other source in this
directory already carries.
"""

from __future__ import annotations

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from supervisor_view import (  # noqa: E402
    DEFAULT_STATE_DIR,
    DEFAULT_TIMEOUT_SECONDS,
    SupervisorUnavailable,
    _decode,
    _subprocess_runner,
    _tail,
)


HERE = Path(__file__).resolve().parent


def _check_worktree(path, *, runner, timeout):
    """Clean/pushed state of one worktree path -- never guessed.

    Every failure path here (missing directory, no git on PATH, a timeout, a
    path that is not a git repo at all) reports `clean`/`unpushed` as `None`
    with a `reason`, and is caught locally rather than left to blow up
    `remove_guard`'s single call for every OTHER worktree -- one unreadable
    path must not blind the whole check.
    """
    try:
        status = runner(["git", "-C", str(path), "status", "--porcelain=v1"], timeout=timeout)
    except SupervisorUnavailable as error:
        return {"path": path, "clean": None, "unpushed": None, "reason": str(error)}
    if status.returncode != 0:
        reason = _tail(status.stderr) or _tail(status.stdout) or f"git status exited {status.returncode}"
        return {"path": path, "clean": None, "unpushed": None, "reason": reason}
    if (status.stdout or "").strip():
        return {"path": path, "clean": False, "unpushed": None, "reason": "worktree has uncommitted changes"}

    try:
        upstream = runner(
            ["git", "-C", str(path), "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"], timeout=timeout
        )
    except SupervisorUnavailable as error:
        return {"path": path, "clean": True, "unpushed": None, "reason": str(error)}
    if upstream.returncode != 0:
        return {"path": path, "clean": True, "unpushed": None, "reason": "no upstream configured, cannot confirm pushed"}

    try:
        count = runner(["git", "-C", str(path), "rev-list", "--count", "@{u}..HEAD"], timeout=timeout)
    except SupervisorUnavailable as error:
        return {"path": path, "clean": True, "unpushed": None, "reason": str(error)}
    if count.returncode != 0:
        reason = _tail(count.stderr) or _tail(count.stdout) or f"git rev-list exited {count.returncode}"
        return {"path": path, "clean": True, "unpushed": None, "reason": reason}

    text = (count.stdout or "").strip()
    if text == "0":
        return {"path": path, "clean": True, "unpushed": False, "reason": None}
    return {"path": path, "clean": True, "unpushed": True, "reason": f"{text or '?'} commit(s) not on the upstream branch"}


def remove_guard(
    session,
    *,
    transport,
    cli=None,
    python=None,
    state_dir=None,
    lanes_script=None,
    runner=_subprocess_runner,
    timeout=DEFAULT_TIMEOUT_SECONDS,
):
    """Evaluate agent-tui#14's three remove refusals for `session`.

    See the module docstring for the shape of the returned dict and for why
    this never raises for a determinable-but-negative answer.
    """
    cli = Path(cli) if cli else HERE / "cli.py"
    python = python or sys.executable or "python3"
    state_dir = Path(state_dir) if state_dir else DEFAULT_STATE_DIR
    lanes_script = Path(lanes_script) if lanes_script else HERE / "lanes.sh"

    exists = transport.session_exists(session)

    state_command = [python, str(cli), "--state-dir", str(state_dir), "session-state", "--session", session]
    state_payload = _decode(runner(state_command, timeout=timeout), label="cli.py session-state")
    if not isinstance(state_payload, dict) or "state" not in state_payload:
        raise SupervisorUnavailable("cli.py session-state returned something that is not a session-state object")
    raw_state = state_payload["state"]
    # #153's tri-state, with unsupervised collapsed into unknown -- the same
    # convention `cli.py`'s own `session_state()` already documents: a
    # session this estate has not decided to adopt reads the same as one it
    # has never heard of at all.
    supervision = "supervised" if raw_state == "supervised" else "unknown"

    lanes_result = runner([str(lanes_script), "--json", session], timeout=timeout)
    if lanes_result.returncode != 0:
        if not exists:
            # lanes.sh's own contract: it exits 1 when the session does not
            # exist, which is not "unreadable" here -- a session tmux does
            # not have cannot have a busy lane, by construction.
            busy_lanes = []
        else:
            raise SupervisorUnavailable(
                "lanes.sh --json exited "
                f"{lanes_result.returncode} for a session tmux reports existing: "
                f"{_tail(lanes_result.stderr) or _tail(lanes_result.stdout) or 'no output'}"
            )
    else:
        rows = _decode(lanes_result, label="lanes.sh --json")
        if not isinstance(rows, list):
            raise SupervisorUnavailable("lanes.sh --json returned something that is not a list of lanes")
        busy_lanes = [row.get("name") for row in rows if row.get("state") == "busy"]

    panes = transport.list_panes(session) if exists else []
    seen = set()
    worktrees = []
    for pane in panes:
        path = pane.get("path")
        if not path or path in seen:
            continue
        seen.add(path)
        worktrees.append(_check_worktree(path, runner=runner, timeout=timeout))

    refusals = []
    if supervision != "supervised":
        refusals.append(f"session {session!r} is not supervised (state: {raw_state!r})")
    for lane in busy_lanes:
        refusals.append(f"lane {lane!r} is busy")
    for worktree in worktrees:
        if worktree["clean"] is not True:
            refusals.append(f"worktree {worktree['path']!r} is dirty or undeterminable: {worktree['reason']}")
        elif worktree["unpushed"] is not False:
            refusals.append(f"worktree {worktree['path']!r} is unpushed or undeterminable: {worktree['reason']}")

    return {
        "session": session,
        "exists": exists,
        "supervision": supervision,
        "busy_lanes": busy_lanes,
        "worktrees": worktrees,
        "safe_to_remove": not refusals,
        "refusals": refusals,
    }
