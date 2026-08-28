"""Ledger id/status constants and the lane-claim placeholder markers.

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- every name here is re-exported by
`core.py` under its original name; `import core` is unchanged.
"""

from __future__ import annotations

import re


TERMINAL_STATUSES = ("complete", "failed", "cancelled")
# The exact `source_tasks.status` CHECK, in one place so a second writer
# (agent-supervisor#127's `reconcile_sources.py`) doesn't have to duplicate a
# constraint it does not own the table definition of.
SOURCE_TASK_STATUSES = ("created", "delivered", "accepted", "running", "complete", "failed", "cancelled")
TASK_ID_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$")
COMPONENT_RE = re.compile(r"^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$")
MAX_RESULT_BYTES = 64 * 1024

# agent-dotfiles#209. `claim_lane` writes a placeholder task under this id
# prefix; `reap_stale_lane_claims` is the only thing that removes one whose
# owner never came back, and it must be able to tell a claim placeholder from
# every other kind of outstanding task by inspection alone -- in particular
# from `mark_lane_held`'s `ledger-hold:` rows (#188), which are DELIBERATE
# holds awaiting a human and must never be reaped.
CLAIM_TASK_PREFIX = "ledger-claim:"

# agent-dotfiles#238. The single row `supervisor_lease` may ever hold.
SUPERVISOR_LEASE_ID = "supervisor"

# The claiming process's identity, recorded in the placeholder's `summary`.
# A suffix on an existing column rather than a new one: a `tasks` column would
# mean a schema migration (see `_migrate_tasks_table`) for a field exactly one
# row-shape in the whole table ever carries. Anchored at the end and matched
# with a strict shape, so an unowned claim -- or a summary that merely happens
# to mention the word -- parses as `None` and is never reaped.
CLAIM_OWNER_RE = re.compile(r" \[owner=(?P<host>[^\]\s:]+):(?P<pid>[1-9][0-9]*)\]$")

# A claim placeholder has exactly two states, and the difference between them
# is the whole of agent-dotfiles#209 round 2.
#
# RESERVED -- the claim exists, nothing has been sent into the pane. Nobody is
# working this lane, so both cleanup paths (`release_lane_claim` on the
# dispatcher's own trap, `reap_stale_lane_claims` when the dispatcher died
# where nothing could trap) MAY free it. That is what #209 round 1 built.
#
# LIVE -- `commit_lane_claim` has been called, which `dispatch.sh` does
# IMMEDIATELY BEFORE the `send-keys Enter` that submits the brief. From here a
# worker may be running in that pane, so NEITHER cleanup path may free it: a
# lane wrongly held costs capacity and has a documented manual recovery, while
# a lane wrongly freed costs a running lane's work, which is the loss this
# whole subsystem exists to prevent (#102/#123/#126, and #124/#126's one-way
# ratchet).
#
# Round 1 drew that line with an in-process bash flag set ~70 lines AFTER the
# submit, so a signal landing in between freed a lane whose brief was already
# live -- reproduced in tests/supervisor/test_dispatch.sh. Both cleanup paths
# scope themselves to RESERVED, so moving a row to LIVE is what puts it out of
# their reach, and it is a durable ledger fact rather than a variable in a
# process that may be killed a microsecond later.
#
# `delivered` is the status LIVE maps onto, and the choice is load-bearing in
# one non-obvious way: `_register_lane_tx` excludes `delivery_pending` from
# the outstanding-task query it cancels through (#871's reconciliation escape
# valve), so a claim parked there would survive `record_dispatch` and then
# collide with its task INSERT under `one_open_task_per_lane`, failing every
# clean dispatch. `delivered` is not excluded, so the ordinary success path
# still cancels this placeholder and replaces it with the real task.
CLAIM_STATUS_RESERVED = "created"
CLAIM_STATUS_LIVE = "delivered"
