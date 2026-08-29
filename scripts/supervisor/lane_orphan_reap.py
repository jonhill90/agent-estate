"""Reap a lane's background descendants once its own turn has ended.

agent-estate#800: `worktree-guard-audit.sh` processes were found alive 26
minutes after the lane that started them had already been recorded
`complete` -- six of them, all reparented to `init` (`ppid=1`), all
ignoring `SIGTERM` and needing `SIGKILL`. The lane had run a smoke test by
hand; when its `claude -p` turn ended, its background descendants
survived with nobody left to signal them. The issue's own comment thread
measured the SAME shape three more times, independently, against the
person who filed it -- this is not lane-specific carelessness, it is the
default behaviour of the turn boundary: a `claude -p` process exiting does
not send anything to whatever it backgrounded.

`reconcile_lane_completions.py` already determines that a lane's task has
gone terminal (complete/failed/cancelled). It never looks at what that
lane left running -- that is the seam this module closes, deliberately
kept in its own file rather than folded into that module's already
extensive (agent-supervisor#155 through #779) sweep logic: reaping a
process is a materially different hazard than writing a ledger row, and
the two want to be reviewable, and testable, independently.

## Identifying a lane's descendants reliably

Reparenting to `init` destroys the ppid chain -- the most obvious lookup
("is this a descendant of the lane's own pid") is exactly the one fact
that no longer holds by the time anything could notice. Of the two
candidates agent-estate#800 named (the worktree path in argv/cwd, or the
process group), this module uses **the worktree path**, checked two ways:

1. The process's own command line (`ps ... args`) contains the lane's
   exact worktree path -- cheap, and how the six orphans on #800 were
   actually found (`ps -eo pid,ppid,command | grep ad-800-callers-91479`).
2. Where `lsof` is available, the process's own current working directory
   (`lsof -a -d cwd -p <pid>`) resolves to that same worktree path -- the
   stronger of the two signals named on #800 ("the cwd" candidate), used
   here to corroborate rather than replace the cheap argv check, never the
   other way around: a process whose argv happens to mention the path
   without actually running inside it is exactly the over-match #800
   warned against ("A pattern that over-matches here kills someone else's
   work"), and cwd is the one signal that resolves that ambiguity.

Both together are required with `ppid == 1` (reparented -- the parent that
would have signalled it is gone) before this module will ever consider a
pid a candidate. A process that merely mentions the path in its argv but
still has a live, non-init parent is not orphaned; it is left alone.

## The hazard that governs this module (agent-estate#800's own words)

"This reaps processes. Getting it wrong destroys running work. The estate
has already lost a lane's work once to an over-eager sweep." Every gate
below is written to refuse rather than guess:

* The task this worktree belongs to must be recorded TERMINAL
  (`complete`/`failed`/`cancelled`) in the ledger. No task record, or a
  non-terminal one, refuses outright.
* The lane must not currently be occupied by ANY open task
  (`Ledger.get_open_task_for_lane`) -- a lane redispatched faster than this
  sweep runs is live, even if this specific worktree's own task is
  terminal.
* The worktree itself must be clean (`git status --porcelain`). Dirty, or
  the check itself failing for any reason (missing directory, non-repo,
  `git` erroring), refuses -- unknown is treated as dirty, never as clean.
* The actual kill is delegated to `reap-verified.sh`'s own
  `reap_pid_verified` (agent-supervisor#104), invoked once per pid, never
  batched into a single shell word -- the exact bug agent-estate#800's own
  investigation hit ("an unquoted `$pids` from `awk` does not word-split
  and `kill` rejects the newline-joined string... silently no-op'd my own
  cleanup"). `reap_pid_verified` already does TERM-then-KILL-then-verify
  and refuses to signal a pid whose command line does not contain the
  sandbox substring it is given -- this module gives it the worktree path
  as that substring, so the same refusal protects here even if this
  module's own gates above were somehow bypassed.
"""

from __future__ import annotations

import re
import subprocess
from pathlib import Path

from core_constants import TERMINAL_STATUSES
from core_lane_relation import normalize_worktree_path

REAP_VERIFIED_SH = str(Path(__file__).resolve().parent / "reap-verified.sh")

_PS_LINE_RE = re.compile(r"^\s*(\d+)\s+(\d+)\s+(.*)$")


def default_runner(argv):
    """`subprocess.run` wrapper matching `reconcile_lane_completions.subprocess_runner`'s
    shape -- stdout text, raises on nonzero exit."""
    return subprocess.run(argv, check=True, capture_output=True, text=True).stdout


