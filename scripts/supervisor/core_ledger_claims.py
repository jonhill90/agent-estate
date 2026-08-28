"""`Ledger`'s lane-claim placeholders and the supervisor lease: `mark_lane_held`,
the reserved/live claim lifecycle (`claim_lane`/`commit_lane_claim`/
`release_lane_claim`/`reap_stale_lane_claims`), and the supervisor lease
guard (`take_supervisor_lease`/`supervisor_lease`/`release_supervisor_lease`/
`reap_stale_supervisor_lease`).

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import re
import socket
import sqlite3
import time

from core_constants import (  # noqa: F401 -- re-exported by core.py
    CLAIM_OWNER_RE,
    CLAIM_STATUS_LIVE,
    CLAIM_STATUS_RESERVED,
    CLAIM_TASK_PREFIX,
    SUPERVISOR_LEASE_ID,
)
from core_lane_relation import claim_owner_token, pid_is_alive  # noqa: F401 -- re-exported by core.py


class LedgerClaimsMixin:
    def mark_lane_held(self, lane, *, note):
        """Force `lane_available` to read `False` after a failed `record_dispatch`.

        agent-dotfiles#188 finding 1. `record_dispatch` runs after the brief
        is already live in a real pane (see its own docstring) and does its
        five writes in one transaction, so any failure rolls all of them
        back. For a lane the ledger already knew as free -- every lane after
        its first backfill, and every lane `lane-done.sh` has ever freed --
        rollback restores exactly that pre-existing free row. The caller
        (`cli.py`'s `record_dispatch`) used to just let that rollback stand
        and trust a comment claiming the lane would read UNKNOWN; it does
        not, it reads FREE, and the next dispatch clobbers a lane that is
        actually working.

        This closes that window with an explicit write instead of an absence
        of one: a placeholder task, status `created`, inserted for `lane` in
        its own transaction. `lane_available`'s occupied branch -- an
        outstanding task owns the lane -- becomes true immediately, and
        stays true until a human reconciles it (`cli.py register` re-issues
        the lane a clean identity, cancelling this placeholder the same way
        `register_lane` cancels any other stale outstanding task) or a later
        dispatch overwrites it, exactly the recovery `record_dispatch`'s own
        failure message already promises.

        Two cases need no placeholder and are both safe no-ops:
        * The lane is unknown to the ledger (never registered, or backfill
          itself never ran) -- `lane_available` already returns `None` there,
          and `None` is not `True`, so there is nothing to close.
        * The lane already carries an outstanding task -- the
          `one_open_task_per_lane` index refuses the INSERT, which only
          happens when something else already makes this lane read
          occupied, the exact state this method exists to guarantee.
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            lane_row = connection.execute("SELECT nonce FROM lanes WHERE lane = ?", (lane,)).fetchone()
            if lane_row is None:
                return None
            task_id = f"ledger-hold:{lane}:{now}"
            try:
                connection.execute(
                    """
                    INSERT INTO tasks(id, lane, pane_nonce, summary, status, created_at, updated_at)
                    VALUES (?, ?, ?, ?, 'created', ?, ?)
                    """,
                    (task_id, lane, lane_row["nonce"], f"ledger record failed: {note}", now, now),
                )
            except sqlite3.IntegrityError:
                return None
            row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            return self._dict(row)

    def claim_lane(self, lane, *, token, note="dispatch claim", owner=None):
        """Atomically reserve `lane` for `token`, or refuse if it is not free.

        agent-dotfiles#184. `lane_free` (the read `dispatch.sh` uses to pick a
        candidate -- see that function's own docstring) is a QUERY, not a
        claim: two dispatchers can both read the same lane free and both
        proceed into the rest of the dispatch pipeline (claim the issue,
        build the worktree, send-keys) before either calls `record_dispatch`.
        `record_dispatch` does not arbitrate between them either -- its
        `_register_lane_tx` treats every call as a new pane incarnation
        (`cli.py`'s `record_dispatch` mints a fresh nonce every call) and
        CANCELS whatever task was outstanding rather than refusing, so the
        second writer always wins silently (measured, #183 round 3).

        This closes the gap the same way `mark_lane_held` (#188) already
        forces `lane_available` to read occupied: insert a placeholder task,
        status `created`, for `lane`, protected by the same
        `one_open_task_per_lane` unique index the rest of this table already
        relies on. Two processes calling this for the same lane are
        serialized by `_locked` (an flock held across the whole
        check-and-insert) plus SQLite's own `BEGIN IMMEDIATE` -- the second
        to acquire the lock finds the first row already committed and its
        INSERT raises `IntegrityError`, so it is refused, not merged.

        Returns a dict with `claimed`: True only when `token` itself now
        owns the lane -- the re-read after the write is the verify half of
        claim-then-verify, not a redundant check: it is what lets a caller
        trust "the value read back is mine" instead of assuming its own
        write could not have lost a race it was never actually exposed to.

        agent-dotfiles#209: `owner` is the claiming PROCESS's identity
        (`claim_owner_token`), recorded so that a claim whose owner died
        before it could release can be told apart from one still in flight.
        Optional, and its absence is not an error -- a claim with no owner is
        simply never reaped automatically, which is exactly the behaviour
        every claim had before #209. `dispatch.sh` always passes one.
        """
        now = int(self.clock())
        summary = note if owner is None else f"{note} [owner={owner}]"
        with self._locked(), self._transaction() as connection:
            lane_row = connection.execute("SELECT nonce FROM lanes WHERE lane = ?", (lane,)).fetchone()
            if lane_row is None:
                return {"lane": lane, "claimed": False, "reason": "unknown"}
            task_id = f"{CLAIM_TASK_PREFIX}{lane}:{token}"
            try:
                connection.execute(
                    """
                    INSERT INTO tasks(id, lane, pane_nonce, summary, status, created_at, updated_at)
                    VALUES (?, ?, ?, ?, 'created', ?, ?)
                    """,
                    (task_id, lane, lane_row["nonce"], summary, now, now),
                )
            except sqlite3.IntegrityError:
                # agent-supervisor#174. `id` is this table's PRIMARY KEY, and
                # `dispatch.sh` derives `token` from the window name -- stable
                # for a given issue/lane pairing, so a RETRY recomputes the
                # exact `task_id` an earlier, now-finished dispatch already
                # used. That earlier row is not deleted when it closes (only
                # `release_lane_claim`'s still-RESERVED case deletes; a claim
                # that reached a real dispatch is cancelled in place by
                # `_register_lane_tx`, same as any completed task), so it is
                # still sitting on this id, just terminal.
                #
                # Two different collisions raise the same `IntegrityError` and
                # must not be handled alike: this PRIMARY KEY collision on
                # `task_id` itself (this caller's own dead token, safe to
                # reuse) versus the `one_open_task_per_lane` partial index
                # (a DIFFERENT task, still active, genuinely occupying the
                # lane). Telling them apart is the fix: a dead row under this
                # exact id is revived as this call's own fresh reservation,
                # so the SELECT below finds it and this claim succeeds; an
                # index collision leaves no row at this id, so the row that
                # collided is a stranger's active claim and the code below
                # falls through to reporting occupied, with that stranger
                # correctly named as holder.
                #
                # Silently ignoring the collision either way -- the previous
                # behaviour -- made the two indistinguishable: the SELECT
                # after an ignored PK collision finds no active row for the
                # lane at all (there is none; it is free) and reported
                # `occupied` with `holder: None`, which is the defect this
                # exists to close. Not a weakening of the check: a lane that
                # is genuinely held (the index-collision case) is refused
                # exactly as before, still naming its real holder.
                existing = connection.execute(
                    "SELECT status FROM tasks WHERE id = ?", (task_id,)
                ).fetchone()
                if existing is not None and existing["status"] in ("complete", "failed", "cancelled"):
                    # agent-supervisor#182. A terminal placeholder does not
                    # mean the LANE is free -- the real dispatch that
                    # superseded it (`record_dispatch`'s `_register_lane_tx`
                    # cancels the placeholder in place rather than deleting
                    # it) can still be genuinely active under a different
                    # task id. Revive-and-succeed only reaches the row above
                    # via `IntegrityError`, which means SOMETHING collided;
                    # if it was `one_open_task_per_lane` rather than this
                    # row's own PRIMARY KEY, that different task is the true
                    # cause and the UPDATE below would raise the exact same
                    # index collision a second time, uncaught. Check first
                    # and, if the lane is genuinely held by someone else,
                    # skip the revival entirely -- the fall-through SELECT
                    # reports the real holder, the same refusal this call
                    # gave before #175.
                    blocking = connection.execute(
                        "SELECT id FROM tasks WHERE lane = ? AND id != ? "
                        "AND status NOT IN ('complete', 'failed', 'cancelled')",
                        (lane, task_id),
                    ).fetchone()
                    if blocking is None:
                        connection.execute(
                            """
                            UPDATE tasks SET status = 'created', summary = ?, pane_nonce = ?,
                                             created_at = ?, updated_at = ?, result_path = NULL,
                                             result_sha256 = NULL, delivery_attempted_at = NULL,
                                             delivered_at = NULL, accepted_at = NULL, completed_at = NULL
                            WHERE id = ?
                            """,
                            (summary, lane_row["nonce"], now, now, task_id),
                        )
            row = connection.execute(
                "SELECT id FROM tasks WHERE lane = ? AND status NOT IN ('complete', 'failed', 'cancelled')",
                (lane,),
            ).fetchone()
            if row is None or row["id"] != task_id:
                return {"lane": lane, "claimed": False, "reason": "occupied", "holder": row["id"] if row else None}
            return {"lane": lane, "claimed": True, "reason": None, "token": token}

    def commit_lane_claim(self, lane, *, token):
        """Mark a claim as having a LIVE brief behind it, so no cleanup frees it.

        agent-dotfiles#209 round 2. `claim_lane` reserves a lane; this is the
        point of no return, and `dispatch.sh` calls it IMMEDIATELY BEFORE the
        `send-keys Enter` that submits the brief into the pane. After it
        returns `committed`, the lane may be working, and both cleanup paths
        stop being allowed to touch the row:

        * `release_lane_claim` (the dispatcher's own EXIT/TERM/INT trap) is
          scoped to `CLAIM_STATUS_RESERVED` and matches nothing here.
        * `reap_stale_lane_claims` selects only `CLAIM_STATUS_RESERVED` rows,
          so a LIVE claim survives even when its owner pid is provably gone --
          which is the case the reap's liveness rule ALONE cannot decide.
          "The owner is dead" does not distinguish "claim taken, nothing sent"
          from "claim taken, brief live in the pane"; before this existed both
          were `created` with a dead owner, and the reap cleared both.

        This has to be a LEDGER fact, not a flag in the dispatcher, for the
        SIGKILL case specifically: a bash variable dies with the process that
        set it, and at that instant this row is the only record that the lane
        is occupied at all -- `record_dispatch` has not run yet.

        Idempotent: committing an already-LIVE claim succeeds and changes
        nothing, so a retry cannot walk the row backwards.

        Refuses rather than invents state. A claim that is missing (never
        made, or already released) is not committed into existence here --
        `dispatch.sh` treats that refusal as fatal and does not send, which is
        the fail-closed direction: nothing has gone into the pane yet, so
        unwinding is still free.
        """
        now = int(self.clock())
        task_id = f"{CLAIM_TASK_PREFIX}{lane}:{token}"
        with self._locked(), self._transaction() as connection:
            row = connection.execute(
                "SELECT status FROM tasks WHERE id = ? AND lane = ?", (task_id, lane)
            ).fetchone()
            if row is None:
                return {"lane": lane, "committed": False, "reason": "missing", "token": token}
            if row["status"] == CLAIM_STATUS_LIVE:
                return {"lane": lane, "committed": True, "reason": None, "token": token}
            if row["status"] != CLAIM_STATUS_RESERVED:
                return {"lane": lane, "committed": False, "reason": row["status"], "token": token}
            connection.execute(
                "UPDATE tasks SET status = ?, updated_at = ?, delivered_at = ? WHERE id = ?",
                (CLAIM_STATUS_LIVE, now, now, task_id),
            )
            return {"lane": lane, "committed": True, "reason": None, "token": token}

    def release_lane_claim(self, lane, *, token):
        """Undo a `claim_lane` this same caller made, and ONLY that claim.

        DELETES the placeholder row rather than marking it `cancelled`: this
        row was never a real dispatch, only a reservation, and dispatch.sh
        calls this on every abort path a dispatch can take -- a worktree
        that fails to build, a message over budget, a brief that never
        submits. None of those should leave anything behind for `cli.py
        status` to show; the abort tests in test_dispatch.sh assert exactly
        `"tasks":[]` in cases like these, same as before this claim step
        existed. A soft-cancel would reintroduce a row those cases never
        used to have.

        Scoped to `(lane, id=ledger-claim:{lane}:{token}, status='created')`
        so this can never touch a real dispatch that has since taken the
        lane over fairly: `record_dispatch`'s own `_register_lane_tx`
        already cancels this placeholder the moment a dispatch it belongs to
        actually lands (any identity change cancels whatever was
        outstanding), so by the time that has happened this DELETE matches
        no row and is a safe no-op -- the caller does not need to know
        which case it is in before calling this.

        agent-dotfiles#209 round 2: that `status` scope is now load-bearing in
        a second way, and it is not incidental. `CLAIM_STATUS_RESERVED` means
        nothing has been sent into the pane; once `commit_lane_claim` has
        moved the row to `CLAIM_STATUS_LIVE` this DELETE deliberately matches
        nothing, because a brief is live behind that claim and freeing it
        would hand a working lane to the next dispatcher. The dispatcher's
        trap calls this unconditionally on every exit including a signal, so
        this scope -- not the caller's own bookkeeping -- is what makes that
        safe.
        """
        task_id = f"{CLAIM_TASK_PREFIX}{lane}:{token}"
        with self._locked(), self._transaction() as connection:
            cursor = connection.execute(
                "DELETE FROM tasks WHERE id = ? AND lane = ? AND status = ?",
                (task_id, lane, CLAIM_STATUS_RESERVED),
            )
            # Returns whether a row actually went away, so a CALLER can report
            # what happened instead of what it attempted. `dispatch.sh`'s trap
            # ignores it -- every no-op case there is expected -- but the
            # refusal text points an OPERATOR at this command, and a CLI that
            # printed `released: true` after matching nothing would tell them
            # a lane was freed when it was not. That is the message/state
            # mismatch #145, #170 and #188 were each filed over.
            return cursor.rowcount > 0

    def reap_stale_lane_claims(self, *, host=None, is_alive=None):
        """Delete `claim_lane` placeholders whose owning process is provably gone.

        agent-dotfiles#209. `claim_lane` + `release_lane_claim` + `dispatch.sh`'s
        EXIT/TERM/INT trap cover every exit a dispatcher can OBSERVE. SIGKILL,
        an OOM kill and a host crash are not observable -- bash cannot trap
        them (`inbox-poll.sh:200` says the same thing about its own EXIT trap)
        -- and a dispatcher lost that way leaves its placeholder behind with
        status `created`. `lane_available` counts any non-terminal status as
        occupied, so that lane reads occupied forever: agent-dotfiles#102's
        failure shape (dispatch capacity silently falling to zero while lanes
        sit idle) arriving through the mechanism built to prevent it.

        This is the untrappable half of the cleanup, and it is deliberately
        NOT a TTL. A TTL short enough to be useful can expire while a real
        dispatch is still running -- worktree creation, the settle sleeps,
        `DISPATCH_CONFIRM_TRIES` x `DISPATCH_SETTLE` (10s by default, and a
        slow harness makes it longer) -- and reaping a LIVE dispatcher's claim
        would reopen the very race #184 closed. Elapsed time cannot tell a
        slow dispatch from a dead one; a pid can. So expiry here is a liveness
        question with no clock in it at all, and it cannot fire early:

        * Only rows under `CLAIM_TASK_PREFIX` are considered. `ledger-hold:`
          rows (`mark_lane_held`, #188) are deliberate holds waiting on a
          human and are never touched, nor is any real dispatch task.
        * Only rows whose recorded owner host matches THIS host. A ledger
          reached from more than one machine cannot have its claims judged
          from here, and an unrecognisable owner is left alone.
        * Only rows whose owner pid is provably gone -- see `pid_is_alive`,
          which resolves every ambiguity as "alive". A recycled pid means a
          claim is not reaped; it never means a live one is.
        * Only rows still in `CLAIM_STATUS_RESERVED` (agent-dotfiles#209 round
          2). This is the constraint a liveness rule cannot supply on its own:
          a SIGKILLed dispatcher's pid is provably gone whether it died before
          sending anything or a microsecond after its brief went live in the
          pane, and in the second case this placeholder is the ONLY record
          that the lane is occupied, because `record_dispatch` never ran. Both
          looked identical -- `created`, dead owner -- so the reap cleared
          both, and the next dispatcher typed a whole second brief into a pane
          that was already working. `commit_lane_claim` writes the fact that
          tells them apart, before the send rather than after it, and a LIVE
          claim is left for the documented manual path instead.

        That is the one-way ratchet of #124/#126 held: this can make a lane
        available only by clearing a claim whose owner no longer exists AND
        which never got as far as putting a brief in front of a worker.

        DELETEs rather than cancels, for `release_lane_claim`'s reason: the
        row was a reservation, never a dispatch, and the abort cases in
        test_dispatch.sh assert an empty task list. Returns the reaped rows,
        so a caller can say out loud what it cleared.
        """
        host = host or socket.gethostname()
        alive = pid_is_alive if is_alive is None else is_alive
        reaped = []
        with self._locked(), self._transaction() as connection:
            rows = connection.execute(
                "SELECT * FROM tasks WHERE status = ? AND id LIKE ? ORDER BY id",
                (CLAIM_STATUS_RESERVED, f"{CLAIM_TASK_PREFIX}%"),
            ).fetchall()
            for row in rows:
                match = CLAIM_OWNER_RE.search(row["summary"] or "")
                if match is None or match.group("host") != host:
                    continue
                if alive(int(match.group("pid"))):
                    continue
                connection.execute("DELETE FROM tasks WHERE id = ?", (row["id"],))
                record = self._dict(row)
                record["owner"] = f"{match.group('host')}:{match.group('pid')}"
                reaped.append(record)
        return reaped

    def take_supervisor_lease(self, *, owner, note="supervisor loop"):
        """Atomically take the single `supervisor` role, or refuse and name the holder.

        agent-dotfiles#238. Mirrors `claim_lane`'s INSERT-or-refuse shape
        (INSERT under `_locked`+`BEGIN IMMEDIATE`; the second writer's INSERT
        raises `IntegrityError` and is refused, never merged) but against the
        singleton `supervisor_lease` row rather than a per-lane placeholder --
        see that table's own comment for why lane claims are not reused
        directly. `owner` is `claim_owner_token(pid)` (`host:pid`), always
        required: an unowned lease can never be told apart from a dead one, so
        `reap_stale_supervisor_lease` below would never have anything to
        compare against.

        Returns `{"leased": True, "owner": owner}` on success, or
        `{"leased": False, "holder": <existing owner>}` when another owner
        already holds it -- including when THIS `owner` already holds it
        (idempotent by identity, not `True` for a stranger).
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            try:
                connection.execute(
                    "INSERT INTO supervisor_lease(id, owner, note, taken_at, updated_at) VALUES (?, ?, ?, ?, ?)",
                    (SUPERVISOR_LEASE_ID, owner, note, now, now),
                )
            except sqlite3.IntegrityError:
                row = connection.execute(
                    "SELECT owner FROM supervisor_lease WHERE id = ?", (SUPERVISOR_LEASE_ID,)
                ).fetchone()
                holder = row["owner"] if row is not None else None
                return {"leased": holder == owner, "holder": holder, "owner": owner}
            return {"leased": True, "holder": owner, "owner": owner}

    def supervisor_lease(self):
        """The current lease row, or `None` if nobody holds it."""
        with self._locked(), self._transaction() as connection:
            row = connection.execute(
                "SELECT * FROM supervisor_lease WHERE id = ?", (SUPERVISOR_LEASE_ID,)
            ).fetchone()
            return None if row is None else self._dict(row)

    def release_supervisor_lease(self, *, owner):
        """Undo a `take_supervisor_lease` this same `owner` holds, and ONLY that one.

        Scoped to `owner` for the same reason `release_lane_claim` is scoped
        to `token`: a graceful shutdown always releases its own lease, but the
        DELETE must never be able to clear a lease some OTHER, still-live
        owner holds -- e.g. a caller that raced `take_supervisor_lease`,
        lost, and cleans up anyway. Returns whether a row actually went away.
        """
        with self._locked(), self._transaction() as connection:
            cursor = connection.execute(
                "DELETE FROM supervisor_lease WHERE id = ? AND owner = ?",
                (SUPERVISOR_LEASE_ID, owner),
            )
            return cursor.rowcount > 0

    def reap_stale_supervisor_lease(self, *, host=None, is_alive=None):
        """Clear the supervisor lease iff its owning process is provably gone.

        agent-dotfiles#238. The other half of the property this table exists
        for: a lease that outlives a crashed supervisor with no way to
        reclaim it would make the estate strictly worse than two dispatchers
        -- it could never be restarted at all. Same liveness rule and same
        one-way ratchet as `reap_stale_lane_claims` (see that method's
        docstring): no TTL, because elapsed time cannot tell a supervisor
        mid-tool-call from a dead one, only a pid can; only the recorded
        owner's HOST is compared, and only when its PID is provably gone
        (`pid_is_alive` resolves every ambiguity -- a foreign-host owner, a
        permission error, a recycled pid -- as alive, never as dead).

        Returns the reaped row (with `owner` split back out for the caller's
        log line) or `None` if there was nothing to reap, including when the
        held lease's owner is still alive.
        """
        host = host or socket.gethostname()
        alive = pid_is_alive if is_alive is None else is_alive
        with self._locked(), self._transaction() as connection:
            row = connection.execute(
                "SELECT * FROM supervisor_lease WHERE id = ?", (SUPERVISOR_LEASE_ID,)
            ).fetchone()
            if row is None:
                return None
            match = re.match(r"^(?P<host>[^\]\s:]+):(?P<pid>[1-9][0-9]*)$", row["owner"] or "")
            if match is None or match.group("host") != host:
                return None
            if alive(int(match.group("pid"))):
                return None
            connection.execute("DELETE FROM supervisor_lease WHERE id = ?", (SUPERVISOR_LEASE_ID,))
            record = self._dict(row)
            record["reaped_host"] = match.group("host")
            record["reaped_pid"] = match.group("pid")
            return record
