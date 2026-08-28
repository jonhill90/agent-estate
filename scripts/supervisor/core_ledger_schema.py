"""`Ledger`'s schema creation and every migration step (`_initialize` plus
the `_migrate_*`/`_backfill_*`/`_restore_*` methods that bring an older
on-disk `ledger.sqlite3` up to the current shape).

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib
import json
import os
import re
import sqlite3
import time

from core_constants import CLAIM_TASK_PREFIX  # noqa: F401 -- re-exported by core.py


class LedgerSchemaMixin:
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
                    worktree_path TEXT NOT NULL DEFAULT '',
                    -- agent-supervisor#631: an IMMUTABLE snapshot of
                    -- `lanes.pane_id`, taken once at assignment time
                    -- (`_assign_tx`), exactly the same pattern `pane_nonce`
                    -- already uses. `lanes` is keyed on the lane STRING and
                    -- `_register_lane_tx` upserts it -- a window closing and
                    -- `renumber-windows on` reassigning that same string to
                    -- a later, unrelated dispatch silently overwrites the
                    -- `lanes` row a historical task's `lane` column still
                    -- names. This column is this task's OWN record of which
                    -- pane it actually ran in, independent of whatever the
                    -- `lanes` row for that string says later. '' (never
                    -- NULL) for a task dispatched before this column
                    -- existed -- see `_migrate_tasks_table` -- and a caller
                    -- must fall back to the mutable `lanes` lookup for
                    -- those rows, never guess.
                    pane_id TEXT NOT NULL DEFAULT ''
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
                    updated_at INTEGER NOT NULL,
                    -- agent-supervisor#640: whether THIS dispatch was known,
                    -- at dispatch time, to be a review (`dispatch.sh
                    -- --reviews-pr`) rather than a fix pass or original
                    -- authoring dispatch (`--pr` / issue-scoped). 1 = review,
                    -- 0 = explicitly recorded as not a review, NULL = not
                    -- recorded (every row written before this column
                    -- existed, and every issue-scoped row -- the property is
                    -- only ever written for `source_kind='pull'` rows,
                    -- because that is the only shape `get_contributor_tasks_
                    -- for_pr` reads it for). NULL is the "don't know, ask
                    -- `_task_looks_like_review`" signal -- see that method's
                    -- own docstring.
                    is_review INTEGER CHECK (is_review IN (0, 1))
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

                -- agent-estate#741: "authored directly by the Director,
                -- verified, no lane contributed" as its OWN first-class,
                -- recordable fact -- distinct from `pr_external_authorship`
                -- above, on purpose. #741/#748 measured why the existing
                -- external-authorship row is the wrong fit: a director-
                -- authored PR is not "outside the lane system" (a human's
                -- own commit, the watchdog acting directly) -- it is an
                -- INTERNAL estate actor, just not one `register-lane-self.sh`
                -- can ever register (the supervisor's own window is
                -- structurally excluded from being a lane). Reusing
                -- `pr_external_authorship` for this would blur that
                -- distinction in every log line and every future reader of
                -- the table; a same-shaped sibling keeps the two visibly
                -- separate while sharing the identical safety discipline
                -- (see `Ledger.mark_pr_director_authored` /
                -- `Ledger.get_pr_director_authored`).
                CREATE TABLE IF NOT EXISTS pr_director_authorship (
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
                        status IN ('open', 'acknowledged', 'acted', 'resolved', 'dropped', 'needs_review')
                    ),
                    -- `dropped(reason)` in the brief: the reason lives here,
                    -- alongside the status it explains, rather than folded
                    -- into the status string itself -- the same shape as
                    -- `pr_verdicts.note` and `source_tasks.status_marker`
                    -- elsewhere in this ledger. NULL unless status='dropped'
                    -- or status='needs_review' (agent-supervisor#652 below).
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

                -- agent-supervisor#652: the confirmation queue for a
                -- structural marker (`context` = CONTEXT_UNDETERMINED,
                -- #583) that flags a CANDIDATE synthetic-fixture prompt but
                -- cannot, by itself, prove one -- a real post-`/clear`
                -- operator turn stamps the identical marker (#652's own
                -- finding) and reads identically to a fixture by content
                -- (#583's own point). Items land here instead of going
                -- straight to `dropped` so an unconfirmed guess never
                -- silently removes a real directive; they leave
                -- `unacknowledged` (so the view still shrinks) without
                -- leaving the ledger (so nothing is lost pending a human or
                -- a later pass with a real second signal confirming which
                -- way it goes).
                CREATE VIEW IF NOT EXISTS needs_review AS
                    SELECT * FROM items WHERE status = 'needs_review';

                -- agent-supervisor#687: capture stopped silently for four
                -- days because nothing watched the rate new prompts were
                -- arriving at -- `unitemised backlog` stayed a correct
                -- zero the entire time, since a healthy JUDGING half says
                -- nothing about a dead CAPTURE half. This is that signal,
                -- read the same plain-SQL way as the other five views: one
                -- row, `newest_prompt_at` (NULL on a completely empty
                -- corpus, never fabricated) and `seconds_since_capture`
                -- computed at query time so the number is always current,
                -- not stamped when the row was last written.
                CREATE VIEW IF NOT EXISTS capture_health AS
                    SELECT
                        MAX(at) AS newest_prompt_at,
                        CASE WHEN MAX(at) IS NULL THEN NULL
                             ELSE CAST(strftime('%s', 'now') AS INTEGER) - MAX(at)
                        END AS seconds_since_capture
                    FROM prompts;
                """
            )
        os.chmod(self.db_path, 0o600)

    # agent-supervisor#117 adds `worktree_path`: the worktree `worktree.sh
    # new` built for this dispatch, known at dispatch time alongside the
    # lane and task id but, before this, carried only as unstructured text
    # inside `summary` (see `cli.py`'s `record_dispatch` docstring) -- so
    # nothing could look it back up. See `Ledger.get_task_for_worktree`.
    # agent-supervisor#631 adds a seventh marker: `pane_id` is a new column,
    # same reasoning as `worktree_path` above -- a ledger created before it
    # existed has no way to acquire it short of this rebuild.
    _TASKS_SCHEMA_MARKERS = ("delivery_pending", "delivery_attempted_at", "worktree_path", "pane_id")
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

        agent-supervisor#635: on an estate old enough to already carry
        `ONE_OPEN_PULL_PER_SOURCE_REF` (created by
        `_migrate_source_tasks_pull_uniqueness`, which runs AFTER this
        migration in `__init__` but may have run in a PRIOR `__init__` call
        against this same file), that trigger's body joins `tasks` --
        so `ALTER TABLE tasks_migrated RENAME TO tasks` below momentarily
        renames a table the trigger references while `tasks` itself does
        not exist (it was just dropped). SQLite >= 3.25 validates every
        schema object's references during a rename, not just the renamed
        table's own, and aborts the whole rename with `error in trigger
        one_open_pull_per_source_ref: no such table: main.tasks` --
        reproduced directly against a throwaway copy of the live ledger.
        The fix is to make the trigger not exist for the moment `tasks`
        doesn't, and exist again the moment it does: drop it before the
        rebuild, recreate it (byte-for-byte the same body, via
        `_pull_trigger_sql`) after `tasks` is back under its real name --
        all inside this same transaction, so a rollback restores both the
        table and the trigger together. This only fires when the trigger
        was ALREADY there; a ledger that has never created it yet (a
        genuinely fresh one, or one still short of
        `_migrate_source_tasks_pull_uniqueness`'s own duplicate-refusal
        gate) must not have it installed here, bypassing that gate --
        see this migration's own regression test for the duplicate-bypass
        case this guards against.
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
                trigger_existed = probe.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='trigger' AND name=?",
                    (self.ONE_OPEN_PULL_PER_SOURCE_REF,),
                ).fetchone() is not None
            attempted_column = "delivery_attempted_at" if "delivery_attempted_at" in columns else "NULL"
            # agent-supervisor#117: a pre-existing row recorded no worktree
            # path anywhere structured -- only as free text inside `summary`
            # (`worktree=<path>`). Backfilling that by parsing `summary`
            # would be writing a guess into a column whose whole point is to
            # be authored, recorded fact, so this deliberately does not
            # parse it: an old row reads '', the same "not recorded" answer
            # `get_task_for_worktree` already gives for any unmatched path.
            worktree_path_column = "worktree_path" if "worktree_path" in columns else "''"
            # agent-supervisor#631: a pre-existing row recorded no frozen
            # pane_id snapshot at all -- the column did not exist yet, so
            # there was nothing to capture at assignment time. Backfilling
            # it by looking up the CURRENT `lanes` row for that lane string
            # would be writing exactly the guess this column exists to
            # replace (that live row may since have been reused by a later
            # dispatch, agent-supervisor#631's whole point) -- so an old row
            # reads '', the same "not recorded" answer `worktree_path`
            # already gives, and callers fall back to the live `lanes`
            # lookup unchanged for it.
            pane_id_column = "pane_id" if "pane_id" in columns else "''"

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    if trigger_existed:
                        # agent-supervisor#635: must be gone before `tasks`
                        # is, so the rename below never sees a trigger body
                        # referencing a table that doesn't exist yet.
                        connection.execute(
                            f"DROP TRIGGER IF EXISTS {self.ONE_OPEN_PULL_PER_SOURCE_REF}"
                        )
                        self._fail(failpoint, "after_drop_pull_trigger")
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
                            worktree_path TEXT NOT NULL DEFAULT '',
                            pane_id TEXT NOT NULL DEFAULT ''
                        )
                        """
                    )
                    self._fail(failpoint, "after_create")
                    connection.execute(
                        f"""
                        INSERT INTO tasks_migrated (
                            id, lane, pane_nonce, summary, status, result_path, result_sha256,
                            created_at, updated_at, delivery_attempted_at, delivered_at,
                            accepted_at, completed_at, worktree_path, pane_id
                        )
                        SELECT id, lane, pane_nonce, summary, status, result_path, result_sha256,
                               created_at, updated_at, {attempted_column}, delivered_at,
                               accepted_at, completed_at, {worktree_path_column}, {pane_id_column}
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
                    if trigger_existed:
                        # `tasks` is back under its real name -- the trigger's
                        # reference to it resolves again. Byte-for-byte the
                        # same body it had before, via `_pull_trigger_sql`,
                        # so the duplicate-open-PR guard is never weakened.
                        connection.execute(self._pull_trigger_sql())
                        self._fail(failpoint, "after_recreate_pull_trigger")
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

        agent-supervisor#635: `ONE_OPEN_PULL_PER_SOURCE_REF` is itself a
        `BEFORE INSERT ON source_tasks` trigger, so on an estate old enough
        to already carry it, `DROP TABLE source_tasks` below takes the
        trigger down with it (SQLite drops a table's own triggers when the
        table goes) -- silently, no error, unlike `_migrate_tasks_table`'s
        case where the trigger merely REFERENCES the dropped table. Left
        alone, the duplicate-open-PR guard would vanish the moment this
        migration runs and never come back on its own within this
        `__init__` unless the later `_migrate_source_tasks_pull_uniqueness`
        step happens to still run after it (it does today, but that is
        this method's implementation detail to rely on, not this one's).
        Recreated explicitly here instead, byte-for-byte the same body via
        `_pull_trigger_sql`, so this rebuild is self-contained and the
        guard's presence does not depend on migration ordering elsewhere.
        Only when it was already there -- a source_tasks table old enough
        to need this rebuild but too old to have the trigger yet must NOT
        get it installed here, bypassing
        `_migrate_source_tasks_pull_uniqueness`'s own duplicate-refusal
        gate.
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
                trigger_existed = probe.execute(
                    "SELECT 1 FROM sqlite_master WHERE type='trigger' AND name=?",
                    (self.ONE_OPEN_PULL_PER_SOURCE_REF,),
                ).fetchone() is not None

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    if trigger_existed:
                        # Explicit, not relied-on-implicitly: `DROP TABLE`
                        # below would take this trigger with it anyway
                        # (it's attached to `source_tasks`), but naming it
                        # here keeps this rebuild's trigger handling
                        # symmetric with `_migrate_tasks_table`'s.
                        connection.execute(
                            f"DROP TRIGGER IF EXISTS {self.ONE_OPEN_PULL_PER_SOURCE_REF}"
                        )
                        self._fail(failpoint, "after_drop_pull_trigger")
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
                    if trigger_existed:
                        # `source_tasks` exists again under its real name --
                        # the trigger can attach to it again. Byte-for-byte
                        # the same body it had before, via `_pull_trigger_sql`.
                        connection.execute(self._pull_trigger_sql())
                        self._fail(failpoint, "after_recreate_pull_trigger")
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    # agent-supervisor#640: the two live rows this issue measured directly
    # as mislabelled -- `_task_looks_like_review`'s regex requires "rev" (or
    # "review") right after `^`, `-` or `_`, and in both ids it is preceded
    # by "re" instead, so the OLD fallback never recognised them as reviews.
    # Both are backfilled explicitly here rather than left to a widened
    # regex: this issue's own "do not fix this ... by editing the regex
    # alone" instruction rules out trying to make the pattern generally
    # correct, and a general widening risks the opposite failure this issue
    # is about -- see `_task_looks_like_review`'s own docstring on why
    # "rev" as a substring is not safe to match unconditionally
    # (`revamp-parser`, `reverse-index`). These two ids are known-good
    # ground truth (agent-supervisor#640's own measurement: both were
    # dispatched with `--reviews-pr`, committed nothing -- `ahead=0,
    # dirty=0` -- and were wrongly scored as authors), so they are named
    # here by hand instead. Every OTHER pre-existing row keeps whatever
    # `_task_looks_like_review` already answers for it, unchanged --
    # documented, not silently patched, per this issue's verification bar.
    _KNOWN_MISCLASSIFIED_REVIEW_TASK_IDS = ("as637-rerev636", "Skills266-rerev284")

    def _backfill_known_misclassified_review_tasks(self, *, failpoint=None):
        """One-time, idempotent backfill for the two rows agent-supervisor#640
        measured by hand -- see `_KNOWN_MISCLASSIFIED_REVIEW_TASK_IDS`.

        Runs every `__init__`, not just once: cheap (`id IN (...)`, at most
        two rows), and idempotent by construction -- `is_review = 1` is a
        no-op against a row that already reads 1, and the `WHERE` clause
        only ever touches a row that is BOTH one of these two known ids AND
        still `source_kind = 'pull'`, so it can never overwrite a row this
        issue did not measure. Requires the `is_review` column to already
        exist (`_migrate_source_tasks_review_column` runs first in
        `__init__`) -- `PRAGMA table_info` guards a ledger that predates
        even that, rather than raising `sqlite3.OperationalError: no such
        column` on a first-ever `__init__` ordering change.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                columns = {row["name"] for row in probe.execute("PRAGMA table_info(source_tasks)").fetchall()}
                if "is_review" not in columns:
                    return
            with self._transaction() as connection:
                self._fail(failpoint, "before_backfill")
                connection.execute(
                    f"""
                    UPDATE source_tasks SET is_review = 1
                    WHERE source_kind = 'pull'
                      AND id IN ({",".join("?" for _ in self._KNOWN_MISCLASSIFIED_REVIEW_TASK_IDS)})
                    """,
                    self._KNOWN_MISCLASSIFIED_REVIEW_TASK_IDS,
                )
                self._fail(failpoint, "after_backfill")

    def _migrate_source_tasks_review_column(self, *, failpoint=None):
        """Add `source_tasks.is_review`, agent-supervisor#640.

        Unlike `_migrate_tasks_table` / `_migrate_source_tasks_table` above,
        this does NOT rebuild the table. `ALTER TABLE ... ADD COLUMN` is
        sufficient here and is preferred over the drop-and-recreate dance
        those two migrations need, for two reasons specific to THIS column:

        1. It needs no CHECK constraint widened and no NOT NULL backfill
           that is not a flat constant -- a nullable `INTEGER CHECK
           (is_review IN (0, 1))` is exactly representable by `ADD COLUMN`
           (verified directly: SQLite accepts a `CHECK` on an added column
           as long as its default -- here, the implicit `NULL` every
           pre-existing row gets -- satisfies it; `NULL` always does, since
           SQL `CHECK` treats an unknown/NULL result as passing, not
           failing).
        2. `ADD COLUMN` never renames the table, so it cannot walk into
           agent-supervisor#635/#636's hazard: `ALTER TABLE ... RENAME`
           validates every OTHER schema object's references as part of the
           rename (SQLite >= 3.25), and `_migrate_tasks_table` /
           `_migrate_source_tasks_table` both have to drop and recreate
           `ONE_OPEN_PULL_PER_SOURCE_REF` around their own rebuilds solely
           to survive that validation. `ADD COLUMN` performs no rename at
           all, so the trigger is never at risk here and this migration
           does not touch it.

        `PRAGMA table_info` decides whether the column already exists
        (rather than probing `sqlite_master.sql` for a text marker, the
        other migrations' approach) because there is no CHECK-widening or
        column-set text to search for -- either the column is there or it
        is not.

        Every row written before this migration reads `is_review IS NULL`
        -- "not recorded" -- and `get_contributor_tasks_for_pr` falls back
        to `_task_looks_like_review` for exactly those rows, unchanged from
        before this column existed (agent-supervisor#640's verification
        bar 3). `_backfill_known_misclassified_review_tasks`, run right
        after this in `__init__`, is the one deliberate exception.
        """
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT sql FROM sqlite_master WHERE type='table' AND name='source_tasks'"
                ).fetchone()
                if existing is None:
                    return
                columns = {row["name"] for row in probe.execute("PRAGMA table_info(source_tasks)").fetchall()}
                if "is_review" in columns:
                    return
            with self._transaction() as connection:
                self._fail(failpoint, "before_add_is_review_column")
                connection.execute(
                    "ALTER TABLE source_tasks ADD COLUMN is_review INTEGER CHECK (is_review IN (0, 1))"
                )
                self._fail(failpoint, "after_add_is_review_column")

    ONE_OPEN_PULL_PER_SOURCE_REF = "one_open_pull_per_source_ref"

    @classmethod
    def _pull_trigger_sql(cls):
        """The `CREATE TRIGGER` statement for `ONE_OPEN_PULL_PER_SOURCE_REF`.

        Factored out so `_migrate_tasks_table` and `_migrate_source_tasks_table`
        can drop-and-recreate the SAME trigger body around their own rebuilds
        (see their docstrings) instead of hand-copying this SQL a second and
        third time -- one definition, three call sites, agent-supervisor#635.
        """
        return f"""
            CREATE TRIGGER IF NOT EXISTS {cls.ONE_OPEN_PULL_PER_SOURCE_REF}
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
                    connection.execute(self._pull_trigger_sql())
                    self._fail(failpoint, "after_pull_trigger")
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    # agent-supervisor#652: `'needs_review'` widens `items.status`'s CHECK.
    # Same SQLite limitation as `_migrate_lanes_table` -- no `ALTER TABLE
    # ... ADD CHECK` / widen -- so a ledger created before this value existed
    # needs its `items` table rebuilt to accept it at all.
    _ITEMS_SCHEMA_MARKERS = ("'needs_review'",)

    # Every view that reads `items` directly or transitively (`possibility_count`
    # reads `live_parameters`, which reads `items`) -- SQLite validates a
    # view's SELECT against the live schema at `ALTER TABLE ... RENAME`,
    # so a view still pointing at the pre-rebuild `items` makes the rename
    # itself fail with "no such table: main.items" mid-rebuild. These must
    # be dropped before `DROP TABLE items` and recreated, verbatim, after
    # the rename -- see `_migrate_items_table` below.
    _ITEMS_DEPENDENT_VIEWS = (
        ("unacknowledged", "SELECT * FROM items WHERE status = 'open'"),
        ("live_parameters", "SELECT * FROM items WHERE kind = 'parameter' AND weight != 'retracted'"),
        ("open_questions", "SELECT * FROM items WHERE kind = 'question' AND status = 'open'"),
        ("needs_review", "SELECT * FROM items WHERE status = 'needs_review'"),
        ("possibility_count", "SELECT COUNT(*) AS count FROM live_parameters WHERE weight = 'hard'"),
        (
            "conflicts",
            """
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
            WHERE l.relation = 'conflicts_with'
            """,
        ),
    )

    def _migrate_items_table(self, *, failpoint=None):
        """Widen an existing `items` table's `status` CHECK to accept
        'needs_review' (agent-supervisor#652). Same rebuild-in-place shape
        as `_migrate_lanes_table`: every row preserved, one transaction,
        rolled back whole on any failure. `links.item_id`/`other_item_id`
        REFERENCE `items(id)`, so foreign keys are off for the duration of
        the rebuild, same reasoning as `_migrate_lanes_table`'s `tasks.lane`
        note -- the table is named `items` again by the time this returns,
        and so is every view in `_ITEMS_DEPENDENT_VIEWS` (dropped and
        recreated around the rebuild; see that tuple's own comment for why
        this cannot just rely on `_initialize`'s `CREATE VIEW IF NOT
        EXISTS` running again -- IF NOT EXISTS never touches a view that
        already exists, and by the time this runs, in `__init__`, it
        already does)."""
        with self._locked():
            with contextlib.closing(self._connect()) as probe:
                existing = probe.execute(
                    "SELECT sql FROM sqlite_master WHERE type='table' AND name='items'"
                ).fetchone()
                if existing is None:
                    return
                if all(marker in existing["sql"] for marker in self._ITEMS_SCHEMA_MARKERS):
                    return

            connection = self._connect(foreign_keys=False)
            try:
                connection.execute("BEGIN IMMEDIATE")
                try:
                    for view_name, _ in self._ITEMS_DEPENDENT_VIEWS:
                        connection.execute(f"DROP VIEW IF EXISTS {view_name}")
                    self._fail(failpoint, "after_drop_views")
                    connection.execute(
                        """
                        CREATE TABLE items_migrated (
                            id TEXT PRIMARY KEY,
                            prompt_id TEXT NOT NULL REFERENCES prompts(id),
                            kind TEXT NOT NULL CHECK (
                                kind IN ('parameter', 'question', 'directive', 'thought', 'correction')
                            ),
                            body TEXT NOT NULL,
                            weight TEXT NOT NULL CHECK (weight IN ('hard', 'preference', 'retracted')),
                            status TEXT NOT NULL DEFAULT 'open' CHECK (
                                status IN ('open', 'acknowledged', 'acted', 'resolved', 'dropped', 'needs_review')
                            ),
                            status_reason TEXT,
                            resolved_to TEXT,
                            acked_at INTEGER
                        )
                        """
                    )
                    self._fail(failpoint, "after_create")
                    connection.execute(
                        """
                        INSERT INTO items_migrated (
                            id, prompt_id, kind, body, weight, status, status_reason, resolved_to, acked_at
                        )
                        SELECT id, prompt_id, kind, body, weight, status, status_reason, resolved_to, acked_at
                        FROM items
                        """
                    )
                    self._fail(failpoint, "after_copy")
                    connection.execute("DROP TABLE items")
                    self._fail(failpoint, "after_drop")
                    connection.execute("ALTER TABLE items_migrated RENAME TO items")
                    self._fail(failpoint, "after_rename")
                    for view_name, view_sql in self._ITEMS_DEPENDENT_VIEWS:
                        connection.execute(f"CREATE VIEW {view_name} AS {view_sql}")
                    self._fail(failpoint, "after_recreate_views")
                except BaseException:
                    connection.rollback()
                    raise
                else:
                    connection.commit()
            finally:
                connection.close()

    # agent-supervisor#652: the reason string `itemize_prompts.py` wrote
    # (via `Ledger.drop_item`) for a #583-marker match BEFORE this fix pass
    # existed -- every item carrying it was dropped on `context` alone, with
    # no corroborating signal, which #652 measured drops real operator
    # directives (see this migration's own docstring). Matched by prefix so
    # this survives the reason text picking up detail later without needing
    # a second migration for the same defect.
    _PRE_652_SYNTHETIC_DROP_REASON_PREFIX = "agent-supervisor#583: synthetic eval-scenario fixture"

    def _restore_items_dropped_on_context_alone(self, *, failpoint=None):
        """One-time-per-row, idempotent correction for agent-supervisor#652:
        every item `reclassify_synthetic` moved straight to `status='dropped'`
        before this fix pass existed is moved to `status='needs_review'`
        instead -- unconfirmed, not un-dropped into `open`/`unacknowledged`
        either, because nothing about this batch was VERIFIED real; #652
        traced exactly one of them (the `AGENTS.md` defect-note update) to a
        genuine post-`/clear` operator turn by hand, in the raw transcript --
        a check that does not scale to the other ~36 by any structural
        signal this ledger currently captures (see `synthetic_provenance_reason`
        and `itemize_prompts.SYNTHETIC_REASON`'s own comments). Restoring the
        whole batch to a confirmation queue, rather than guessing which of
        the 37 are real, is the same "refuse rather than invent" posture
        CLAUDE.md's invariant 3 (`restore.sh`) already takes: report
        unrecoverable/unconfirmed and stop, never invent a confident answer
        this data cannot support.

        Idempotent by construction: only touches rows still
        `status='dropped'` with this exact reason prefix, and this method's
        own write always leaves the reason on a NEW prefix (see
        `itemize_prompts.NEEDS_REVIEW_REASON`), so a second run finds
        nothing left to match. Runs every `__init__`, not just once -- cheap
        (bounded by how many rows the pre-#652 `reclassify_synthetic` ever
        touched, 37 on the live ledger) and safe to repeat on a ledger that
        never had the defect at all (0 rows matched, 0 rows changed)."""
        restored_reason = (
            "agent-supervisor#652: restored from a #583 context-alone drop -- "
            "context=CONTEXT_UNDETERMINED alone is not proof of a synthetic fixture "
            "(a real post-/clear operator turn carries the same marker); needs a "
            "confirming read before this can be dropped or reopened"
        )
        with self._locked(), self._transaction() as connection:
            self._fail(failpoint, "before_restore")
            connection.execute(
                """
                UPDATE items
                SET status = 'needs_review',
                    status_reason = ?
                WHERE status = 'dropped'
                  AND status_reason LIKE ?
                """,
                (restored_reason, f"{self._PRE_652_SYNTHETIC_DROP_REASON_PREFIX}%"),
            )
