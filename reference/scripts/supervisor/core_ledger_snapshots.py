"""`Ledger`'s session-event/component/PR-verdict/snapshot recording:
`record_session_event`, `record_component`, `record_pr_verdict`,
`get_pr_verdict`, the atomic-replace helper, and `record_snapshot`.

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib
import difflib
import hashlib
import json
import os
import re
import tempfile
import time

from core_constants import COMPONENT_RE, MAX_RESULT_BYTES  # noqa: F401 -- re-exported by core.py


class LedgerSnapshotsMixin:
    # agent-tui#14. `session_remove`'s write path -- log every removal to the
    # ledger with what was running at the time, the literal text of the
    # issue this exists to satisfy. Reuses the SAME `events` table every
    # other durable action in this ledger already writes to
    # (`complete`/`observe_attention`/`record_snapshot` above), rather than a
    # second table this estate would have to keep in sync with it -- an
    # `events` row here is queryable by `cli.py events` exactly like a
    # completion or attention event is.
    #
    # `detail` is written to `event_payloads_dir`, the same place
    # `record_snapshot`'s diff blobs live, rather than packed into a column:
    # the full `session_guard.remove_guard` payload that authorized a removal
    # is exactly the kind of unbounded, structured evidence that pattern
    # already exists for. `task_id` is left NULL -- a session-management
    # event is not about any one task -- which `events.task_id` already
    # allows (nullable FK).
    def record_session_event(self, session, *, event, detail):
        if not session or not isinstance(session, str):
            raise ValueError("session is required")
        if not event or not isinstance(event, str):
            raise ValueError("event is required")
        payload = json.dumps(detail, sort_keys=True).encode("utf-8")
        digest = hashlib.sha256(payload).hexdigest()[:16]
        now = int(self.clock())
        # Includes both `now` and a content digest so two removals of the
        # same session (created, removed, re-created, removed again) each
        # get their own durable row instead of colliding on `INSERT OR
        # IGNORE` -- unlike `completion:<task_id>`, a session name is not a
        # one-shot identity.
        key = f"session:{event}:{session}:{now}:{digest}"
        payload_path = self.event_payloads_dir / f"{key.replace(':', '_')}.json"
        with self._locked():
            self._atomic_replace(payload_path, payload)
            with self._transaction() as connection:
                connection.execute(
                    """
                    INSERT OR IGNORE INTO events(key, type, status, payload_path, created_at)
                    VALUES (?, 'session', 'pending', ?, ?)
                    """,
                    (key, str(payload_path), now),
                )
                row = connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone()
        return self._dict(row)

    def record_component(self, name, *, healthy, snapshot=None, error=None):
        if healthy and snapshot is None:
            raise ValueError("healthy component requires a snapshot")
        if not healthy and not error:
            raise ValueError("failed component requires an error")
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            existing = connection.execute("SELECT * FROM components WHERE name=?", (name,)).fetchone()
            digest = hashlib.sha256(snapshot).hexdigest() if healthy else (existing["snapshot_sha256"] if existing else None)
            connection.execute(
                """
                INSERT INTO components(name, healthy, error, snapshot_sha256, updated_at)
                VALUES (?, ?, ?, ?, ?)
                ON CONFLICT(name) DO UPDATE SET
                    healthy=excluded.healthy,
                    error=excluded.error,
                    snapshot_sha256=excluded.snapshot_sha256,
                    updated_at=excluded.updated_at
                """,
                (name, int(healthy), None if healthy else error, digest, now),
            )
            row = connection.execute("SELECT * FROM components WHERE name=?", (name,)).fetchone()
        return self._dict(row)

    def record_pr_verdict(self, *, repo, number, verdict, head_sha, reviewer, note=None):
        """Record what this estate decided about a PR, independent of whether
        GitHub can represent it (agent-dotfiles#203). `repo` is the full
        `owner/name` slug so a verdict source needs no other context to look
        one up. Overwrites any prior verdict for the same repo+number -- a
        reviewing lane re-recording after a fixup replaces its own earlier
        answer rather than appending to a history nothing reads."""
        if verdict not in ("approved", "rejected"):
            raise ValueError("verdict must be 'approved' or 'rejected'")
        if not repo or not isinstance(repo, str):
            raise ValueError("repo is required")
        if not head_sha or not isinstance(head_sha, str):
            raise ValueError("head_sha is required")
        if not reviewer or not isinstance(reviewer, str):
            raise ValueError("reviewer is required")
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            connection.execute(
                """
                INSERT INTO pr_verdicts(repo, number, verdict, head_sha, reviewer, note, updated_at)
                VALUES (?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(repo, number) DO UPDATE SET
                    verdict=excluded.verdict,
                    head_sha=excluded.head_sha,
                    reviewer=excluded.reviewer,
                    note=excluded.note,
                    updated_at=excluded.updated_at
                """,
                (repo, number, verdict, head_sha, reviewer, note, now),
            )
            row = connection.execute(
                "SELECT * FROM pr_verdicts WHERE repo=? AND number=?", (repo, number)
            ).fetchone()
        return self._dict(row)

    def get_pr_verdict(self, *, repo, number):
        with self._locked(), self._transaction() as connection:
            row = connection.execute(
                "SELECT * FROM pr_verdicts WHERE repo=? AND number=?", (repo, number)
            ).fetchone()
        return self._dict(row) if row is not None else None

    @staticmethod
    def _atomic_replace(destination, content):
        descriptor, temporary_name = tempfile.mkstemp(prefix=f".{destination.name}.", dir=destination.parent)
        try:
            with os.fdopen(descriptor, "wb") as handle:
                handle.write(content)
                handle.flush()
                os.fsync(handle.fileno())
            os.chmod(temporary_name, 0o600)
            os.replace(temporary_name, destination)
            os.chmod(destination, 0o600)
        finally:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(temporary_name)

    def record_snapshot(self, name, snapshot):
        """Record a healthy component snapshot and emit one bounded diff event."""
        if not COMPONENT_RE.fullmatch(name):
            raise ValueError("invalid component name")
        if not isinstance(snapshot, bytes):
            raise TypeError("snapshot must be bytes")
        digest = hashlib.sha256(snapshot).hexdigest()
        baseline_path = self.snapshots_dir / f"{name}.txt"
        with self._locked():
            if not baseline_path.exists():
                self.record_component(name, snapshot=snapshot, healthy=True)
                self._atomic_replace(baseline_path, snapshot)
                return None
            previous = baseline_path.read_bytes()
            if previous == snapshot:
                self.record_component(name, snapshot=snapshot, healthy=True)
                return None

            old_text = previous.decode("utf-8", errors="replace").splitlines(keepends=True)
            new_text = snapshot.decode("utf-8", errors="replace").splitlines(keepends=True)
            diff = "".join(
                difflib.unified_diff(old_text, new_text, fromfile=f"{name}:previous", tofile=f"{name}:{digest}")
            ).encode()
            marker = b"\n[DIFF TRUNCATED]\n"
            if len(diff) > MAX_RESULT_BYTES:
                diff = diff[: MAX_RESULT_BYTES - len(marker)] + marker
            payload_path = self.event_payloads_dir / f"{name}-{digest}.diff"
            if payload_path.exists():
                if payload_path.read_bytes() != diff:
                    raise ValueError("sensor event payload conflicts with existing content")
            else:
                self._atomic_replace(payload_path, diff)

            now = int(self.clock())
            key = f"sensor:{name}:{digest}"
            with self._transaction() as connection:
                connection.execute(
                    """
                    INSERT INTO components(name, healthy, error, snapshot_sha256, updated_at)
                    VALUES (?, 1, NULL, ?, ?)
                    ON CONFLICT(name) DO UPDATE SET
                        healthy=1, error=NULL, snapshot_sha256=excluded.snapshot_sha256,
                        updated_at=excluded.updated_at
                    """,
                    (name, digest, now),
                )
                connection.execute(
                    """
                    INSERT OR IGNORE INTO events(key, type, status, payload_path, created_at)
                    VALUES (?, 'sensor', 'pending', ?, ?)
                    """,
                    (key, str(payload_path), now),
                )
                event = connection.execute("SELECT * FROM events WHERE key=?", (key,)).fetchone()
            self._atomic_replace(baseline_path, snapshot)
        return self._dict(event)