def parse_ps_lines(ps_output):
    """`ps -axo pid,ppid,args` output -> `[{"pid": int, "ppid": int, "args": str}, ...]`.

    Skips the header line and anything that does not parse as
    `<pid> <ppid> <rest>` -- a line this cannot parse is dropped, never
    guessed at (the same fail-closed posture as the rest of this module).
    """
    rows = []
    for line in ps_output.splitlines():
        match = _PS_LINE_RE.match(line)
        if match is None:
            continue
        pid_text, ppid_text, args = match.groups()
        try:
            pid, ppid = int(pid_text), int(ppid_text)
        except ValueError:
            continue
        rows.append({"pid": pid, "ppid": ppid, "args": args})
    return rows


def find_candidate_pids(ps_rows, worktree_path):
    """Reparented (`ppid == 1`) processes whose own command line names this
    exact worktree path. This is the cheap argv half of the two-signal
    check this module's docstring describes -- callers should still
    confirm cwd via `cwd_matches_worktree` before reaping, where `lsof` is
    available.

    Checked against BOTH the raw `worktree_path` string and its
    `normalize_worktree_path` spelling: `ps ... args` shows whatever
    literal spelling a process was actually invoked with (often the
    unresolved `/var/...` form on macOS), while the ledger's own
    `worktree_path` column and `normalize_worktree_path` may carry the
    resolved `/private/var/...` form instead (agent-supervisor#624). Either
    spelling appearing in a row's argv is enough -- this mirrors
    `get_task_for_worktree`'s own reason for normalizing both sides rather
    than assuming one canonical spelling reaches this function.

    A blank/unnormalizable `worktree_path` matches nothing -- see
    `core_lane_relation.normalize_worktree_path`'s own docstring for why an
    empty needle must never become a substring match against every row.
    """
    normalized = normalize_worktree_path(worktree_path)
    if not normalized:
        return []
    needles = {worktree_path, normalized}
    return [
        row["pid"]
        for row in ps_rows
        if row["ppid"] == 1 and any(needle in row["args"] for needle in needles)
    ]


def cwd_matches_worktree(pid, worktree_path, *, runner=default_runner, lsof_bin="lsof"):
    """Best-effort cwd corroboration for one candidate pid, via `lsof -a -d
    cwd -p <pid> -Fn`.

    A genuine three-state answer, distinguishing "confirmed" from "refuted"
    from "could not check" -- collapsing the last two into one `False`
    would make an unrelated process's real, different cwd read exactly
    like `lsof` merely being absent, and a caller that then falls back to
    argv-only trust would reap a pid this check actually refuted.

    * `True` -- `lsof` ran and its cwd normalizes to the same path.
    * `False` -- `lsof` ran and named a DIFFERENT cwd. A confirmed refutation.
    * `None` -- could not determine (no `lsof`, the pid already exited
      between the `ps` snapshot and this check, or an unparseable answer).
      Callers must treat this as "unconfirmed", not as either extreme.
    """
    normalized = normalize_worktree_path(worktree_path)
    if not normalized:
        return None
    try:
        output = runner([lsof_bin, "-a", "-d", "cwd", "-p", str(pid), "-Fn"])
    except Exception:
        return None
    cwd = None
    for line in output.splitlines():
        if line.startswith("n"):
            cwd = line[1:]
    if cwd is None:
        return None
    return normalize_worktree_path(cwd) == normalized


