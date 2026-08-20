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

agent-supervisor#374: everything above assumes a pane to poll. A
`claude-print`/`pi-rpc` lane id never matches `_parse_lane`'s
`<session>:<index>` shape -- there is no window to look up -- so every one
of those tasks fell straight into `unresolved` above and stayed `delivered`
forever; 101 of them were found stuck this way in one ledger. Those
transports genuinely have nothing to poll (`ClaudePrintAdapter`'s own
docstring: "there is no long-lived protocol session to resume mid-call"),
so this sweep's second half judges them on wall-clock dwell since the
row's own `updated_at` instead of a pane reading -- weaker evidence (an
absence, not an observation), so it is NEVER used to assert success on its
own: `stale_after` past due with no corroborating evidence (see #401
below) resolves to `failed` via `Ledger.fail_stale_delivery`, never to
`complete`. A task younger than `stale_after` is left `unresolved`, same
as before -- it may simply still be the one live turn that will call
`complete` itself.

agent-supervisor#414: #374's fix covers a no-pane task stuck at
`status='delivered'`, but a claude-print/pi-rpc worker that gets far enough
to call `cli.py accept` moves its own row to `status='accepted'` --
`Ledger.accept`, the ONE place that happens, is called from nowhere else
(see `Ledger.list_accepted_open_tasks`'s docstring). That status is outside
`list_delivered_open_tasks`'s `WHERE status='delivered'` filter, so the
row vanishes from every sweep above -- including #374's own -- the instant
it is accepted. Measured live: five claude-print dispatches sat at
`status=accepted` for 2+ hours, zero commits, zero comments, and this sweep
never looked at them again. The third pass below is `_sweep_nonobservable`
applied to `list_accepted_open_tasks()` instead of
`list_delivered_open_tasks()`, same `stale_after` dwell, terminating via
`Ledger.fail_stale_acceptance` instead of `fail_stale_delivery` -- the only
difference is which query feeds it and which terminal write is eligible,
because the source status differs.

agent-supervisor#401: "no signal arrived" and "the work did not happen" are
different claims, and the wording this sweep used to write conflated them
-- `results/ad275-fix275.md` read "failed, not completed" while its own
lane-log named PR #283, MERGED. Two changes here:

* Before either `_fail_unaccepted` or `_sweep_nonobservable` stamps a
  failure, `_lane_log_pr_url` checks the one piece of cheap evidence
  already sitting on disk at stamp time: does this lane's own transport
  log (`lane-logs/<task_id>.log`, written by `ClaudePrintAdapter.
  assign_task`) name a pull request it opened? If so, that settles it --
  `_complete_from_evidence` completes the task instead of failing it. This
  is deliberately the same check agent-supervisor#401's own acceptance
  script runs (`grep -qoE '.../pull/[0-9]+' lane-logs/$t.log`): a specimen
  this sweep would now still fail is a specimen that script would flag.
* When there is genuinely no evidence either way, the note text no longer
  asserts "failed, not completed" -- it says only what is actually known
  (no signal arrived) and says explicitly that this is not a claim the
  work itself failed. The ledger status written is still `failed` (it has
  to be: `one_open_task_per_lane` needs a terminal status to free the
  lane, and there is no weaker terminal status in this schema to reach
  for) -- but `_write_result` is now given `suffix=".reconcile"`, so this
  verdict lands at `<task_id>.reconcile.md`, never at `<task_id>.md`. That
  canonical slot stays free for the lane's own report, if one ever
  arrives late -- see `Ledger._write_result`'s own docstring for why
  writing there first was the actual "overwrite" the issue's title named:
  not a literal overwrite (this method's underlying write is
  immutable-once), but a late, genuine `complete()` call finding the slot
  already claimed and its content rejected as conflicting.
"""

from __future__ import annotations

import json
import re
import subprocess

from core import CLAIM_TASK_PREFIX

# Mirrors agent-supervisor#401's own acceptance script's grep exactly, so a
# specimen this finds is a specimen that script would also flag.
_PR_URL_RE = re.compile(r'https://github\.com/[^\s",]+/pull/[0-9]+')

DEFAULT_IDLE_AFTER_SECONDS = 300
# agent-supervisor#374: deliberately much longer than DEFAULT_IDLE_AFTER_SECONDS.
# The tmux path's dwell gates a POSITIVE observation (pane read free N seconds
# ago); this one gates the ABSENCE of any observation at all for a transport
# that never had a pane to poll, so it needs enough headroom that a long-running
# claude-print/pi-rpc turn is not mistaken for an abandoned one.
DEFAULT_STALE_AFTER_SECONDS = 3600


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

    def __init__(
        self,
        ledger,
        runner=None,
        lanes_bin="lanes.sh",
        idle_after=DEFAULT_IDLE_AFTER_SECONDS,
        stale_after=DEFAULT_STALE_AFTER_SECONDS,
        clock=None,
    ):
        self.ledger = ledger
        self.runner = runner or subprocess_runner
        self.lanes_bin = lanes_bin
        self.idle_after = idle_after
        self.stale_after = stale_after
        self.clock = clock or ledger.clock

    def _fetch_session_lanes(self, session):
        """One batched `lanes.sh --json <session>` call -> {index: row}."""
        payload = json.loads(self.runner([self.lanes_bin, "--json", session]))
        return {str(row["window"]): row for row in payload}

    def sweep(self):
        """Complete every ACCEPTED lane observably free long enough. Safe on
        a schedule.

        Returns a report dict: `completed` (task ids actually released as
        `complete`), `failed_unaccepted` (task ids terminated `failed`
        instead -- observably free long enough, but never accepted; see
        below), `unresolved` (left alone -- not observably free long
        enough, or the pane could not be read this run), and `errors` (a
        session's `lanes.sh --json` call itself failed, affecting every
        task that depended on it).

        agent-supervisor#193: "lane free, idle past the dwell" alone is not
        evidence a task's work ever started -- `at25-rev33`'s brief landed
        as noise the harness discarded, the lane went quiet exactly like a
        finished one, and this sweep used to certify it `complete` from that
        alone. `accepted_at` is now the gate: it is set only by
        `record_dispatch`'s own confirmation that its send actually landed
        (see that method's docstring), never by mere pane quiet. A task that
        is free-and-idle-enough WITH `accepted_at` set completes exactly as
        before; one free-and-idle-enough WITHOUT it is terminated `failed`
        instead -- never silently left `delivered` forever (that would just
        strand the lane), and never asserted `complete` (that would be the
        false record #193 filed against). Fail-closed cuts one way here: a
        real completion is never reported as accepted-but-wasn't, because
        `accepted_at` is written before this sweep ever runs, not inferred
        by it.
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

        # agent-supervisor#414: a second, independent unresolvable set,
        # fed from `list_accepted_open_tasks()` rather than
        # `list_delivered_open_tasks()` -- see this module's own docstring
        # for why a no-pane lane's row moves out of the first query the
        # instant its worker calls `accept`. Only the non-observable half
        # applies here: a tmux lane's `accepted_at` never moves `status`
        # off `delivered` (`record_dispatch`'s own flag, not `Ledger.
        # accept`), so a lane id that DOES parse as `<session>:<index>`
        # can never actually reach `status='accepted'` in the first place --
        # there is no per-session pane-observed half to add.
        unresolvable_accepted = [
            task for task in self.ledger.list_accepted_open_tasks() if _parse_lane(task["lane"]) is None
        ]

        report = {
            "completed": [],
            # agent-supervisor#401: subset of "completed" -- task ids that
            # would otherwise have been stamped failed, but a PR named in
            # their lane-log settled it first. Called out separately so a
            # human reading the report can tell "observed free" apart from
            # "cheap evidence recovered this one" at a glance.
            "completed_from_evidence": [],
            "failed_unaccepted": [],
            "failed_stale_delivery": [],
            "failed_stale_acceptance": [],
            "unresolved": [],
            "errors": [],
        }

        now = self.clock()

        for task in unresolvable:
            self._sweep_nonobservable(task, now=now, report=report)
        for task in unresolvable_accepted:
            self._sweep_nonobservable_accepted(task, now=now, report=report)
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
                if task.get("accepted_at") is None:
                    self._fail_unaccepted(task, session=session, idle_seconds=idle_seconds, now=now, report=report)
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

    def _lane_log_pr_url(self, task_id):
        """agent-supervisor#401: the cheap evidence check -- does this
        lane's own transport log already name a pull request it opened?
        `lane-logs/<task_id>.log` is written by `ClaudePrintAdapter.
        assign_task` (the detached `claude -p` transcript) and is available
        at stamp time, no network call needed. Absence (no file, no match)
        answers `None`, same fail-closed posture as the rest of this
        module -- it is evidence FOR completion, never evidence against it.
        """
        log_path = self.ledger.root / "lane-logs" / f"{task_id}.log"
        try:
            text = log_path.read_text(errors="replace")
        except OSError:
            return None
        match = _PR_URL_RE.search(text)
        return match.group(0) if match else None

    def _complete_from_evidence(self, task, *, pr_url, now, report):
        """agent-supervisor#401: the lane never signalled, but its own
        lane-log names a PR it opened -- cheap evidence available at stamp
        time that settles what neither `_fail_unaccepted` nor
        `_sweep_nonobservable` can observe directly. Completes the task
        (via the ordinary `complete()` path, so the note lands at the
        canonical `<task_id>.md`) instead of stamping a failure the
        evidence already contradicts.
        """
        note = (
            f"reconcile-lane-completions: {task['lane']} never signalled completion, "
            f"but its lane-log names {pr_url} as of {int(now)} -- cheap evidence "
            "available at stamp time settles this before a failure is stamped: "
            "auto-completed from lane-log PR evidence, not from an observed pane "
            "(agent-supervisor#401)"
        ).encode("utf-8")
        allow_claim = task["id"].startswith(CLAIM_TASK_PREFIX)
        try:
            self.ledger.complete(task["id"], note, pane_nonce=task["pane_nonce"], allow_claim=allow_claim)
        except Exception as error:
            report["errors"].append({"task": task["id"], "error": str(error)})
            return
        report["completed"].append(task["id"])
        report["completed_from_evidence"].append(task["id"])

    def _fail_unaccepted(self, task, *, session, idle_seconds, now, report):
        """agent-supervisor#193: observed free/idle with no `accepted_at` is
        not a completion -- see `sweep`'s own docstring. Terminated `failed`
        instead, loud about exactly why (#118's lesson, same as
        `_complete_observed`'s note), so a human reading the report or
        `watchdog.status` can tell this apart from an ordinary completion at
        a glance, not just from the ledger's status column.
        """
        pr_url = self._lane_log_pr_url(task["id"])
        if pr_url is not None:
            self._complete_from_evidence(task, pr_url=pr_url, now=now, report=report)
            return
        note = (
            f"reconcile-lane-completions: {task['lane']} observed free for "
            f"{int(idle_seconds)}s (>= {self.idle_after}s) as of {int(now)} -- "
            "never signalled completion AND no accepted_at recorded (this dispatch "
            "was never confirmed to land); no completion signal arrived, which is "
            "not the same claim as the work having failed -- terminated failed only "
            "to free the lane for redispatch (agent-supervisor#193, agent-supervisor#401)"
        ).encode("utf-8")
        allow_claim = task["id"].startswith(CLAIM_TASK_PREFIX)
        try:
            self.ledger.fail_unaccepted(task["id"], note, pane_nonce=task["pane_nonce"], allow_claim=allow_claim)
        except Exception as error:
            report["errors"].append({"task": task["id"], "error": str(error)})
            return
        report["failed_unaccepted"].append(task["id"])

    def _sweep_nonobservable(self, task, *, now, report):
        """agent-supervisor#374: a lane id `_parse_lane` cannot read as
        `<session>:<index>` (claude-print, pi-rpc) has no pane at all, so the
        tmux path above can never reach it. Resolve on wall-clock dwell
        since the row's own `updated_at` instead -- an absence of signal,
        not an observation, so on its own this only ever moves a stale row
        to `failed` (`fail_stale_delivery`), never asserts `complete`. A row
        younger than `stale_after` is left `unresolved`, exactly like a
        session `lanes.sh` could not read.

        agent-supervisor#401: "on its own" is the qualifier that changed --
        101 of the 133 wrong results this issue measured came through this
        exact path (every claude-print/pi-rpc lane routes here, and it is
        the ONLY sweep half with nothing but silence to go on). Before
        stamping failure from that silence, `_lane_log_pr_url` checks
        whether the lane's own transport log already names a PR it opened;
        if so, `_complete_from_evidence` completes the task from THAT
        instead -- a positive fact, not an absence.
        """
        updated_at = task.get("updated_at")
        if not isinstance(updated_at, (int, float)):
            report["unresolved"].append(task["id"])
            return
        age_seconds = now - updated_at
        if age_seconds < self.stale_after:
            report["unresolved"].append(task["id"])
            return
        pr_url = self._lane_log_pr_url(task["id"])
        if pr_url is not None:
            self._complete_from_evidence(task, pr_url=pr_url, now=now, report=report)
            return
        note = (
            f"reconcile-lane-completions: {task['lane']} has no observable pane "
            f"(non-tmux lane id) and has sat at status=delivered for "
            f"{int(age_seconds)}s (>= {self.stale_after}s) as of {int(now)} -- "
            "no completion signal arrived, and this transport has no pane to poll "
            "for one; that is not the same claim as the work having failed, only "
            "that nothing was observed -- terminated failed only to free the lane "
            "for redispatch (agent-supervisor#374, agent-supervisor#401)"
        ).encode("utf-8")
        allow_claim = task["id"].startswith(CLAIM_TASK_PREFIX)
        try:
            self.ledger.fail_stale_delivery(task["id"], note, pane_nonce=task["pane_nonce"], allow_claim=allow_claim)
        except Exception as error:
            report["errors"].append({"task": task["id"], "error": str(error)})
            return
        report["failed_stale_delivery"].append(task["id"])

    def _sweep_nonobservable_accepted(self, task, *, now, report):
        """agent-supervisor#414: `_sweep_nonobservable`'s counterpart for a
        no-pane task already moved to `status='accepted'` -- same dwell
        gate against `updated_at`, same `stale_after` threshold, but the
        source status this method's caller has already filtered for is
        `accepted`, not `delivered`, so the terminal write is
        `fail_stale_acceptance`, the one Ledger method eligible for it.
        """
        updated_at = task.get("updated_at")
        if not isinstance(updated_at, (int, float)):
            report["unresolved"].append(task["id"])
            return
        age_seconds = now - updated_at
        if age_seconds < self.stale_after:
            report["unresolved"].append(task["id"])
            return
        note = (
            f"reconcile-lane-completions: {task['lane']} has no observable pane "
            f"(non-tmux lane id) and has sat at status=accepted for "
            f"{int(age_seconds)}s (>= {self.stale_after}s) as of {int(now)} -- "
            "accepted but never signalled completion and this transport has no "
            "pane to poll -- failed, not completed (agent-supervisor#414)"
        ).encode("utf-8")
        allow_claim = task["id"].startswith(CLAIM_TASK_PREFIX)
        try:
            self.ledger.fail_stale_acceptance(
                task["id"], note, pane_nonce=task["pane_nonce"], allow_claim=allow_claim
            )
        except Exception as error:
            report["errors"].append({"task": task["id"], "error": str(error)})
            return
        report["failed_stale_acceptance"].append(task["id"])
