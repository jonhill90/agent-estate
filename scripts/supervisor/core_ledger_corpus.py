"""`Ledger`'s prompt-corpus tables (agent-supervisor#280): `record_prompt`,
`update_text_clean`, the `items` writers/readers (`add_item`, `link_items`,
`get_item`, `drop_item`, `flag_needs_review`, `list_open_items`), and the
prompt readers (`list_unitemised_prompts`, `get_prompt`, `read_prompt_view`).

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib
import re
import time


class LedgerCorpusMixin:
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
        if status not in ("open", "acknowledged", "acted", "resolved", "dropped", "needs_review"):
            raise ValueError("invalid status")
        if status in ("dropped", "needs_review") and not status_reason:
            raise ValueError(f"{status} status requires status_reason")
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

    def drop_item(self, item_id, status_reason):
        """Correct an already-recorded item's status to 'dropped' in place --
        `itemize_prompts.py --reclassify`'s write (agent-supervisor#583). A
        prompt itemised before a structural filter existed keeps its
        original id, kind, body and weight (the judgement record itself is
        evidence); only `status`/`status_reason` change, so the item leaves
        `unacknowledged` without being deleted -- the same "reviewable and
        reversible" contract `add_item`'s dropped rows already honour."""
        if not status_reason:
            raise ValueError("status_reason is required")
        with self._locked(), self._transaction() as connection:
            connection.execute(
                "UPDATE items SET status='dropped', status_reason=? WHERE id=?",
                (status_reason, item_id),
            )
            row = connection.execute("SELECT * FROM items WHERE id=?", (item_id,)).fetchone()
        if row is None:
            raise ValueError(f"no such item: {item_id}")
        return self._dict(row)

    def flag_needs_review(self, item_id, status_reason):
        """Correct an already-recorded item's status to 'needs_review' in
        place -- the agent-supervisor#652 counterpart to `drop_item`, for a
        structural marker that is a CANDIDATE, not a confirmed drop (see the
        `needs_review` view's own comment for why: `context` =
        CONTEXT_UNDETERMINED alone cannot tell a synthetic eval fixture from
        a real post-`/clear` operator turn). Same shape as `drop_item`:
        kind/body/weight untouched, no delete, no duplicate row -- only
        `status`/`status_reason` change, so the item leaves `unacknowledged`
        without leaving the ledger. A later pass (human review, or a real
        second signal if one is ever found) moves it on from here via
        `drop_item` (confirmed synthetic) or back to 'open' (confirmed
        real) -- this method only ever produces 'needs_review'."""
        if not status_reason:
            raise ValueError("status_reason is required")
        with self._locked(), self._transaction() as connection:
            connection.execute(
                "UPDATE items SET status='needs_review', status_reason=? WHERE id=?",
                (status_reason, item_id),
            )
            row = connection.execute("SELECT * FROM items WHERE id=?", (item_id,)).fetchone()
        if row is None:
            raise ValueError(f"no such item: {item_id}")
        return self._dict(row)

    def list_open_items(self, *, limit=None):
        """Every currently-open item, each carrying its originating prompt's
        `context` -- the reclassification queue `itemize_prompts.py
        --reclassify` reads (agent-supervisor#583). This re-reads the same
        structural marker `drop_noise` keys on at itemisation time; it never
        re-judges body/kind/weight, only whether an item that predates the
        filter should have been dropped."""
        sql = """
            SELECT i.*, p.context AS prompt_context
            FROM items i JOIN prompts p ON p.id = i.prompt_id
            WHERE i.status = 'open'
            ORDER BY p.at
        """
        params = ()
        if limit is not None:
            sql += " LIMIT ?"
            params = (int(limit),)
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(sql, params).fetchall()
        return [self._dict(row) for row in rows]

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

    # The original five views ARE the deliverable (agent-supervisor#280,
    # #303) -- whitelisted by name, the same posture `lanes.sh` takes on
    # offering an idle shape (CLAUDE.md invariant 6): a view name this does
    # not recognise is refused, never interpolated into SQL on trust.
    # `needs_review` (agent-supervisor#652) is a sixth, added the same way.
    # `capture_health` (agent-supervisor#687) is a seventh, added the same way.
    PROMPT_VIEWS = (
        "unacknowledged", "live_parameters", "conflicts", "open_questions",
        "possibility_count", "needs_review", "capture_health",
    )

    def read_prompt_view(self, view):
        """Read one of the named `PROMPT_VIEWS`, plain SQL, no model
        involved -- every read against `items`/`links`/`prompts` after
        capture and itemisation is meant to be exactly this and nothing
        more."""
        if view not in self.PROMPT_VIEWS:
            raise ValueError(f"unknown prompt view: {view}")
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(f"SELECT * FROM {view}").fetchall()  # noqa: S608 -- view is whitelisted above
        return [self._dict(row) for row in rows]
