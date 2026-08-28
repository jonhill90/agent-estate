"""`Ledger`'s connection/locking/transaction primitives, plus the row-shaping
and validation helpers (`_dict`, `_require_task_id`, `_verify_lane_nonce`,
`_cancel_task_row`) nearly every other mixin calls through `self`.

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- `LedgerCoreMixin` is combined with the
other `Ledger*Mixin` classes into the single `Ledger` class in `core.py`,
so every method keeps its original signature and `self` access to the
others. `LockTimeout` is re-exported by `core.py` under its original name.
"""

from __future__ import annotations

import contextlib
import fcntl
import os
import re
import sqlite3
import time
from pathlib import Path

from core_constants import CLAIM_TASK_PREFIX, TASK_ID_RE  # noqa: F401 -- re-exported by core.py


class LockTimeout(TimeoutError):
    """Raised by `Ledger._locked()` when constructed with `lock_timeout` and
    the ledger's flock is not acquired within that bound. Ordinary callers
    (CLI, tests, the Director loop) never pass `lock_timeout` and so never
    see this -- they keep the old wait-forever behavior. It exists for a
    caller like `prompt_capture_hook.py` (agent-supervisor#687/#693) that
    sits on a human's live prompt-submission path and must never block on a
    contended or abandoned lock: *a locked ledger costs that caller a
    bounded, small delay*, not an indefinite hang."""


