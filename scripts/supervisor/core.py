"""Transactional task/event ledger for the Hill90 supervisor.

The core deliberately knows nothing about Codex, Claude terminal chrome, tmux
keystrokes, or GitHub. Harness adapters register pane incarnations and move
tasks through this shared lifecycle.
"""

from __future__ import annotations

import contextlib
import difflib
import fcntl
import hashlib
import json
import os
import re
import socket
import sqlite3
import tempfile
import time
from pathlib import Path


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


def claim_owner_token(pid, *, host=None):
    """The `host:pid` string `claim_lane` records for `owner`.

    Composed HERE rather than by the shell caller so both sides of the
    liveness check spell the host the same way: `reap_stale_lane_claims`
    compares against `socket.gethostname()`, and `$(hostname)` in bash is not
    guaranteed to agree with it. A mismatch would be safe (the claim simply
    would not be reaped) but permanently so, which is the failure this exists
    to end.
    """
    return f"{host or socket.gethostname()}:{int(pid)}"


# agent-supervisor#108. A lane id is spelled `<session>:<index>`, and the
# session part is a LABEL -- `tmux rename-session` changes it without touching
# a single window, pane or process. On 2026-08-14 the live session was renamed
# from `agent-dotfiles` to `agent-supervisor` to recover from #102, and every
# task row written before that names a lane whose STRING no longer matches the
# same physical window's current name. Comparing lane ids as strings therefore
# answered "different lane" for one window, and the author-exclusion guard --
# whose whole job is to keep a lane off its own PR -- stopped excluding it.
#
# The index is the part this system authors: `lanes.sh` reads it off the
# window, `dispatch.sh` keys the ledger on it, and it survives a window being
# closed and recreated. So identity is compared on the index, and the session
# name is not consulted at all.
LANE_ID_RE = re.compile(r"^(?P<session>[^:\s]*):(?P<index>[0-9]+)$")


def lane_relation(one, other):
    """How two lane ids relate: `same`, `different`, or `unknown`.

    Three answers, not two, because "I cannot tell" is a real state and the
    callers must not be handed a boolean that hides it (agent-supervisor#108):

    * `same` -- identical strings, or the same window index. Two ids that
      differ only in the session name name ONE window: a session rename is the
      only thing that produces that pair on a single server, and the estate has
      now produced it once. Answering `same` here is also the fail-closed
      direction if two differently-named sessions ever did coexist: the cost is
      one candidate lane withheld from one review, against a self-review
      dispatched and reported as independent.
    * `different` -- both parse and their indices differ. Different windows,
      positively established. This is the ONLY answer a caller may treat as
      permission, and it is what keeps the guard from refusing every review.
    * `unknown` -- either id is missing, or does not parse as `<session>:<index>`
      (a `Review-Lane:` stamp someone typed by hand, a lane id from a shape this
      system does not mint). Nothing was established; a caller must not read it
      as `different`.
    """
    if not isinstance(one, str) or not isinstance(other, str):
        return "unknown"
    one, other = one.strip(), other.strip()
    if not one or not other:
        return "unknown"
    if one == other:
        return "same"
    left, right = LANE_ID_RE.match(one), LANE_ID_RE.match(other)
    if left is None or right is None:
        return "unknown"
    # int(), not string equality: `03` and `3` are the same window index, and a
    # comparison that said otherwise would be a smaller version of this bug.
    if int(left.group("index")) == int(right.group("index")):
        return "same"
    return "different"


# agent-supervisor#292. `lane_relation` above is ENTIRELY a string-shape
# check: `<session>:<index>`, minted only for a tmux window. A claude-print
# or pi-rpc lane has no window to index -- `dispatch-claude-print.sh` and
# `dispatch-pi-rpc.sh` both set `LANE="$LABEL"`, the task id itself -- so
# paired against one of those, `lane_relation` can never answer `same` or
# `different`: it always says `unknown`, and every caller of it (author
# exclusion in dispatch.sh, verdict independence in merge-pr.sh/digest.sh)
# treats `unknown` as "refuse". Measured 2026-08-16: three fresh review
# candidates for PR #288, authored by a claude-print lane, all refused --
# not because any of them WAS the author, but because none of them could be
# PROVEN not to be.
#
# This is the widening: identity for a pair the shape check could not place
# comes from the ledger's own registry instead, keyed on `lanes.pane_id` --
# written once, by EVERY transport, at `register_lane` time. A tmux lane's
# pane_id is tmux's own pane id (`%12`), stable across a session RENAME
# (`lane_relation`'s own motivating case, #108) without needing string
# surgery on the lane id at all. A claude-print lane's pane_id is
# `claude-print:<lane>` (`dispatch-claude-print.sh` step 4), unique to the
# one `claude -p` process it names. Two rows sharing a pane_id are positively
# the SAME lane; two known rows with different pane_id are positively
# DIFFERENT, whichever population either is in.
#
# Pure and DB-free itself -- the fetch (`Ledger.get_lane`) is the caller's --
# so this is unit-testable without a ledger, the same posture `lane_relation`
# above takes. Still fails CLOSED: either row missing (lane never registered,
# or the lookup itself failed) or either `pane_id` empty answers `unknown`,
# never a guess. This WIDENS what is establishable; it does not loosen what
# counts as established -- a caller that could not tell two lanes apart
# before this still cannot, if the ledger has nothing to say either.
def lane_relation_from_rows(one_row, other_row):
    """same/different/unknown, comparing two ALREADY-FETCHED `lanes` rows
    (dicts, or `None` for "not in the ledger") by `pane_id`."""
    if one_row is None or other_row is None:
        return "unknown"
    pane_one = (one_row.get("pane_id") or "").strip()
    pane_other = (other_row.get("pane_id") or "").strip()
    if not pane_one or not pane_other:
        return "unknown"
    return "same" if pane_one == pane_other else "different"


# agent-supervisor#605. `daemon`/`d-<task>` (`daemon/internal/ledger/
# ledger.go`'s `EnsureLane`, called from `main.go:216`'s hardcoded `-lane`
# default and `batch.go:103`'s per-job `d-<task>`) and `<session>:<index>`
# (`LANE_ID_RE`, a tmux window) are two namespaces that can never denote the
# same actor BY CONSTRUCTION: a tmux lane has no write path into the
# daemon's own author-lane field, and `EnsureLane` is the only thing in this
# estate that ever inserts a `lanes` row with `pane_id=''` and
# `server_id='supervisord'` -- nothing on the tmux dispatch path can produce
# that combination (`register_lane`'s tmux callers always pass a real pane
# id). Recognizing that disjointness is different in kind from #539/#552/
# #556, each of which tried to ESTABLISH identity from a gameable
# self-declared signal and was rejected for exactly that; this widens
# nothing about what "same" means; it only lets `different` be told apart
# from `unknown` for a pair whose respective shape/pane-id checks
# (`lane_relation`, `lane_relation_from_rows`) were never built with the
# other's namespace in mind.
DAEMON_LANE_RE = re.compile(r"^(daemon|d-.+)$")


def is_daemon_shaped(lane_id):
    """String-shape only -- exactly as untrustworthy standing alone as any
    other self-declared id. See `daemon_lane_verified` for the actual proof;
    nothing here is ever treated as identity by itself."""
    return isinstance(lane_id, str) and bool(DAEMON_LANE_RE.match(lane_id.strip()))


def daemon_lane_verified(lane_id, row):
    """True only when `lane_id` is daemon-shaped AND the ledger's OWN row for
    it carries the exact signature `EnsureLane` writes and nothing else in
    this estate ever writes: `server_id == 'supervisord'`,
    `transport == 'claude-print'`, `pane_id == ''`. This is the "real ledger
    row for the daemon-authored task" #605's decision requires -- a
    hand-typed `Author-Lane: daemon` trailer (or a PR body claiming it)
    names no such row, or names one with a different signature, so it
    cannot satisfy this and falls through to the existing fail-closed
    `unknown` unchanged.
    """
    if not is_daemon_shaped(lane_id) or not row:
        return False
    return (
        row.get("server_id") == "supervisord"
        and row.get("transport") == "claude-print"
        and (row.get("pane_id") or "") == ""
    )


def cross_namespace_lane_relation(one_id, one_row, other_id, other_row):
    """`different` when exactly one side is a VERIFIED daemon lane (see
    `daemon_lane_verified`) and the other is a genuine tmux lane id
    (matches `LANE_ID_RE`) with a resolvable ledger `pane_id`. `None` when
    this rule does not apply -- the caller must fall through to
    `lane_relation`/`lane_relation_from_rows` unchanged in that case.

    Daemon-vs-daemon (or any pair where neither/both sides verify as a
    daemon lane) is deliberately OUT of scope here and always returns
    `None`: same-namespace comparison keeps using the existing same/
    different/unknown machinery, per #605's decision.

    Never answers `same`: a verified daemon lane and a genuine tmux lane
    cannot be the same actor by construction, so this function's only
    possible answers are `different` (the cross-namespace case resolves)
    or `None` (it does not apply).
    """
    one_is_daemon = daemon_lane_verified(one_id, one_row)
    other_is_daemon = daemon_lane_verified(other_id, other_row)
    if one_is_daemon == other_is_daemon:
        return None  # both daemon, or neither -- not this rule's case
    _, tmux_id, tmux_row = (
        (one_id, other_id, other_row) if one_is_daemon else (other_id, one_id, one_row)
    )
    if not isinstance(tmux_id, str) or not LANE_ID_RE.match(tmux_id.strip()):
        return None
    if not tmux_row or not (tmux_row.get("pane_id") or "").strip():
        return None
    return "different"


# agent-supervisor#292 item 3: when a candidate is refused, say WHICH
# population each side is in, so the message is actionable. The pre-existing
# text ("a session rename changes a lane's name, not which window it is")
# is right for a same-population rename but actively misleading against a
# claude-print author -- there was never a window to rename in the first
# place.
#
# Prefers the ledger's own `transport` column when a row is known (an
# authoritative fact this system wrote, never a guess): `send-keys`/`acp`
# both drive a live tmux pane, so both read as `tmux`; `claude-print` and
# `pi-rpc` each name themselves, since both are the population with no pane
# at all (`dispatch-pi-rpc.sh`'s own header: "there is no tmux pane... no
# window to rename"). Falls back to the id's own shape only when no row is
# available -- `LANE_ID_RE` for `tmux`, `off-pane` otherwise -- which is
# still correct because a tmux lane id can ONLY be minted in that shape.
def lane_population(lane_id, row=None):
    """Best-effort population label for one lane id -- for an actionable
    refusal message ONLY, never for identity (that stays `lane_relation` /
    `lane_relation_from_rows`)."""
    if row:
        transport = row.get("transport") or ""
        if transport in ("send-keys", "acp"):
            return "tmux"
        if transport:
            return transport
    if isinstance(lane_id, str) and LANE_ID_RE.match(lane_id.strip()):
        return "tmux"
    return "off-pane"


def pid_is_alive(pid):
    """True unless `pid` is provably gone on THIS host.

    Deliberately asymmetric, because the two errors are not equally bad. A
    false "alive" leaves a stranded claim for the next dispatch to reap or an
    operator to clear by hand -- the cost #209 is reducing. A false "dead"
    reaps a LIVE dispatcher's claim and reopens the race #184 closed, which is
    the one-way ratchet of #124/#126. So `PermissionError` (the pid exists and
    belongs to another user) reads as alive, and anything unparseable reads as
    alive too.
    """
    try:
        pid = int(pid)
    except (TypeError, ValueError):
        return True
    if pid <= 0:
        return True
    try:
        os.kill(pid, 0)
    except ProcessLookupError:
        return False
    except PermissionError:
        return True
    except OSError:
        return True
    return True


