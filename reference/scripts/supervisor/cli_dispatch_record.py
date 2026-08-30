"""Dispatch recording for cli.py: record-dispatch and its supporting writes.

Split out of cli.py (agent-supervisor#722, following core.py's split in
#706/#708). Pure move -- `record_dispatch`, `_unique_redispatch_task_id`
(the redispatch id suffixer `record_dispatch` calls) and
`_release_issue_claim_for_task` (the best-effort GitHub claim release both
`record_dispatch`'s caller and `record_completion`/`cancel-open-task` use)
are unchanged from cli.py. `cli.py` imports all three back under their
original names.
"""

from __future__ import annotations

import re
import secrets
import sqlite3
import subprocess
import sys
from pathlib import Path

from core import TERMINAL_STATUSES

from cli_lane_ops import HARNESS_BY_COMMAND


def _unique_redispatch_task_id(ledger, task_id):
    """agent-supervisor#140: give a redispatch of the same issue+slug its own
    row instead of colliding with the finished one already sitting at
    `task_id`.

    `tasks.id` is `<prefix><issue>-<slug>` (`dispatch.sh`'s `WINDOW_NAME`),
    which is per issue+slug, not per dispatch ATTEMPT -- `source_tasks`
    already models one row per attempt (`record_dispatch`'s own docstring
    for #159/#169 makes the same distinction the issue asks for here). A
    completed prior attempt's row is the historical record invariant 1
    requires (do not overwrite it); a fresh attempt needs a row of its own.

    Only a TERMINAL prior row (`complete`/`failed`/`cancelled`) is treated
    as "this is a redispatch" -- a non-terminal one (still `created`/
    `delivery_pending`/`delivered`/`accepted`) is a genuine identity
    collision with a lane that is ACTIVELY working under this id right now,
    and must still fail loud exactly as before
    (`tests/supervisor/test_dispatch.sh`'s `ledger-write-broken` case, and
    `Ledger._assign_tx`'s own "task id already exists with different
    assignment" refusal, both unchanged): passing through unresolved lets
    `_assign_tx` raise and `mark_lane_held` fire, same as pre-#140-fix.

    Suffix, not overwrite: `-r2`, `-r3`, ... appended to `task_id` until an
    id nothing in the ledger holds is found, so `tasks.id == source_tasks.id`
    (`record_dispatch`'s own invariant) still holds for whatever id this
    returns.
    """
    existing = ledger.get_task(task_id)
    if existing is None or existing["status"] not in TERMINAL_STATUSES:
        return task_id
    attempt = 2
    while True:
        candidate = f"{task_id}-r{attempt}"
        if ledger.get_task(candidate) is None:
            return candidate
        attempt += 1



