"""Lane availability and session-supervision reads for cli.py.

Split out of cli.py (agent-supervisor#722, following core.py's split in
#706/#708). Pure move -- `lane_free` and `session_state`, and the constants
`lane_free` reads (`HARNESS_BY_COMMAND`, `FREE_WINDOW_NAME_RE`,
`HARNESS_OPTION`), unchanged from cli.py. `cli.py` imports all five back
under their original names -- `tests/supervisor/test_cli_lane_free.py` and
`test_cli_session_state.py` reach them as `cli.lane_free`/`cli.session_state`/
`cli.HARNESS_OPTION`, so those names must keep resolving there.
"""

from __future__ import annotations

import re
import secrets

from adapter import HARNESS_COMMANDS, TmuxAdapter


# NARROW inference, for a command that is actually diagnostic on its own
# (agent-dotfiles#216). Every ledger and every lane bootstrapped before #216
# has no `HARNESS_OPTION` recorded anywhere -- if `lane_free` required that
# option unconditionally, EVERY existing claude/codex lane would go from
# dispatchable to permanently refused the moment this shipped, which is
# obviously not what "fail closed" is supposed to protect. This dict stays
# exactly as narrow as it always was (exact binary name only, no "node"
# entry: `node` names several harnesses and must never resolve from a name
# alone) -- it is what keeps every already-working lane working. It is
# consulted FIRST, in `lane_free`; the recorded `HARNESS_OPTION` is the
# fallback for a command this dict cannot place, e.g. `node`.
HARNESS_BY_COMMAND = {"codex": "codex", "claude": "claude", "claude.exe": "claude"}
FREE_WINDOW_NAME_RE = re.compile(r"^free-[0-9]+$")
# agent-dotfiles#216: the pane option `lane_free`'s backfill reads as the
# RECORDED harness, instead of inferring it from `#{pane_current_command}` --
# see `TmuxAdapter.HARNESS_OPTION` and `bootstrap-session.sh`, the two
# writers.
HARNESS_OPTION = TmuxAdapter.HARNESS_OPTION



# agent-supervisor#153. Three states, and `unknown` collapses to the SAME
# treatment as `unsupervised` in every caller -- this module deliberately
# returns them as distinct strings rather than a bool, because a future
# `supervisor release <session>` (issue #153's own vocabulary, not built
# here) needs to tell "known not ours" apart from "never told either way";
# nothing yet writes the former, so it is not reachable today, but the
# three-way shape is set up so adding it later is additive, not a rename of
# an existing state's meaning.
def session_state(ledger, transport, *, session):
    """supervised / unsupervised / unknown -- the one answer #153 exists for.

    Two independent checks, both required, neither trusted alone:

    * `transport.session_exists` -- does tmux actually have this session
      right now. A stale ledger row for a session that is gone (#153
      measured `agent-dotfiles` and `ad241repro-22535` exactly this way)
      must never read as `supervised` -- there is nothing to act on, and
      reporting one as actionable is worse than reporting nothing, so this
      collapses straight to `unknown`, without even asking the ledger.
    * `ledger.session_marked_supervised` -- did WE decide to adopt it. This
      is the one-way ratchet: unless this call plainly returns True, the
      result is `unknown`, never `supervised`. Caught broadly on purpose --
      a locked ledger, a corrupt file, an old ledger missing the `sessions`
      table (`session_marked_supervised` raising `sqlite3.OperationalError`
      rather than returning) are all "the marker could not be read", and
      #153's acceptance test is exactly this: break that read and confirm
      the result is `unknown`, never `supervised`. A caller-side try/except
      would work too, but every caller would have to remember it; failing
      closed belongs here, once, where it cannot be forgotten by a future
      caller that assumes a clean read.
    """
    if not transport.session_exists(session):
        return "unknown"
    try:
        marked = ledger.session_marked_supervised(session)
    except Exception:
        return "unknown"
    return "supervised" if marked else "unknown"



