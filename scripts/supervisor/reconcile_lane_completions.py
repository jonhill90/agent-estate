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

agent-supervisor#488: "no signal arrived" is not "the process has exited",
either. `_sweep_nonobservable`/`_sweep_nonobservable_accepted` judge a
claude-print/pi-rpc row on wall-clock dwell since `updated_at` alone --
measured live, that stamped `as473-as473x` `failed` (`completed_at`
recorded) while `ps` showed its `claude -p ... [Hill90 task as473-as473x]`
subprocess still alive and accumulating CPU roughly an hour past that
timestamp. A supervisor trusting that stamp would redispatch the same
issue to a second lane, or reclaim the first lane's worktree out from under
a still-running process -- exactly the two outcomes agent-supervisor#401's
own `_complete_from_evidence` exists to avoid on the completion side. The
fix: before either method writes a failure, `_lane_log_pid` reads the pid
`ClaudePrintAdapter.assign_task` already wrote to this same lane-log at
dispatch time (`--- dispatched detached: task=... lane=... pid=... ---`)
and `core.pid_is_alive` checks it with `os.kill(pid, 0)`. A pid that
answers alive blocks the failure stamp outright (`unresolved`, named in
`report["liveness_alive"]` so it reads distinctly from a merely-too-young
row). A row whose log never named a pid at all (missing log, unreadable,
no dispatch line) is liveness-INDETERMINATE, not assumed alive or dead --
also left `unresolved`, named separately in `report["liveness_indeterminate"]`
so the two reasons are never conflated in what gets reported. Only a row
with a demonstrably dead pid (`pid_is_alive` returns `False`) reaches the
`_lane_log_pr_url` evidence check and, failing that, the failure stamp
below -- unchanged from before this fix in that case.

agent-supervisor#779 (#774 half C): a task's recorded lane names a tmux
SESSION that no longer exists -- renamed away (#739, #752, and this issue
are three renames in one session's history; "the next rename" is not
hypothetical) or destroyed outright. Before this fix, `_fetch_session_lanes`
raising for that session left every task in it `unresolved` forever: the
stored session name never changes on its own, and nothing ever revisited
the row. #774's own three stuck reviewer tasks were exactly this.

