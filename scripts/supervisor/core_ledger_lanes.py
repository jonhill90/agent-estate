"""`Ledger`'s lane and session registration/discovery: `register_lane` and
its transaction, availability/open-task lookups, session bootstrap/list,
the restore plan, and component lookup.

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib
import re
import time


class LedgerLanesMixin:
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