def record_dispatch(
    ledger,
    *,
    lane,
    task,
    summary,
    pane_id,
    pane_path,
    command,
    server_id,
    session_id,
    issues,
    github="",
    harness=None,
    harness_session_id="",
    harness_project_dir="",
    worktree_path="",
    pr=None,
    is_review=False,
    confirm_landed=False,
):
    """Record a dispatch that ALREADY happened. Writes; never sends.

    This is deliberately not `register` + `assign`, and the difference is the
    whole point of agent-dotfiles#140. Read what those two do before assuming
    this duplicates them:

    * `assign` is not a recorder. `TmuxAdapter.assign_task` classifies the
      pane, refuses unless it reads `idle`, and then SENDS a prompt with
      `send_literal`. Calling it from `dispatch.sh` would type a second,
      competing task prompt into a lane that has just been given its brief --
      one telling the worker to run `hill90-supervisor accept`, a binary that
      is not on PATH (docs/supervisor-disposition.md §1.3). It would also put
      `classify_capture` -- whose approval/blocked matching is a known defect,
      §3 of the same document -- back into the dispatch path that #131 just
      finished taking pane inference OUT of. Routed around on purpose.
    * `Ledger.assign` also refuses any task without a reconstructed, OPEN
      `source_tasks` row, and the only writer of those rows requires a
      `hill90-supervisor:v1` marker in the issue body. Measured across all
      four repos: zero issues carry one (§2.1). So the source row is written
      here instead, from what this dispatch itself just observed -- claim.sh
      confirmed the issue open and assigned it seconds ago -- rather than from
      a marker that does not exist. `source_ref` is the issue number, not the
      commit SHA `GithubTaskSource` would put there; nothing reads either yet.
    * No tmux. The pane identity is passed in, already observed by the caller
      that was talking to tmux anyway. A durable record that cannot be written
      without a live tmux server is not the portability fix #140 asks for, and
      it makes this testable without a transport stub.

    agent-dotfiles#174: `lane_free` now reads this record back to decide
    whether a lane is safe to dispatch to. "Nothing reads any of this yet"
    was true under #140 and is not true anymore -- update this paragraph,
    not just the callers, the next time this changes again. `lanes.sh`
    still classifies panes exactly as it did; only availability/ownership
    authority moved onto what this function writes.

    agent-dotfiles#144 finding 2: this used to make five independent `Ledger`
    calls -- register, reconstruct, assign, mark-pending, mark-delivered --
    each its own lock and transaction. A crash between any two left whatever
    had already committed, including an orphan `lanes` row claiming a lane
    occupied for a dispatch nothing else records. `Ledger.record_dispatch`
    does the same five writes in ONE transaction now; this function's job is
    just shaping `dispatch.sh`'s raw inputs into that call.

    agent-dotfiles#188 finding 1: a failure here used to just raise and let
    the caller (`dispatch.sh`) print a warning. The transaction's own
    rollback is not a safe failure mode by itself -- for a lane the ledger
    already knew as free, rollback restores exactly that free row, and the
    brief is already running in the pane. On ANY failure this now also calls
    `Ledger.mark_lane_held` before re-raising, so the lane reads occupied
    instead of whatever it read before this call, regardless of which of
    the five writes failed or why.

    agent-supervisor#159: `pr` is the PR-scoped sibling of the issue-keyed
    write below -- passed by `dispatch.sh` for a review or a fix pass on PR
    <N> whose underlying issue is deliberately left claimed by the in-flight
    work that opened it. When given, the `source_tasks` row this writes is
    keyed `source_kind='pull'`, `source_ref=str(pr)` instead of by the
    primary issue, so `Ledger.get_open_task_for_pr` can find it -- the same
    one-transaction write, the same `one_open_task_per_lane` uniqueness,
    nothing new. `issues` is still recorded in `evidence` either way: this
    dispatch still names which issue(s) it closes, it is only not claimed as
    the SOURCE of the task.

    agent-supervisor#169: step 0.6's `pr-lane` read (`dispatch.sh`, before a
    lane is even picked) is a TOCTOU by itself -- this write, seconds later,
    is the actual gate. `Ledger._migrate_source_tasks_pull_uniqueness`'s
    `one_open_pull_per_source_ref` trigger (a `BEFORE INSERT` trigger, not a
    plain partial index -- `source_tasks.status` never advances, so a
    same-table index cannot ask the real "is this PR still open" question;
    see that migration's own docstring) makes a second dispatcher's INSERT
    for the same open PR raise `sqlite3.IntegrityError`, atomically, no
    matter how close the two dispatchers' checks landed. When that happens
    for a PR-scoped call, this prints one recognisable `PR-DUPLICATE:` line
    (in addition to the generic `mark_lane_held` handling every other
    failure already gets) naming the lane that actually won, so
    `dispatch.sh` can tell this collision apart from an ordinary ledger
    failure and refuse loud instead of folding it into the silent,
    non-fatal `ledger_record_failed` path every other write failure takes.

    agent-supervisor#640: `is_review` is `dispatch.sh`'s own `--reviews-pr`
    (explicit or inferred) flag, forwarded verbatim -- the exact fact this
    call knows and `source_tasks` never used to record, leaving
    `Ledger._task_looks_like_review`'s regex to guess it back later from
    `task`/`summary` text and get it wrong for a task named `rerev...`.
    Only meaningful when `pr` is also given (`source_kind='pull'`, the only
    row shape `Ledger.get_contributor_tasks_for_pr` reads it for): recorded
    as `1` when `is_review` is truthy, `0` -- an explicit, known "not a
    review" -- otherwise. An issue-scoped dispatch (`pr` falsy) records
    `None`; the property does not apply to that row shape at all.
    """
    task = _unique_redispatch_task_id(ledger, task)
    try:
        harness = harness or HARNESS_BY_COMMAND.get(command)
        if harness is None:
            raise RuntimeError(f"cannot tell which harness pane command {command!r} is -- pass --harness")
        primary = issues[0]
        evidence = [f"claimed by dispatch.sh for lane {lane}", f"issues: {','.join(str(i) for i in issues)}"]
        if pr:
            source_kind = "pull"
            source_url = f"https://github.com/{github}/pull/{pr}" if github else f"pull:{pr}@{Path(pane_path).name}"
            source_ref = str(pr)
            evidence.append(f"pr: {pr}")
            recorded_is_review = 1 if is_review else 0
        else:
            source_kind = "issue"
            source_url = (
                f"https://github.com/{github}/issues/{primary}" if github else f"issue:{primary}@{Path(pane_path).name}"
            )
            source_ref = str(primary)
            recorded_is_review = None
        return ledger.record_dispatch(
            lane=lane,
            pane_id=pane_id,
            nonce=secrets.token_hex(16),
            harness=harness,
            # The pane's own working directory, which is what
            # `TmuxAdapter._verified_lane` compares this column against. NOT
            # the lane's worktree -- that belongs to the task, and (as of
            # agent-supervisor#117) is passed separately below as
            # `worktree_path` rather than only living inside `summary` text.
            repo=pane_path,
            server_id=server_id,
            session_id=session_id,
            # agent-dotfiles#237: the harness conversation id the dispatcher
            # resolved (`harness-session.sh`), or "" when it could not. Never
            # guessed here -- this function has no way to observe a pane.
            harness_session_id=harness_session_id,
            # agent-supervisor#172: the directory `harness_session_id` was
            # resolved in, never guessed here either -- see the matching
            # argument's docstring above.
            harness_project_dir=harness_project_dir,
            command=command,
            task_id=task,
            source_kind=source_kind,
            source_url=source_url,
            source_ref=source_ref,
            summary=summary,
            source_state="OPEN",
            evidence=evidence,
            status_marker=None,
            worktree_path=worktree_path,
            accepted=confirm_landed,
            is_review=recorded_is_review,
        )
    except Exception as error:
        ledger.mark_lane_held(lane, note=f"record_dispatch failed for task {task}: {error}")
        # agent-supervisor#169: the write-time PR-duplicate collision is a
        # distinct, expected failure -- not a generic ledger error -- and the
        # caller (`dispatch.sh`) needs to be able to tell them apart to
        # refuse loud rather than fold this into the silent non-fatal path
        # every other record_dispatch failure takes. Detected by the index
        # name rather than a generic IntegrityError check, so an unrelated
        # constraint violation (a real bug) still surfaces as an ordinary,
        # unrecognised failure instead of being misreported as this one.
        if pr and isinstance(error, sqlite3.IntegrityError) and "source_tasks.source_ref" in str(error):
            holder = ledger.get_open_task_for_pr(pr)
            holder_lane = holder["lane"] if holder else "unknown"
            holder_task = holder["id"] if holder else "unknown"
            print(
                f"PR-DUPLICATE: PR #{pr} is already claimed by lane {holder_lane} (task {holder_task}) "
                f"-- the write refused this second open source_tasks row; lane {lane} (task {task}) "
                "is marked HELD",
                file=sys.stderr,
            )
        raise