`_resolve_renamed_session` is the fallback, tried only when the ordinary
per-session `lanes.sh --json` call itself fails. It resolves the SAME lane
under its CURRENT session name using the one identity a `tmux
rename-session` does not touch: the pane_id the ledger recorded for this
lane at registration time (`lanes.pane_id`, confirmed stable across a
rename by #739's own direct test). Every step is live evidence, never a
guess: the pane_id comes from the ledger's own row, "does it still answer"
is checked with a live `tmux display-message` against that exact pane_id,
the session it now names is checked against `tmux list-sessions`'s own
live output before being trusted, and the window at the resolved index is
then read the ordinary way (`lanes.sh --json` on THAT session) so the
state/idle_seconds evidence downstream is identical either way -- this
changes HOW the pane is found for the failure case only, never whether
idleness alone is sufficient to complete it (unchanged from every fix
above). Any step that cannot confirm a single live location -- the pane_id
column empty, the pane gone, `tmux list-sessions` not naming the resolved
session, the resolved window absent from that session's own answer --
returns `None` and the task stays `unresolved`, the same fail-closed
posture as "old name, gone forever". This never touches the 931 historical
`agent-supervisor:%` rows already `complete` (#728): only tasks still
`delivered`/`accepted` ever reach `sweep()`'s per-session loop at all.

agent-supervisor#774 half B, rescoped after #781 (#779/half C) shipped:
`_resolve_renamed_session` above resolves a lane whose SESSION was renamed
-- it needs a live pane to answer `tmux display-message`. A pane that is
genuinely, permanently gone (killed, crashed, or torn down before
`lane-done.sh` ran) answers nothing under ANY name, so that fallback
returns `None` for it too -- reproduced live, not assumed: see
`tests/supervisor/test_reconcile_lane_completions.py`'s
`ReviewerTaskWithGenuinelyDeadPaneTest`, which actually kills a tmux
session (not renames it) under isolation and confirms the task stays
`delivered` forever without this fix. That is structurally the SAME gap
agent-supervisor#401 already closed for AUTHOR tasks (below): a lane that
finished and left real evidence behind -- a posted `Verdict:`/
`Review-Lane:` comment, a merged PR -- has no live pane to observe under
any name. `#401`'s fix reads a lane-log for a PR an AUTHOR task opened; a
REVIEWER task never opens anything, so `_reviewer_task_merge_evidence`
reads the review's own evidence instead: the task's own `source_tasks` row
naming the PR it reviewed (`source_kind='pull'`, `is_review=1`), that PR's
operative verdict resolved through `verdict.py`'s own tested
`GithubReviewVerdictSource` and attributed to precisely this task's lane,
and the PR's live, independently-confirmed `MERGED` state via `gh pr
view`. Tried in the SAME except-branch as `_resolve_renamed_session`,
after it (a live pane, when one can still be found, is strictly better
evidence than a review's paper trail) and before falling back to
`unresolved` -- never in place of the session-read error already reported.

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

from core import CLAIM_TASK_PREFIX, pid_is_alive
from verdict import GithubReviewVerdictSource

# Mirrors agent-supervisor#401's own acceptance script's grep exactly, so a
# specimen this finds is a specimen that script would also flag.
_PR_URL_RE = re.compile(r'https://github\.com/[^\s",]+/pull/[0-9]+')

# agent-supervisor#488: mirrors the exact line `ClaudePrintAdapter.
# assign_task` (`adapter.py`) writes to `lane-logs/<task_id>.log` right
# after `run_detached` returns -- the only place this sweep can learn the
# pid of the subprocess it is about to judge.
_DISPATCHED_PID_RE = re.compile(r"dispatched detached: task=\S+ lane=\S+ pid=(\d+)")

# agent-supervisor#774 half B: `source_tasks.source_url` for a `--pr`/
# `--reviews-pr`-scoped dispatch (`source_kind='pull'`) is minted by
# `cli_dispatch_record.py` as exactly this shape -- see that module's own
# `source_url = f"https://github.com/{github}/pull/{pr}"` line. Anchored
# full-match (not `_PR_URL_RE`'s bare substring search above), because this
# is read back FROM a column this same system wrote, not scraped out of
# free-form prose.
_PULL_SOURCE_URL_RE = re.compile(r"https://github\.com/(?P<repo>[^/\s]+/[^/\s]+)/pull/(?P<number>[0-9]+)")

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
        gh_bin="gh",
        idle_after=DEFAULT_IDLE_AFTER_SECONDS,
        stale_after=DEFAULT_STALE_AFTER_SECONDS,
        clock=None,
        orphan_reaper=None,
    ):
        self.ledger = ledger
        self.runner = runner or subprocess_runner
        self.lanes_bin = lanes_bin
        # agent-supervisor#774 half B: same `gh_bin` convention
        # `reconcile_sources.py`'s `SourceTaskReconciler` already uses --
        # one env-configurable binary name, not a second hardcoded "gh".
        self.gh_bin = gh_bin
        self.idle_after = idle_after
        self.stale_after = stale_after
        self.clock = clock or ledger.clock
        # agent-estate#800: this sweep already determines a lane's task has
        # gone terminal; it never looked at what that lane left running.
        # `None` by default (every existing caller/test that does not pass
        # this keeps behaving exactly as before -- reaping OS processes is
        # a materially different hazard than writing a ledger row, kept
        # opt-in here and wired explicitly at the one real caller, `cli.py`
        # reconcile-lane-completions). Injected here (rather than
        # constructed fresh per sweep) so a caller can share one instance's
        # `runner`/binaries and so tests can substitute a fake without
        # touching this class's own constructor signature further.
        self.orphan_reaper = orphan_reaper

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
            # agent-supervisor#692: subset of failed_stale_delivery/
            # failed_stale_acceptance -- task ids terminated on a CONFIRMED
            # dead pid (see `_pid_confirmed_dead`) rather than on having
            # merely outlived `stale_after` with no corroborating evidence.
            # Named separately so a human (or watchdog.log) can tell "this
            # one died and we know it" from "this one just went quiet long
            # enough" without reading the note text -- the same reason
            # #488's liveness_alive/liveness_indeterminate are split out
            # below.
            "died_without_completing": [],
            # agent-supervisor#488: subsets of "unresolved" -- task ids left
            # alone specifically because a liveness check blocked a failure
            # stamp, named separately so a human (or watchdog.log) can tell
            # "this one is too young" apart from "this one is provably still
            # running" or "this one could not be checked at all" without
            # reading the note text.
            "liveness_alive": [],
            "liveness_indeterminate": [],
            "errors": [],
            # agent-estate#800: one entry per task this sweep just moved to
            # a terminal status, naming what `self.orphan_reaper` (if any)
            # found and did with that task's worktree -- see
            # `_reap_orphans_for_this_sweep`'s own docstring. Always
            # present so a caller can tell "reaping ran and found nothing"
            # from "reaping was never wired" without inspecting `self`.
            "orphans": [],
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
                # agent-supervisor#779 (#774 half C): the recorded session
                # itself is unreadable -- try the SAME lane under its
                # current name before giving up. See `_resolve_renamed_
                # session`'s own docstring for what "evidence-based" means
                # here; `None` means it could not confirm a single live
                # location, and the task is left unresolved exactly as
                # before this fallback existed.
                for task, _index in entries:
                    resolved = self._resolve_renamed_session(task, stale_session=session)
                    if resolved is not None:
                        resolved_session, window = resolved
                        self._evaluate_window(
                            task, window, session=resolved_session, now=now, report=report, via_fallback=True
                        )
                        continue
                    # agent-supervisor#774 half B: no live pane could be
                    # found under any name -- the pane is genuinely gone,
                    # not merely renamed. A REVIEWER task stuck in exactly
                    # this shape can still be settled from real merge/
                    # verdict evidence instead (see
                    # `_reviewer_task_merge_evidence`'s own docstring),
                    # tried before falling back to unresolved.
                    pr_url = self._reviewer_task_merge_evidence(task)
                    if pr_url is not None:
                        self._complete_from_evidence(
                            task,
                            pr_url=pr_url,
                            now=now,
                            report=report,
                            evidence_description=(
                                f"its own review of {pr_url} was approved under this exact "
                                "lane's Review-Lane: trailer and that PR is confirmed MERGED"
                            ),
                        )
                        continue
                    report["unresolved"].append(task["id"])
                continue

            for task, index in entries:
                window = windows.get(index)
                self._evaluate_window(task, window, session=session, now=now, report=report)

        self._reap_orphans_for_this_sweep(report)
        return report

    def _reap_orphans_for_this_sweep(self, report):
        """agent-estate#800: for every task THIS sweep just moved to a
        terminal status (`completed`, `failed_unaccepted`,
        `failed_stale_delivery`, `failed_stale_acceptance` -- the four
        lists a task id can land in exactly once per sweep), ask
        `self.orphan_reaper` (if one was given) to reap whatever background
        descendants that task's worktree left running.

        A no-op when `self.orphan_reaper` is `None` (the default -- see
        this class's own `__init__` docstring for why reaping stays
        opt-in). Never lets a reaper's own exception break this sweep's
        ledger writes, which have already committed by the time this runs
        -- a reap failure is recorded in `report["orphans"]` as an error
        entry, not raised.
        """
        if self.orphan_reaper is None:
            return
        terminal_task_ids = (
            report["completed"]
            + report["failed_unaccepted"]
            + report["failed_stale_delivery"]
            + report["failed_stale_acceptance"]
        )
        for task_id in terminal_task_ids:
            task = self.ledger.get_task(task_id)
            if task is None:
                continue
            try:
                result = self.orphan_reaper.reap_task_orphans(task)
            except Exception as error:
                result = {"task": task_id, "outcome": "error", "error": str(error)}
            report["orphans"].append(result)

    def _evaluate_window(self, task, window, *, session, now, report, via_fallback=False):
        """The state/idle/accepted evidence check shared by the ordinary
        per-session lookup and `_resolve_renamed_session`'s fallback -- the
        two differ only in HOW `window` was found, never in what counts as
        enough evidence to act on it (#774 half C's own constraint)."""
        if window is None or window.get("state") != "free":
            report["unresolved"].append(task["id"])
            return
        idle_seconds = window.get("idle_seconds")
        if not isinstance(idle_seconds, (int, float)) or idle_seconds < self.idle_after:
            report["unresolved"].append(task["id"])
            return
        if task.get("accepted_at") is None:
            self._fail_unaccepted(
                task, session=session, idle_seconds=idle_seconds, now=now, report=report, via_fallback=via_fallback
            )
            return
        self._complete_observed(
            task, session=session, idle_seconds=idle_seconds, now=now, report=report, via_fallback=via_fallback
        )

    def _resolve_renamed_session(self, task, *, stale_session):
        """agent-supervisor#779 (#774 half C): find this task's lane under
        its CURRENT session name, using the pane_id the ledger recorded for
        it at registration time -- the one identity a `tmux rename-session`
        does not touch (#739). See this module's top-level docstring for
        the full rationale.

        Returns `(resolved_session, window)` -- `window` in the exact shape
        `_fetch_session_lanes` already produces, so `_evaluate_window` reads
        it identically either way -- or `None` if any step below cannot
        confirm a single live location with certainty:

        * no `lanes` row for this task's OWN recorded lane string, or that
          row carries no `pane_id` (never registered, or predates the
          column)
        * `tmux display-message` against that exact pane_id fails (the pane
          itself is gone, not merely renamed) or answers a session that is
          somehow still `stale_session` (contradicts the caller's own
          failed lookup; something else is wrong, so this refuses rather
          than loop back into it)
        * `tmux list-sessions` -- a live enumeration, never a hardcoded or
          guessed name -- does not name the session `display-message`
          claimed
        * `lanes.sh --json` on the resolved session itself fails, or does
          not carry a window at the resolved index

        Every one of those is a positive live check, not an inference --
        the same "only ones a caller can enumerate live" discipline this
        brief asked for.
        """
        lane_row = self.ledger.get_lane(task["lane"])
        if lane_row is None:
            return None
        pane_id = (lane_row.get("pane_id") or "").strip()
        if not pane_id:
            return None
        try:
            target = self.runner(["tmux", "display-message", "-t", pane_id, "-p", "#{session_name}:#{window_index}"])
        except Exception:
            return None
        session, sep, index = target.strip().rpartition(":")
        if not sep or not session or not index.isdigit() or session == stale_session:
            return None
        try:
            live_sessions = self.runner(["tmux", "list-sessions", "-F", "#{session_name}"])
        except Exception:
            return None
        if session not in live_sessions.splitlines():
            return None
        try:
            windows = self._fetch_session_lanes(session)
        except Exception:
            return None
        window = windows.get(index)
        if window is None:
            return None
        return session, window

    def _reviewer_task_merge_evidence(self, task):
        """agent-supervisor#774 half B: `_lane_log_pr_url`'s counterpart for
        a REVIEWER task, not an author task, tried only when
        `_resolve_renamed_session` has already failed to find a live pane
        under any name (this module's top-level docstring). A review task
        never opens anything -- it reviews someone else's PR and posts a
        `Verdict:`/`Review-Lane:` comment on it -- so the evidence this
        checks is different in kind, not just in source: three independent,
        checkable facts, never an inference from idleness (this sweep's
        central rule, restated in its own module docstring).

        1. This task's own `source_tasks` row (`Ledger.get_source_task`,
           written once at dispatch time by `cli_dispatch_record.py` for a
           `--reviews-pr`-scoped dispatch) is a REVIEW of a real PR --
           `source_kind='pull'` AND `is_review=1`, never inferred from the
           task id or brief text.
        2. That PR's OPERATIVE verdict, resolved through `verdict.py`'s own
           already-tested `GithubReviewVerdictSource` (no second regex over
           `Verdict:`/`Review-Lane:` lines -- `merge-pr.sh`'s own gate reads
           through the exact same class), is `approved`, AND is attributed
           (`reviewer_lane`) to precisely THIS task's own lane -- a verdict
           posted by some other reviewer settles nothing about whether
           *this* task's work happened.
        3. That PR is independently confirmed MERGED via a live `gh pr
           view`, not inferred from the verdict alone -- an APPROVE can be
           posted and the PR still sit open.

        Returns the PR's URL (the same shape `_complete_from_evidence`'s
        note already expects) when all three hold; `None` the moment any
        one of them does not -- a `gh`/network failure, a PR that is not
        this task's own review, a verdict from a different lane, or a PR
        that is approved but not yet merged are all `None`, never guessed
        into a completion.
        """
        source = self.ledger.get_source_task(task["id"])
        if source is None or source.get("source_kind") != "pull" or source.get("is_review") != 1:
            return None
        match = _PULL_SOURCE_URL_RE.fullmatch(source.get("source_url") or "")
        if match is None:
            return None
        repo, number = match.group("repo"), match.group("number")
        verdict_source = GithubReviewVerdictSource(runner=self.runner, ledger=self.ledger)
        try:
            result = verdict_source.verdict(repo=repo, number=int(number))
        except Exception:
            return None
        if result.get("verdict") != "approved" or result.get("reviewer_lane") != task["lane"]:
            return None
        try:
            raw = self.runner([self.gh_bin, "pr", "view", number, "--repo", repo, "--json", "state,url"])
            payload = json.loads(raw)
        except Exception:
            return None
        if payload.get("state") != "MERGED":
            return None
        return payload.get("url") or source["source_url"]

    def _complete_observed(self, task, *, session, idle_seconds, now, report, via_fallback=False):
        fallback_note = (
            f" (recorded lane's session was gone; resolved via its pane_id to "
            f"{session!r}, current as of {int(now)} -- agent-supervisor#779)"
            if via_fallback
            else ""
        )
        note = (
            f"reconcile-lane-completions: {task['lane']} observed free for "
            f"{int(idle_seconds)}s (>= {self.idle_after}s) as of {int(now)} -- "
            f"never signalled completion; auto-completed from observed pane state{fallback_note}"
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

    def _lane_log_pid(self, task_id):
        """agent-supervisor#488: the pid `ClaudePrintAdapter.assign_task`
        recorded in this lane's own transport log at dispatch time, or
        `None` if the log is missing, unreadable, or never got that far
        (no `--- dispatched detached: ... ---` line). `None` is NOT the
        same claim as "no pid" -- it means this sweep has no way to check
        liveness at all, and callers must treat it as indeterminate, not
        as a stand-in for either alive or dead. When a lane-log records
        more than one dispatch (a rare retried send), the LAST one is the
        pid actually running now.
        """
        log_path = self.ledger.root / "lane-logs" / f"{task_id}.log"
        try:
            text = log_path.read_text(errors="replace")
        except OSError:
            return None
        matches = _DISPATCHED_PID_RE.findall(text)
        return int(matches[-1]) if matches else None

    def _pid_confirmed_dead(self, task):
        """agent-supervisor#692: is this row a `claude-print` lane whose
        dispatch-time pid (`_lane_log_pid`, #488) has DEMONSTRABLY exited --
        the one signal strong enough to terminate a row immediately,
        without waiting out `stale_after`.

        Three of the four lanes #692 measured dead (as679, as680, at167)
        ended their OWN turn -- `claude -p` exited normally, on purpose,
        because the brief it was given asked it to wait for an asynchronous
        callback nothing in this estate ever sends. The pid this sweep can
        already read from the lane-log (`ClaudePrintAdapter.assign_task`
        writes it at spawn, before the row even reaches `delivered`) is not
        an absence to wait out like `updated_at` staleness is -- it is a
        positive fact, available from the very first sweep tick after the
        process exits. A row this method returns `True` for is, by
        construction, still `delivered` or `accepted` (the caller only
        reaches here via `list_delivered_open_tasks`/`list_accepted_open_
        tasks`), so there is no race with a genuine late completion: if the
        process that would have called `complete()` has already exited and
        the row is still open, it never will.

        Returns `False` for every case this fast path does not cover --
        not a claude-print lane, no lane record, no pid ever logged, or a
        pid still alive. `False` here is NOT a claim the row is alive or
        indeterminate; it only means "this fast path does not apply, fall
        back to the wall-clock dwell + `_liveness_blocks_failure` gate a
        `pi-rpc` row (or a claude-print row `_lane_log_pid` cannot read)
        still needs."
        """
        lane = self.ledger.get_lane(task["lane"])
        if lane is None or lane.get("transport") != "claude-print":
            return False
        pid = self._lane_log_pid(task["id"])
        if pid is None:
            return False
        return not pid_is_alive(pid)

    def _liveness_blocks_failure(self, task, *, now, report):
        """agent-supervisor#488: the gate `_sweep_nonobservable`/
        `_sweep_nonobservable_accepted` both run before writing a failure
        stamp. Returns `True` (and records why in `report`) when the
        subprocess this row's lane-log named is demonstrably still alive,
        OR when a `claude-print` lane's liveness cannot be determined at
        all -- the safe answer in both cases is to leave the row alone
        rather than guess, because stamping a LIVE task failed destroys
        work (see this module's top-level docstring); only a DEMONSTRABLY
        dead pid, or a lane transport with no live subprocess to protect in
        the first place, lets the caller proceed to `_lane_log_pr_url` and,
        failing that, the failure write.

        Scoped to `transport == 'claude-print'` deliberately: that is the
        ONE transport whose `assign_task` starts a detached, genuinely
        long-running subprocess and returns before it finishes (`adapter.py`'s
        own comment: "the child is in its own process group, so it survives
        this process exiting"). A `pi-rpc` lane's transport is terminated
        before `assign_task` ever returns (`PiRPCAdapter`'s own docstring:
        "the resulting transport is always terminated before returning"), so
        by the time this sweep's `stale_after` dwell has even elapsed there
        is no live process left to protect -- treating an unreadable pid as
        indeterminate for THAT transport would just resurrect the #374/#414
        stuck-forever shape this module's own fix history already closed.
        A lane this method cannot even look up (`get_lane` returns `None`)
        is treated the same as non-`claude-print`, for the same reason
        `_parse_lane` already fails closed elsewhere in this module: an
        absent record is not evidence of a live claude-print subprocess.
        """
        lane = self.ledger.get_lane(task["lane"])
        if lane is None or lane.get("transport") != "claude-print":
            return False
        pid = self._lane_log_pid(task["id"])
        if pid is None:
            report["unresolved"].append(task["id"])
            report["liveness_indeterminate"].append(task["id"])
            return True
        if pid_is_alive(pid):
            report["unresolved"].append(task["id"])
            report["liveness_alive"].append(task["id"])
            return True
        return False

    def _complete_from_evidence(self, task, *, pr_url, now, report, evidence_description=None):
        """agent-supervisor#401: the lane never signalled, but its own
        lane-log names a PR it opened -- cheap evidence available at stamp
        time that settles what neither `_fail_unaccepted` nor
        `_sweep_nonobservable` can observe directly. Completes the task
        (via the ordinary `complete()` path, so the note lands at the
        canonical `<task_id>.md`) instead of stamping a failure the
        evidence already contradicts.

        `evidence_description` overrides the default "its lane-log names"
        phrasing below -- agent-supervisor#774 half B's reviewer-task path
        (`_reviewer_task_merge_evidence`) settles this from a REVIEW's own
        merge/verdict evidence, never a lane-log, and the note text must
        say so rather than claim a lane-log match that never happened.
        """
        if evidence_description is None:
            evidence_description = f"its lane-log names {pr_url}"
            evidence_kind = "lane-log PR evidence"
            issue_ref = "agent-supervisor#401"
        else:
            evidence_kind = "review/merge evidence"
            issue_ref = "agent-supervisor#774"
        note = (
            f"reconcile-lane-completions: {task['lane']} never signalled completion, "
            f"but {evidence_description} as of {int(now)} -- cheap evidence "
            "available at stamp time settles this before a failure is stamped: "
            f"auto-completed from {evidence_kind}, not from an observed pane "
            f"({issue_ref})"
        ).encode("utf-8")
        allow_claim = task["id"].startswith(CLAIM_TASK_PREFIX)
        try:
            self.ledger.complete(task["id"], note, pane_nonce=task["pane_nonce"], allow_claim=allow_claim)
        except Exception as error:
            report["errors"].append({"task": task["id"], "error": str(error)})
            return
        report["completed"].append(task["id"])
        report["completed_from_evidence"].append(task["id"])

    def _fail_unaccepted(self, task, *, session, idle_seconds, now, report, via_fallback=False):
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
        fallback_note = (
            f" (recorded lane's session was gone; resolved via its pane_id to "
            f"{session!r}, current as of {int(now)} -- agent-supervisor#779)"
            if via_fallback
            else ""
        )
        note = (
            f"reconcile-lane-completions: {task['lane']} observed free for "
            f"{int(idle_seconds)}s (>= {self.idle_after}s) as of {int(now)} -- "
            "never signalled completion AND no accepted_at recorded (this dispatch "
            "was never confirmed to land); no completion signal arrived, which is "
            "not the same claim as the work having failed -- terminated failed only "
            f"to free the lane for redispatch (agent-supervisor#193, agent-supervisor#401){fallback_note}"
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

        agent-supervisor#692: "nothing but silence" no longer describes a
        `claude-print` row whose dispatch-time pid has demonstrably exited
        -- `_pid_confirmed_dead` is a positive fact, not an absence, so it
        bypasses `stale_after` entirely rather than waiting the full dwell
        to reach the same conclusion a much-cheaper check already settled.
        Four lanes sat `delivered`/`accepted` for 110-227 minutes before
        anything noticed; this is what turns that into a same-tick signal.
        """
        updated_at = task.get("updated_at")
        if not isinstance(updated_at, (int, float)):
            report["unresolved"].append(task["id"])
            return
        age_seconds = now - updated_at
        died_without_completing = self._pid_confirmed_dead(task)
        if not died_without_completing:
            if age_seconds < self.stale_after:
                report["unresolved"].append(task["id"])
                return
            # agent-supervisor#488: a demonstrably alive (or unknowable) pid
            # blocks this failure stamp outright, before any evidence check
            # -- see `_liveness_blocks_failure`'s own docstring for why both
            # directions resolve to "leave the row alone".
            if self._liveness_blocks_failure(task, now=now, report=report):
                return
        pr_url = self._lane_log_pr_url(task["id"])
        if pr_url is not None:
            self._complete_from_evidence(task, pr_url=pr_url, now=now, report=report)
            return
        if died_without_completing:
            note = (
                f"reconcile-lane-completions: {task['lane']} died_without_completing -- "
                f"its dispatch-time pid has demonstrably exited as of {int(now)}, "
                f"{int(age_seconds)}s after its last update; a claude-print lane gets no "
                "automatic resume, so an exited process on a still-open task is a "
                "terminal fact on its own, not something worth a stale_after dwell to "
                "confirm -- terminated failed only to free the lane for redispatch "
                "(agent-supervisor#692)"
            ).encode("utf-8")
        else:
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
        if died_without_completing:
            report["died_without_completing"].append(task["id"])

    def _sweep_nonobservable_accepted(self, task, *, now, report):
        """agent-supervisor#414: `_sweep_nonobservable`'s counterpart for a
        no-pane task already moved to `status='accepted'` -- same dwell
        gate against `updated_at`, same `stale_after` threshold, but the
        source status this method's caller has already filtered for is
        `accepted`, not `delivered`, so the terminal write is
        `fail_stale_acceptance`, the one Ledger method eligible for it.

        agent-supervisor#401 (found late, in review of the #401 fix itself):
        this path had nothing but silence to reason from too -- a
        claude-print/pi-rpc lane that got as far as `accept()`, shipped a PR
        that MERGED (named in its own lane-log), then went quiet, was still
        stamped "failed, not completed" with zero evidence check. Same
        `_lane_log_pr_url` check as `_fail_unaccepted`/`_sweep_nonobservable`,
        applied here for the same reason.

        agent-supervisor#692: same immediate-pid-death fast path as
        `_sweep_nonobservable` -- see that method's own docstring for why
        `_pid_confirmed_dead` bypasses `stale_after` rather than waiting it
        out. `as679-lease-anchor` and `as680-lane-completion`, two of the
        four lanes #692 measured, had both reached `accepted` before ending
        their own turn to wait on a callback nothing sends; this is the
        path that stamps them, same tick their process actually exited.
        """
        updated_at = task.get("updated_at")
        if not isinstance(updated_at, (int, float)):
            report["unresolved"].append(task["id"])
            return
        age_seconds = now - updated_at
        died_without_completing = self._pid_confirmed_dead(task)
        if not died_without_completing:
            if age_seconds < self.stale_after:
                report["unresolved"].append(task["id"])
                return
            # agent-supervisor#488: same liveness gate as
            # `_sweep_nonobservable` -- the measured case (`as473-as473x`)
            # reached exactly this method via `status='accepted'`, stamped
            # `failed` while its `claude -p` subprocess was still alive and
            # accumulating CPU an hour later.
            if self._liveness_blocks_failure(task, now=now, report=report):
                return
        pr_url = self._lane_log_pr_url(task["id"])
        if pr_url is not None:
            self._complete_from_evidence(task, pr_url=pr_url, now=now, report=report)
            return
        if died_without_completing:
            note = (
                f"reconcile-lane-completions: {task['lane']} died_without_completing -- "
                f"its dispatch-time pid has demonstrably exited as of {int(now)}, "
                f"{int(age_seconds)}s after its last update; a claude-print lane gets no "
                "automatic resume, so an exited process on a still-open task is a "
                "terminal fact on its own, not something worth a stale_after dwell to "
                "confirm -- terminated failed only to free the lane for redispatch "
                "(agent-supervisor#692)"
            ).encode("utf-8")
        else:
            note = (
                f"reconcile-lane-completions: {task['lane']} has no observable pane "
                f"(non-tmux lane id) and has sat at status=accepted for "
                f"{int(age_seconds)}s (>= {self.stale_after}s) as of {int(now)} -- "
                "no completion signal arrived, and this transport has no pane to poll "
                "for one; that is not the same claim as the work having failed, only "
                "that nothing was observed -- terminated failed only to free the lane "
                "for redispatch (agent-supervisor#414, agent-supervisor#401)"
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
        if died_without_completing:
            report["died_without_completing"].append(task["id"])
