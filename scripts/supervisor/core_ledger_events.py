"""`Ledger`'s events/notification queue: `observe_attention`/`observe_idle`,
listing/reading events, `events_due`, `mark_notified`, and `ack`.

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib

from core_constants import TERMINAL_STATUSES  # noqa: F401 -- re-exported by core.py


class LedgerEventsMixin:
    def observe_attention(self, lane, *, pane_nonce, reason="idle"):
        """Durably record that an outstanding task's lane needs attention.

        `reason` names *why* (idle, blocked, approval, unknown). Idle keeps
        the original unsuffixed key (`attention:<task>`) so existing
        idle-triggered event consumers are unaffected; every other reason
        gets its own key (`attention:<task>:<reason>`) so a lane that is
        blocked and later becomes idle (or vice versa) is tracked as two
        distinct durable events, not silently collapsed into one.
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            self._verify_lane_nonce(connection, lane, pane_nonce)
            task = connection.execute(
                """
                SELECT * FROM tasks WHERE lane=?
                  AND status IN ('delivered','accepted','running')
                """,
                (lane,),
            ).fetchone()
            if task is None:
                return None
            key = f"attention:{task['id']}" if reason == "idle" else f"attention:{task['id']}:{reason}"
            connection.execute(
                """
                INSERT OR IGNORE INTO events(key, type, task_id, status, created_at)
                VALUES (?, 'attention', ?, 'pending', ?)
                """,
                (key, task["id"], now),
            )
            event = connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone()
        return self._dict(event)

    def observe_idle(self, lane, *, pane_nonce):
        return self.observe_attention(lane, pane_nonce=pane_nonce, reason="idle")

    def list_events(self, *, task_id=None, event_type=None):
        clauses = []
        values = []
        if task_id is not None:
            clauses.append("task_id = ?")
            values.append(task_id)
        if event_type is not None:
            clauses.append("type = ?")
            values.append(event_type)
        where = f" WHERE {' AND '.join(clauses)}" if clauses else ""
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(f"SELECT * FROM events{where} ORDER BY created_at, key", values).fetchall()
        return [self._dict(row) for row in rows]

    def get_event(self, key):
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone())

    def events_due(self, *, now=None):
        now = int(self.clock() if now is None else now)
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT * FROM events
                 WHERE status='pending'
                    OR (status='notified' AND retry_at IS NOT NULL AND retry_at <= ?)
                 ORDER BY created_at, key
                """,
                (now,),
            ).fetchall()
        return [self._dict(row) for row in rows]

    def mark_notified(self, keys, *, retry_after):
        now = int(self.clock())
        retry_at = now + int(retry_after)
        with self._locked(), self._transaction() as connection:
            for key in keys:
                if connection.execute("SELECT 1 FROM events WHERE key=?", (key,)).fetchone() is None:
                    raise ValueError(f"unknown event: {key}")
                connection.execute(
                    "UPDATE events SET status='notified', notified_at=?, retry_at=? WHERE key=? AND status!='acked'",
                    (now, retry_at, key),
                )

    def ack(self, keys):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            for key in keys:
                event = connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone()
                if event is None:
                    raise ValueError(f"unknown event: {key}")
                if event["type"] == "attention":
                    task = connection.execute("SELECT status FROM tasks WHERE id=?", (event["task_id"],)).fetchone()
                    if task is not None and task["status"] not in TERMINAL_STATUSES:
                        raise ValueError("attention requires task disposition before acknowledgement")
                connection.execute(
                    "UPDATE events SET status='acked', acked_at=?, retry_at=NULL WHERE key=?",
                    (now, key),
                )
