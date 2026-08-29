"""`Ledger`'s `source_tasks` table lifecycle: row shaping, single/list
lookup, reconstruction from a source, state updates, and the
worktree-path backfill queries/writer.

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib
import json
import re
import time

from core_constants import SOURCE_TASK_STATUSES, TERMINAL_STATUSES  # noqa: F401 -- re-exported by core.py


class LedgerSourceTasksMixin:
    @staticmethod
    def _source_task_dict(row):
        if row is None:
            return None
        value = dict(row)
        value["task_id"] = value.pop("id")
        value["evidence"] = json.loads(value.pop("evidence_json"))
        value.pop("updated_at")
        return value

    def get_source_task(self, task_id):
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute("SELECT * FROM source_tasks WHERE id = ?", (task_id,)).fetchone()
        return self._source_task_dict(row)

    def list_source_tasks(self):
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute("SELECT * FROM source_tasks ORDER BY id").fetchall()
        return [self._source_task_dict(row) for row in rows]

    def _reconstruct_task_tx(
        self,
        connection,
        *,
        task_id,
        source_kind,
        source_url,
        source_ref,
        summary,
        source_state,
        status,
        evidence,
        status_marker,
        now,
        is_review=None,
    ):
        self._require_task_id(task_id)
        if source_kind not in ("issue", "pull"):
            raise ValueError("unsupported GitHub source kind")
        if status not in ("created", "delivered", "accepted", "running", "complete", "failed", "cancelled"):
            raise ValueError("unsupported source task status")
        if not all(isinstance(value, str) and value for value in (source_url, source_ref, summary, source_state)):
            raise ValueError("source task fields must be non-empty")
        if not isinstance(evidence, list) or not all(isinstance(value, str) and value for value in evidence):
            raise ValueError("source task evidence must be non-empty strings")
        if status in TERMINAL_STATUSES and not evidence:
            raise ValueError("terminal source task requires evidence")
        if status_marker is not None and not isinstance(status_marker, str):
            raise ValueError("source task status marker must be text")
        # agent-supervisor#640: `None` means "not recorded" (see the
        # `is_review` column's own comment on `source_tasks`), NOT "false" --
        # a caller must say so explicitly with `0` to record "known,
        # confirmed NOT a review" rather than leave the fallback regex to
        # answer for a row it could instead have known for certain.
        if is_review is not None and is_review not in (0, 1):
            raise ValueError("is_review must be 0, 1, or None")
        encoded_evidence = json.dumps(evidence, sort_keys=True, separators=(",", ":"))
        connection.execute(
            """
            INSERT INTO source_tasks(
                id, source_kind, source_url, source_ref, summary, source_state,
                status, evidence_json, status_marker, updated_at, is_review
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                source_kind=excluded.source_kind,
                source_url=excluded.source_url,
                source_ref=excluded.source_ref,
                summary=excluded.summary,
                source_state=excluded.source_state,
                status=excluded.status,
                evidence_json=excluded.evidence_json,
                status_marker=excluded.status_marker,
                updated_at=excluded.updated_at,
                is_review=excluded.is_review
            """,
            (
                task_id,
                source_kind,
                source_url,
                source_ref,
                summary,
                source_state,
                status,
                encoded_evidence,
                status_marker,
                now,
                is_review,
            ),
        )
        row = connection.execute("SELECT * FROM source_tasks WHERE id = ?", (task_id,)).fetchone()
        return self._source_task_dict(row)

    def reconstruct_task(
        self,
        *,
        task_id,
        source_kind,
        source_url,
        source_ref,
        summary,
        source_state,
        status,
        evidence,
        status_marker,
        is_review=None,
    ):
        """Replace one local source spool record with facts read from GitHub.

        This intentionally has no lane or pane dependency: reconstruction must
        work after the entire supervisor state directory has been recreated.

        agent-estate#838: `is_review` defaults to `None` ("not recorded"),
        unchanged for every existing caller (`github_source.py`'s spool
        reconstruction, `dispatch-pi-rpc.sh`'s plain issue dispatch) -- only
        `dispatch-claude-print.sh`'s new `--reviews-pr` path passes `1`, the
        same fact `record_dispatch` already records for the tmux flow's
        `--reviews-pr` dispatches (see that function's own docstring on
        `is_review`).
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._reconstruct_task_tx(
                connection,
                task_id=task_id,
                source_kind=source_kind,
                source_url=source_url,
                source_ref=source_ref,
                summary=summary,
                source_state=source_state,
                status=status,
                evidence=evidence,
                status_marker=status_marker,
                now=now,
                is_review=is_review,
            )

    def update_source_task_state(self, task_id, *, source_state=None, status=None):
        """Advance one `source_tasks` row's two DERIVED columns -- and only those.

        agent-supervisor#127: `source_tasks` is written once, at dispatch
        (`record_dispatch`) or reconstruction (`reconstruct_task`), and never
        touched again -- `source_state` and `status` look like a state
        machine's schema but nothing advances them. This is that writer. It
        deliberately touches only `source_state` and `status`; every other
        column (`source_url`, `source_ref`, `summary`, `evidence_json`,
        `status_marker`) is what was actually dispatched, and reconciliation
        has no business rewriting a record of what happened.

        Either argument may be omitted (left `None`) to leave that column
        untouched. This matters because the two columns are derived from
        different, independent facts: `reconcile_sources.SourceTaskReconciler`
        can always read this task's local `tasks` row to recompute `status`,
        but can only recompute `source_state` when GitHub actually answered
        for that row's repo. A caller that could not resolve `source_state`
        must be able to still advance `status` (or vice versa) without
        clobbering the column it has no fresh fact for back to some default.

        Idempotent by construction: if the requested value(s) already match
        the row, this returns without writing -- `updated_at` does not move.
        A sweep run twice therefore performs zero writes the second time.
        """
        if source_state is None and status is None:
            raise ValueError("update_source_task_state requires source_state or status")
        if status is not None and status not in SOURCE_TASK_STATUSES:
            raise ValueError("unsupported source task status")
        if source_state is not None and not source_state:
            raise ValueError("source task state must be non-empty")
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            row = connection.execute("SELECT * FROM source_tasks WHERE id = ?", (task_id,)).fetchone()
            if row is None:
                raise ValueError("unknown source task")
            next_source_state = source_state if source_state is not None else row["source_state"]
            next_status = status if status is not None else row["status"]
            if next_source_state == row["source_state"] and next_status == row["status"]:
                return self._source_task_dict(row)
            connection.execute(
                "UPDATE source_tasks SET source_state = ?, status = ?, updated_at = ? WHERE id = ?",
                (next_source_state, next_status, now, task_id),
            )
            row = connection.execute("SELECT * FROM source_tasks WHERE id = ?", (task_id,)).fetchone()
        return self._source_task_dict(row)

    def list_complete_tasks_missing_worktree_path(self):
        """Every `tasks` row a backfill sweep may have work to do on --
        agent-supervisor#611. Scoped to `status='complete'` (a task still in
        flight has no final worktree to confirm yet) and `worktree_path=''`
        (a populated row is the historical record invariant 1 requires; this
        never overwrites one).
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                "SELECT * FROM tasks WHERE status = 'complete' AND worktree_path = '' ORDER BY created_at, id"
            ).fetchall()
        return [self._dict(row) for row in rows]

    def get_pr_for_task(self, task_id):
        """The `(repo, pr_number)` a `record_pr_for_task` call recorded for
        this task, if any -- the reverse lookup `get_task_for_pr_number`
        does not provide. Read-only, and returns `None` rather than guessing
        when no such row exists: a task with no recorded PR link has no
        answer here, not an inferred one.
        """
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute(
                "SELECT repo, pr_number FROM pr_authorship WHERE task_id = ?", (task_id,)
            ).fetchone()
        return {"repo": row["repo"], "pr_number": row["pr_number"]} if row is not None else None

    def backfill_task_worktree_path(self, task_id, worktree_path):
        """Write `worktree_path` for exactly one row, and only into the gap
        this issue describes -- agent-supervisor#611's sweep is the one
        caller.

        Refuses outright (does not silently no-op) unless the row is
        `status='complete'` with `worktree_path=''` right now: a non-empty
        column is the historical record invariant 1 requires (never
        overwrite it), and a non-complete task's worktree may still be
        live. Idempotent by construction the same way
        `update_source_task_state` is: a second call after this one already
        wrote finds `worktree_path` no longer empty and refuses too, so a
        sweep re-run against an already-fixed row performs zero writes
        rather than raising -- callers check `list_complete_tasks_missing_
        worktree_path()` first, which already excludes it.
        """
        if not worktree_path:
            raise ValueError("backfill_task_worktree_path requires a non-empty worktree_path")
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
            if row is None:
                raise ValueError("unknown task")
            if row["status"] != "complete":
                raise ValueError(f"task {task_id} is not complete (status={row['status']!r})")
            if row["worktree_path"]:
                raise ValueError(f"task {task_id} already has a recorded worktree_path -- refusing to overwrite it")
            connection.execute(
                "UPDATE tasks SET worktree_path = ?, updated_at = ? WHERE id = ?",
                (worktree_path, now, task_id),
            )
            row = connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone()
        return self._dict(row)