class Ledger:
    def __init__(self, root: Path | str, *, clock=None, _migration_failpoint=None):
        self.root = Path(root)
        self.clock = clock or (lambda: int(time.time()))
        self.results_dir = self.root / "results"
        self.snapshots_dir = self.root / "snapshots"
        self.event_payloads_dir = self.root / "event-payloads"
        self.db_path = self.root / "ledger.sqlite3"
        self.lock_path = self.root / "ledger.lock"
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
        self._migrate_source_tasks_pull_uniqueness(failpoint=_migration_failpoint)

    def _connect(self, *, foreign_keys=True):
        connection = sqlite3.connect(self.db_path, timeout=30, isolation_level=None)
        connection.row_factory = sqlite3.Row
        connection.execute(f"PRAGMA foreign_keys = {'ON' if foreign_keys else 'OFF'}")
        connection.execute("PRAGMA journal_mode = WAL")
        connection.execute("PRAGMA synchronous = FULL")
        return connection

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
            fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX)
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

    def _initialize(self):
        with self._locked(), self._transaction() as connection:
            connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS lanes (
                    lane TEXT PRIMARY KEY,
                    pane_id TEXT NOT NULL,
                    nonce TEXT NOT NULL,
                    harness TEXT NOT NULL CHECK (harness IN ('codex', 'claude', 'copilot', 'copilot-acp', 'pi')),
                    repo TEXT NOT NULL,
                    server_id TEXT NOT NULL,
                    session_id TEXT NOT NULL,
                    command TEXT NOT NULL,
                    -- agent-dotfiles#237. NOT `session_id` above, which is
                    -- tmux's own `#{session_id}` (`$0`) and dies with the tmux
                    -- server. This is the HARNESS's conversation id -- the
                    -- thing `claude --resume` takes -- and it is the only part
                    -- of a lane's identity that survives a server loss,
                    -- because it names a file on disk that tmux never owned.
                    -- Empty (or NULL -- both read as "not resolved", never
                    -- "none": agent-supervisor#65) means `restore.sh` reports
                    -- this lane unrecoverable rather than starting a fresh
                    -- agent in its place. Nullable rather than NOT NULL
                    -- because a lane recorded before this column had a
                    -- resolver for its harness (every codex lane, forever)
                    -- legitimately has no value here -- that absence is
                    -- correct data, not a gap to paper over with a fake id.
                    harness_session_id TEXT DEFAULT '',
                    -- agent-supervisor#172. `repo` above is the lane's
                    -- WORKING directory -- a worktree, replaced every
                    -- dispatch -- and `claude --resume <id>` is scoped to the
                    -- directory the harness process was actually LAUNCHED in,
                    -- which is a different fact once the two diverge. They
                    -- coincide for almost every lane today, which is exactly
                    -- why this survived undetected: they do NOT coincide for
                    -- any lane whose process predates the Phase 1.5 split (the
                    -- supervisor lived in `agent-dotfiles`; `repo` was
                    -- rewritten to `agent-supervisor` on the next dispatch,
                    -- but the running process, and the id it resolves to,
                    -- still belongs to the directory it was actually started
                    -- in). Written together with `harness_session_id`
                    -- (dispatch.sh records the pane's cwd at the SAME moment
                    -- it resolves the session id, never independently), so
                    -- the two can never name different conversations. Empty
                    -- means the same as an empty `harness_session_id`: never
                    -- resolved, or a row recorded before this column existed
                    -- -- `restore.sh` refuses either case rather than
                    -- guessing `repo` in its place.
                    harness_project_dir TEXT DEFAULT '',
                    -- agent-supervisor#58 (Phase 4a): the thing this whole
                    -- migration exists for. `harness` names the AGENT; this
                    -- names how a prompt actually reaches it. Every lane
                    -- before this column existed was driven by `send-keys`
                    -- in / pattern-match out, which is exactly the class
                    -- Phase 4a retires -- so that is the default, not a
                    -- guess: an old row backfilled by this migration is
                    -- described exactly as it was actually driven, including
                    -- 'copilot-acp' lanes, which backfill to 'acp' rather
                    -- than the default (see `_migrate_lanes_table`). Never
                    -- inferred from `harness` at read time -- recorded once,
                    -- at registration, by the adapter that is actually doing
                    -- the delivering (agent-dotfiles#216 took the same
                    -- position on `harness` itself).
                    -- agent-supervisor#171: 'claude-print' added the same way
                    -- 'pi-rpc' was -- opt-in per lane, not a replacement for
                    -- the default. `claude` lanes still default to
                    -- 'send-keys' (Jon's persistent, watchable terminals are
                    -- untouched); this is for a NEW dispatch-and-collect
                    -- lane that receives a brief and returns a PR with no
                    -- pane to watch, over `claude -p --output-format json`
                    -- -- the one harness with real capacity when codex and
                    -- copilot are both exhausted (see dispatch-claude-
                    -- print.sh's header for the measurement).
                    transport TEXT NOT NULL DEFAULT 'send-keys' CHECK (transport IN ('send-keys', 'acp', 'pi-rpc', 'claude-print')),
                    updated_at INTEGER NOT NULL
                );

                -- agent-dotfiles#238. WHICH process is the supervisor was
                -- never a recorded fact -- `lanes.sh`/`dispatch.sh` inferred
                -- it from a tmux window index, and on 2026-08-12 a second,
                -- fully legitimate instance resumed in an ordinary window,
                -- inherited the full loop prompt, and dispatched the same
                -- five issues a first instance had just claimed seconds
                -- earlier. Not piggybacked onto `tasks`/`CLAIM_TASK_PREFIX`
                -- (the `lane_claim` shape this is modelled on) because
                -- `tasks.lane` is `NOT NULL REFERENCES lanes(lane)` and the
                -- supervisor is not a dispatched pane -- forcing a fake
                -- `lanes` row to hang a claim off of would make `lanes.sh`,
                -- `lane_free` and every lane-shaped reader answer questions
                -- about a pane that does not exist. A dedicated singleton
                -- table keeps the SAME pattern (owner=host:pid, INSERT-or-
                -- refuse under `_locked`+`BEGIN IMMEDIATE`, pid-liveness-
                -- gated reap, no TTL) without overloading lane semantics.
                CREATE TABLE IF NOT EXISTS supervisor_lease (
                    id TEXT PRIMARY KEY CHECK (id = 'supervisor'),
                    owner TEXT NOT NULL,
                    note TEXT NOT NULL DEFAULT '',
                    taken_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL
                );

                CREATE TABLE IF NOT EXISTS tasks (
                    id TEXT PRIMARY KEY,
                    lane TEXT NOT NULL REFERENCES lanes(lane),
                    pane_nonce TEXT NOT NULL,
                    summary TEXT NOT NULL,
                    status TEXT NOT NULL CHECK (
                        status IN ('created', 'delivery_pending', 'delivered', 'accepted',
                                   'running', 'complete', 'failed', 'cancelled')
                    ),
                    result_path TEXT,
                    result_sha256 TEXT,
                    created_at INTEGER NOT NULL,
                    updated_at INTEGER NOT NULL,
                    delivery_attempted_at INTEGER,
                    delivered_at INTEGER,
                    accepted_at INTEGER,
                    completed_at INTEGER,
                    -- agent-supervisor#117: the worktree this dispatch's
                    -- branch actually lives in, recorded once at dispatch
                    -- time. '' (never NULL) for a task dispatched before
                    -- this column existed -- see `_migrate_tasks_table`.
                    worktree_path TEXT NOT NULL DEFAULT ''
                );

                CREATE UNIQUE INDEX IF NOT EXISTS one_open_task_per_lane
                    ON tasks(lane)
                    WHERE status NOT IN ('complete', 'failed', 'cancelled');

                CREATE TABLE IF NOT EXISTS source_tasks (
                    id TEXT PRIMARY KEY,
                    source_kind TEXT NOT NULL CHECK (source_kind IN ('issue', 'pull')),
                    source_url TEXT NOT NULL,
                    source_ref TEXT NOT NULL,
                    summary TEXT NOT NULL,
                    source_state TEXT NOT NULL,
                    status TEXT NOT NULL CHECK (
                        status IN ('created', 'delivered', 'accepted', 'running',
                                   'complete', 'failed', 'cancelled')
                    ),
                    evidence_json TEXT NOT NULL,
                    status_marker TEXT,
                    updated_at INTEGER NOT NULL
                );

                CREATE TABLE IF NOT EXISTS events (
                    key TEXT PRIMARY KEY,
                    type TEXT NOT NULL,
                    task_id TEXT REFERENCES tasks(id),
                    status TEXT NOT NULL CHECK (status IN ('pending', 'notified', 'acked')),
                    payload_path TEXT,
                    created_at INTEGER NOT NULL,
                    notified_at INTEGER,
                    retry_at INTEGER,
                    acked_at INTEGER
                );

                CREATE TABLE IF NOT EXISTS components (
                    name TEXT PRIMARY KEY,
                    healthy INTEGER NOT NULL,
                    error TEXT,
                    snapshot_sha256 TEXT,
                    updated_at INTEGER NOT NULL
                );

                CREATE TABLE IF NOT EXISTS pr_verdicts (
                    repo TEXT NOT NULL,
                    number INTEGER NOT NULL,
                    verdict TEXT NOT NULL CHECK (verdict IN ('approved', 'rejected')),
                    head_sha TEXT NOT NULL,
                    reviewer TEXT NOT NULL,
                    note TEXT,
                    updated_at INTEGER NOT NULL,
                    PRIMARY KEY (repo, number)
                );

                -- agent-supervisor#308: the PR a task's own work OPENED,
                -- recorded explicitly once known, so a later
                -- `dispatch.sh --reviews-pr` authorship check is a lookup
                -- rather than a reconstruction from a branch name or a live
                -- `git worktree list`. Written best-effort, after the fact
                -- (`lane-done.sh`), because an issue-keyed dispatch has no
                -- PR number yet when it starts -- `source_tasks`'
                -- `source_kind='pull'` rows record the OPPOSITE case (a
                -- task dispatched AGAINST an already-existing PR, e.g. a
                -- `--pr`-scoped fix-pass) and are read separately, by
                -- `get_contributor_tasks_for_pr` below.
                CREATE TABLE IF NOT EXISTS pr_authorship (
                    repo TEXT NOT NULL,
                    pr_number TEXT NOT NULL,
                    task_id TEXT NOT NULL REFERENCES tasks(id),
                    recorded_at INTEGER NOT NULL,
                    PRIMARY KEY (repo, pr_number)
                );

                -- agent-supervisor#308 item 3: "no lane contributed to this
                -- PR" as a FIRST-CLASS, RECORDABLE fact, distinct from
                -- "authorship could not be resolved". A PR opened directly
                -- by a human or the watchdog has no lane contributors at
                -- all -- every free lane is a valid independent reviewer,
                -- the SAFE case -- but with nothing recording that decision
                -- it read identically to "unresolvable" and dispatch.sh
                -- refused it exactly the same way (#316/#301/#300: refused
                -- for hours, PR authored outside the lane system entirely).
                -- A row here is a DECISION an operator makes once, the same
                -- authorship test AGENTS.md applies to `sessions` above.
                CREATE TABLE IF NOT EXISTS pr_external_authorship (
                    repo TEXT NOT NULL,
                    pr_number TEXT NOT NULL,
                    note TEXT,
                    recorded_at INTEGER NOT NULL,
                    PRIMARY KEY (repo, pr_number)
                );

                -- agent-supervisor#153. Whether a tmux SESSION (not a lane,
                -- not a pane) is one this estate may act on -- dispatch to,
                -- rename windows in, kill windows in. Jon's own sessions
                -- (`Hill90`, and whatever else he runs) have been destroyed
                -- three times because nothing recorded this; the ledger's
                -- prior notion of "which sessions exist" was INFERRED from
                -- `lanes.lane` strings, and #153 measured that inference
                -- drifting both ways live: it named two sessions that no
                -- longer exist and missed three real ones, `Hill90` chief
                -- among them.
                --
                -- A row here is a DECISION, not a measurement -- the
                -- estate's own authorship test (see AGENTS.md) puts it in
                -- the ledger for that reason: adopting a session is
                -- something WE decide and must remember, the same way a
                -- lane registration is. `bootstrap-session.sh` writes the
                -- only row a fresh clone ever gets, at the moment it creates
                -- a session -- see Ledger.adopt_session. A session tmux
                -- knows about that has no row here is UNKNOWN, never
                -- supervised: absence of a row must never be read as
                -- permission (see session_state below, and
                -- TmuxTransport.session_exists for the live half of that
                -- check, deliberately kept a separate query rather than
                -- merged into this table -- a row can outlive the session it
                -- names, and conflating the two would let a stale row read
                -- as "supervised" for a session that no longer exists).
                CREATE TABLE IF NOT EXISTS sessions (
                    session TEXT PRIMARY KEY,
                    supervised_at INTEGER NOT NULL,
                    source TEXT NOT NULL DEFAULT 'bootstrap-session.sh'
                );

                -- agent-supervisor#280 (Jon: "my prompts are supposed to mean
                -- something ... the system should know"). Second set of
                -- tables in this SAME ledger -- not a new store, and
                -- deliberately not a vector database: his queries are
                -- relational and temporal ("does A conflict with B", "is
                -- this still live"), which a similarity search answers
                -- wrong on purpose -- a RETRACTED constraint reads exactly
                -- like a live one to an embedding.
                --
                -- `mine_prompts.py` (agent-supervisor#296) harvests raw
                -- turns from transcripts; it writes nothing here. A prompt
                -- becomes a row via a separate loader, out of scope for this
                -- migration.
                --
                -- `text_raw` is NEVER altered after insert -- it is the
                -- evidence a later query settles disputes against.
                -- `text_clean` is derived (spelling/grammar fixed) and may
                -- be updated in place; on any disagreement between the two,
                -- `text_raw` wins. Enforced by convention here (no writer of
                -- `text_raw` exists after `record_prompt`), and by
                -- `tests/supervisor/test_core.py`'s
                -- `test_text_raw_survives_text_clean_update`.
                CREATE TABLE IF NOT EXISTS prompts (
                    id TEXT PRIMARY KEY,
                    at INTEGER NOT NULL,
                    text_raw TEXT NOT NULL,
                    text_clean TEXT,
                    -- Load-bearing, not decoration: a prompt alone is
                    -- ambiguous ("Live." means nothing without knowing it
                    -- answered "live terminal or refreshed preview?"). This
                    -- records what was being decided at the time so a row
                    -- is never misread later for lack of it.
                    context TEXT NOT NULL,
                    session TEXT,
                    source_file TEXT
                );

                -- One row per judgement extracted from a prompt --
                -- `kind`/`weight`/`status` are the vocabulary Jon asked for
                -- by name. Itemising a prompt is a model's job, done ONCE
                -- at write time (see core.py module docstring intent and
                -- agent-supervisor#280); every read against this table
                -- after that is SQL, no model involved.
                CREATE TABLE IF NOT EXISTS items (
                    id TEXT PRIMARY KEY,
                    prompt_id TEXT NOT NULL REFERENCES prompts(id),
                    kind TEXT NOT NULL CHECK (
                        kind IN ('parameter', 'question', 'directive', 'thought', 'correction')
                    ),
                    body TEXT NOT NULL,
                    weight TEXT NOT NULL CHECK (weight IN ('hard', 'preference', 'retracted')),
                    status TEXT NOT NULL DEFAULT 'open' CHECK (
                        status IN ('open', 'acknowledged', 'acted', 'resolved', 'dropped')
                    ),
                    -- `dropped(reason)` in the brief: the reason lives here,
                    -- alongside the status it explains, rather than folded
                    -- into the status string itself -- the same shape as
                    -- `pr_verdicts.note` and `source_tasks.status_marker`
                    -- elsewhere in this ledger. NULL unless status='dropped'.
                    status_reason TEXT,
                    -- The parameter this prompt actually produced, e.g.
                    -- 'render=LIVE'. Turns "what did he mean" from literary
                    -- interpretation into a lookup -- the entire point.
                    resolved_to TEXT,
                    acked_at INTEGER
                );

                CREATE TABLE IF NOT EXISTS links (
                    item_id TEXT NOT NULL REFERENCES items(id),
                    other_item_id TEXT NOT NULL REFERENCES items(id),
                    relation TEXT NOT NULL CHECK (
                        relation IN ('conflicts_with', 'supersedes', 'depends_on')
                    ),
                    PRIMARY KEY (item_id, other_item_id, relation)
                );

                -- The views ARE the deliverable (agent-supervisor#280) --
                -- Jon asked for these five by name. Each is a plain read
                -- over `items`/`links`; nothing here calls a model, and
                -- nothing downstream should ever need to.

                -- "The thing he is anxious about": every item nobody has
                -- acknowledged yet.
                CREATE VIEW IF NOT EXISTS unacknowledged AS
                    SELECT * FROM items WHERE status = 'open';

                -- Parameters currently in force. `weight = 'hard'` implies
                -- not retracted (the three weight values are mutually
                -- exclusive), so this excludes only explicitly retracted
                -- rows, hard or soft.
                CREATE VIEW IF NOT EXISTS live_parameters AS
                    SELECT * FROM items WHERE kind = 'parameter' AND weight != 'retracted';

                -- Pairs of items Jon (or a later pass) marked as disagreeing,
                -- with enough of each side to read the conflict without a
                -- second query.
                CREATE VIEW IF NOT EXISTS conflicts AS
                    SELECT
                        l.item_id,
                        l.other_item_id,
                        a.prompt_id AS item_prompt_id,
                        a.kind AS item_kind,
                        a.status AS item_status,
                        b.prompt_id AS other_prompt_id,
                        b.kind AS other_kind,
                        b.status AS other_status
                    FROM links l
                    JOIN items a ON a.id = l.item_id
                    JOIN items b ON b.id = l.other_item_id
                    WHERE l.relation = 'conflicts_with';

                CREATE VIEW IF NOT EXISTS open_questions AS
                    SELECT * FROM items WHERE kind = 'question' AND status = 'open';

                -- The zero/too-many check: how many live HARD parameters
                -- exist right now. Zero means nothing is pinned down; more
                -- than expected means two of them disagree and `conflicts`
                -- has not been told yet.
                CREATE VIEW IF NOT EXISTS possibility_count AS
                    SELECT COUNT(*) AS count FROM live_parameters WHERE weight = 'hard';
                """
            )
        os.chmod(self.db_path, 0o600)

    # agent-supervisor#117 adds `worktree_path`: the worktree `worktree.sh
    # new` built for this dispatch, known at dispatch time alongside the
    # lane and task id but, before this, carried only as unstructured text
    # inside `summary` (see `cli.py`'s `record_dispatch` docstring) -- so
    # nothing could look it back up. See `Ledger.get_task_for_worktree`.
    _TASKS_SCHEMA_MARKERS = ("delivery_pending", "delivery_attempted_at", "worktree_path")
    # agent-dotfiles#216 added 'copilot' (a plain tmux/Node lane, distinct
    # from the ACP-driven 'copilot-acp' already here) to the same CHECK
    # constraint this migration widens -- both markers must be present or a
    # ledger created before #216 keeps rejecting the new harness forever.
    # agent-dotfiles#237 adds a THIRD marker for the same reason: a ledger
    # created before #237 has no `harness_session_id` column at all, and
    # `CREATE TABLE IF NOT EXISTS` will never add one. Without this marker the
    # live ledger -- the one carrying every lane the restore path exists for --
    # keeps the old table and every write naming the new column fails.
    # agent-supervisor#58 adds two more, for the same reason again: `'pi'`
    # (quoted, so it cannot false-match the "pi" inside "copilot") widens the
    # harness CHECK, and `transport` is a column that plain
    # `CREATE TABLE IF NOT EXISTS` will never retrofit onto an existing table.
    # agent-supervisor#172 adds a sixth: `harness_project_dir` is a new
    # column, same reasoning as `harness_session_id` above -- a ledger
    # created before it existed has no way to acquire it short of this
    # rebuild.
    # agent-supervisor#171: "'claude-print'" added as its own marker -- an
    # existing lanes table can carry every marker before it (including
    # "transport" itself, from the #58 migration) while its CHECK constraint
    # still only allows 'send-keys'/'acp'/'pi-rpc'. Without a dedicated
    # marker for the new value, a table that already has a `transport`
    # column would read as current and never rebuild, leaving the narrower
    # CHECK in place forever -- the same trap #58's own comment describes
    # for a narrow harness CHECK.
    _LANES_SCHEMA_MARKERS = (
        "copilot-acp", "'copilot'", "harness_session_id", "'pi'", "transport",
        "harness_project_dir", "'claude-print'",
    )

    def _migrate_lanes_table(self, *, failpoint=None):
        """Widen an existing `lanes` table to the current schema in place.

        `CREATE TABLE IF NOT EXISTS` in `_initialize` never touches a table
        that already exists, so a ledger created before `copilot-acp` (or
        `pi`, or the `transport` column) existed keeps rejecting/lacking them
        forever unless this runs. SQLite has no `ALTER TABLE ... ALTER
        COLUMN` / `DROP CONSTRAINT`, so the only way to widen a CHECK
        constraint -- or add a NOT NULL column with a backfill that is not a
        flat constant -- is to rebuild the table, mirroring
        `_migrate_tasks_table`.

        Every row is preserved. The rebuild is one transaction: any failure
        mid-migration rolls back to the original table, unmodified. Foreign
        key enforcement is turned off only around this rebuild (it cannot be
        toggled mid-transaction) because `tasks.lane REFERENCES lanes(lane)`
        would otherwise block dropping the original table while rows still
        reference it; the table this rebuild produces is named `lanes` again
        by the time this returns, so that reference is satisfied exactly as
        before.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT sql FROM sqlite_master WHERE type='table' AND name='lanes'"
                ).fetchone()
                if existing is None:
                    return
                if all(marker in existing["sql"] for marker in self._LANES_SCHEMA_MARKERS):
                    return
                # agent-dotfiles#237: which columns the OLD table actually has,
                # asked rather than assumed. This rebuild now runs for FOUR
                # different reasons -- a narrow harness CHECK (pre-#216), a
                # missing `harness_session_id` (pre-#237), a missing
                # `transport` (pre-agent-supervisor#58), and a missing
                # `harness_project_dir` (pre-agent-supervisor#172) -- and a
                # ledger can need any subset of them. A hardcoded copy list
                # would read a column that does not exist yet on one path, and
                # silently DROP recorded session ids on another.
                old_columns = {row["name"] for row in probe.execute("PRAGMA table_info(lanes)").fetchall()}

            harness_session_expr = "harness_session_id" if "harness_session_id" in old_columns else "''"
            # agent-supervisor#172: a pre-existing row has no recorded
            # originating project directory -- and, per that issue, it must
            # NOT be guessed as `repo`. Empty is the correct backfill, the
            # same "not resolved" reading `harness_session_id` already gets;
            # `restore.sh` fails closed on either.
            harness_project_dir_expr = "harness_project_dir" if "harness_project_dir" in old_columns else "''"
            # agent-supervisor#58: a pre-existing row was, in fact, driven by
            # `send-keys` for every harness except `copilot-acp`, which was
            # already ACP-driven (`ACPAdapter.register_lane` is the only
            # writer of that harness value) -- so the backfill records what
            # actually happened instead of a uniform guess. A ledger that
            # already has the column keeps its own recorded value untouched.
            transport_expr = (
                "transport" if "transport" in old_columns
                else "CASE WHEN harness = 'copilot-acp' THEN 'acp' ELSE 'send-keys' END"
            )

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute(
                        """
                        CREATE TABLE lanes_migrated (
                            lane TEXT PRIMARY KEY,
                            pane_id TEXT NOT NULL,
                            nonce TEXT NOT NULL,
                            harness TEXT NOT NULL CHECK (harness IN ('codex', 'claude', 'copilot', 'copilot-acp', 'pi')),
                            repo TEXT NOT NULL,
                            server_id TEXT NOT NULL,
                            session_id TEXT NOT NULL,
                            command TEXT NOT NULL,
                            -- agent-supervisor#65: NOT NULL here rejected
                            -- every pre-existing row with no resolved
                            -- session id -- every codex lane, since codex has
                            -- no resolver -- and that is legitimate data, not
                            -- a gap. See the matching column in `_initialize`.
                            harness_session_id TEXT DEFAULT '',
                            harness_project_dir TEXT DEFAULT '',
                            transport TEXT NOT NULL DEFAULT 'send-keys' CHECK (transport IN ('send-keys', 'acp', 'pi-rpc', 'claude-print')),
                            updated_at INTEGER NOT NULL
                        )
                        """
                    )
                    self._fail(failpoint, "after_create")
                    connection.execute(
                        f"""
                        INSERT INTO lanes_migrated (
                            lane, pane_id, nonce, harness, repo, server_id, session_id, command,
                            harness_session_id, harness_project_dir, transport, updated_at
                        )
                        SELECT lane, pane_id, nonce, harness, repo, server_id, session_id, command,
                               {harness_session_expr}, {harness_project_dir_expr}, {transport_expr}, updated_at
                        FROM lanes
                        """
                    )
                    self._fail(failpoint, "after_copy")
                    connection.execute("DROP TABLE lanes")
                    self._fail(failpoint, "after_drop")
                    connection.execute("ALTER TABLE lanes_migrated RENAME TO lanes")
                    self._fail(failpoint, "after_rename")
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    def _migrate_tasks_table(self, *, failpoint=None):
        """Widen an existing `tasks` table to the current schema in place.

        `CREATE TABLE IF NOT EXISTS` in `_initialize` never touches a table
        that already exists, so a ledger created before `delivery_pending`
        and `delivery_attempted_at` existed keeps its old CHECK constraint
        and column set forever unless this runs. SQLite has no
        `ALTER TABLE ... ALTER COLUMN` / `DROP CONSTRAINT`, so the only way
        to widen a CHECK constraint is to rebuild the table.

        Every row and the `one_open_task_per_lane` index are preserved. The
        rebuild is one transaction: any failure mid-migration rolls back to
        the original table, unmodified. Foreign key enforcement is turned
        off only around this rebuild (it cannot be toggled mid-transaction)
        because `events.task_id REFERENCES tasks(id)` would otherwise block
        dropping the original table while rows still reference it; the
        table this rebuild produces is named `tasks` again by the time this
        returns, so that reference is satisfied exactly as before.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT sql FROM sqlite_master WHERE type='table' AND name='tasks'"
                ).fetchone()
                if existing is None:
                    return
                if all(marker in existing["sql"] for marker in self._TASKS_SCHEMA_MARKERS):
                    return
                columns = {info["name"] for info in probe.execute("PRAGMA table_info(tasks)").fetchall()}
            attempted_column = "delivery_attempted_at" if "delivery_attempted_at" in columns else "NULL"
            # agent-supervisor#117: a pre-existing row recorded no worktree
            # path anywhere structured -- only as free text inside `summary`
            # (`worktree=<path>`). Backfilling that by parsing `summary`
            # would be writing a guess into a column whose whole point is to
            # be authored, recorded fact, so this deliberately does not
            # parse it: an old row reads '', the same "not recorded" answer
            # `get_task_for_worktree` already gives for any unmatched path.
            worktree_path_column = "worktree_path" if "worktree_path" in columns else "''"

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute(
                        """
                        CREATE TABLE tasks_migrated (
                            id TEXT PRIMARY KEY,
                            lane TEXT NOT NULL REFERENCES lanes(lane),
                            pane_nonce TEXT NOT NULL,
                            summary TEXT NOT NULL,
                            status TEXT NOT NULL CHECK (
                                status IN ('created', 'delivery_pending', 'delivered', 'accepted',
                                           'running', 'complete', 'failed', 'cancelled')
                            ),
                            result_path TEXT,
                            result_sha256 TEXT,
                            created_at INTEGER NOT NULL,
                            updated_at INTEGER NOT NULL,
                            delivery_attempted_at INTEGER,
                            delivered_at INTEGER,
                            accepted_at INTEGER,
                            completed_at INTEGER,
                            worktree_path TEXT NOT NULL DEFAULT ''
                        )
                        """
                    )
                    self._fail(failpoint, "after_create")
                    connection.execute(
                        f"""
                        INSERT INTO tasks_migrated (
                            id, lane, pane_nonce, summary, status, result_path, result_sha256,
                            created_at, updated_at, delivery_attempted_at, delivered_at,
                            accepted_at, completed_at, worktree_path
                        )
                        SELECT id, lane, pane_nonce, summary, status, result_path, result_sha256,
                               created_at, updated_at, {attempted_column}, delivered_at,
                               accepted_at, completed_at, {worktree_path_column}
                        FROM tasks
                        """
                    )
                    self._fail(failpoint, "after_copy")
                    connection.execute("DROP TABLE tasks")
                    self._fail(failpoint, "after_drop")
                    connection.execute("ALTER TABLE tasks_migrated RENAME TO tasks")
                    self._fail(failpoint, "after_rename")
                    connection.execute(
                        """
                        CREATE UNIQUE INDEX IF NOT EXISTS one_open_task_per_lane
                            ON tasks(lane)
                            WHERE status NOT IN ('complete', 'failed', 'cancelled')
                        """
                    )
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    _SOURCE_TASKS_OLD_UNIQUE_MARKER = "source_url TEXT NOT NULL UNIQUE"

    def _migrate_source_tasks_table(self, *, failpoint=None):
        """Drop `source_tasks.source_url`'s table-level UNIQUE constraint.

        agent-dotfiles#144 finding 1: `source_url` is derived from the issue
        number alone (`cli.py`'s `record_dispatch`), and `reconstruct_task`
        upserts `ON CONFLICT(id)` -- a DIFFERENT key. A second dispatch of the
        same issue under a different task id (a retry after a lane died, a
        re-brief, a follow-up review -- this estate does this constantly; see
        the docstring on `reconstruct_task`) raised `UNIQUE constraint failed:
        source_tasks.source_url` on the second attempt, permanently, for that
        issue.

        The modelling decision, argued once here rather than worked around:
        each `source_tasks` row is one recorded DISPATCH ATTEMPT (its primary
        key is the task id, not the issue), not one row per GitHub issue. The
        one caller that DOES want "one row per issue" --
        `GithubTaskSource.reconstruct`, the marker-based path -- already gets
        that for free, because its `task_id` is `_task_id(parts)`, a pure
        function of the same URL; reconstructing the same issue twice through
        that path always hits the same `id` and upserts in place regardless of
        this constraint. The URL-uniqueness was never doing anything for that
        caller and was actively wrong for `cli.py`'s -- several attempts
        legitimately share a URL, so the column is dropped rather than
        replaced with a scoped ("one OPEN attempt per URL") constraint: this
        table's own `status` is never advanced past 'created' by the
        `record_dispatch` recording path (nothing here tracks a task's real
        lifecycle back into `source_tasks`), so an open-attempt constraint
        would immediately wedge on the very case it exists to protect --
        finding 3's issue by another name. Concurrent double-dispatch of the
        same issue is `claim.sh`'s job, upstream of this layer entirely (see
        `record_dispatch`'s own docstring); this table records what was
        dispatched, it does not arbitrate whether it should have been.

        Same SQLite limitation as `_migrate_lanes_table` / `_migrate_tasks_table`:
        there is no `ALTER TABLE ... DROP CONSTRAINT`, so the only way to widen
        a column is to rebuild the table. Every row is preserved; the rebuild
        is one transaction, rolled back whole on any failure.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT sql FROM sqlite_master WHERE type='table' AND name='source_tasks'"
                ).fetchone()
                if existing is None:
                    return
                if self._SOURCE_TASKS_OLD_UNIQUE_MARKER not in existing["sql"]:
                    return

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    connection.execute(
                        """
                        CREATE TABLE source_tasks_migrated (
                            id TEXT PRIMARY KEY,
                            source_kind TEXT NOT NULL CHECK (source_kind IN ('issue', 'pull')),
                            source_url TEXT NOT NULL,
                            source_ref TEXT NOT NULL,
                            summary TEXT NOT NULL,
                            source_state TEXT NOT NULL,
                            status TEXT NOT NULL CHECK (
                                status IN ('created', 'delivered', 'accepted', 'running',
                                           'complete', 'failed', 'cancelled')
                            ),
                            evidence_json TEXT NOT NULL,
                            status_marker TEXT,
                            updated_at INTEGER NOT NULL
                        )
                        """
                    )
                    self._fail(failpoint, "after_create")
                    connection.execute(
                        """
                        INSERT INTO source_tasks_migrated (
                            id, source_kind, source_url, source_ref, summary, source_state,
                            status, evidence_json, status_marker, updated_at
                        )
                        SELECT id, source_kind, source_url, source_ref, summary, source_state,
                               status, evidence_json, status_marker, updated_at
                        FROM source_tasks
                        """
                    )
                    self._fail(failpoint, "after_copy")
                    connection.execute("DROP TABLE source_tasks")
                    self._fail(failpoint, "after_drop")
                    connection.execute("ALTER TABLE source_tasks_migrated RENAME TO source_tasks")
                    self._fail(failpoint, "after_rename")
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    ONE_OPEN_PULL_PER_SOURCE_REF = "one_open_pull_per_source_ref"

    def _migrate_source_tasks_pull_uniqueness(self, *, failpoint=None):
        """Close the PR-duplicate TOCTOU (agent-supervisor#169) at the WRITE,
        not the read.

        `dispatch.sh`'s step 0.6 (`cli.py pr-lane --pr N`, see
        `get_open_task_for_pr`) is a plain read, executed once, before a lane
        is even picked -- the write that actually claims a PR
        (`record_dispatch`, via `_reconstruct_task_tx`'s INSERT) happens
        seconds later, after lane selection, worktree creation and the brief
        being sent. That gap is real wall-clock seconds, not the sub-second
        window `claim.sh`'s own docstring accepts for the GitHub-assignee
        race -- reproduced directly (see `tests/supervisor/test_dispatch.sh`'s
        `run_race` case for this issue): two full dispatches, two lanes, one
        PR, both `delivered`, and `pr-lane` -- `ORDER BY created_at DESC LIMIT
        1` -- answers with only the winner, so a third party asking "is this
        PR claimed" sees one clean holder while a second lane silently works
        it too. The check cannot see the collision it exists to catch.

        The fix, per the reviewer's own suggestion (option (b) over (a)):
        make the SECOND writer's INSERT fail, atomically, no matter how
        close together the two attempts land -- unlike holding
        `ledger.lock` across step 0.6 through `record_dispatch` (option (a)),
        this needs no new lock discipline in `dispatch.sh` and holds nothing
        across worktree creation or a `send-keys`, seconds of I/O in a script
        whose failure modes include being SIGTERMed mid-dispatch. Step 0.6
        stays: it is now the friendly, early refusal for the common case
        (saves a wasted worktree and a stray brief); this is what makes the
        refusal true even when step 0.6 raced and both readers saw "open".

        NOT a plain partial UNIQUE index, despite `one_open_task_per_lane`
        being exactly that shape -- measured why it cannot be, here, before
        assuming this is just that pattern copied: `source_tasks.status` is
        NEVER advanced past `'created'` by the recording path (see
        `_migrate_source_tasks_table`'s own docstring, and confirmed by
        running a dispatch through `complete()` and reading
        `get_source_task` back -- `status` stays `'created'` forever). A
        partial index `WHERE status NOT IN (closed)` on `source_tasks`
        alone is therefore true for every 'pull' row ever written, closed
        or not, and would refuse a legitimate FRESH dispatch of a PR whose
        prior review or fix-pass already completed -- exactly the case
        `get_open_task_for_pr`'s own docstring (and its
        `test_get_open_task_for_pr_ignores_completed_or_cancelled_tasks`)
        says must not be blocked. The REAL lifecycle status lives on
        `tasks.status` (the state machine `_transition_tx` drives), not
        `source_tasks.status` -- and `tasks.id == source_tasks.id`, written
        in the same transaction, so a `BEFORE INSERT` trigger that joins
        `tasks` can ask the right question where a same-table partial index
        cannot. This is the same atomic-write-time gate the reviewer asked
        for -- SQLite evaluates a trigger's `RAISE(ABORT, ...)` inside the
        INSERT itself, so a second writer's transaction still fails exactly
        as a unique-index violation would, and `sqlite3.IntegrityError` is
        exactly what Python's `sqlite3` module raises for it (verified
        directly: `RAISE(ABORT, 'UNIQUE constraint failed: ...')` inside a
        trigger surfaces to Python as `sqlite3.IntegrityError`, same class
        as a real index violation) -- callers (`cli.py`'s `record_dispatch`)
        do not need to know or care which mechanism caught it.

        `id != NEW.id` in the trigger's own EXISTS clause is load-bearing:
        `_reconstruct_task_tx`'s INSERT is an `ON CONFLICT(id) DO UPDATE`
        upsert, and re-registering the SAME task id (a legitimate retry) is
        NOT a second dispatcher -- verified directly (see this migration's
        own tests) that without the exclusion, SQLite's BEFORE INSERT
        trigger fires on the initial insert attempt even when the row will
        end up UPDATEd in place, which would wrongly refuse an idempotent
        re-registration of a task that is itself the PR's own open holder.

        This never touches `source_tasks` itself -- only
        `_migrate_source_tasks_table` above does that table-rebuild dance;
        a trigger, like an index, attaches to an existing table with no
        rebuild needed. But an existing ledger can carry real pre-#169
        duplicates: exactly the "b"-suffixed double-dispatch this whole PR
        is about (#157, #149, and agent-supervisor#181/#182's merged
        defect) leaves two OPEN pull-kind rows for the same `source_ref`
        sitting in `source_tasks` right now, on any estate that hit the bug
        before this migration ran. Creating the trigger over such a ledger
        would succeed (a trigger only fires on FUTURE inserts, it does not
        scan existing rows the way `CREATE UNIQUE INDEX` does) -- which
        would silently leave the pre-existing duplicate in place forever
        while looking, to a fresh caller, exactly like a clean ledger the
        guarantee already covers. So this checks for that duplicate BY
        HAND, joined against `tasks.status` the same way the trigger itself
        will, before ever creating the trigger.

        Decision, argued once here: on finding such a duplicate, this DOES
        NOT pick a winner and silently cancel the loser -- that is exactly
        the silent data loss the brief for this fix pass forbids ("failing
        to migrate is better than silently dropping a row"), and this layer
        has no way to know which of two open dispatches is the one actually
        safe to abandon (a real agent may be mid-brief in either lane's
        pane). Instead this raises loudly, naming every conflicting id, and
        creates no trigger -- `Ledger.__init__` propagates the failure, so
        EVERY ledger operation refuses until a human reconciles the
        duplicate by hand (`cli.py record-completion` / `cli.py
        cancel-open-task --lane <lane>` on whichever lane is not actually
        still working the PR) and reopens the ledger. Blunt, but the same
        posture `_migrate_tasks_table`'s own failpoint tests already prove
        this codebase takes for a migration that cannot proceed safely: an
        unusable ledger until fixed beats a ledger that quietly drops the
        guarantee this whole fix pass exists to add.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='trigger' AND name=?",
                    (self.ONE_OPEN_PULL_PER_SOURCE_REF,),
                ).fetchone()
                if existing is not None:
                    return
                dupes = probe.execute(
                    """
                    SELECT source_tasks.source_ref AS source_ref,
                           GROUP_CONCAT(source_tasks.id) AS ids,
                           COUNT(*) AS n
                    FROM source_tasks
                    JOIN tasks ON tasks.id = source_tasks.id
                    WHERE source_tasks.source_kind = 'pull'
                      AND tasks.status NOT IN ('complete', 'failed', 'cancelled')
                    GROUP BY source_tasks.source_ref
                    HAVING COUNT(*) > 1
                    """
                ).fetchall()
            if dupes:
                detail = "; ".join(f"PR #{row['source_ref']}: {row['ids']}" for row in dupes)
                raise RuntimeError(
                    f"cannot create {self.ONE_OPEN_PULL_PER_SOURCE_REF}: pre-existing duplicate open "
                    f"pull-kind source_tasks rows for the same PR ({detail}) -- resolve by hand "
                    "(cli.py record-completion, or cli.py cancel-open-task --lane <lane>, on whichever "
                    "lane is not actually still working the PR) and reopen the ledger; refusing to "
                    "silently pick a winner or drop a row"
                )
            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    self._fail(failpoint, "before_pull_trigger")
                    connection.execute(
                        f"""
                        CREATE TRIGGER IF NOT EXISTS {self.ONE_OPEN_PULL_PER_SOURCE_REF}
                        BEFORE INSERT ON source_tasks
                        WHEN NEW.source_kind = 'pull' AND EXISTS (
                            SELECT 1 FROM source_tasks
                            JOIN tasks ON tasks.id = source_tasks.id
                            WHERE source_tasks.source_kind = 'pull'
                              AND source_tasks.source_ref = NEW.source_ref
                              AND source_tasks.id != NEW.id
                              AND tasks.status NOT IN ('complete', 'failed', 'cancelled')
                        )
                        BEGIN
                            SELECT RAISE(ABORT, 'UNIQUE constraint failed: source_tasks.source_ref');
                        END
                        """
                    )
                    self._fail(failpoint, "after_pull_trigger")
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
        row = connection.execute("SELECT nonce FROM lanes WHERE lane = ?", (lane,)).fetchone()
        if row is None:
            raise ValueError(f"unknown lane: {lane}")
        if row["nonce"] != pane_nonce:
            raise ValueError("pane incarnation does not match registered lane")

    @staticmethod
    def _cancel_task_row(connection, task_id, now):
        connection.execute(
            "UPDATE tasks SET status='cancelled', updated_at=?, completed_at=? WHERE id=?",
            (now, now, task_id),
        )
        connection.execute(
            "UPDATE source_tasks SET status='cancelled', updated_at=? WHERE id=?",
            (now, task_id),
        )

    # agent-supervisor#58: which transport a harness is allowed to record
    # itself under. Not "every harness may claim any transport" -- a
    # 'copilot-acp' lane IS the ACP adapter by construction
    # (`ACPAdapter.register_lane` refuses any other harness), so a
    # send-keys-labelled 'copilot-acp' row would be a lie no reader could
    # detect. 'pi' is the one harness genuinely allowed either: a pi lane may
    # be driven over its documented RPC (`PiRPCAdapter`) or, same as any
    # other harness, over plain `send-keys` if that is what actually
    # dispatched it.
    #
    # agent-supervisor#171: 'claude' widened the same way 'pi' was in #58 --
    # a second, genuinely opt-in transport, not a replacement for the
    # default. `send-keys` stays first here ON PURPOSE, and this tuple is
    # NOT where #171's "flip the default" lives -- do not "fix" it by
    # reordering to ('claude-print', 'send-keys'). This is also `bootstrap-
    # session.sh`'s and `cli.py register`'s own undeclared-transport default
    # for a REAL tmux pane (a standing lane Jon watches/interrupts), so
    # reordering it would make every one of those registrations lie about
    # its transport -- claiming `claude-print` for a lane still driven by
    # actual send-keys. The real default flip is in `dispatch.sh`, one layer
    # up: a plain, single-issue, non-PR-scoped `claude` dispatch now mints a
    # brand-new `claude-print` lane instead of using a `lane-free` tmux
    # candidate at all (see dispatch.sh's own "1.5" step) -- so it never
    # reaches `_register_lane_tx` with `transport=None` for that case, and
    # this default is simply never consulted for it. What DOES still reach
    # this default undeclared: `bootstrap-session.sh`, a bare `cli.py
    # register`, and any `claude` candidate a caller opts back onto tmux
    # with `--live-pane` -- all genuinely tmux/send-keys lanes, which is
    # exactly what this tuple's order is for. 'claude-print' is for a lane
    # that is dispatched, does not need to be watched, and reports back
    # once -- `ClaudePrintAdapter` refuses to register any lane whose
    # harness is not 'claude', same posture `PiRPCAdapter` already takes
    # for 'pi'.
    _TRANSPORTS_BY_HARNESS = {
        "codex": ("send-keys",),
        "claude": ("send-keys", "claude-print"),
        "copilot": ("send-keys",),
        "copilot-acp": ("acp",),
        "pi": ("send-keys", "pi-rpc"),
    }

    def _register_lane_tx(
        self,
        connection,
        *,
        lane,
        pane_id,
        nonce,
        harness,
        repo,
        server_id,
        session_id,
        command,
        now,
        harness_session_id="",
        harness_project_dir="",
        transport=None,
    ):
        if harness not in self._TRANSPORTS_BY_HARNESS:
            raise ValueError("unsupported harness")
        if transport is None:
            # Undeclared means "the pre-Phase-4a default for this harness":
            # every harness that has ever been dispatchable only one way gets
            # that way; 'copilot-acp' gets 'acp', its only real transport.
            transport = self._TRANSPORTS_BY_HARNESS[harness][0]
        if transport not in self._TRANSPORTS_BY_HARNESS[harness]:
            raise ValueError(f"harness {harness!r} cannot record transport {transport!r}")
        if not all((lane, pane_id, nonce, repo, server_id, session_id, command)):
            raise ValueError("lane registration fields must be non-empty")
        current = connection.execute("SELECT * FROM lanes WHERE lane = ?", (lane,)).fetchone()
        # agent-dotfiles#237: `harness_session_id` is deliberately NOT in the
        # identity tuple below. A lane's conversation id changes within one
        # pane incarnation every time the agent runs `/clear` (measured: a
        # Claude Code pane's process env keeps its launch-time id while the
        # live session file is a different uuid), and treating that as a new
        # incarnation would CANCEL the lane's open task -- turning a routine
        # `/clear` into a lost dispatch.
        changed_identity = current is not None and any(
            current[field] != value
            for field, value in (
                ("pane_id", pane_id),
                ("nonce", nonce),
                ("harness", harness),
                ("repo", repo),
                ("server_id", server_id),
                ("session_id", session_id),
                ("command", command),
                ("transport", transport),
            )
        )
        # What an empty `harness_session_id` argument means, and it is never
        # "erase what is recorded": callers that do not resolve one (the #174
        # first-sight backfill, `register`, `bootstrap-session.sh`) pass
        # nothing, and must not wipe an id a real dispatch resolved. The one
        # case where the recorded id is actively WRONG is a new incarnation --
        # a different pane or a restarted agent -- so that, and only that,
        # clears it. Empty then reads "not resolved" for the new incarnation,
        # which is what `restore.sh` refuses on.
        if not harness_session_id:
            harness_session_id = "" if changed_identity or current is None else current["harness_session_id"]
        # agent-supervisor#172: the same rule, for the same reason, applied to
        # the directory that id was resolved IN. A caller that resolves a
        # fresh `harness_session_id` always passes both together (see
        # dispatch.sh); a caller that does not resolve one (this row's
        # identity changed, or nothing was resolved) must not have this
        # column diverge from `harness_session_id` -- a stale project dir
        # paired with a cleared session id would be meaningless, and a stale
        # dir paired with an UNCHANGED session id would silently assert a
        # directory nobody just verified.
        if not harness_project_dir:
            harness_project_dir = "" if changed_identity or current is None else current["harness_project_dir"]
        if changed_identity:
            # A changed identity is a genuinely new incarnation. An
            # outstanding task still bound to the old incarnation must not
            # be silently REBOUND to it -- except `delivery_pending`,
            # which has its own reconciliation escape valve keyed off the
            # task's own recorded pane_nonce (`_reconcile_transition`),
            # not the lane's current one. #871 depends on re-registration
            # succeeding over a `delivery_pending` task to recover a dead
            # pane; every other outstanding status has no such recovery
            # path and would otherwise be orphaned by this rebind.
            outstanding = connection.execute(
                "SELECT id FROM tasks WHERE lane = ? AND status NOT IN "
                "('complete', 'failed', 'cancelled', 'delivery_pending')",
                (lane,),
            ).fetchone()
            if outstanding is not None:
                # agent-dotfiles#144 finding 3: this used to raise here,
                # unconditionally, and the ONLY way out was `lane-done.sh`
                # moving that exact task to a terminal status by renaming the
                # window it owns. A lane freed any other way -- renamed by
                # hand, a worker that died mid-turn, the completion signal
                # never firing (#102, #123, #126 were exactly this) -- wedged
                # every subsequent `register_lane` call for that lane forever,
                # with no self-heal. `cancel_open_task` already existed for
                # exactly this and had no caller anywhere in this tree: the
                # fifth instance of a durability tool built with no wiring
                # (`acp_transport.py` #56, `worktree.sh` #81, `advance-live.sh`,
                # and #140 itself named this exact risk before merging).
                #
                # A CHANGED IDENTITY is the evidence that the old task can
                # never complete through this pane again -- nothing watching
                # tmux would ever see the rename or crash that produced this
                # call; only the caller registering a BRAND NEW incarnation in
                # the lane's place can know that. Cancel the stale task and
                # proceed, rather than wedge the recorder for that lane
                # permanently. Scoped to the exact row `outstanding` above
                # already found -- `delivery_pending` is excluded from that
                # query and stays excluded here, unchanged from #871: it has
                # its own reconciliation path and must not be silently
                # discarded by a re-registration racing an in-flight send.
                self._cancel_task_row(connection, outstanding["id"], now)
        connection.execute(
            """
            INSERT INTO lanes(lane, pane_id, nonce, harness, repo, server_id,
                              session_id, command, harness_session_id, harness_project_dir, transport, updated_at)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(lane) DO UPDATE SET
                pane_id=excluded.pane_id,
                nonce=excluded.nonce,
                harness=excluded.harness,
                repo=excluded.repo,
                server_id=excluded.server_id,
                session_id=excluded.session_id,
                command=excluded.command,
                harness_session_id=excluded.harness_session_id,
                harness_project_dir=excluded.harness_project_dir,
                transport=excluded.transport,
                updated_at=excluded.updated_at
            """,
            (lane, pane_id, nonce, harness, repo, server_id, session_id, command, harness_session_id,
             harness_project_dir, transport, now),
        )
        row = connection.execute("SELECT * FROM lanes WHERE lane = ?", (lane,)).fetchone()
        return self._dict(row)

    def register_lane(
        self, *, lane, pane_id, nonce, harness, repo, server_id, session_id, command, harness_session_id="",
        harness_project_dir="", transport=None
    ):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._register_lane_tx(
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
                transport=transport,
                now=now,
            )

    def get_lane(self, lane):
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM lanes WHERE lane = ?", (lane,)).fetchone())

    def lane_available(self, lane):
        """Tri-state: None (lane unknown to the ledger), True (registered,
        no outstanding task), False (registered, an outstanding task owns it).

        agent-dotfiles#174: this is the query `dispatch.sh` now trusts instead
        of the tmux window name. `None` is deliberately distinct from `False`
        -- a lane the ledger has never heard of is not the same claim as one
        it knows is busy, and the caller (dispatch.sh's lane-free backfill)
        needs to tell them apart to decide whether a first-sight registration
        is even in play. "Outstanding" mirrors the `one_open_task_per_lane`
        index: any task not in `complete`, `failed` or `cancelled`.
        """
        with contextlib.closing(self._connect()) as connection:
            if connection.execute("SELECT 1 FROM lanes WHERE lane = ?", (lane,)).fetchone() is None:
                return None
            open_task = connection.execute(
                "SELECT 1 FROM tasks WHERE lane = ? AND status NOT IN ('complete', 'failed', 'cancelled')",
                (lane,),
            ).fetchone()
            return open_task is None

    def open_task_for_lane(self, lane):
        """Return the outstanding task that makes `lane_available` false.

        This is the diagnostic half of `lane_available`: dispatch still makes
        the yes/no decision through that API, but a refusal must explain which
        row caused the lane to be excluded instead of collapsing every case
        into "no free lane".
        """
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute(
                "SELECT * FROM tasks WHERE lane = ? AND status NOT IN ('complete', 'failed', 'cancelled') "
                "ORDER BY created_at DESC LIMIT 1",
                (lane,),
            ).fetchone()
        return self._dict(row)

    def list_lanes(self):
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute("SELECT * FROM lanes ORDER BY lane").fetchall()
        return [self._dict(row) for row in rows]

    # agent-supervisor#153. Idempotent by design (`INSERT ... ON CONFLICT DO
    # UPDATE`, not `INSERT OR IGNORE`): `bootstrap-session.sh --add-lanes`
    # against a session it already adopted must not fail this call, and a
    # later re-adopt should refresh `supervised_at` rather than silently keep
    # whichever timestamp happened to land first.
    def adopt_session(self, session, *, source="bootstrap-session.sh"):
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            connection.execute(
                "INSERT INTO sessions (session, supervised_at, source) VALUES (?, ?, ?) "
                "ON CONFLICT(session) DO UPDATE SET supervised_at = excluded.supervised_at, "
                "source = excluded.source",
                (session, now, source),
            )
        return {"session": session, "supervised_at": now, "source": source}

    def session_marked_supervised(self, session):
        """The ledger's half of a session's supervision state -- a pure
        record read, no tmux, no default-true anywhere on this path.

        Absence of a row returns False, never raises and never guesses: a
        lookup error here (a locked ledger, a missing table on an old copy)
        must surface as an exception the caller fails closed on, not as this
        function inventing an answer. See TmuxTransport.session_exists for
        the other half (does the session actually still exist) and
        `session_state` in cli.py for where the two are combined into the
        three-state read #153 asked for.
        """
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute(
                "SELECT 1 FROM sessions WHERE session = ?", (session,)
            ).fetchone()
        return row is not None

    def list_sessions(self):
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute("SELECT * FROM sessions ORDER BY session").fetchall()
        return [self._dict(row) for row in rows]

    def restore_plan(self):
        """Every lane the ledger knows, with the one thing tmux cannot hold.

        agent-dotfiles#237. This is the whole read side of the restore path,
        and it is a LEDGER query on purpose: after a tmux server loss there is
        no window name left to consult, and the names a restored server does
        produce are a stale snapshot -- the #237 incident is an operator
        trusting exactly those names and killing nine live agents.

        Per lane: its registered pane cwd, its harness, its harness session id
        (empty when none was ever resolved), the directory that session id was
        resolved IN (agent-supervisor#172 -- empty under the same rule, and
        never assumed equal to the pane cwd: `--resume` is scoped to the
        directory the harness process actually started in, which a worktree
        rewrite can leave behind), and the open task that owns it if one does.
        `task` is the task id, which `dispatch.sh` sets to the window name it
        intends -- so the name is a PROJECTION of this record, never its
        source. A lane with no open task is reported with `task: None`; the
        caller decides whether a lane with nothing outstanding is worth
        resuming (`restore.sh` starts it fresh, since there is no conversation
        to lose).
        """
        with contextlib.closing(self._connect()) as connection:
            lanes = connection.execute("SELECT * FROM lanes ORDER BY lane").fetchall()
            plan = []
            for lane in lanes:
                task = connection.execute(
                    "SELECT id, summary, status FROM tasks WHERE lane = ? "
                    "AND status NOT IN ('complete', 'failed', 'cancelled') "
                    "ORDER BY created_at DESC LIMIT 1",
                    (lane["lane"],),
                ).fetchone()
                plan.append(
                    {
                        "lane": lane["lane"],
                        "harness": lane["harness"],
                        "harness_session_id": lane["harness_session_id"],
                        "harness_project_dir": lane["harness_project_dir"],
                        "repo": lane["repo"],
                        "pane_id": lane["pane_id"],
                        "task": None if task is None else task["id"],
                        "summary": None if task is None else task["summary"],
                        "status": None if task is None else task["status"],
                    }
                )
        return plan

    def get_component(self, name):
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute("SELECT * FROM components WHERE name = ?", (name,)).fetchone()
        return self._dict(row)

    def get_task(self, task_id):
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone())

    def get_open_task_for_lane(self, lane):
        """The single outstanding row that occupies a lane, whatever its id shape.

        agent-supervisor#36 (second issue comment): a lane's outstanding row
        is not always a dispatched task -- `claim_lane` writes a
        `ledger-claim:<lane>:<token>` row under the same `tasks` table, and an
        operator recovering a stranded lane by hand does not always know
        which shape it is, only the lane. Same SELECT `_cancel_open_task_tx`
        uses to find "whatever owns this lane" -- that method exists
        precisely because a lane can be occupied by either shape and the
        caller should not have to know which -- but this is read-only, for a
        caller (`record_completion`) that must NOT cancel what it finds.
        """
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute(
                "SELECT * FROM tasks WHERE lane = ? AND status NOT IN ('complete','failed','cancelled')",
                (lane,),
            ).fetchone()
        return self._dict(row)

    # agent-supervisor#146: issue and PR numbers are NOT unique across the
    # repos this estate tracks in parallel since #111's session-per-repo --
    # `#181` names both a `skills` issue and an `agent-dotfiles` issue, and
    # a number-keyed lookup that does not also key on repo silently answers
    # for whichever repo's row happens to sort first/last. `source_url` is
    # the one place a dispatch already records which repo it was FOR (see
    # `record_dispatch` in cli.py, which writes
    # `https://github.com/<owner>/<name>/issues/<n>` or `.../pull/<n>`) --
    # this is the same extraction `cli.py`'s own
    # `_release_issue_claim_for_task` already does for the identical reason,
    # kept here rather than imported so `core.py` has no dependency on
    # `cli.py`.
    _SOURCE_URL_REPO_RE = re.compile(r"github\.com/([^/\s]+/[^/\s]+)/(?:issues|pull)/")

    @classmethod
    def _repo_from_source_url(cls, source_url):
        if not source_url:
            return None
        match = cls._SOURCE_URL_REPO_RE.search(source_url)
        return match.group(1) if match else None

    def get_task_for_issue(self, issue_ref, repo=None):
        """The most recent task dispatched for a GitHub issue -- keyed by the
        issue number, never by a branch name.

        `record_dispatch` (via `cli.py`'s free `record_dispatch`) writes
        `source_tasks.source_ref` as `str(primary)`, the issue this dispatch
        was FOR -- see that function's docstring. `source_tasks.id` and
        `tasks.id` are the same task id, written in the same transaction, so
        this join needs no third mapping table. Ordered by `tasks.created_at`
        DESC: an issue re-dispatched after a prior task finished (recycled,
        or given to a second lane) has more than one row, and the most
        recent dispatch is the one that actually holds the issue now.

        agent-supervisor#146: `repo`, when given (`"<owner>/<name>"`),
        narrows to rows whose `source_url` names that exact repo -- see
        `_repo_from_source_url`. When omitted and the issue number resolves
        in more than one repo, this refuses (`None`) rather than guess which
        repo's row is "most recent" -- the same fail-closed posture
        `get_author_task_for_issue` takes for the identical ambiguity.
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT tasks.*, source_tasks.source_url AS _source_url FROM tasks
                JOIN source_tasks ON source_tasks.id = tasks.id
                WHERE source_tasks.source_kind = 'issue' AND source_tasks.source_ref = ?
                ORDER BY tasks.created_at DESC
                """,
                (str(issue_ref),),
            ).fetchall()
        candidates = [self._dict(row) for row in rows]
        for candidate in candidates:
            candidate["_repo"] = self._repo_from_source_url(candidate.pop("_source_url", None))
        if repo is not None:
            candidates = [c for c in candidates if c["_repo"] == repo]
        elif len({c["_repo"] for c in candidates}) > 1:
            return None
        if not candidates:
            return None
        winner = dict(candidates[0])
        winner.pop("_repo", None)
        return winner

    def get_open_task_for_pr(self, pr_ref, repo=None):
        """The open task (if any) already dispatched FOR a PR, keyed by PR
        number -- never by issue, never by branch name.

        agent-supervisor#159: `dispatch.sh` used to have no way to represent
        "work on PR N" as distinct from "work on issue N" -- a review or a
        fix pass on a PR whose underlying issue was still claimed by the
        in-flight work that opened the PR had nowhere to record itself
        except `claim.sh take` on that same issue, which correctly refused
        (it is claimed) and pushed dispatch to a ledger-invisible tmux
        hand-off instead. THE HARM #159 measured from that hand-off: a
        second dispatcher, unable to see the first lane's claim anywhere,
        minted a second task for the same PR ("...b" suffixed window names
        on #157's review and #149's fix pass).

        The fix is not a new claim primitive: `record_dispatch` (via
        `cli.py`'s free function) already writes a `source_tasks` row per
        dispatch, and that table's `source_kind` column has allowed `'pull'`
        alongside `'issue'` since #144 -- this is the FIRST caller to ask for
        one back. A PR-scoped dispatch (`dispatch.sh --pr <N>`, and
        `--reviews-pr <N>` which now implies it) records `source_kind='pull'`,
        `source_ref=str(N)` instead of the issue-keyed pair, going through
        the exact same one-transaction `record_dispatch` write and the same
        `one_open_task_per_lane` uniqueness every other dispatch already
        relies on -- no new table, no second bookkeeping mechanism to keep in
        sync with the first.

        UNLIKE `get_task_for_issue` (which answers with the most recent row
        regardless of status, because its only caller today is a diagnostic
        query with no live caller in dispatch.sh), this filters to OPEN
        status -- the same `NOT IN ('complete','failed','cancelled')` test
        `get_open_task_for_lane` uses -- because the question this answers is
        "is somebody working this PR RIGHT NOW", asked by `dispatch.sh`
        BEFORE it selects a lane, so a finished or cancelled prior review of
        the same PR does not wrongly refuse a fresh one.

        agent-supervisor#146: `repo`, when given, narrows to a row whose
        `source_url` names that exact repo -- see `_repo_from_source_url`.
        `one_open_pull_per_source_ref` (the trigger backing this table)
        currently keys ONLY on PR number, not `(repo, number)`, so at most
        one open row can exist for a given number regardless of repo; this
        parameter still matters when a caller asks about a specific repo's
        PR N and the one open row that number has belongs to a DIFFERENT
        repo's PR N -- without filtering, this answered `known:true` for a
        PR nobody had actually claimed in the caller's repo.
        """
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute(
                """
                SELECT tasks.*, source_tasks.source_url AS _source_url FROM tasks
                JOIN source_tasks ON source_tasks.id = tasks.id
                WHERE source_tasks.source_kind = 'pull' AND source_tasks.source_ref = ?
                  AND tasks.status NOT IN ('complete','failed','cancelled')
                ORDER BY tasks.created_at DESC
                LIMIT 1
                """,
                (str(pr_ref),),
            ).fetchone()
        result = self._dict(row)
        if result is None:
            return None
        source_url = result.pop("_source_url", None)
        if repo is not None and self._repo_from_source_url(source_url) != repo:
            return None
        return result

    @staticmethod
    def _task_looks_like_review(task_id, summary):
        # `task_id` and `summary` are joined with a literal space before
        # matching, so a task id whose "review"/"rev" run sits at the id's
        # own end (e.g. "as76-rev73b") is followed by that space, not by
        # end-of-string or a `-`/`_` -- the trailing boundary must accept
        # whitespace too, or an id-only match like that silently never fires
        # unless the summary text happens to say "review" as well.
        text = f"{task_id or ''} {summary or ''}".lower()
        return bool(
            re.search(r"(^|[-_])(review|rev)[-_0-9a-z]*($|[-_\s])", text)
            or re.search(r"\breview(ing|s)?\s+(pr|pull request|#[0-9]+)", text)
        )

    def _non_review_tasks_for_issue(self, issue_ref, repo=None):
        """Every non-review task ever dispatched against this issue, oldest
        first -- the raw candidate pool `get_author_task_for_issue` narrows
        to one and `get_contributor_tasks_for_issue` (agent-supervisor#190)
        returns whole. One query, so the two callers cannot drift on what
        counts as a candidate the way #108 already drifted on lane identity.

        agent-supervisor#146: each candidate carries an internal `_repo` key
        (the owner/name extracted from its dispatch's `source_url`, `None`
        when unextractable) so callers can tell a same-numbered issue in a
        DIFFERENT repo apart from a genuine re-dispatch of the SAME repo's
        issue. `repo`, when given, narrows the candidate pool to that repo
        up front; callers strip `_repo` before returning a row to their own
        caller.
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT tasks.*, source_tasks.source_url AS _source_url FROM tasks
                JOIN source_tasks ON source_tasks.id = tasks.id
                WHERE source_tasks.source_kind = 'issue' AND source_tasks.source_ref = ?
                ORDER BY tasks.created_at ASC, tasks.id ASC
                """,
                (str(issue_ref),),
            ).fetchall()
        candidates = []
        for row in rows:
            if self._task_looks_like_review(row["id"], row["summary"]):
                continue
            candidate = self._dict(row)
            candidate["_repo"] = self._repo_from_source_url(candidate.pop("_source_url", None))
            candidates.append(candidate)
        if repo is not None:
            candidates = [c for c in candidates if c["_repo"] == repo]
        return candidates

    @staticmethod
    def _strip_repo(candidate):
        return {key: value for key, value in candidate.items() if key != "_repo"}

    # `dispatch.sh`/`worktree.sh` mint every branch as `<prefix>/<issue>-<slug>`
    # (or a hand-pushed `fix|feat|chore|docs/<issue>-<slug>`) and the SAME
    # dispatch records that task under id `<window-prefix><issue>-<slug>` --
    # see dispatch.sh's own comment above `AUTHOR_LANE=""`. `<issue>-<slug>`
    # is therefore a suffix of the authoring task's id, deterministically, for
    # every dispatch that followed the convention.
    _HEAD_REF_RE = re.compile(r"^(?:lane|fix|feat|chore|docs)/([0-9]+)-(.+)$")

    def get_contributor_tasks_for_issue(self, issue_ref, repo=None):
        """The full CONTRIBUTOR SET for an issue's PR -- every non-review
        task ever dispatched against it, not narrowed to a single "author".

        agent-supervisor#190. `get_author_task_for_issue` below deliberately
        narrows multiple non-review candidates down to the one that produced
        the PR's branch (or refuses, returning `None`, when it cannot tell).
        That narrowing is correct for NAMING "the author" but wrong for
        author-EXCLUSION at a review dispatch: a fix-pass task dispatched
        against the SAME issue to address review findings (e.g.
        `as178-fix186`, fixing PR #186) is itself a non-review candidate for
        that issue's `source_ref`, and #190 recorded two live dispatches
        where a fix-pass lane was handed the re-review of its own fix
        because only the single narrowed-down author was excluded -- the
        fix-pass task was sitting in this exact candidate pool the whole
        time, just discarded by the narrowing.

        Every non-review candidate for the issue is returned here, unfiltered
        by branch-name matching. This can over-include (an abandoned prior
        attempt at the same issue, never actually part of the PR now under
        review) but that is the SAFE direction: it costs dispatch.sh a
        candidate lane it would otherwise have picked, never the reverse,
        and dispatch.sh only refuses the whole dispatch if EVERY free lane
        is in the excluded set (agent-supervisor#124/#126 -- an unresolvable
        or over-cautious answer must make a lane less dispatchable, never
        more).

        Deliberately does NOT read `git log` for author identity: every lane
        in this estate commits under the same GitHub identity (`Jon Hill` /
        `jonhill90`, see agent-supervisor#184), so git authorship cannot
        distinguish one lane's commits from another's on a shared branch --
        only the ledger, which recorded who each task was dispatched to,
        can.

        agent-supervisor#146: `repo`, when given, narrows the candidate pool
        to that repo before anything else runs -- see
        `_non_review_tasks_for_issue`. Deliberately does NOT fail closed on
        cross-repo ambiguity when `repo` is omitted, unlike
        `get_author_task_for_issue`: over-including a same-numbered issue's
        tasks from a DIFFERENT repo in this SET only costs dispatch.sh an
        extra excluded candidate lane, the same safe direction the
        docstring above already documents for an abandoned same-repo
        attempt.
        """
        return [self._strip_repo(c) for c in self._non_review_tasks_for_issue(issue_ref, repo=repo)]

    def get_author_task_for_issue(self, issue_ref, head_ref=None, repo=None):
        """The task whose dispatch produced this issue's current PR.

        agent-supervisor#76: a review task must never be eligible as the
        author of the PR it reviewed.

        agent-supervisor#77: position in the task list (first, or most
        recent) is not a reliable signal of authorship either -- an issue
        re-dispatched after a prior attempt was abandoned has more than one
        non-review task, and neither "first" nor "last" is right in general;
        the reviewer reproduced the "first" rule picking a stale, abandoned
        attempt over the task that actually produced the PR. `head_ref`, the
        PR's own head branch, resolves this the way the review asked: by
        what actually produced the branch, not by ordering. When it is
        absent, or it does not disambiguate, this is only safe to answer
        when exactly one non-review task exists -- anything else is a
        genuine "don't know", returned as `None` rather than guessed at.

        agent-supervisor#146: `repo`, when given, narrows to that repo's
        candidates before anything else runs. When omitted and the SAME
        issue number resolves in more than one repo -- `#181` is both a
        `skills` issue and an `agent-dotfiles` issue -- this refuses
        (`None`) rather than answer for whichever repo's row the ordering
        or head-ref match happens to favor. This is THE fix for
        agent-supervisor#146: before it, an unscoped lookup answered
        `known:true` for a different repo's lane entirely, which the
        author-exclusion guard could not tell apart from a real answer.
        """
        candidates = self._non_review_tasks_for_issue(issue_ref, repo=repo)
        if not candidates:
            return None
        if repo is None and len({c["_repo"] for c in candidates}) > 1:
            return None

        if head_ref:
            match = self._HEAD_REF_RE.match(head_ref)
            if match and match.group(1) == str(issue_ref):
                suffix = f"{match.group(1)}-{match.group(2)}"
                by_branch = [task for task in candidates if task["id"].endswith(suffix)]
                if len(by_branch) == 1:
                    return self._strip_repo(by_branch[0])

        if len(candidates) == 1:
            return self._strip_repo(candidates[0])
        return None

    def get_contributor_tasks_for_pr(self, pr_ref, repo=None):
        """The full CONTRIBUTOR SET dispatched DIRECTLY against this PR --
        every non-review `source_kind='pull'` task ever recorded for it,
        unfiltered by status. Resolution path five (agent-supervisor#308),
        alongside `get_contributor_tasks_for_issue` (by issue),
        `get_task_for_worktree` (by worktree path) and the legacy
        branch-name convention dispatch.sh falls back to.

        `get_open_task_for_pr` above answers "is somebody working this PR
        RIGHT NOW" and deliberately filters to open status for that reason.
        Authorship exclusion asks a different question -- "has anybody EVER
        contributed to this PR" -- which a completed or cancelled prior
        review or fix-pass still answers `yes` to.

        agent-supervisor#308 (#302's own measurement): a fix-pass or review
        dispatched with `--pr <N>` / `--reviews-pr <N>` writes
        `source_kind='pull', source_ref=str(N)` at dispatch time -- an
        exact, structured record of "this task worked PR N directly", no
        branch name or live git state involved. Before this method, nothing
        ever read that record back for authorship: `--reviews-pr`'s
        resolution chain queried `source_kind='issue'` and the
        worktree/branch fallbacks, but never the PR's own `source_kind='pull'`
        rows -- the most direct evidence the ledger has. Two live fix-pass
        tasks dispatched directly against PR #302 sat unconsulted in this
        exact table while its review refused for six hours.

        agent-supervisor#146: `repo`, when given, narrows to that repo's
        rows (see `_repo_from_source_url`); omitted, this stays over-inclusive
        on purpose, the same safe direction `get_contributor_tasks_for_issue`
        documents -- a same-numbered PR in a different repo costs an extra
        excluded candidate lane, never a missed one.
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT tasks.*, source_tasks.source_url AS _source_url FROM tasks
                JOIN source_tasks ON source_tasks.id = tasks.id
                WHERE source_tasks.source_kind = 'pull' AND source_tasks.source_ref = ?
                ORDER BY tasks.created_at ASC, tasks.id ASC
                """,
                (str(pr_ref),),
            ).fetchall()
        candidates = []
        for row in rows:
            if self._task_looks_like_review(row["id"], row["summary"]):
                continue
            candidate = self._dict(row)
            candidate_repo = self._repo_from_source_url(candidate.pop("_source_url", None))
            if repo is not None and candidate_repo != repo:
                continue
            candidates.append(candidate)
        return candidates

    def record_pr_for_task(self, *, task_id, repo, pr_number, now=None):
        """Record explicitly that `task_id`'s own work OPENED `pr_number` in
        `repo` -- agent-supervisor#308 item 1.

        Distinct from `source_tasks`' `source_kind='pull'` rows (which
        record a PR-SCOPED DISPATCH, made when the PR already exists): this
        is written after the fact, for the ORIGINATING dispatch -- one made
        by issue number, before its own PR existed -- which `source_tasks`
        never associates with the PR number at all, and which the issue-based
        resolution path can already answer while it is open but loses once
        the PR's body/commits stop naming the issue in a form `dispatch.sh`
        can parse. `INSERT OR REPLACE`: a PR has one recorded author task; a
        second call for the same (repo, pr_number) corrects rather than
        duplicates. The caller is trusted to have confirmed the task actually
        produced this PR (`lane-done.sh`, from the branch its own worktree
        built) -- this write has no independent way to verify that itself.
        """
        if not self.get_task(task_id):
            raise ValueError(f"unknown task: {task_id}")
        now = int(now if now is not None else self.clock())
        with self._transaction() as connection:
            connection.execute(
                """
                INSERT INTO pr_authorship (repo, pr_number, task_id, recorded_at)
                VALUES (?, ?, ?, ?)
                ON CONFLICT (repo, pr_number) DO UPDATE SET
                    task_id = excluded.task_id, recorded_at = excluded.recorded_at
                """,
                (repo, str(pr_number), task_id, now),
            )

    def get_task_for_pr_number(self, *, repo, pr_number):
        """The task explicitly recorded (`record_pr_for_task`) as having
        opened `pr_number` in `repo`, if any -- a lookup, not a heuristic.
        Regardless of the task's current status: the record is a durable
        fact about what happened, not a claim about what is happening now.
        """
        with contextlib.closing(self._connect()) as connection:
            link = connection.execute(
                "SELECT task_id FROM pr_authorship WHERE repo = ? AND pr_number = ?",
                (repo, str(pr_number)),
            ).fetchone()
            if link is None:
                return None
            row = connection.execute(
                "SELECT * FROM tasks WHERE id = ?", (link["task_id"],)
            ).fetchone()
        return self._dict(row)

    def mark_pr_external(self, *, repo, pr_number, note, chain_verified=False, now=None):
        """Record that `pr_number` in `repo` was authored OUTSIDE the lane
        system (a human, or the watchdog acting directly) -- a first-class,
        recordable state distinct from "unknown" (agent-supervisor#308 item
        3). Once marked, this PR's contributor set resolves to KNOWN-EMPTY:
        no lane wrote it, so no lane is excluded from reviewing it -- the
        safe case, not the dangerous one. `INSERT OR REPLACE`: idempotent,
        the note/timestamp of the most recent marking wins.

        GATED (agent-supervisor#308 item 3 / #321's own review, item 5;
        widened by the PR #331 review's finding 2): this is the one write in
        this class an operator can call to widen a PR's reviewer pool, and
        #321's review measured that it had NO caller verification at all --
        any lane with shell access could call it against a PR it
        contributed to itself and launder that PR as "no lane contributed",
        then have any lane (including itself) review it.
        `scripts/supervisor/mark-pr-external.sh` is the recommended entry
        point -- it runs the full exhaustive resolution chain (issue,
        PR-task, PR-contributor, worktree, legacy branch, all of which need
        `gh`/`git` and so cannot live here) before ever reaching this
        method. This method itself refuses independently, on the two
        sources it CAN check with no external process: an explicit
        `record_pr_for_task` row, and a PR-scoped `source_tasks` row
        (`get_contributor_tasks_for_pr`) -- but those two paths do not cover
        issue-linkage, the most common contributor shape for an ordinary
        issue-scoped task (its `record_pr_for_task` row is only written by
        `lane-done.sh` at completion, so it does not exist yet for a task
        still in progress). A caller that bypassed the shell wrapper and
        called this directly, before its own completion step ran, sailed
        straight through that gap -- reproduced in the PR #331 review.

        `chain_verified` must be passed `True` by a caller that has actually
        run the exhaustive chain (`mark-pr-external.sh` does, and only after
        `resolve_pr_contributors` completed clean); it is refused when
        false or omitted, regardless of what the two ledger-only checks
        below find. This is not an authentication check -- nothing stops a
        caller from passing `True` without having run the chain -- it
        converts an unsafe SILENT default (a direct `cli.py
        mark-pr-external` skipping the chain with no signal that anything
        was skipped) into a caller having to explicitly claim the chain ran,
        which is the remedy the #331 review named.
        """
        if not chain_verified:
            raise ValueError(
                f"refusing to mark PR #{pr_number} in {repo} external -- "
                f"chain_verified was not set. This method can only check two "
                f"of the five resolution paths (an explicit record_pr_for_task "
                f"row, and a PR-scoped source_tasks row); the other three "
                f"(issue-linkage, worktree, legacy branch) need gh/git and "
                f"cannot run here. Call scripts/supervisor/mark-pr-external.sh, "
                f"which runs the full exhaustive chain first and passes "
                f"chain_verified=True only once it completes clean -- a direct "
                f"call cannot silently skip that chain"
            )
        existing_task = self.get_task_for_pr_number(repo=repo, pr_number=pr_number)
        if existing_task is not None:
            raise ValueError(
                f"refusing to mark PR #{pr_number} in {repo} external -- "
                f"the ledger already records task {existing_task['id']!r} (lane "
                f"{existing_task['lane']!r}) as having opened it; marking this "
                f"external now would erase a known contributor, not record an "
                f"absent one"
            )
        contributor_tasks = self.get_contributor_tasks_for_pr(pr_number)
        if contributor_tasks:
            names = ", ".join(f"{t['id']!r} (lane {t['lane']!r})" for t in contributor_tasks)
            raise ValueError(
                f"refusing to mark PR #{pr_number} in {repo} external -- "
                f"the ledger already records {names} dispatched directly against "
                f"it; marking this external now would erase known contributor(s), "
                f"not record an absent one"
            )
        now = int(now if now is not None else self.clock())
        with self._transaction() as connection:
            connection.execute(
                """
                INSERT INTO pr_external_authorship (repo, pr_number, note, recorded_at)
                VALUES (?, ?, ?, ?)
                ON CONFLICT (repo, pr_number) DO UPDATE SET
                    note = excluded.note, recorded_at = excluded.recorded_at
                """,
                (repo, str(pr_number), note, now),
            )

    def get_pr_external(self, *, repo, pr_number):
        """The external-authorship marking for `pr_number` in `repo`, if any
        recorded by `mark_pr_external`."""
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute(
                "SELECT * FROM pr_external_authorship WHERE repo = ? AND pr_number = ?",
                (repo, str(pr_number)),
            ).fetchone()
        return self._dict(row)

    def get_task_for_worktree(self, worktree_path, *, include_reviews=False):
        """The task recorded against one exact worktree path (agent-supervisor#117).

        `worktree.sh new` mints a fresh path per dispatch (its destination
        name embeds the dispatching process's pid), so at most one task is
        expected to ever match -- this is a point lookup, not a fallback
        chain like `get_author_task_for_issue`. It exists because a branch
        cannot be trusted to still spell what it was dispatched as: a lane
        routinely renames its worktree's branch to satisfy the type-prefix
        convention (`fix/`, `feat/`, ...) with a slug of its own choosing,
        so reconstructing a task id from that branch name misses exactly
        the lane-authored PRs this lookup exists to find (dispatch.sh's own
        `--reviews-pr` fallback, replaced by this). The worktree itself does
        not get renamed, so its recorded path is stable even when its
        current branch is not.

        `include_reviews` (agent-supervisor#212) picks which of two
        different questions this answers:

        - `False` (default) -- "who could plausibly have AUTHORED this PR?"
          A review task can never be its own PR's author (agent-supervisor#76),
          so review tasks are filtered out here exactly as
          `get_author_task_for_issue` filters them. This is what
          `dispatch.sh --reviews-pr` needs, and the only caller today.
        - `True` -- "which task is THIS worktree, whatever it is?" A
          reviewing lane confirming its OWN identity before stamping
          `Review-Lane:` (AGENTS.md invariant 10) is asking exactly this,
          and its own worktree is legitimately parked on a task that looks
          like a review -- filtering it out here answers `known:false` for
          a row the ledger has, which is #212's own measured bug: invariant
          10 documented the `False` behaviour as "the correct self-lookup"
          without ever running it from a reviewing lane's worktree.

        Blank `worktree_path` never matches: rows written before this
        column existed carry '' (see `_migrate_tasks_table`), and matching
        one blank against another would wrongly declare every pre-#117 task
        the same worktree.
        """
        if not worktree_path:
            return None
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                "SELECT * FROM tasks WHERE worktree_path = ? ORDER BY created_at ASC, id ASC",
                (worktree_path,),
            ).fetchall()
        candidates = [
            self._dict(row) for row in rows
            if include_reviews or not self._task_looks_like_review(row["id"], row["summary"])
        ]
        if len(candidates) == 1:
            return candidates[0]
        return None

    def list_tasks(self):
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute("SELECT * FROM tasks ORDER BY created_at, id").fetchall()
        return [self._dict(row) for row in rows]

    def list_delivered_open_tasks(self):
        """Rows that claim delivered work but have no completion record yet."""
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT * FROM tasks
                WHERE status='delivered' AND completed_at IS NULL
                ORDER BY updated_at, id
                """
            ).fetchall()
        return [self._dict(row) for row in rows]

    def list_accepted_open_tasks(self):
        """Rows a worker explicitly accepted but never completed.

        agent-supervisor#414. `accept()` is the ONE place `status` ever
        becomes `'accepted'` -- and it is called from exactly one caller,
        `cli.py accept`, the self-report step the claude-print/pi-rpc
        contract hands a worker (`ClaudePrintAdapter.assign_task`'s own
        delivered prompt: "Before working run: ... accept ..."). A tmux
        lane's `accepted_at` is set a different way entirely --
        `record_dispatch`'s own `accepted=True` flag, written in the same
        transaction as `mark_delivered`, which never touches `status` --
        so a tmux task stays visible to `list_delivered_open_tasks` for as
        long as it is open. The instant a no-pane lane's worker calls
        `accept`, though, its row leaves `list_delivered_open_tasks` for
        good: `reconcile_lane_completions.py`'s sweep, and every reaper
        built on that query, stops looking at it forever. That is exactly
        the shape #414 measured -- five claude-print dispatches sitting at
        status=accepted for 2+ hours, zero commits, zero comments, and
        nothing anywhere noticing. This is the parallel query a sweep needs
        to see that state at all.
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT * FROM tasks
                WHERE status='accepted' AND completed_at IS NULL
                ORDER BY updated_at, id
                """
            ).fetchall()
        return [self._dict(row) for row in rows]

    def list_open_worktrees(self):
        """(lane, task id, worktree path) for every IN-FLIGHT task with a
        recorded worktree -- agent-supervisor#291's collision check.

        "In flight" means the same thing `get_open_task_for_lane` already
        uses: not `complete`, `failed`, or `cancelled`. That covers a lane
        between claim and delivery (`created`, `delivery_pending`) as well as
        one already working a delivered brief -- both are lanes a fresh
        dispatch could collide with; only a task that has actually stopped
        is excluded.

        `worktree_path` is blank for a placeholder claim row (`claim_lane`
        writes one under this same table, see `get_open_task_for_lane`'s own
        docstring) and for any task dispatched before agent-supervisor#117
        added the column -- both are filtered out here, not left for the
        caller to notice: neither names a directory `git diff` could read.
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT * FROM tasks
                WHERE status NOT IN ('complete','failed','cancelled')
                  AND worktree_path != ''
                ORDER BY updated_at, id
                """
            ).fetchall()
        return [self._dict(row) for row in rows]

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
        encoded_evidence = json.dumps(evidence, sort_keys=True, separators=(",", ":"))
        connection.execute(
            """
            INSERT INTO source_tasks(
                id, source_kind, source_url, source_ref, summary, source_state,
                status, evidence_json, status_marker, updated_at
            ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            ON CONFLICT(id) DO UPDATE SET
                source_kind=excluded.source_kind,
                source_url=excluded.source_url,
                source_ref=excluded.source_ref,
                summary=excluded.summary,
                source_state=excluded.source_state,
                status=excluded.status,
                evidence_json=excluded.evidence_json,
                status_marker=excluded.status_marker,
                updated_at=excluded.updated_at
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
    ):
        """Replace one local source spool record with facts read from GitHub.

        This intentionally has no lane or pane dependency: reconstruction must
        work after the entire supervisor state directory has been recreated.
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

    def _assign_tx(self, connection, *, task_id, lane, pane_nonce, summary, now, worktree_path=""):
        self._require_task_id(task_id)
        if not summary.strip():
            raise ValueError("task summary must be non-empty")
        self._verify_lane_nonce(connection, lane, pane_nonce)
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
                INSERT INTO tasks(id, lane, pane_nonce, summary, status, created_at, updated_at, worktree_path)
                VALUES (?, ?, ?, ?, 'created', ?, ?, ?)
                """,
                (task_id, lane, pane_nonce, summary, now, now, worktree_path),
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

    def _cancel_open_task_tx(self, connection, lane, now):
        if connection.execute("SELECT 1 FROM lanes WHERE lane = ?", (lane,)).fetchone() is None:
            raise ValueError(f"unknown lane: {lane}")
        row = connection.execute(
            "SELECT * FROM tasks WHERE lane = ? AND status NOT IN ('complete','failed','cancelled')",
            (lane,),
        ).fetchone()
        if row is None:
            return None
        self._cancel_task_row(connection, row["id"], now)
        return self._dict(connection.execute("SELECT * FROM tasks WHERE id=?", (row["id"],)).fetchone())

    def cancel_open_task(self, lane):
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
        """
        now = int(self.clock())
        with self._locked(), self._transaction() as connection:
            return self._cancel_open_task_tx(connection, lane, now)

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

    # -- agent-supervisor#280: the prompt corpus -------------------------
    #
    # `record_prompt` is the ONLY writer of `text_raw` -- nothing here ever
    # updates that column again, which is what makes "raw wins on conflict"
    # true by construction rather than by convention alone.

    def record_prompt(self, prompt_id, *, at, text_raw, context, text_clean=None, session=None, source_file=None):
        """Write one prompt row. `text_raw` is set once, here, and never again."""
        if not prompt_id or not isinstance(prompt_id, str):
            raise ValueError("prompt_id is required")
        if not text_raw or not isinstance(text_raw, str):
            raise ValueError("text_raw is required")
        if not context or not isinstance(context, str):
            raise ValueError("context is required -- a prompt with no context will be misread later")
        with self._locked(), self._transaction() as connection:
            connection.execute(
                """
                INSERT INTO prompts(id, at, text_raw, text_clean, context, session, source_file)
                VALUES (?, ?, ?, ?, ?, ?, ?)
                """,
                (prompt_id, int(at), text_raw, text_clean, context, session, source_file),
            )
            row = connection.execute("SELECT * FROM prompts WHERE id=?", (prompt_id,)).fetchone()
        return self._dict(row)

    def update_text_clean(self, prompt_id, text_clean):
        """Replace the derived, cleaned-up text. `text_raw` is untouched --
        this statement does not even name that column."""
        with self._locked(), self._transaction() as connection:
            connection.execute(
                "UPDATE prompts SET text_clean=? WHERE id=?", (text_clean, prompt_id)
            )
            row = connection.execute("SELECT * FROM prompts WHERE id=?", (prompt_id,)).fetchone()
        if row is None:
            raise ValueError(f"no such prompt: {prompt_id}")
        return self._dict(row)

    def add_item(self, item_id, *, prompt_id, kind, body, weight, status="open",
                 status_reason=None, resolved_to=None, acked_at=None):
        """Record one judgement extracted from a prompt. This is the
        itemisation step the brief calls model work done ONCE per prompt --
        this method just writes the row the model (or a test) hands it."""
        if kind not in ("parameter", "question", "directive", "thought", "correction"):
            raise ValueError("invalid kind")
        if weight not in ("hard", "preference", "retracted"):
            raise ValueError("invalid weight")
        if status not in ("open", "acknowledged", "acted", "resolved", "dropped"):
            raise ValueError("invalid status")
        if status == "dropped" and not status_reason:
            raise ValueError("dropped status requires status_reason")
        with self._locked(), self._transaction() as connection:
            connection.execute(
                """
                INSERT INTO items(id, prompt_id, kind, body, weight, status, status_reason, resolved_to, acked_at)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (item_id, prompt_id, kind, body, weight, status, status_reason, resolved_to, acked_at),
            )
            row = connection.execute("SELECT * FROM items WHERE id=?", (item_id,)).fetchone()
        return self._dict(row)

    def link_items(self, item_id, other_item_id, relation):
        """Record a relation between two items -- `conflicts_with`,
        `supersedes`, or `depends_on`. Symmetric relations (conflict) are
        recorded once, in whichever direction the caller names it; the
        `conflicts` view reports the pair regardless of which side is which."""
        if relation not in ("conflicts_with", "supersedes", "depends_on"):
            raise ValueError("invalid relation")
        with self._locked(), self._transaction() as connection:
            connection.execute(
                """
                INSERT OR IGNORE INTO links(item_id, other_item_id, relation)
                VALUES (?, ?, ?)
                """,
                (item_id, other_item_id, relation),
            )

    def get_item(self, item_id):
        """Read one item row, or None -- the same idempotency check
        `itemize_prompts.py --load` uses before writing, so a re-run over
        overlapping input never re-inserts (and never fails on) a
        judgement already recorded."""
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM items WHERE id=?", (item_id,)).fetchone())

    def list_unitemised_prompts(self, *, limit=None):
        """Prompts with no `items` row yet -- the itemisation queue for
        `itemize_prompts.py --extract` (agent-supervisor#303). Item-lessness
        via LEFT JOIN, not a second `itemised` flag on `prompts` that could
        drift from what `items` actually holds."""
        sql = """
            SELECT p.* FROM prompts p
            LEFT JOIN items i ON i.prompt_id = p.id
            WHERE i.id IS NULL
            ORDER BY p.at
        """
        params = ()
        if limit is not None:
            sql += " LIMIT ?"
            params = (int(limit),)
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(sql, params).fetchall()
        return [self._dict(row) for row in rows]

    def get_prompt(self, prompt_id):
        """Read one prompt row, or None. The idempotency check a loader
        (`mine_prompts.py --store`, agent-supervisor#303) needs BEFORE
        calling `record_prompt` again for a turn it has already written --
        `record_prompt` itself has no INSERT OR IGNORE/UPSERT because
        `text_raw` must never silently re-write on a second pass; deciding
        "already have this one" is the caller's job, not a swallowed
        constraint violation."""
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM prompts WHERE id=?", (prompt_id,)).fetchone())

    # The five views ARE the deliverable (agent-supervisor#280, #303) --
    # whitelisted by name, the same posture `lanes.sh` takes on offering an
    # idle shape (CLAUDE.md invariant 6): a view name this does not
    # recognise is refused, never interpolated into SQL on trust.
    PROMPT_VIEWS = ("unacknowledged", "live_parameters", "conflicts", "open_questions", "possibility_count")

    def read_prompt_view(self, view):
        """Read one of the five named views, plain SQL, no model involved --
        every read against `items`/`links` after itemisation is meant to be
        exactly this and nothing more."""
        if view not in self.PROMPT_VIEWS:
            raise ValueError(f"unknown prompt view: {view}")
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(f"SELECT * FROM {view}").fetchall()  # noqa: S608 -- view is whitelisted above
        return [self._dict(row) for row in rows]
