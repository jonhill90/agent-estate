"""`Ledger`'s terminal-result writes: `_write_result` (the shared result-file
writer) and the four disposition methods (`complete`, `fail_unaccepted`,
`fail_stale_delivery`, `fail_stale_acceptance`).

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib
import hashlib
import os
import tempfile
import time

from core_constants import MAX_RESULT_BYTES  # noqa: F401 -- re-exported by core.py


class LedgerCompletionMixin:
    def _write_result(self, task_id, result, *, suffix="", known_hash=None):
        """`suffix` (agent-supervisor#401) lets a caller that is NOT the
        lane's own report -- `fail_unaccepted`/`fail_stale_delivery`
        stamping a verdict the lane never asserted itself -- land in a
        SIBLING file (`<task_id><suffix>.md`) instead of claiming
        `<task_id>.md`. That path is otherwise the one and only slot a
        genuine, late `complete()` from the lane itself can ever write to:
        this method is immutable-once, so if the reaper's fabricated note
        gets there first, the lane's real report is later rejected outright
        (`immutable result conflicts with existing content`) instead of
        ever being persisted anywhere -- see agent-supervisor#401's
        ad275-fix275 specimen, where exactly that already-free canonical
        slot exists to be squatted on. `complete()` never passes a suffix:
        an OBSERVED completion is what `<task_id>.md` is for.

        `known_hash` (agent-supervisor#623): the ROW's own already-recorded
        `result_sha256`, fetched by the caller before this write, or `None`
        if the row has never recorded one. This is what tells a genuine
        overwrite attempt (the row already has a recorded result and the
        caller now supplies different bytes -- still refuse, that is what
        the immutability guard is for) apart from an ORPHANED file: one
        that is already on disk but that no row has ever recorded a hash
        for, because a prior call to this same method wrote it and then
        crashed before the row update that follows a write ever ran
        (`complete()`/`fail_unaccepted`/etc. each write the file, then
        update the row, in that order, and are not atomic across the two).
        In the orphan case the file on disk IS the lane's own genuine
        result -- there is no recorded hash to compare the caller's bytes
        against, so refusing would strand the task forever (#623) even
        though nothing has actually been overwritten. Adopt the file's own
        hash instead of raising.
        """
        if not isinstance(result, bytes):
            raise TypeError("result must be bytes")
        if not result.strip():
            raise ValueError("result must be non-empty")
        if len(result) > MAX_RESULT_BYTES:
            raise ValueError("result exceeds 64 KiB limit")
        digest = hashlib.sha256(result).hexdigest()
        destination = self.results_dir / f"{task_id}{suffix}.md"
        if destination.exists():
            existing_digest = hashlib.sha256(destination.read_bytes()).hexdigest()
            if existing_digest != digest:
                if known_hash is not None:
                    raise ValueError("immutable result conflicts with existing content")
                # No row has ever recorded a result for this task -- the file
                # on disk predates any completion this ledger row knows
                # about and is the lane's genuine, orphaned output. Adopt it
                # rather than treat the caller's bytes as canonical.
                return destination, existing_digest
            return destination, digest
        descriptor, temporary_name = tempfile.mkstemp(prefix=f".{task_id}{suffix}.", dir=self.results_dir)
        try:
            with os.fdopen(descriptor, "wb") as handle:
                handle.write(result)
                handle.flush()
                os.fsync(handle.fileno())
            os.chmod(temporary_name, 0o600)
            try:
                os.link(temporary_name, destination)
            except FileExistsError:
                if hashlib.sha256(destination.read_bytes()).hexdigest() != digest:
                    raise ValueError("immutable result conflicts with concurrent content")
            os.chmod(destination, 0o600)
        finally:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(temporary_name)
        return destination, digest

    @staticmethod
    def _fail(failpoint, expected):
        if failpoint == expected:
            raise RuntimeError(expected)

    def complete(self, task_id, result, *, pane_nonce, failpoint=None, allow_claim=False):
        self._require_task_id(task_id, allow_claim=allow_claim)
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if existing is None:
                    raise ValueError("unknown task")
                if existing["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
            destination, digest = self._write_result(task_id, result, known_hash=existing["result_sha256"])
            self._fail(failpoint, "after_result")
            now = int(self.clock())
            with self._transaction() as connection:
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if row is None:
                    raise ValueError("unknown task")
                if row["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
                if row["status"] == "complete":
                    if row["result_sha256"] != digest:
                        raise ValueError("immutable result conflicts with completed task")
                    return self._dict(row)
                if row["status"] in ("failed", "cancelled"):
                    raise ValueError(f"cannot complete {row['status']} task")
                connection.execute(
                    """
                    UPDATE tasks SET status='complete', result_path=?, result_sha256=?,
                                     updated_at=?, completed_at=? WHERE id=?
                    """,
                    (str(destination), digest, now, now, task_id),
                )
                self._fail(failpoint, "after_task")
                connection.execute(
                    """
                    INSERT OR IGNORE INTO events(key, type, task_id, status, payload_path, created_at)
                    VALUES (?, 'completion', ?, 'pending', ?, ?)
                    """,
                    (f"completion:{task_id}", task_id, str(destination), now),
                )
                self._fail(failpoint, "after_event")
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            return self._dict(row)

    def fail_unaccepted(self, task_id, result, *, pane_nonce, failpoint=None, allow_claim=False):
        """Terminate a `delivered` task that shows no evidence of acceptance.

        agent-supervisor#193. `reconcile_lane_completions.py` used to treat
        "lane free, idle past the dwell" as evidence a delivered task was
        done -- `at25-rev33`'s brief landed as noise a harness discarded as
        an unknown command, the lane went quiet exactly like a finished one,
        and the sweep certified it `complete`. `accepted_at` is the fact
        that distinguishes the two: nothing sets it except `record_dispatch`
        confirming its own send actually landed (see that method's
        docstring). A `delivered` task the sweep finds idle with no
        `accepted_at` gets THIS terminal state instead -- `failed`, not
        `complete` -- so the ledger records what is actually known (this
        dispatch was never seen to start) rather than asserting the work
        finished. `failed` is still terminal (`one_open_task_per_lane`
        excludes it), so the lane is free for a fresh dispatch; nothing here
        claims to know why the send never took, only that it never did.

        Mirrors `complete()`'s shape deliberately -- same immutable-result
        write, same idempotency, same pane-nonce check -- so a caller reading
        one already knows what the other does. The two ways this MUST differ
        from `complete()`: the source status is checked (only `delivered`,
        and only with `accepted_at` still NULL, is eligible -- anything else
        means the caller's own evidence is stale or wrong, and this refuses
        rather than guess), and the terminal status written is `failed`.

        agent-supervisor#401: a third way it must differ -- `_write_result`
        is given `suffix=".reconcile"`, so this verdict lands at
        `<task_id>.reconcile.md`, never `<task_id>.md`. That canonical slot
        is reserved for the LANE's own report; leaving it free means a late,
        genuine `complete()` call still gets to write its real content
        there instead of being rejected by the immutability check against a
        note this method fabricated, or worse, being unable to write at all
        because a DIFFERENT verdict already got there first.
        """
        self._require_task_id(task_id, allow_claim=allow_claim)
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if existing is None:
                    raise ValueError("unknown task")
                if existing["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
            destination, digest = self._write_result(
                task_id, result, suffix=".reconcile", known_hash=existing["result_sha256"]
            )
            self._fail(failpoint, "after_result")
            now = int(self.clock())
            with self._transaction() as connection:
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if row is None:
                    raise ValueError("unknown task")
                if row["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
                if row["status"] == "failed":
                    if row["result_sha256"] != digest:
                        raise ValueError("immutable result conflicts with already-failed task")
                    return self._dict(row)
                if row["status"] in ("complete", "cancelled"):
                    raise ValueError(f"cannot fail-unaccepted a {row['status']} task")
                if row["status"] != "delivered":
                    raise ValueError(f"cannot fail-unaccepted a {row['status']} task -- only 'delivered' is eligible")
                if row["accepted_at"] is not None:
                    raise ValueError("task has accepted_at set -- it WAS accepted, use complete() instead")
                connection.execute(
                    """
                    UPDATE tasks SET status='failed', result_path=?, result_sha256=?,
                                     updated_at=?, completed_at=? WHERE id=?
                    """,
                    (str(destination), digest, now, now, task_id),
                )
                self._fail(failpoint, "after_task")
                connection.execute(
                    """
                    INSERT OR IGNORE INTO events(key, type, task_id, status, payload_path, created_at)
                    VALUES (?, 'completion', ?, 'pending', ?, ?)
                    """,
                    (f"completion:{task_id}", task_id, str(destination), now),
                )
                self._fail(failpoint, "after_event")
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            return self._dict(row)

    def fail_stale_delivery(self, task_id, result, *, pane_nonce, failpoint=None, allow_claim=False):
        """Terminate a `delivered` task whose lane has no pane to observe at all.

        agent-supervisor#374. `fail_unaccepted` and `_complete_observed`
        (`reconcile_lane_completions.py`) both resolve a stuck `delivered`
        row from a POSITIVE pane observation -- "free right now", "free for
        N seconds". A `claude-print`/`pi-rpc` lane has no such pane to poll
        in between: `ClaudePrintAdapter`'s own docstring is explicit that
        "there is no long-lived protocol session to resume mid-call" -- the
        process either is still the one live turn that will itself call
        `complete`, or it has already exited, in which case there will NEVER
        be a pane transition for anything to observe. `_parse_lane` already
        reflects this: a lane id of this shape does not match
        `<session>:<index>` and every existing sweep routes it straight to
        `unresolved` forever -- which is exactly how 101 rows went stuck
        (agent-supervisor#374's own count).

        The only fact available for this transport class is wall-clock
        dwell since the row's own `updated_at` -- not a pane reading, an
        ABSENCE of one. That is weaker evidence than `_complete_observed`'s
        (a `free` pane is a positive fact; long silence from a transport
        that was never polled is not), so this never asserts success: the
        terminal status is always `failed`, regardless of `accepted_at` --
        unlike `fail_unaccepted`, which exists specifically for the
        never-accepted case and refuses outright if `accepted_at` IS set.
        A `delivered` row can reach here with `accepted_at` set (the lane
        picked up the brief and then never called `complete`) or NULL (it
        never even got that far); both are terminal here because for this
        transport neither can be distinguished from "genuinely still
        running" except by how long it has been silent, and by the time a
        caller reaches for this method that bound has already been checked.

        Mirrors `fail_unaccepted`'s shape otherwise: same immutable-result
        write, same idempotency, same pane-nonce check against the row's
        OWN recorded nonce (not the lane's current one -- the lane may be
        long gone, same reasoning as `_reconcile_transition`).

        agent-supervisor#401: also mirrors `fail_unaccepted`'s
        `suffix=".reconcile"` -- this is the exact path 101 of the 133
        wrong results (agent-supervisor#401) came through, since every
        claude-print/pi-rpc lane routes here. Writing to `<task_id>.md`
        directly is what let this method's verdict become the estate's
        ONLY record of a task whose lane never got the chance to write its
        own -- see `_write_result`'s docstring.
        """
        self._require_task_id(task_id, allow_claim=allow_claim)
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if existing is None:
                    raise ValueError("unknown task")
                if existing["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
            destination, digest = self._write_result(
                task_id, result, suffix=".reconcile", known_hash=existing["result_sha256"]
            )
            self._fail(failpoint, "after_result")
            now = int(self.clock())
            with self._transaction() as connection:
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if row is None:
                    raise ValueError("unknown task")
                if row["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
                if row["status"] == "failed":
                    if row["result_sha256"] != digest:
                        raise ValueError("immutable result conflicts with already-failed task")
                    return self._dict(row)
                if row["status"] in ("complete", "cancelled"):
                    raise ValueError(f"cannot fail-stale-delivery a {row['status']} task")
                if row["status"] != "delivered":
                    raise ValueError(
                        f"cannot fail-stale-delivery a {row['status']} task -- only 'delivered' is eligible"
                    )
                connection.execute(
                    """
                    UPDATE tasks SET status='failed', result_path=?, result_sha256=?,
                                     updated_at=?, completed_at=? WHERE id=?
                    """,
                    (str(destination), digest, now, now, task_id),
                )
                self._fail(failpoint, "after_task")
                connection.execute(
                    """
                    INSERT OR IGNORE INTO events(key, type, task_id, status, payload_path, created_at)
                    VALUES (?, 'completion', ?, 'pending', ?, ?)
                    """,
                    (f"completion:{task_id}", task_id, str(destination), now),
                )
                self._fail(failpoint, "after_event")
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            return self._dict(row)

    def fail_stale_acceptance(self, task_id, result, *, pane_nonce, failpoint=None, allow_claim=False):
        """Terminate an `accepted` task whose lane has no pane to observe at all.

        agent-supervisor#414, `fail_stale_delivery`'s (#374) sibling for the
        state that method's own eligibility check refuses: it requires
        `status == 'delivered'`, which is exactly wrong for a row that has
        already moved to `'accepted'` -- see `list_accepted_open_tasks`'s
        docstring for why that transition happens at all for a no-pane
        lane and why it is otherwise invisible to every existing sweep.
        Same absence-not-observation posture as `fail_stale_delivery`: the
        only fact available is wall-clock dwell since `updated_at`, so the
        terminal status is always `failed`, never `complete`.

        Mirrors `fail_stale_delivery`'s shape otherwise: same immutable-result
        write, same idempotency, same pane-nonce check against the row's OWN
        recorded nonce.

        agent-supervisor#401 (found late, in review of the #401 fix itself):
        also mirrors `fail_unaccepted`/`fail_stale_delivery`'s
        `suffix=".reconcile"` -- writing straight to `<task_id>.md` here was
        the same trap those two already closed: a late, genuine `complete()`
        from an accepted claude-print/pi-rpc lane would find the canonical
        slot already claimed by this method's fabricated verdict.
        """
        self._require_task_id(task_id, allow_claim=allow_claim)
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if existing is None:
                    raise ValueError("unknown task")
                if existing["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
            destination, digest = self._write_result(
                task_id, result, suffix=".reconcile", known_hash=existing["result_sha256"]
            )
            self._fail(failpoint, "after_result")
            now = int(self.clock())
            with self._transaction() as connection:
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
                if row is None:
                    raise ValueError("unknown task")
                if row["pane_nonce"] != pane_nonce:
                    raise ValueError("pane incarnation does not match task")
                if row["status"] == "failed":
                    if row["result_sha256"] != digest:
                        raise ValueError("immutable result conflicts with already-failed task")
                    return self._dict(row)
                if row["status"] in ("complete", "cancelled"):
                    raise ValueError(f"cannot fail-stale-acceptance a {row['status']} task")
                if row["status"] != "accepted":
                    raise ValueError(
                        f"cannot fail-stale-acceptance a {row['status']} task -- only 'accepted' is eligible"
                    )
                connection.execute(
                    """
                    UPDATE tasks SET status='failed', result_path=?, result_sha256=?,
                                     updated_at=?, completed_at=? WHERE id=?
                    """,
                    (str(destination), digest, now, now, task_id),
                )
                self._fail(failpoint, "after_task")
                connection.execute(
                    """
                    INSERT OR IGNORE INTO events(key, type, task_id, status, payload_path, created_at)
                    VALUES (?, 'completion', ?, 'pending', ?, ?)
                    """,
                    (f"completion:{task_id}", task_id, str(destination), now),
                )
                self._fail(failpoint, "after_event")
                row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            return self._dict(row)