def _release_issue_claim_for_task(ledger, task_id):
    """Best-effort GH claim release for a task's issue(s), on any terminal
    completion path (agent-supervisor#359): nothing released the GitHub
    assignee `claim.sh take` wrote once a lane finished, so the claim
    outlived the lane and the issue silently locked itself out of dispatch
    forever -- 16 of 37 claimed issues were measured stale in one audit,
    including #310 and #313.

    NEVER RAISES: a failed release must not turn a genuine completion into a
    reported failure -- the same posture `lane-done.sh`'s best-effort
    PR-authorship recording already takes for its own side write.

    Skips PR-scoped tasks (`source_kind == 'pull'`) outright -- that issue is
    deliberately left claimed by the ORIGINAL work, per `dispatch.sh`'s own
    `--reviews-pr`/`--pr` comment (a review or fix-pass task never held the
    issue's claim to begin with; there is nothing here for it to release).
    """
    try:
        source = ledger.get_source_task(task_id)
    except Exception as error:
        print(f"cli.py: could not read source task for {task_id} to release its GitHub claim: {error}", file=sys.stderr)
        return
    if not source or source.get("source_kind") != "issue":
        return
    source_url = source.get("source_url") or ""
    match = re.search(r"github\.com/([^/\s]+/[^/\s]+)/issues/", source_url)
    if not match:
        # `source_url` is `issue:<n>@<dirname>` when no `--github`/`github`
        # config was resolvable at dispatch time (record_dispatch's own
        # fallback) -- no OWNER/NAME to release against. Not an error: the
        # claim (if any) was never taken through a repo this can name either.
        return
    repo = match.group(1)
    # `record_dispatch` records every issue this task closes in `evidence`
    # (`"issues: 1,2,3"`, agent-dotfiles#112) even though `source_ref` only
    # ever names the primary one -- release all of them, the same set
    # `dispatch.sh`'s own claim loop took at dispatch time.
    issues = set()
    ref = source.get("source_ref")
    if ref:
        issues.add(str(ref))
    for line in source.get("evidence") or ():
        found = re.match(r"issues:\s*(.+)$", line.strip())
        if found:
            issues.update(part.strip() for part in found.group(1).split(",") if part.strip())
    claim_sh = Path(__file__).resolve().parent / "claim.sh"
    for issue in sorted(issues):
        try:
            result = subprocess.run(
                [str(claim_sh), "release", issue, repo],
                capture_output=True,
                text=True,
                # Bounded well under any test harness's own command timeout:
                # a caller with no gh stub on PATH (most ledger-only tests)
                # still makes one real, fast-failing `gh api` round trip per
                # issue here -- unavoidable without a second, test-only
                # release path -- so this must not be the thing that makes a
                # slow CI runner time out.
                timeout=10,
                check=False,
            )
            if result.returncode != 0:
                print(
                    f"cli.py: could not release GitHub claim on {repo}#{issue} for task {task_id} "
                    f"-- release it by hand: {(result.stderr or result.stdout).strip()}",
                    file=sys.stderr,
                )
        except Exception as error:
            print(
                f"cli.py: could not release GitHub claim on {repo}#{issue} for task {task_id} "
                f"-- release it by hand: {error}",
                file=sys.stderr,
            )
