"""Is the supervisor loop asleep with a wakeup pending, or is it actually down?

Split per `docs/SPEC.md` §15 like `recycle.py` and `watchdog_notify.py`:
`decide_liveness` is pure and unit-tested without a real transcript; `main` is
the thin actuator that finds the newest transcript and exits with a code the
shell watchdog can branch on.

Why this exists, measured rather than argued. A dynamic `/loop` sleeps by
scheduling its own wakeup, so between ticks its pane is idle -- identical to a
loop that has died. On 2026-08-11 the supervisor's transcript showed eleven
`ScheduleWakeup` calls: one `stop=True` and the rest `delaySeconds=3600`
("maximum sleep keeps the loop alive"). It had crashed zero times. The shell
watchdog was nonetheless restarting it every cooldown, because it counted nine
open issues the supervisor had correctly judged gated on Jon. The watchdog was
interrupting the loop it existed to protect.

This reads the last `ScheduleWakeup` out of the live transcript and computes
when the loop is genuinely due. It observes rather than trusting a cooperating
writer -- the same reason completion is never inferred from echoed prompt text.
"""

from __future__ import annotations

import glob
import json
import os
import sys
import time
from dataclasses import dataclass
from datetime import datetime


DEFAULT_PROJECT_DIR = os.path.expanduser(
    "~/.claude/projects/-Users-jon-source-repos-Personal-agent-dotfiles"
)
DEFAULT_GRACE_SECONDS = 300


@dataclass(frozen=True)
class Liveness:
    """`asleep` is the only state that means "leave the loop alone"."""

    asleep: bool
    reason: str


def decide_liveness(
    scheduled_at: str | None,
    delay_seconds: float | None,
    stopped: bool,
    now: float,
    grace_seconds: float = DEFAULT_GRACE_SECONDS,
) -> Liveness:
    """Pure decision. No filesystem, no clock of its own.

    Fails toward *not* asleep in every ambiguous case: a watchdog that wrongly
    restarts a healthy loop costs one redundant turn, while one that wrongly
    leaves a dead loop alone costs the whole night. That asymmetry is why the
    unknown branches all return asleep=False.
    """
    if stopped:
        return Liveness(False, "loop stopped itself")
    if scheduled_at is None:
        return Liveness(False, "no ScheduleWakeup found in transcript")
    if not delay_seconds:
        return Liveness(False, "wakeup recorded with no delay")

    try:
        started = datetime.fromisoformat(scheduled_at.replace("Z", "+00:00")).timestamp()
    except ValueError:
        return Liveness(False, f"unparseable timestamp {scheduled_at!r}")

    remaining = started + float(delay_seconds) - now
    if remaining > 0:
        return Liveness(True, f"asleep, wakes in {int(remaining)}s")

    overdue = int(-remaining)
    if overdue < grace_seconds:
        return Liveness(True, f"wakeup due {overdue}s ago, within {int(grace_seconds)}s grace")
    return Liveness(False, f"wakeup overdue by {overdue}s")


def last_wakeup(transcript: str) -> tuple[str | None, float | None, bool]:
    """Return (timestamp, delaySeconds, stopped) for the final ScheduleWakeup."""
    found: tuple[str | None, float | None, bool] = (None, None, False)
    with open(transcript, errors="replace") as handle:
        for line in handle:
            if "ScheduleWakeup" not in line:
                continue
            try:
                record = json.loads(line)
            except ValueError:
                continue
            for block in record.get("message", {}).get("content") or []:
                if not isinstance(block, dict):
                    continue
                if block.get("type") != "tool_use" or block.get("name") != "ScheduleWakeup":
                    continue
                params = block.get("input") or {}
                found = (
                    record.get("timestamp"),
                    params.get("delaySeconds"),
                    bool(params.get("stop")),
                )
    return found


def latest_wakeup_across(project_dir: str) -> tuple[str | None, float | None, bool]:
    """Find the most recent ScheduleWakeup in ANY transcript in the project.

    Every lane -- supervisor and workers alike -- runs with the same working
    directory, so they all write into one Claude project directory. Picking
    the newest *file* therefore almost always picks a busy worker, and workers
    never schedule wakeups.

    Measured 2026-08-11: the three newest transcripts had `ScheduleWakeup=0`
    while the supervisor's older one held eleven. sleepcheck reported "no
    ScheduleWakeup found", the watchdog read that as a dead loop, restarted a
    healthy supervisor three times, hit its escalation cap and paged Jon at
    06:37Z for a problem that did not exist.

    Only the loop schedules wakeups, so the most recent wakeup across all
    transcripts IS the loop's state, whichever file it happens to live in.
    """
    latest: tuple[str | None, float | None, bool] = (None, None, False)
    # sorted() so iteration order is deterministic: without it the result
    # depends on filesystem order, and a test that pins ordering behaviour
    # can pass by luck while the comparison below is broken.
    for path in sorted(glob.glob(os.path.join(project_dir, "*.jsonl"))):
        try:
            stamp, delay, stopped = last_wakeup(path)
        except OSError:
            continue
        if stamp is None:
            continue
        if latest[0] is None or stamp > latest[0]:
            latest = (stamp, delay, stopped)
    return latest


def main(argv: list[str]) -> int:
    grace = float(argv[1]) if len(argv) > 1 else DEFAULT_GRACE_SECONDS
    project_dir = os.environ.get("SLEEPCHECK_DIR") or DEFAULT_PROJECT_DIR

    scheduled_at, delay, stopped = latest_wakeup_across(project_dir)
    verdict = decide_liveness(scheduled_at, delay, stopped, time.time(), grace)
    print(verdict.reason)
    return 0 if verdict.asleep else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv))