class LaneOrphanReaper:
    """Reap the background descendants a lane's own worktree left running,
    only once the task that worktree belongs to is confirmed terminal, the
    lane is confirmed not otherwise occupied, and the worktree is confirmed
    clean. See this module's own docstring for the full rationale."""

    def __init__(self, ledger, *, runner=None, ps_bin="ps", git_bin="git", lsof_bin="lsof", require_cwd_match=None):
        self.ledger = ledger
        self.runner = runner or default_runner
        self.ps_bin = ps_bin
        self.git_bin = git_bin
        self.lsof_bin = lsof_bin
        # Tests inject `require_cwd_match=False` to exercise the argv-only
        # path on a host without `lsof`; production leaves this `None` so
        # `reap_task_orphans` decides per-pid based on whether `lsof`
        # actually answered (see `cwd_matches_worktree`'s own docstring).
        self._require_cwd_match_override = require_cwd_match

    def _worktree_is_dirty(self, worktree_path):
        """Fail-closed: a `git status --porcelain` that returns anything, or
        that cannot be run at all (missing directory, not a repo, `git`
        erroring), is treated as dirty -- never as "unknown, so assume
        clean". agent-estate#800's own hazard note: getting this wrong
        destroys running work."""
        try:
            output = self.runner([self.git_bin, "-C", worktree_path, "status", "--porcelain"])
        except Exception:
            return True
        return bool(output.strip())

    def _lane_is_live(self, lane):
        return self.ledger.get_open_task_for_lane(lane) is not None

    def _candidate_pids(self, worktree_path):
        try:
            ps_output = self.runner([self.ps_bin, "-axo", "pid,ppid,args"])
        except Exception:
            return None
        rows = parse_ps_lines(ps_output)
        return find_candidate_pids(rows, worktree_path)

    def reap_task_orphans(self, task):
        """Reap every confirmed-orphaned process left behind by `task`'s
        worktree. Never raises -- every refusal and every outcome is
        returned in the report dict, exactly one of:

        `{"outcome": "not_terminal" | "no_worktree_path" | "lane_live" |
          "worktree_dirty" | "ps_unavailable" | "no_candidates" | "reaped",
          "task": task_id, "lane": lane, "worktree_path": path,
          "reaped": [pid, ...], "refused": [pid, ...], "failed": [pid, ...]}`

        `reaped`/`refused`/`failed` are always present (possibly empty) so
        a caller never has to guess whether a key's absence means zero or
        means "did not get that far".
        """
        base = {"task": task.get("id"), "lane": task.get("lane"), "reaped": [], "refused": [], "failed": []}
        worktree_path = task.get("worktree_path") or ""
        base["worktree_path"] = worktree_path

        if task.get("status") not in TERMINAL_STATUSES:
            return {**base, "outcome": "not_terminal"}
        if not normalize_worktree_path(worktree_path):
            return {**base, "outcome": "no_worktree_path"}
        if self._lane_is_live(task.get("lane")):
            return {**base, "outcome": "lane_live"}
        if self._worktree_is_dirty(worktree_path):
            return {**base, "outcome": "worktree_dirty"}

        candidate_pids = self._candidate_pids(worktree_path)
        if candidate_pids is None:
            return {**base, "outcome": "ps_unavailable"}

        confirmed_pids = []
        for pid in candidate_pids:
            if self._require_cwd_match_override is False:
                # Test-only escape hatch (no `lsof` in the sandbox): argv +
                # ppid==1 alone, exactly as `find_candidate_pids` already
                # filtered.
                confirmed_pids.append(pid)
                continue
            cwd_result = cwd_matches_worktree(pid, worktree_path, runner=self.runner, lsof_bin=self.lsof_bin)
            if cwd_result is False:
                # Confirmed REFUTED: this pid's argv mentions the worktree
                # path but it is not actually running there -- the exact
                # over-match agent-estate#800 warned against. Never reaped.
                base["refused"].append(pid)
                continue
            if cwd_result is None and self._require_cwd_match_override is True:
                # Caller demanded cwd corroboration and it could not be
                # obtained -- fail closed rather than fall back to argv alone.
                base["refused"].append(pid)
                continue
            # cwd_result is True (confirmed), or None with no cwd
            # requirement in force (lsof unavailable; fall back to the
            # argv + ppid==1 signal already established).
            confirmed_pids.append(pid)

        if not candidate_pids:
            return {**base, "outcome": "no_candidates"}

        for pid in confirmed_pids:
            argv = ["bash", REAP_VERIFIED_SH, str(pid), worktree_path]
            try:
                result = subprocess.run(argv, capture_output=True, text=True)
            except Exception:
                base["failed"].append(pid)
                continue
            if result.returncode == 0:
                base["reaped"].append(pid)
            elif result.returncode == 1:
                base["refused"].append(pid)
            else:
                base["failed"].append(pid)

        # Named for what actually happened, not merely attempted -- a
        # caller reading `report["outcome"]` alone must be able to tell
        # "something was reaped" from "everything found was refused or
        # could not be confirmed dead" without inspecting the lists.
        if base["reaped"]:
            outcome = "reaped"
        elif base["failed"]:
            outcome = "reap_failed"
        elif base["refused"]:
            outcome = "refused"
        else:
            outcome = "no_candidates"
        return {**base, "outcome": outcome}
