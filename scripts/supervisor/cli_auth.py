"""Caller/supervisor-lane identity checks for cli.py.

Split out of cli.py (agent-supervisor#722, following core.py's split in
#706/#708). Pure move -- `_verify_caller`, `_is_supervisor_lane` and
`_SUPERVISOR_LANE_ALIASES` unchanged from cli.py. `cli.py` imports all
three back under their original names.
"""

from __future__ import annotations

import os


# as#132: the ledger's registered lane id can still be the pre-migration
# "architecture", the post-migration "supervisor", or whatever the caller
# passed via --supervisor-lane/--architecture-lane -- any of those must be
# recognised as THE supervisor lane so a lane-exclusion check does not fail
# open (agent-supervisor#132, cli.py's `observe` filter used to hardcode the
# literal "architecture" and silently stopped excluding anything once the
# flag's default moved). Drop the "architecture" alias once every estate has
# migrated its ledger rows and callers to "supervisor".
_SUPERVISOR_LANE_ALIASES = frozenset({"architecture", "supervisor"})


def _is_supervisor_lane(lane_id, configured):
    return lane_id == configured or lane_id in _SUPERVISOR_LANE_ALIASES



def _verify_caller(adapter, ledger, lane):
    """Confirm the process invoking `accept`/`complete` IS the lane it claims
    to be, not merely that the lane it claims exists (agent-supervisor#362).

    `adapter` must be the one `adapter_for_lane(lane)` returns, not a bare
    `TmuxAdapter` -- `ClaudePrintAdapter._verified_lane` does not require a
    tmux pane at all, while `TmuxAdapter._verified_lane` requires one, and
    calling the wrong one raises for a transport it was never meant to judge.

    The identity check below is verified BY WHAT THE LANE ACTUALLY HAS: a
    tmux/send-keys lane has a pane, so it is checked against `TMUX_PANE`, the
    same as before this fix (that path is unchanged). A claude-print lane has
    no pane -- `ClaudePrintAdapter`'s own docstring says so -- so there is
    nothing for `TMUX_PANE` to authenticate; what it does have is the
    `session_id` its own `claude -p --session-id <id>` process was launched
    with, which the harness publishes back to that same process as
    `CLAUDE_CODE_SESSION_ID`. Checked the same way as the pane case: skipped
    if unset (an operator running this by hand off-harness), refused if set
    and it disagrees with the lane's recorded session.
    """
    record = adapter._verified_lane(lane)
    if record.get("transport") == "claude-print":
        caller = os.environ.get("CLAUDE_CODE_SESSION_ID")
        if caller and caller != record["session_id"]:
            raise RuntimeError(f"caller session {caller} does not own lane {lane}")
        return record
    caller = os.environ.get("TMUX_PANE")
    if caller and caller != record["pane_id"]:
        raise RuntimeError(f"caller pane {caller} does not own lane {lane}")
    return record
