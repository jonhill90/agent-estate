"""Reap a lane's worktree once its own task goes terminal (agent-estate#804).

`reconcile_lane_completions.py` already determines that a lane's task has
gone terminal and already reaps whatever background descendants that lane
left running (`lane_orphan_reap.py`, agent-estate#800/#802). It never
touches the *worktree* itself -- every dispatch creates one
(`worktree.sh new`), and nothing removes it: `git worktree prune` only
drops registrations whose directory is already gone, and `worktree.sh gc`
refuses anything outside `.worktrees/` by default. Before agent-estate#821,
that was NOT where a dispatched lane worktree actually lived (`${WORKTREE_ROOT:-
${TMPDIR:-/tmp}}/ad-<slug>-<pid>`, invisible to `gc`'s default scope unless
opted in via `WORKTREE_GC_EXTRA_ROOTS`); #821 moved `new`'s own default to
create every NEW worktree inside `$REPO/.worktrees/` in the first place, so
this sweep and `gc`'s default scope now agree without an opt-in for
anything dispatched after that change. Worktrees created before #821 landed
are unaffected and still need `WORKTREE_GC_EXTRA_ROOTS` (a separate reaper,
not something this sweep runs on a schedule). That accumulation caused
#800's six-day `advance-live` stall: `worktree-guard-audit.sh` is
O(worktrees), and it took 198 registered worktrees (168 abandoned) pushing
the audit past `advance-live`'s 150s window before anyone noticed.

## Measured accrual rate (agent-estate#804's own open question)

843 tasks recorded a non-empty `worktree_path` over the 14.2 days spanned
by the live ledger's `tasks.created_at` column (2026-08-14 through
2026-08-29) -- roughly 59/day, but with real daily variance (11 on a quiet
day, 147 on 2026-08-28 alone). 842 of those 843 rows are already terminal
(`complete`/`failed`/`cancelled`); only one is still `accepted`. That
means almost every one of those 843 directories is *already* eligible for
this sweep to have reaped by now -- this is not a slow leak that a human
would notice building over weeks, it is the ordinary rate of one lane
finishing its task.

At that rate, the 84-worktree gap #800's own incident measured (198 minus
the 114 a manual cleanup left behind) regenerates in under a day and a
half. A sweep that only ran once a day, or once per `director-loop.sh`
restart, would let the count climb most of the way back to the 198 that
caused the stall before its next tick ever ran. That is the basis for
running this on every terminal-task tick alongside `#802`'s process reap,
not as a separate periodic job.

## Immediate, not deferred -- and why that call is safe here

`#804` left "immediate vs. deferred" an open question, noting the cost of
being wrong is higher for a worktree (uncommitted work) than for a process
(a stray audit). That is true in the abstract, but it does not argue for a
grace period here, because THIS module never makes the removal decision
itself -- every check that would make removing a worktree unsafe (dirty
tree, unmerged branch, a live tmux pane or process actually inside it,
even a detached HEAD with an unreachable commit) is `worktree.sh`'s own
`gc`/`done` guard chain, reused verbatim through the `reap` subcommand this
issue adds (see that file's own usage comment). Those guards already fail
CLOSED on any doubt -- an unreadable git state, an `lsof` that can't be
asked, a merge-base that can't be computed, all refuse rather than assume
safe. A grace period would only delay reaping the SAFE cases (already
merged/pushed, already idle, already clean) that these guards let through
today; it would not make an unsafe case (dirty, unmerged, live) any safer
to remove later, because the exact same guards would still refuse it then.
Deferring adds a second clock to reason about for zero additional safety
-- the guard chain is already the safety margin, precisely the way #802's
own immediate process reap already leans on ITS OWN gates (task terminal,
lane not live, worktree clean) rather than a dwell timer. If a future
incident shows the terminality signal itself is untrustworthy (a task
marked terminal while its lane keeps writing), that is a defect in
`reconcile_lane_completions.py`'s own terminality determination -- fixed
there, not papered over with a delay here.

## What this module does NOT do

It never touches `worktree.sh`'s own guard functions, `lane_orphan_reap.py`'s
process reaper, or `worktree-guard-audit.sh`'s polling -- all three are
frozen inputs to this change, not things it revises. `reap_task_worktree`
below is a thin caller: three cheap ledger/filesystem checks this module
owns (task terminal, worktree path present, lane not live -- the identical
three-check shape `LaneOrphanReaper.reap_task_orphans` already uses, for
the identical reason: catching the redispatched-before-the-sweep-ran case
without shelling out at all), then one call to `worktree.sh reap <path>
[base]`, whose own exit code is the only source of truth for whether the
removal actually happened. A caller wanting to know WHY a removal was
refused reads this module's own `stderr`-sourced `reason` field, but never
substitutes it for the exit code.
"""