class LedgerCoreMixin:

    def __init__(self, root: Path | str, *, clock=None, _migration_failpoint=None, lock_timeout=None):
        self.root = Path(root)
        self.clock = clock or (lambda: int(time.time()))
        self.results_dir = self.root / "results"
        self.snapshots_dir = self.root / "snapshots"
        self.event_payloads_dir = self.root / "event-payloads"
        self.db_path = self.root / "ledger.sqlite3"
        self.lock_path = self.root / "ledger.lock"
        # None (default) preserves the original indefinite flock/sqlite wait
        # for every existing caller. A caller that cannot afford to hang
        # (see `LockTimeout` above) passes a small number of seconds instead.
        self.lock_timeout = lock_timeout
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.root, 0o700)
        self.results_dir.mkdir(exist_ok=True, mode=0o700)
        os.chmod(self.results_dir, 0o700)
        self.snapshots_dir.mkdir(exist_ok=True, mode=0o700)
        self.event_payloads_dir.mkdir(exist_ok=True, mode=0o700)
        os.chmod(self.snapshots_dir, 0o700)
        os.chmod(self.event_payloads_dir, 0o700)
        self.lock_path.touch(mode=0o600, exist_ok=True)
        os.chmod(self.lock_path, 0o600)
        self._lock_depth = 0
        self._initialize()
        self._migrate_lanes_table(failpoint=_migration_failpoint)
        self._migrate_tasks_table(failpoint=_migration_failpoint)
        self._migrate_source_tasks_table(failpoint=_migration_failpoint)
        self._migrate_source_tasks_review_column(failpoint=_migration_failpoint)
        self._backfill_known_misclassified_review_tasks(failpoint=_migration_failpoint)
        self._migrate_source_tasks_pull_uniqueness(failpoint=_migration_failpoint)
        self._migrate_items_table(failpoint=_migration_failpoint)
        self._restore_items_dropped_on_context_alone(failpoint=_migration_failpoint)
        self._migrate_prompts_pane_columns(failpoint=_migration_failpoint)

    def _connect(self, *, foreign_keys=True):
        # `self.lock_timeout` also bounds sqlite3's own busy-wait (its
        # `timeout` kwarg): with `_locked()` already serializing every
        # writer through the flock below, contention reaching sqlite itself
        # is rare, but a bounded caller means "bounded", not "bounded except
        # here."
        connect_timeout = 30 if self.lock_timeout is None else self.lock_timeout
        connection = sqlite3.connect(self.db_path, timeout=connect_timeout, isolation_level=None)
        connection.row_factory = sqlite3.Row
        connection.execute(f"PRAGMA foreign_keys = {'ON' if foreign_keys else 'OFF'}")
        connection.execute("PRAGMA journal_mode = WAL")
        connection.execute("PRAGMA synchronous = FULL")
        return connection

    def _acquire_flock(self, lock_file):
        """Block forever (old behavior, `lock_timeout is None`) or retry
        non-blocking acquisition until `self.lock_timeout` elapses, then
        raise `LockTimeout`. `LOCK_NB` + short sleeps rather than a signal
        alarm: signal-based timeouts are process-global and would corrupt
        any other alarm a caller has set, and `flock` has no native
        wait-with-timeout of its own."""
        if self.lock_timeout is None:
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
            return
        deadline = time.monotonic() + self.lock_timeout
        while True:
            try:
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                return
            except OSError:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise LockTimeout(
                        f"ledger lock not acquired within {self.lock_timeout}s ({self.lock_path})"
                    )
                time.sleep(min(0.05, remaining))

    @contextlib.contextmanager
    def _locked(self):
        if self._lock_depth:
            self._lock_depth += 1
            try:
                yield
            finally:
                self._lock_depth -= 1
            return
        with self.lock_path.open("r+") as lock_file:
            self._acquire_flock(lock_file)
            self._lock_depth = 1
            try:
                yield
            finally:
                self._lock_depth = 0
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_UN)

    def operation_lock(self):
        """Hold the ledger lock across adapter I/O and state transitions."""
        return self._locked()

    @contextlib.contextmanager
    def _transaction(self):
        connection = self._connect()
        connection.execute("BEGIN IMMEDIATE")
        try:
            yield connection
        except BaseException:
            connection.rollback()
            raise
        else:
            connection.commit()
        finally:
            connection.close()


    @staticmethod
    def _dict(row):
        return dict(row) if row is not None else None

    @staticmethod
    def _require_task_id(task_id, *, allow_claim=False):
        if TASK_ID_RE.fullmatch(task_id):
            return
        # agent-supervisor#36: `complete()` used to reject every
        # `ledger-claim:<lane>:<token>` row outright, because TASK_ID_RE has
        # no colons in it. That was never a deliberate refusal of claim rows
        # specifically -- nothing that creates one (`claim_lane`) validates
        # its shape either, so the row already sits in `tasks` unguarded;
        # this was only ever reachable from `complete()`'s own regex, which
        # exists to bound what an UNTRUSTED caller (`cli.py complete`, a
        # worker naming its own task id from inside its pane) can put into
        # `_write_result`'s file path. `record_completion` never passes a
        # caller-typed id here -- only one already read back out of the
        # ledger via `get_task`/`get_open_task_for_lane` -- so `allow_claim`
        # is opt-in per call, not a blanket loosening of the check every
        # other caller still gets. Still bounds the token half to the same
        # character set as an ordinary task id: only the `ledger-claim:` /
        # lane prefix is exempted, not arbitrary content after it.
        if allow_claim and task_id.startswith(CLAIM_TASK_PREFIX):
            token = task_id.rsplit(":", 1)[-1]
            if TASK_ID_RE.fullmatch(token):
                return
        raise ValueError("invalid task id")

    @staticmethod
    def _verify_lane_nonce(connection, lane, pane_nonce):
        """Raises unless `pane_nonce` matches the lane's CURRENT registration.

        Returns that same row's `pane_id` (agent-supervisor#631) so a caller
        already paying for this lookup -- `_assign_tx` is the one that
        matters -- can snapshot it onto a task row without a second query.
        Every other caller ignores the return value; adding it here changes
        nothing for them.
        """
        row = connection.execute("SELECT nonce, pane_id FROM lanes WHERE lane = ?", (lane,)).fetchone()
        if row is None:
            raise ValueError(f"unknown lane: {lane}")
        if row["nonce"] != pane_nonce:
            raise ValueError("pane incarnation does not match registered lane")
        return row["pane_id"]

    @staticmethod
    def _cancel_task_row(connection, task_id, now, *, result_path=None, result_sha256=None):
        """`result_path`/`result_sha256` (agent-supervisor#649): every prior
        caller left these NULL, which is exactly the bug -- an operator's
        `cancel-open-task` was the ONLY door left once a lane's pane was gone
        (`complete --task` correctly refuses a dead pane's stale nonce), so
        it recorded whatever the lane had actually delivered as
        indistinguishable from genuine abandonment. Every INTERNAL caller
        (the re-registration cleanup in `_register_lane_tx`) still passes
        neither -- that path has no result to offer and no operator judgement
        behind it, so it stays a bare cancellation, unchanged. Only
        `cancel_open_task`'s own caller-facing path supplies these now, and
        only after the caller has explicitly said whether there is a result
        to record (see that method's docstring).
        """
        connection.execute(
            "UPDATE tasks SET status='cancelled', result_path=?, result_sha256=?, updated_at=?, completed_at=? WHERE id=?",
            (result_path, result_sha256, now, now, task_id),
        )
        connection.execute(
            "UPDATE source_tasks SET status='cancelled', updated_at=? WHERE id=?",
            (now, task_id),
        )
