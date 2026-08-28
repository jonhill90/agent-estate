"""`Ledger`'s dispatch -> accept task lifecycle writes: assign, the
delivery/acceptance transitions, reconciliation, cancel-open-task, and
`record_dispatch` (the dispatcher's single entry point).

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib
import re
import sqlite3
import time


class LedgerLifecycleMixin:
    def _assign_tx(self, connection, *, task_id, lane, pane_nonce, summary, now, worktree_path=""):
        self._require_task_id(task_id)
        if not summary.strip():
            raise ValueError("task summary must be non-empty")
        # agent-supervisor#631: the lane's CURRENT pane_id, read from the
        # same `lanes` row `_verify_lane_nonce` already fetches for the
        # nonce check -- frozen onto this task row below so this task's own
        # identity never depends on that `lanes` row surviving unmodified
        # after a later dispatch reuses the same lane STRING for a
        # different, unrelated pane (`renumber-windows on` reassigning a
        # closed window's index; see the `tasks.pane_id` column comment in
        # `_initialize`).
        lane_pane_id = self._verify_lane_nonce(connection, lane, pane_nonce) or ""
        source = connection.execute("SELECT * FROM source_tasks WHERE id = ?", (task_id,)).fetchone()
        if source is None:
            raise ValueError("task requires a reconstructed GitHub source")
        if source["source_state"].upper() != "OPEN":
            raise ValueError("GitHub source is not open")
        if source["status"] != "created":
            raise ValueError(f"GitHub source is already {source['status']}")
        existing = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        if existing is not None:
            if existing["lane"] == lane and existing["pane_nonce"] == pane_nonce and existing["summary"] == summary:
                return self._dict(existing)
            raise ValueError("task id already exists with different assignment")
        try:
            connection.execute(
                """
                INSERT INTO tasks(id, lane, pane_nonce, summary, status, created_at, updated_at, worktree_path, pane_id)
                VALUES (?, ?, ?, ?, 'created', ?, ?, ?, ?)
                """,
                (task_id, lane, pane_nonce, summary, now, now, worktree_path, lane_pane_id),
            )
        except sqlite3.IntegrityError as error:
            if "tasks.lane" in str(error):
                raise ValueError(f"lane has an outstanding task: {lane}") from error
            raise
        row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        return self._dict(row)

    def assign(self, *, task_id, lane, pane_nonce, summary, worktree_path=""):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._assign_tx(
                connection,
                task_id=task_id,
                lane=lane,
                pane_nonce=pane_nonce,
                summary=summary,
                now=now,
                worktree_path=worktree_path,
            )

    def _transition_tx(self, connection, task_id, pane_nonce, allowed, target, timestamp_column, now):
        row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        if row is None:
            raise ValueError("unknown task")
        self._verify_lane_nonce(connection, row["lane"], pane_nonce)
        if row["pane_nonce"] != pane_nonce:
            raise ValueError("pane incarnation does not match task")
        if row["status"] == target:
            return self._dict(row)
        if row["status"] not in allowed:
            raise ValueError(f"cannot transition task from {row['status']} to {target}")
        connection.execute(
            f"UPDATE tasks SET status = ?, updated_at = ?, {timestamp_column} = ? WHERE id = ?",
            (target, now, now, task_id),
        )
        row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        return self._dict(row)

    def _transition(self, task_id, pane_nonce, allowed, target, timestamp_column):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._transition_tx(connection, task_id, pane_nonce, allowed, target, timestamp_column, now)

    def mark_delivery_pending(self, task_id, *, pane_nonce):
        """Persist an ambiguous, non-resendable state before the physical send.

        A task in this state has had delivery attempted but not confirmed. It
        is deliberately not "delivered": nothing here trusts that the send
        reached the pane, only that an attempt is now on record (in
        `delivery_attempted_at`, not `delivered_at`) and cannot be silently
        repeated. `delivered_at` stays null until delivery is genuinely
        confirmed.
        """
        return self._transition(task_id, pane_nonce, ("created",), "delivery_pending", "delivery_attempted_at")

    def mark_delivered(self, task_id, *, pane_nonce):
        return self._transition(task_id, pane_nonce, ("delivery_pending",), "delivered", "delivered_at")

    def _reconcile_transition(self, task_id, pane_nonce, target, timestamp_column):
        """Resolve a `delivery_pending` task without requiring the current lane.

        Unlike `_transition`, this does not check the *lane's* current
        nonce. Reconciliation exists precisely for the case where the pane
        that received the ambiguous send is gone and its lane has since
        been re-registered with a new nonce - requiring the current
        incarnation here would make every stuck delivery permanently
        unreconcilable. Authentication instead comes from the task's own
        `pane_nonce`, recorded at send time: a caller who does not know it
        cannot reconcile the wrong task by guessing.
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            if row is None:
                raise ValueError("unknown task")
            if row["pane_nonce"] != pane_nonce:
                raise ValueError("pane incarnation does not match task")
            if row["status"] == target:
                return self._dict(row)
            if row["status"] != "delivery_pending":
                raise ValueError(f"cannot reconcile a {row['status']} task")
            connection.execute(
                f"UPDATE tasks SET status = ?, updated_at = ?, {timestamp_column} = ? WHERE id = ?",
                (target, now, now, task_id),
            )
            row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        return self._dict(row)

    def reconcile_delivery(self, task_id, *, pane_nonce, outcome):
        """Resolve an ambiguous delivery by explicit human decision.

        This is the only path out of `delivery_pending` other than the
        adapter's own post-send confirmation. It exists for the case where
        that confirmation itself failed or the operator inspected the pane
        directly and reached a conclusion the ledger cannot infer on its own
        from echoed terminal text. It authenticates against the task's own
        recorded pane_nonce, not the lane's current one - see
        `_reconcile_transition`.
        """
        if outcome == "delivered":
            return self._reconcile_transition(task_id, pane_nonce, "delivered", "delivered_at")
        if outcome == "failed":
            return self._reconcile_transition(task_id, pane_nonce, "failed", "completed_at")
        raise ValueError("reconciliation outcome must be 'delivered' or 'failed'")

    def accept(self, task_id, *, pane_nonce):
        return self._transition(task_id, pane_nonce, ("delivered",), "accepted", "accepted_at")

    def _find_open_task_for_cancel(self, connection, lane):
        if connection.execute("SELECT 1 FROM lanes WHERE lane = ?", (lane,)).fetchone() is None:
            raise ValueError(f"unknown lane: {lane}")
        return connection.execute(
            "SELECT * FROM tasks WHERE lane = ? AND status NOT IN ('complete','failed','cancelled')",
            (lane,),
        ).fetchone()

    def cancel_open_task(self, lane, *, result=None, abandoned=False):
        """Free a lane's outstanding task by marking it cancelled.

        agent-dotfiles#144 finding 3: this had no caller anywhere in the tree
        until that review. `_register_lane_tx` now calls the narrower
        `_cancel_task_row` directly (same UPDATE, scoped to the specific
        outstanding row it already found, excluding `delivery_pending` --
        this method's own SELECT is intentionally broader and stays available
        standalone, e.g. for a human operator freeing a lane by hand).

        agent-supervisor#17: an unrecognised lane id and a real lane with
        nothing outstanding both returned `None`, so
        `{"cancelled":null}` meant two different things -- and the second
        one, an unknown id silently reported as "already free", is how a
        typo'd `--lane` looks identical to a real completion. Raises for the
        former; only a registered lane with no open task returns `None` now.

        agent-supervisor#649: `complete --task` correctly refuses once a
        lane's pane is gone (the pane-incarnation guard is the invariant, not
        the bug), so this became the ONLY door out for a task whose lane died
        after the work actually shipped -- and it wrote `result_path: NULL`
        every time, indistinguishable from a task that genuinely produced
        nothing. Measured: all 951 of the ledger's `cancelled` rows carry a
        null result, and every one of the 14 that are PR-scoped belongs to a
        PR that merged. `result` and `abandoned` are mutually exclusive and
        one of them is now REQUIRED -- there is no default, on purpose: a
        caller that does not know whether there is a result to record is not
        in a position to have this method guess. Pass `result` (bytes) when
        the caller has recovered what the lane delivered before its pane
        went away; pass `abandoned=True` when there genuinely is nothing.
        Written through the same immutable, hashed `_write_result` path
        `complete()` uses, so a cancelled-with-result row carries
        `result_sha256` exactly the way a completed one does.
        """
        if abandoned and result is not None:
            raise ValueError("cancel_open_task: pass a result or abandoned=True, not both")
        if not abandoned and result is None:
            raise ValueError("cancel_open_task: pass a result or abandoned=True")
        now = int(self.clock())
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                row = self._find_open_task_for_cancel(probe, lane)
            if row is None:
                return None
            destination = digest = None
            if result is not None:
                destination, digest = self._write_result(row["id"], result, known_hash=row["result_sha256"])
            with self._transaction() as connection:
                self._cancel_task_row(
                    connection,
                    row["id"],
                    now,
                    result_path=str(destination) if destination is not None else None,
                    result_sha256=digest,
                )
                return self._dict(connection.execute("SELECT * FROM tasks WHERE id=?", (row["id"],)).fetchone())

    def record_dispatch(
        self,
        *,
        lane,
        pane_id,
        nonce,
        harness,
        repo,
        server_id,
        session_id,
        command,
        task_id,
        source_kind,
        source_url,
        source_ref,
        summary,
        source_state,
        evidence,
        status_marker=None,
        harness_session_id="",
        harness_project_dir="",
        transport="send-keys",
        worktree_path="",
        accepted=False,
        is_review=None,
        failpoint=None,
    ):
        """Atomically register the lane, record the GitHub source, assign, and mark delivered.

        agent-dotfiles#144 finding 2: `cli.py`'s free function `record_dispatch`
        used to call `register_lane`, `reconstruct_task`, `assign`,
        `mark_delivery_pending` and `mark_delivered` as five INDEPENDENT
        `Ledger` calls, each its own lock and its own transaction. A crash
        between any two of them left on disk whatever had already committed --
        reproduced by the #144 review as an orphan `lanes` row (`register_lane`
        had committed; nothing after it had) claiming a lane occupied for a
        dispatch that was otherwise never recorded. An orphan row asserting a
        lane is busy is exactly the state this layer exists to prevent.

        This performs the same five writes against ONE connection inside ONE
        transaction, so a failure at any step rolls back everything this call
        has done so far -- there is no window where step 1 committed and step
        2 did not. `failpoint` exists so a test can inject that failure after
        any named step below without a real crash; see `_fail`.

        This does not weaken #140's central safety property. The caller
        (`cli.py`'s `record_dispatch`, invoked from `dispatch.sh`) still
        tolerates this raising -- a ledger failure here still cannot abort a
        dispatch that has already physically happened in the pane. This only
        removes the partial-write window *inside* the recording step itself;
        it does not make the recording step itself load-bearing for the
        dispatch.

        agent-supervisor#193: `accepted` is the caller's own evidence that
        the brief did more than get typed -- it landed under a
        position-anchored proof check and the box was then confirmed to go
        empty (`dispatch.sh`'s `verified_type --proof-head` /
        `verified_submit`, both fixed by this same issue). Nothing upstream
        of `record_dispatch` self-reports acceptance; this is dispatch's OWN
        confirmation, recorded durably so a later reconciler never has to
        take "the lane went quiet" as a stand-in for "the work began" --
        that substitution is exactly what let `reconcile_lane_completions.py`
        certify `at25-rev33` `complete` after its brief landed as noise a
        harness discarded. `accepted=False` (the default) leaves the task
        `delivered`, same as before this parameter existed -- every existing
        caller is unaffected until it opts in.

        agent-supervisor#640: `is_review` is `cli.py`'s own recorded answer
        to "did `dispatch.sh --reviews-pr` invoke this?" -- `1` when it did,
        `0` when this is a `--pr`-scoped fix pass (known, structurally, NOT
        a review), `None` (the default) for an issue-scoped dispatch, where
        the property is moot -- `get_contributor_tasks_for_pr` only ever
        reads it off a `source_kind='pull'` row. Passed straight through to
        `_reconstruct_task_tx`; see that method and the `is_review` column's
        own comment on `source_tasks`' `CREATE TABLE` for what each value
        means.
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            lane_record = self._register_lane_tx(
                connection,
                lane=lane,
                pane_id=pane_id,
                nonce=nonce,
                harness=harness,
                repo=repo,
                server_id=server_id,
                session_id=session_id,
                command=command,
                harness_session_id=harness_session_id,
                harness_project_dir=harness_project_dir,
                now=now,
            )
            self._fail(failpoint, "after_register_lane")
            registered_nonce = lane_record["nonce"]
            self._reconstruct_task_tx(
                connection,
                task_id=task_id,
                source_kind=source_kind,
                source_url=source_url,
                source_ref=source_ref,
                summary=summary,
                source_state=source_state,
                status="created",
                evidence=evidence,
                status_marker=status_marker,
                now=now,
                is_review=is_review,
            )
            self._fail(failpoint, "after_reconstruct_task")
            self._assign_tx(
                connection,
                task_id=task_id,
                lane=lane,
                pane_nonce=registered_nonce,
                summary=summary,
                now=now,
                worktree_path=worktree_path,
            )
            self._fail(failpoint, "after_assign")
            # The intermediate `delivery_pending` write is not skipped: it is
            # the state machine's only route to `delivered`, and its own
            # guard against a silent resend (see `mark_delivery_pending`'s
            # docstring).
            self._transition_tx(
                connection, task_id, registered_nonce, ("created",), "delivery_pending", "delivery_attempted_at", now
            )
            self._fail(failpoint, "after_mark_delivery_pending")
            task = self._transition_tx(
                connection, task_id, registered_nonce, ("delivery_pending",), "delivered", "delivered_at", now
            )
            self._fail(failpoint, "after_mark_delivered")
            if accepted:
                # Deliberately NOT `_transition_tx(..., "accepted", ...)`:
                # that moves `status` itself to `accepted`, which is the
                # SELF-REPORT path's state (`Ledger.accept`, caller-verified
                # against the lane's own pane_id) -- a distinct, visible
                # status change other readers may key on (`status='delivered'`
                # is what `list_delivered_open_tasks` -- and so the
                # completion reconciler's whole candidate set -- selects on).
                # This is dispatch's OWN evidence, not a self-report, and it
                # must not remove the task from that candidate set; only
                # `accepted_at` is stamped, `status` stays `delivered`.
                connection.execute(
                    "UPDATE tasks SET accepted_at = ? WHERE id = ? AND accepted_at IS NULL",
                    (now, task_id),
                )
                self._fail(failpoint, "after_mark_accepted")
                task = self._dict(connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone())
        return {"lane": lane_record, "task": task}
