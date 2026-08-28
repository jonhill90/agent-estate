"""record-completion for cli.py.

Split out of cli.py (agent-supervisor#722, following core.py's split in
#706/#708). Pure move -- `record_completion` unchanged from cli.py.
`cli.py` imports it back under its original name.
"""

from __future__ import annotations

from core import CLAIM_TASK_PREFIX, TERMINAL_STATUSES

from cli_dispatch_record import _release_issue_claim_for_task


def record_completion(ledger, *, task, lane, note):
    """Record that a dispatched task finished. Writes; never sends.

    Not `cli.py complete`: that path verifies `TMUX_PANE` belongs to the
    lane's own pane and takes a `--result-file`. `lane-done.sh` runs in the
    supervisor's pane, not the worker's, and holds no result artifact -- the
    only thing it knows is that the worker's `wait-for` channel fired for a
    window still carrying the expected name. It cannot know the window was
    renamed: since agent-dotfiles#194 this release runs BEFORE the rename and
    unconditionally, and the rename is cosmetic. So it authenticates with the
    task's own recorded `pane_nonce` and records that fact as the result.

    Note the one thing this does that is not inert: `Ledger.complete` inserts
    a `completion:<task>` event, and `cli.py notify`/`tick` would send those
    to the supervisor lane. Neither can fire from this wiring -- both go
    through `_verified_lane`, which requires a supervisor lane registered
    with matching tmux options, and nothing here registers one.

    agent-supervisor#36 (second issue comment): `--task` used to be the only
    way in, and `get_task` is an exact id match -- so a `ledger-claim:<lane>:
    <token>` row (how the codex harness's completions land) could not be
    recorded honestly by an operator who only had the bare token, and
    `cancel-open-task` was the only verb that worked, which records the wrong
    terminal outcome instead of completing it. Resolution order: an exact `--task` id match
    first (unchanged behaviour for an ordinary task); failing that, with
    `--lane` also given, the claim row that token would produce under that
    lane; failing that, with `--lane` given (whether or not `--task` was
    also given), whichever single row is still open for it -- mirroring
    `cancel_open_task`'s own lookup, so the caller does not have to know the
    row's shape before asking to close it.

    agent-supervisor#140 (fix pass): an exact `--task` match is no longer
    proof this is the right row. `lane-done.sh` always passes `--task` with
    the bare window name `dispatch.sh` set (`<prefix><issue>-<slug>`), and
    that id NEVER stops existing -- it is the historical record of the
    FIRST dispatch of that issue+slug, forever (invariant 1). A REDISPATCH
    of the same issue+slug is recorded under a `-r2`/`-r3`/... suffixed id
    instead (`cli.py`'s `_unique_redispatch_task_id`), precisely so it does
    not collide with that prior row -- which means the exact `--task` match
    below will keep resolving to the FIRST attempt's already-`complete`(/
    `failed`/`cancelled`) row on every later redispatch's own completion,
    not the one that just finished. When that happens and `--lane` is also
    given, prefer that lane's own still-open task instead: a TERMINAL row
    cannot be the one whose worker just signalled completion, and the lane
    that just finished has exactly one open task to mean.
    """
    if not task and not lane:
        raise RuntimeError("record-completion requires --task or --lane")
    row = ledger.get_task(task) if task else None
    if row is not None and lane and row["status"] in TERMINAL_STATUSES:
        open_row = ledger.get_open_task_for_lane(lane)
        if open_row is not None:
            row = open_row
    if row is None and lane and task:
        row = ledger.get_task(f"{CLAIM_TASK_PREFIX}{lane}:{task}")
    if row is None and lane:
        row = ledger.get_open_task_for_lane(lane)
    if row is None:
        identity = f"task: {task}" if task else f"lane: {lane}"
        raise RuntimeError(f"unknown {identity}")
    allow_claim = row["id"].startswith(CLAIM_TASK_PREFIX)
    # agent-supervisor#359: only a transition INTO 'complete' releases the
    # GitHub claim -- an idempotent replay of an already-complete row (the
    # same immutable-result short-circuit `Ledger.complete` itself takes)
    # must not re-run the release against whatever NEW work has since
    # legitimately re-claimed the same issue number.
    already_complete = row["status"] == "complete"
    value = ledger.complete(row["id"], note.encode("utf-8"), pane_nonce=row["pane_nonce"], allow_claim=allow_claim)
    if not already_complete:
        _release_issue_claim_for_task(ledger, row["id"])
    return value
