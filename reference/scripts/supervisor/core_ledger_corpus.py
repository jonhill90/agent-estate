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
import hashlib
import re
import time


class LedgerCorpusMixin:
    # -- agent-supervisor#280: the prompt corpus -------------------------
    #
    # `record_prompt` is the ONLY writer of `text_raw` -- nothing here ever
    # updates that column again, which is what makes "raw wins on conflict"
    # true by construction rather than by convention alone.

    def record_prompt(self, prompt_id, *, at, text_raw, context, text_clean=None, session=None, source_file=None,
                       tmux_pane=None, tmux_pane_target=None, author="unknown"):
        """Write one prompt row. `text_raw` is set once, here, and never again.

        `tmux_pane`/`tmux_pane_target` (agent-supervisor#755 part B): which
        pane, if any, submitted this prompt -- the raw `$TMUX_PANE` value
        the capturing process saw, and that pane resolved to `session:window`
        at capture time. Both NULL for a prompt captured with no pane (no
        tmux at all, or a `claude-print`/`pi-rpc` lane) -- never guessed or
        backfilled after the fact, same as every other column here. See
        `core_ledger_schema.py`'s `_migrate_prompts_pane_columns` for why
        these are a candidate signal only, never proof on their own.

        `author` (agent-estate#1395/#1394): `'jon'`, `'supervisor'`,
        `'director'`, or `'unknown'` -- the default. Callers should pass
        `'unknown'` unless they hold a real, proven fact (see
        `consume_pending_author` below); this method does not validate the
        value beyond what the column's own CHECK enforces, so an invalid
        string still raises, just later and less legibly -- deliberate:
        this mixin is the writer, not the place author values get decided."""
        if not prompt_id or not isinstance(prompt_id, str):
            raise ValueError("prompt_id is required")
        if not text_raw or not isinstance(text_raw, str):
            raise ValueError("text_raw is required")
        if not context or not isinstance(context, str):
            raise ValueError("context is required -- a prompt with no context will be misread later")
        with self._locked(), self._transaction() as connection:
            connection.execute(
                """
                INSERT INTO prompts(id, at, text_raw, text_clean, context, session, source_file,
                                     tmux_pane, tmux_pane_target, author)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (prompt_id, int(at), text_raw, text_clean, context, session, source_file,
                 tmux_pane, tmux_pane_target, author),
            )
            row = connection.execute("SELECT * FROM prompts WHERE id=?", (prompt_id,)).fetchone()
        return self._dict(row)

    # -- agent-estate#1395/#1394: proven (not guessed) prompt authorship --
    #
    # `register_pending_author`/`consume_pending_author` are the durable,
    # hash-keyed handoff between an injecting caller (a supervisor/Director
    # tool that is ABOUT to type text into a lane's pane) and
    # `prompt_capture_hook.py`, which captures whatever the pane submits
    # without itself knowing who sent it.
    #
    # HOW THE FULL SET WAS FOUND (wire-author-registration's own fix pass,
    # after a reviewer caught the first pass's "3 bash-side call sites
    # found" undercounting by at least 4): every `send-keys` occurrence in
    # the tree, not just files already known to matter (`grep -rn
    # "send-keys" --include="*.sh" --include="*.py" --include="*.go" .`,
    # every hit read, not pattern-matched); every caller of `send.sh`'s
    # `verified_type`/`verified_submit`/`verified_send`/`blind_send`
    # primitives across the whole repo, not just `reference/scripts/
    # supervisor`; a separate check for `tmux paste-buffer`/`load-buffer`
    # (a second way to inject text that never says `send-keys` at all --
    # none found outside the Go-side ban tests); and the Go side confirmed
    # clean structurally, not just unaudited (`mirror_wiring_test.go`/
    # `mirror_test.go` assert send-keys/paste-buffer/respawn-pane never
    # appear anywhere in `src/estate`'s own tmux-adjacent code).
    #
    # WIRED -- 12 call sites, all registering `author="supervisor"`
    # immediately before their own send:
    #   Python (7, unchanged from the first pass): `TmuxAdapter.assign_task`/
    #   `notify_supervisor`, `ACPAdapter.assign_task`, `PiRPCAdapter.
    #   assign_task`, `ClaudePrintAdapter.assign_task` (`run_detached`, not
    #   `send_literal` by name, same act), `recycle.respawn_supervisor`.
    #   `dispatch-claude-print.sh`/`dispatch-pi-rpc.sh` deliver through the
    #   claude-print/pi-rpc adapters above via `cli.py assign` -- covered
    #   transitively, not separate sites.
    #   Bash (5, new in this pass, via a new `cli.py register-pending-author`
    #   subcommand -- text on stdin, never an argv, so a multi-line brief or
    #   shell-special characters never have to survive an argv hop):
    #   `director-loop.sh` (the Director's own recurring tick -- the
    #   highest-volume site in the tree, and the one a reviewer had to name
    #   before it was found), `heartbeat.sh` (stall nudge), `quota-watch.sh`
    #   (wind-down/resume messages), `director-route.sh` (idle-nudge),
    #   `watchdog.sh` (`blind_send` restart nudge). All five log and send
    #   anyway on a registration failure -- these are unattended, operationally
    #   load-bearing loops with no reconciliation path for a blocked send;
    #   an attribution write is advisory and must fail open, never block the
    #   send it's attached to (the opposite ordering from the Python sites'
    #   own transaction, and deliberately so -- see each site's own comment).
    #
    # NOT WIRED, DELIBERATELY, EACH FOR A DIFFERENT REASON:
    #   `dispatch-send.sh` (dispatch.sh's own brief-to-a-new-lane send) --
    #   the most heavily-scrutinized, correctness-critical path in this tree
    #   (#178/#186/#446's own history), and its own "point of no return"
    #   sequence. The CLI bridge the 5 bash sites above now use makes wiring
    #   this mechanically easier than it was, but touching that specific
    #   sequence is still its own, separately-reviewed change, not bundled
    #   into a fix pass already covering five other files.
    #   `inbox-route.sh` (relays a Telegram reply into a lane) -- the text is
    #   genuinely Jon's own words, not a script's. `register_pending_author`
    #   correctly refuses `author="jon"` (registration is proof of NON-Jon
    #   authorship; no injection site can prove itself to BE Jon). Registering
    #   `"supervisor"` here would be a NEW misattribution in the opposite
    #   direction from the one this whole feature exists to fix. Correctly
    #   `unknown` -- this is the one case where that is not absence of
    #   effort, it is the honest answer.
    #   `reference/scripts/estate-loop/check.sh` -- a different subtree
    #   (`estate-loop`, not `supervisor`) whose own header states its design
    #   explicitly: "No supervisor, no ledger, no lease." Reaching across
    #   that stated boundary to depend on `supervisor/cli.py` for a
    #   secondary concern (author labeling) crosses an architectural line
    #   its own author drew on purpose -- not this fix pass's call to cross.
    #
    # Landing the primitive first (PR #1398) meant the schema and the
    # consuming half (the hook) never had to migrate twice.

    # How long an unconsumed registration survives before it is treated as
    # abandoned (a `send_literal` that raised before the text ever reached
    # a pane, a process that registered and then crashed) and purged rather
    # than lingering to falsely match some unrelated future prompt that
    # happens to share the same text. Purged opportunistically on the next
    # `register_pending_author` call -- no separate cron needed for a table
    # this small and this short-lived by design.
    PENDING_PROMPT_AUTHOR_TTL_SECONDS = 300

    @staticmethod
    def _pending_author_hash(text):
        """`sha1` of the text alone -- deliberately NOT combined with
        `session_id` the way `prompt_capture_hook._prompt_id` is. The
        injecting caller registers this BEFORE the send, when it does not
        yet reliably know which session/pane will end up capturing the
        text (a fresh lane's harness session id is not always known before
        the first prompt lands in it); the hook looks up by the same hash
        of the text it just captured, independent of session."""
        return hashlib.sha1(text.encode("utf-8", errors="ignore")).hexdigest()

    def register_pending_author(self, text, author):
        """Durably record, BEFORE the physical send, that `text` is about
        to be injected by `author` (`'supervisor'` or `'director'` only --
        `'jon'`/`'unknown'` are never registered, because this table exists
        to prove the NON-Jon case; Jon's own prompts need no registration,
        they are simply never matched here). Idempotent per exact text:
        a second registration of identical text before the first is
        consumed replaces it (`INSERT OR REPLACE`) rather than erroring,
        since the caller re-sending after a retry is a real, ordinary case,
        not a bug.

        Returns nothing -- callers that need to confirm the write landed
        can re-derive `_pending_author_hash(text)` and call
        `consume_pending_author` in a test; production callers do not,
        same as `record_prompt`'s siblings here treat their own writes as
        trusted once `_transaction()` returns without raising."""
        if author not in ("supervisor", "director"):
            raise ValueError(f"register_pending_author: author must be 'supervisor' or 'director', got {author!r}")
        if not text or not isinstance(text, str):
            raise ValueError("text is required")
        now = int(self.clock())
        text_hash = self._pending_author_hash(text)
        with self._locked(), self._transaction() as connection:
            connection.execute(
                "DELETE FROM pending_prompt_authors WHERE registered_at < ?",
                (now - self.PENDING_PROMPT_AUTHOR_TTL_SECONDS,),
            )
            connection.execute(
                "INSERT OR REPLACE INTO pending_prompt_authors(text_hash, author, registered_at) VALUES (?, ?, ?)",
                (text_hash, author, now),
            )

    def consume_pending_author(self, text):
        """Look up and DELETE a matching `pending_prompt_authors` row for
        `text`, returning the registered author (`'supervisor'`/
        `'director'`) or `None` if nothing matches (never registered, TTL
        already purged it, or already consumed by an earlier capture of
        the same text). Consuming rather than merely reading is what makes
        this safe against reuse: once matched, the fact is spent, so a
        LATER, unrelated prompt that happens to share the same literal text
        cannot silently inherit someone else's registration."""
        if not text:
            return None
        text_hash = self._pending_author_hash(text)
        with self._locked(), self._transaction() as connection:
            row = connection.execute(
                "SELECT author FROM pending_prompt_authors WHERE text_hash=?", (text_hash,)
            ).fetchone()
            if row is None:
                return None
            connection.execute("DELETE FROM pending_prompt_authors WHERE text_hash=?", (text_hash,))
        return row["author"]

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
            SELECT i.*, p.context AS prompt_context, p.tmux_pane_target AS prompt_tmux_pane_target
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