from __future__ import annotations

import subprocess
from pathlib import Path

from core_constants import TERMINAL_STATUSES
from core_lane_relation import normalize_worktree_path

WORKTREE_SH = str(Path(__file__).resolve().parent / "worktree.sh")

DEFAULT_BASE = "origin/main"


def default_runner(argv):
    """`subprocess.run` wrapper matching `reconcile_lane_completions.
    subprocess_runner`'s and `lane_orphan_reap.default_runner`'s shape --
    stdout text, raises `CalledProcessError` (carrying `.stderr`) on a
    nonzero exit."""
    return subprocess.run(argv, check=True, capture_output=True, text=True).stdout


class LaneWorktreeReaper:
    """Reap the worktree a lane's task leaves behind, once that task is
    confirmed terminal, the lane is confirmed not otherwise occupied, and
    `worktree.sh reap`'s own guard chain agrees the tree is safe to remove.
    See this module's own docstring for the full rationale."""

    def __init__(self, ledger, *, runner=None, worktree_bin=None, base=DEFAULT_BASE):
        self.ledger = ledger
        self.runner = runner or default_runner
        self.worktree_bin = worktree_bin or WORKTREE_SH
        self.base = base

    def _lane_is_live(self, lane):
        return self.ledger.get_open_task_for_lane(lane) is not None

    def reap_task_worktree(self, task, *, merged_prs_file=None):
        """Reap `task`'s own worktree. Never raises -- every refusal and
        every outcome is returned in the report dict, exactly one of:

        `{"outcome": "not_terminal" | "no_worktree_path" | "lane_live" |
          "worktree_missing" | "reaped" | "refused" | "error",
          "task": task_id, "lane": lane, "worktree_path": path,
          "reason": <worktree.sh's own stderr, present on "refused"/"error">}`

        `reason` is omitted on "reaped" and on every gate this module's own
        three checks refuse before ever shelling out -- there is nothing
        `worktree.sh` said in those cases because it was never called.

        `merged_prs_file` (agent-estate#847): a path a caller already
        fetched the MERGED-PR set into (`worktree.sh fetch-merged-prs`,
        called once for a whole sweep of many `reap_task_worktree` calls)
        rather than paying `worktree.sh reap`'s own `_gc_fetch_merged_prs`
        network round trip on every single call. `None` (the default) keeps
        every existing caller's behaviour exactly as before -- `reap` fetches
        its own snapshot fresh, unchanged.
        """
        base = {"task": task.get("id"), "lane": task.get("lane")}
        worktree_path = task.get("worktree_path") or ""
        base["worktree_path"] = worktree_path

        if task.get("status") not in TERMINAL_STATUSES:
            return {**base, "outcome": "not_terminal"}
        if not normalize_worktree_path(worktree_path):
            return {**base, "outcome": "no_worktree_path"}
        if self._lane_is_live(task.get("lane")):
            return {**base, "outcome": "lane_live"}
        if not Path(worktree_path).is_dir():
            # Already gone -- `git worktree prune` or a prior sweep beat
            # this one to it. Not a failure; nothing left to reap.
            return {**base, "outcome": "worktree_missing"}

        argv = ["bash", self.worktree_bin, "reap"]
        if merged_prs_file:
            argv += ["--merged-file", merged_prs_file]
        argv += [worktree_path, self.base]
        try:
            self.runner(argv)
        except subprocess.CalledProcessError as error:
            return {**base, "outcome": "refused", "reason": (error.stderr or "").strip()}
        except Exception as error:
            return {**base, "outcome": "error", "error": str(error)}
        return {**base, "outcome": "reaped"}
