"""Transactional task/event ledger for the Hill90 supervisor.

The core deliberately knows nothing about Codex, Claude terminal chrome, tmux
keystrokes, or GitHub. Harness adapters register pane incarnations and move
tasks through this shared lifecycle.

This file is the composition root (agent-supervisor#706, the `sync.py`/#336
pattern): the free functions and constants above `Ledger` live in
`core_constants.py`/`core_lane_relation.py`, and `Ledger` itself is
assembled from the responsibility mixins in `core_ledger_*.py` --

- `core_ledger_core`      -- connection/locking/transaction primitives and
                              the row-shaping/validation helpers nearly
                              every other mixin calls through `self`
- `core_ledger_schema`    -- schema creation and every migration step
- `core_ledger_lanes`     -- lane/session registration and discovery
- `core_ledger_task_queries` -- read-side task/PR lookups
- `core_ledger_source_tasks` -- the `source_tasks` table's own lifecycle
- `core_ledger_lifecycle` -- dispatch -> accept task lifecycle writes
- `core_ledger_claims`    -- lane-claim placeholders and the supervisor lease
- `core_ledger_completion` -- terminal-result writes
- `core_ledger_events`    -- the events/notification queue
- `core_ledger_snapshots` -- session-event/component/verdict/snapshot recording
- `core_ledger_corpus`    -- the prompt-corpus tables (agent-supervisor#280)

every public name the old single 4906-line module exposed is still
importable from here under its original name, and `Ledger` is still one
class -- so every method keeps its original signature and `self` access
to the others. Behaviour-preserving move only -- no new features, no
signature changes, no tidying of code passed through along the way.
"""

from __future__ import annotations

import contextlib  # noqa: F401 -- re-exported; some callers/tests reach it via core.contextlib
import difflib  # noqa: F401 -- re-exported
import fcntl  # noqa: F401 -- re-exported
import hashlib  # noqa: F401 -- re-exported
import json  # noqa: F401 -- re-exported
import os  # noqa: F401 -- re-exported
import re  # noqa: F401 -- re-exported
import socket  # noqa: F401 -- re-exported
import sqlite3  # noqa: F401 -- re-exported
import tempfile  # noqa: F401 -- re-exported
import time  # noqa: F401 -- re-exported
from pathlib import Path  # noqa: F401 -- re-exported

from core_constants import (  # noqa: F401 -- re-exported for callers/tests
    CLAIM_OWNER_RE,
    CLAIM_STATUS_LIVE,
    CLAIM_STATUS_RESERVED,
    CLAIM_TASK_PREFIX,
    COMPONENT_RE,
    MAX_RESULT_BYTES,
    SOURCE_TASK_STATUSES,
    SUPERVISOR_LEASE_ID,
    TASK_ID_RE,
    TERMINAL_STATUSES,
)
from core_lane_relation import (  # noqa: F401 -- re-exported for callers/tests
    DAEMON_LANE_RE,
    LANE_ID_RE,
    _MACOS_PRIVATE_SYMLINK_PREFIXES,
    claim_owner_token,
    cross_namespace_lane_relation,
    daemon_lane_verified,
    is_daemon_shaped,
    lane_or_task_row,
    lane_population,
    lane_relation,
    lane_relation_from_rows,
    normalize_worktree_path,
    pane_id_for_task,
    pid_is_alive,
)
from core_ledger_core import LedgerCoreMixin, LockTimeout  # noqa: F401 -- LockTimeout re-exported
from core_ledger_schema import LedgerSchemaMixin
from core_ledger_lanes import LedgerLanesMixin
from core_ledger_task_queries import LedgerTaskQueriesMixin
from core_ledger_source_tasks import LedgerSourceTasksMixin
from core_ledger_lifecycle import LedgerLifecycleMixin
from core_ledger_claims import LedgerClaimsMixin
from core_ledger_completion import LedgerCompletionMixin
from core_ledger_events import LedgerEventsMixin
from core_ledger_snapshots import LedgerSnapshotsMixin
from core_ledger_corpus import LedgerCorpusMixin


class Ledger(
    LedgerCoreMixin,
    LedgerSchemaMixin,
    LedgerLanesMixin,
    LedgerTaskQueriesMixin,
    LedgerSourceTasksMixin,
    LedgerLifecycleMixin,
    LedgerClaimsMixin,
    LedgerCompletionMixin,
    LedgerEventsMixin,
    LedgerSnapshotsMixin,
    LedgerCorpusMixin,
):
    """The Hill90 supervisor ledger, assembled from the responsibility
    mixins in `core_ledger_*.py` (see this module's docstring). See each
    mixin's own module docstring for what it owns; this class adds nothing
    of its own beyond composing them into the single object every caller
    already knew as `Ledger`."""


# The mixin names above are captured as `Ledger`'s base classes at the class
# statement itself; deleting them here does not touch the MRO, it only keeps
# `dir(core)` identical to the pre-split module's -- no public name this file
# didn't already expose before the split (agent-supervisor#706) leaks out as
# a side effect of how the split was implemented.
del (
    LedgerCoreMixin,
    LedgerSchemaMixin,
    LedgerLanesMixin,
    LedgerTaskQueriesMixin,
    LedgerSourceTasksMixin,
    LedgerLifecycleMixin,
    LedgerClaimsMixin,
    LedgerCompletionMixin,
    LedgerEventsMixin,
    LedgerSnapshotsMixin,
    LedgerCorpusMixin,
)
