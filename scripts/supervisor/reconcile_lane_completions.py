"""Reconciliation sweep for lanes a worker finished without signalling.

agent-supervisor#155: five, then six, lanes in one night finished their
work -- opened a PR, posted a review verdict -- and then went idle without
ever running `lane-done.sh`'s `tmux wait-for -S <channel>`. `lanes.sh`
correctly read the pane `free`; the ledger correctly kept the task
`delivered`, because nothing had told it otherwise. `dispatch.sh` then
correctly refused to offer that lane -- the ledger is the record -- leaving
capacity idle until a human ran `cli.py record-completion` by hand. Measured
end to end (see the PR body): every one of the six briefs was missing the
"Final shell action: tmux wait-for -S ..." instruction `loop-tick.md`
describes as the mechanism's load-bearing step. That step lived in prose,
not in `dispatch.sh`'s own auto-appended brief footer, and was never
consistently added -- a completion mechanism that depends on the lane (or
the operator, on its behalf) remembering to announce itself is exactly the
shape #133/#142 already replaced for `source_tasks`: this sweep is that same
fix applied to lane occupancy instead of issue state.

Design mirrors `reconcile_sources.py` deliberately:

* The observed facts, not an announcement. `Ledger.list_delivered_open_tasks`
  is the durable fact ("this task was dispatched and never completed");
  `lanes.sh --json <session>` is the observed fact ("this lane's pane is
  free, right now, and has been for N seconds") -- read-only, exactly what
  `dispatch.sh` already reads to decide whether to offer a lane, and exactly
  what `digest.sh`'s existing delivered-vs-pane reconciliation section
  already computes for a human to act on by hand (this sweep is that same
  join, wired to act instead of only to report).
* Batched per session, not per lane: one `lanes.sh --json <session>` call
  answers for every delivered task whose lane lives in that session, the
  same batching argument `reconcile_sources.py` makes for `gh`.
* Fail-closed / unknown stays unknown: a lane whose session cannot be read,
  whose window is missing from `lanes.sh`'s answer, whose pane is anything
  other than `free`, or that has been free for less than `idle_after`
  seconds is left untouched. Marking a lane complete because it merely
  *might* be done is worse than leaving one idle -- the whitelist discipline
  `lanes.sh` itself documents ("`unknown` means not offered, not broken").
* The `idle_after` dwell (default 300s, matching `digest.sh`'s
  `DIGEST_RECONCILE_IDLE_AFTER`) exists for the exact danger `lane-done.sh`'s
  own header names: "idle" also means "between tool calls" or "blocked on an
  approval prompt holding an unposted verdict", and reclaiming on idle alone
  once nearly destroyed a verdict (#102). A lane that has read `free`
  continuously for five minutes is not a pane mid-repaint.
* Loud, not silent (#118's lesson, acceptance #4): every lane this sweep
  completes is named in its report, and `record_completion`'s own note text
  says exactly why -- so a human reading `watchdog.status` can tell "the
  sweep is doing its job" from "nothing happened this tick" without opening
  a pane.
* Idempotent: once a task is `complete`, `list_delivered_open_tasks` no
  longer returns it, so a second sweep with nothing new to find performs no
  writes and reports it as such.

This does NOT replace `lane-done.sh`. A worker whose brief DOES carry the
`wait-for` instruction still gets the fast, `dwell`-free release the moment
its channel fires -- this sweep is the backstop for every dispatch that
mechanism's own fragility (a brief that never got the line, a background
waiter lost across a supervisor `/clear`) leaves stranded, on a bounded
delay instead of "until a human notices".
"""

from __future__ import annotations

import json
import subprocess

from core import CLAIM_TASK_PREFIX

DEFAULT_IDLE_AFTER_SECONDS = 300


def subprocess_runner(command):
    return subprocess.run(command, check=True, capture_output=True, text=True).stdout


def _parse_lane(lane):
    """`<session>:<index>` -> `(session, index)`, or `None` if it doesn't parse.

    Mirrors the shape `dispatch.sh`/`lanes.sh` mint every lane id in
    (`core.py`'s `lane_relation` parses the same shape for the same reason).
    A lane id that does not parse this way was never minted by this system
    -- left unresolved rather than guessed at, same fail-closed posture
    `reconcile_sources.py` takes for an unparseable `source_url`.
    """
    if not isinstance(lane, str):
        return None
    session, sep, index = lane.rpartition(":")
    if not sep or not session or not index.isdigit():
        return None
    return session, index


class LaneCompletionReconciler:
    """Sweep every open `delivered` task forward from OBSERVED pane state."""

    def __init__(self, ledger, runner=None, lanes_bin="lanes.sh", idle_after=DEFAULT_IDLE_AFTER_SECONDS, clock=None):
        self.ledger = ledger
        self.runner = runner or subprocess_runner
        self.lanes_bin = lanes_bin
        self.idle_after = idle_after
        self.clock = clock or ledger.clock

    def _fetch_session_lanes(self, session):
        """One batched `lanes.sh --json <session>` call -> {index: row}."""
        payload = json.loads(self.runner([self.lanes_bin, "--json", session]))
        return {str(row["window"]): row for row in payload}

    def sweep(self):
        """Complete every lane observably free long enough. Safe on a schedule.

        Returns a report dict: `completed` (task ids actually released),
        `unresolved` (left alone -- not observably free long enough, or the
        pane could not be read this run), and `errors` (a session's
        `lanes.sh --json` call itself failed, affecting every task that
        depended on it).
        """
        tasks = self.ledger.list_delivered_open_tasks()

        by_session = {}
        unresolvable = []
        for task in tasks:
            parsed = _parse_lane(task["lane"])
            if parsed is None:
                unresolvable.append(task)
                continue
            session, index = parsed
            by_session.setdefault(session, []).append((task, index))

        report = {"completed": [], "unresolved": [], "errors": []}

        for task in unresolvable:
            report["unresolved"].append(task["id"])

        now = self.clock()
        for session, entries in by_session.items():
            try:
                windows = self._fetch_session_lanes(session)
            except Exception as error:
                report["errors"].append({"session": session, "error": str(error)})
                for task, _index in entries:
                    report["unresolved"].append(task["id"])
                continue

            for task, index in entries:
                window = windows.get(index)
                if window is None or window.get("state") != "free":
                    report["unresolved"].append(task["id"])
                    continue
                idle_seconds = window.get("idle_seconds")
                if not isinstance(idle_seconds, (int, float)) or idle_seconds < self.idle_after:
                    report["unresolved"].append(task["id"])
                    continue
                self._complete_observed(task, session=session, idle_seconds=idle_seconds, now=now, report=report)

        return report

    def _complete_observed(self, task, *, session, idle_seconds, now, report):
        note = (
            f"reconcile-lane-completions: {task['lane']} observed free for "
            f"{int(idle_seconds)}s (>= {self.idle_after}s) as of {int(now)} -- "
            "never signalled completion; auto-completed from observed pane state"
        ).encode("utf-8")
        allow_claim = task["id"].startswith(CLAIM_TASK_PREFIX)
        try:
            self.ledger.complete(task["id"], note, pane_nonce=task["pane_nonce"], allow_claim=allow_claim)
        except Exception as error:
            report["errors"].append({"task": task["id"], "error": str(error)})
            return
        report["completed"].append(task["id"])
