"""Lane-identity free functions.

Comparing/relating lane ids and pane records across a tmux rename
(Invariant 9), classifying daemon-shaped lanes, resolving a task's pane,
normalizing worktree paths, and checking whether a pid is still alive.
None of this needs a `Ledger` instance -- these are the free functions
`core.py` exposed alongside the class. Split from `core.py`
(agent-supervisor#706); re-exported by `core.py` under its original name.
"""

from __future__ import annotations

import os
import re
import socket
import time


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


# agent-supervisor#631. `lane_relation_from_rows` above compares two
# `lanes` rows -- fetched by the mutable lane STRING, `Ledger.get_lane`.
# That is exactly the lookup a later, unrelated dispatch can silently
# invalidate for a HISTORICAL contributor: `_register_lane_tx` upserts
# `lanes` keyed on the string, so a task assigned under `agent-supervisor:4`
# yesterday and a task assigned under the same string today, after a window
# closed and `renumber-windows on` handed that index to a different pane,
# both resolve `get_lane("agent-supervisor:4")` to the SAME row -- today's.
#
# `tasks.pane_id` (see `_assign_tx`) is this task's own frozen snapshot,
# immune to that overwrite by construction: it is written once, at
# assignment time, and never touched again. This is the read side of that
# snapshot -- pure, like `lane_relation_from_rows`, so the caller's fetch
# (`Ledger.get_task`) stays swappable and this stays unit-testable without a
# ledger.
#
# '' for a task unknown to the ledger, or one dispatched before this column
# existed (`_migrate_tasks_table` backfills those to '', never a guess) --
# a caller must treat that exactly like a missing `pane_id` on a `lanes`
# row: fall back to the live `Ledger.get_lane(lane)` lookup this function
# exists to bypass, not fail or invent one.
def pane_id_for_task(ledger, task_id):
    """The frozen `pane_id` snapshot recorded on `task_id` at assignment
    time, or `''` if the task is unknown or predates this column."""
    if not task_id:
        return ""
    row = ledger.get_task(task_id)
    if row is None:
        return ""
    return (row.get("pane_id") or "").strip()


# agent-supervisor#689 (the second half of #685): `Ledger.get_lane` alone
# cannot resolve a `Review-Lane:`/`Author-Lane:` trailer that names a TASK id
# rather than a registered `lanes` row. That is the shape `lane-whoami.sh`
# emits for a pane-less (claude-print/pi-rpc) lane -- `dispatch-claude-print.
# sh` registers `lanes.lane = <task id>` for those, so `get_lane` already
# finds them (agent-supervisor#292) -- but it is ALSO the shape an older
# brief (predating #688) told a PANE-having lane to name itself with, and
# that string was never written to `lanes` at all: a task id belongs to
# `tasks`, keyed by whichever real lane the ledger dispatched it to
# (`tasks.lane`), with its own frozen `pane_id` snapshot (`#631`, the exact
# mechanism `pane_id_for_task` above reads).
#
# So this tries a registered lane row FIRST -- the primary, still-correct
# identity for a genuine off-pane lane -- and only when that is absent does
# it fall back to treating `ident` as a task id and resolving through that
# task's own frozen `pane_id`, the same snapshot `author_lane_for`
# (verdict-independence.sh) already trusts for the AUTHOR side (#631). This
# does not widen what counts as "same" or "different": the answer is still
# decided by comparing `pane_id` values (`lane_relation_from_rows`), so a
# task id that resolves to the SAME pane as the other side still reports
# `same`, exactly as intended (agent-supervisor#689 point 3: this must not
# turn a self-review into "independent" just because it is spelled as a task
# id instead of a lane id).
#
# `None` when NEITHER a `lanes` row nor a `tasks` row exists for `ident`, or
# when the task exists but carries no frozen `pane_id` (predates #631) --
# unchanged fail-closed posture: an id this cannot place still refuses,
# never guesses.
def lane_or_task_row(ledger, ident):
    """A pane-id-bearing dict for `ident`, trying a registered `lanes` row
    first and a known `tasks` row's frozen `pane_id` snapshot second.
    `None` when neither resolves."""
    if not ident:
        return None
    row = ledger.get_lane(ident)
    if row is not None:
        return row
    pane_id = pane_id_for_task(ledger, ident)
    if not pane_id:
        return None
    return {"pane_id": pane_id}


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


_MACOS_PRIVATE_SYMLINK_PREFIXES = ("/tmp", "/var", "/etc")


def normalize_worktree_path(path):
    """Canonical spelling of a worktree path, for comparison only
    (agent-supervisor#624, fix-pass #632).

    `#624`'s first cut called `os.path.realpath`, which resolves symlinks
    by asking the LOCAL filesystem -- and that is exactly what made it
    wrong. Two things broke it, both found by #632's failing test:

    1. This repo's own CI (`.github/workflows/*.yml`, `runs-on:
       ubuntu-latest`) has no `/var` -> `/private/var` symlink at all --
       that mapping is a macOS convention, not a universal one. `realpath`
       on Linux is a no-op for these paths, so the unresolved and resolved
       spellings compared UNEQUAL there even though they name the same
       directory on the macOS host that actually wrote them. A comparison
       whose correctness depends on which machine happens to run it is not
       a fix, it just moves where the bug hides.
    2. Even on macOS, a worktree the dispatching host has since torn down
       (`worktree.sh done`/`gc`) still has a `tasks.worktree_path` row
       naming it -- and MUST still resolve to its lane, because #632's own
       cross-check items (`lane-retire.sh` reading `worktree_path` after
       `#628`, `#629`'s authorship resolution) both read this column long
       after the directory that could still corroborate its plumbing is
       often already gone. `realpath` degrading gracefully for a missing
       PATH is not the same guarantee as the SYMLINK PREFIX itself still
       being resolvable -- and relying on either was never necessary: the
       set of macOS symlinks this defect actually turns on is small,
       fixed, and well-known, so it is spelled out explicitly here instead
       of asked of a filesystem that may not agree, may not still hold the
       directory, or may not even be macOS.

    So this now does its own two, purely textual, steps:

    - `os.path.normpath` collapses repeated separators (`//` inside
      `$TMPDIR`, seen live in `#624`'s own report) -- string manipulation,
      no filesystem access, no existence requirement.
    - A literal prefix rewrite maps `/tmp`, `/var`, `/etc` to their
      `/private/...` spelling -- the exact, closed set of top-level
      symlinks macOS ships by default (`/private/tmp`, `/private/var`,
      `/private/etc` are the real directories; `/tmp`, `/var`, `/etc` are
      the symlinks). Deliberately NOT `os.path.realpath`: this holds
      identically whether the path still exists, whether it ever existed
      on THIS host, and whether this process is running on macOS or the
      Linux CI runner reading the same ledger row in a test.

    Blank stays blank, on purpose: `get_task_for_worktree` relies on this
    to keep refusing a blank/NULL `worktree_path` rather than having an
    empty path normalize to something that could spuriously match.
    """
    if not path:
        return ""
    normalized = os.path.normpath(path)
    for prefix in _MACOS_PRIVATE_SYMLINK_PREFIXES:
        if normalized == prefix or normalized.startswith(prefix + "/"):
            return "/private" + normalized
    return normalized


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