def lane_free(ledger, transport, *, lane, target, window_name):
    """Answer "is `lane` safe to dispatch to?" from the ledger, not the name.

    agent-dotfiles#174. Three outcomes:

    * The ledger already knows this lane (`lane_available` returns True or
      False) -- that answer wins outright, REGARDLESS of what the window is
      currently named. A hand-renamed window, or one still carrying a task
      name for a task the ledger has never heard finished, must not change
      this: the whole point of the change is that authority moved off the
      name.
    * The ledger has never heard of this lane, and the window is currently
      named by the `free-N` convention -- the one-time MIGRATION path for a
      lane whose availability today exists only as that name (every lane
      alive before this landed, and any future lane opened by hand). This
      registers it in the ledger, with no open task, so it reads free from
      here on without ever consulting the name again. This is "lazy backfill
      on first sight", the option agent-dotfiles#174 itself named: it only
      ever fires once per lane, because the second call finds the lane
      already known and takes the first branch above.
    * Neither -- an unregistered lane not currently named `free-N` is
      UNKNOWN, and unknown is not free. This is the fail-closed default: a
      lane this code cannot positively place is never offered, the same
      posture `lanes.sh`'s own whitelist (#126) already takes for pane state.

    agent-dotfiles#216: harness identity for the backfill branch above tries
    `HARNESS_BY_COMMAND` first -- unchanged, exact-binary-name inference,
    same as before #216 -- and only falls back to the pane's `HARNESS_OPTION`
    (a RECORDED fact, written by `bootstrap-session.sh` or `cli.py register`)
    for a command that dict cannot place. That ordering, not the option
    alone, is what keeps every lane bootstrapped before #216 dispatchable:
    none of them ever had the option written, and requiring it unconditionally
    would have refused all of them the moment this shipped. The option only
    matters for a command `HARNESS_BY_COMMAND` was never able to place --
    every Node-based harness's process reads "node", so copilot (and codex
    whenever its binary is not literally named `codex`) needs a written
    record to be identified at all. An unrecorded option, or one that names a
    harness the live pane's command visibly contradicts
    (`TmuxAdapter._command_matches`), still refuses: this closes the gap for
    a harness that WAS written down, it does not turn "cannot tell" into "go
    ahead". The success reply's `"harness"` key lets a caller (`dispatch.sh`)
    forward the same resolved value into `record-dispatch` instead of that
    command re-deriving one from the pane command on its own.

    WHAT THIS DOES NOT DO (agent-dotfiles#188 finding 2, in the terms
    `claim.sh`'s own header uses for its sub-second race): this is a QUERY,
    not a claim. It takes no lock, writes no assignment for the caller, and
    grants no exclusion. Two dispatchers that both call this for the same
    lane within the same tick both read `"free":true` -- measured, on one
    seeded ledger, two consecutive calls with nothing written in between
    returned the identical answer both times. Nothing re-checks between a
    caller picking this lane and its first `send-keys`, and that window is
    not sub-second the way `claim.sh`'s is: it spans claim, worktree
    creation and the send itself, and NOTHING here stops two dispatchers
    from both typing a competing brief into the same live pane during it.
    `record_dispatch`'s `one_open_task_per_lane` constraint does NOT catch a
    double-dispatch either, measured: `cli.record_dispatch` mints a fresh
    `nonce` on every call, so `_register_lane_tx`'s "changed identity ->
    new incarnation" test is always true, even for the same pane seconds
    apart. A second writer's call does not refuse or hold -- it succeeds,
    cancels the first writer's task, and installs its own as the lane's one
    open task. The ledger ends up recording one clean occupancy, with
    nothing left to show two briefs went into that pane (agent-dotfiles#188
    finding 2 / #183 round 3). So there is no bookkeeping honesty here
    either, only pane exclusion's absence: this function's "free" is honest
    only as of the instant it was asked, exactly as `claim.sh` says of its
    own assignee check. This estate runs two dispatchers on unrelated
    cadences and has already paid for a duplicate dispatch once (#70); the
    name of this function is not a claim that the gap is closed.
    """
    known = ledger.lane_available(lane)
    if known is not None:
        record = ledger.get_lane(lane)
        return {
            "lane": lane,
            "known": True,
            "free": known,
            "backfilled": False,
            "harness": record["harness"] if record else None,
        }
    if not FREE_WINDOW_NAME_RE.match(window_name):
        return {"lane": lane, "known": False, "free": False, "backfilled": False}
    metadata = transport.metadata(target)
    command = metadata["command"]
    # Narrow inference first (unchanged from before #216: exact binary name,
    # never guessed for an ambiguous one like `node`), THEN the recorded
    # option as the fallback for a command this dict cannot place. Checking
    # both, rather than the option alone, is what keeps every pre-#216 lane
    # (no option ever written for it) dispatchable exactly as before.
    inferred = HARNESS_BY_COMMAND.get(command)
    recorded = transport.get_option(target, HARNESS_OPTION)
    recorded = recorded if recorded in HARNESS_COMMANDS else None
    if inferred and recorded and inferred != recorded:
        return {
            "lane": lane,
            "known": False,
            "free": False,
            "backfilled": False,
            "reason": (
                f"recorded harness {recorded!r} does not match pane command {command!r}"
            ),
        }
    if inferred:
        harness = inferred
    elif recorded and TmuxAdapter._command_matches(recorded, command):
        harness = recorded
    elif recorded:
        return {
            "lane": lane,
            "known": False,
            "free": False,
            "backfilled": False,
            "reason": (
                f"recorded harness {recorded!r} does not match pane command {command!r}"
            ),
        }
    else:
        return {
            "lane": lane,
            "known": False,
            "free": False,
            "backfilled": False,
            "reason": (
                f"cannot tell which harness pane command {command!r} is "
                f"-- no {HARNESS_OPTION} recorded on the pane"
            ),
        }
    # No tmux options are set here (unlike `TmuxAdapter.register_lane`): this
    # mirrors `record_dispatch`'s own choice not to touch tmux beyond reading
    # it (see that function's docstring) -- a real dispatch re-registers this
    # lane with a fresh identity moments later anyway, so nothing here needs
    # to survive past this one query. The harness option itself was already
    # written by whoever recorded it (bootstrap or `register`); this function
    # only ever reads it.
    ledger.register_lane(
        lane=lane,
        pane_id=metadata["pane_id"],
        nonce=secrets.token_hex(16),
        harness=harness,
        repo=metadata["path"],
        server_id=metadata["server_id"],
        session_id=metadata["session_id"],
        command=metadata["command"],
    )
    return {"lane": lane, "known": True, "free": True, "backfilled": True, "harness": harness}
