"""`Ledger`'s read-side task/PR lookups: single-task/issue/PR resolution,
review-shape detection, contributor/author task queries, PR-external
marking, and the various `list_*` task queries.

Split from `core.py` (agent-supervisor#706, the `sync.py`/#336 pattern).
Behaviour-preserving move only -- combined with the other `Ledger*Mixin`
classes into the single `Ledger` class in `core.py`.
"""

from __future__ import annotations

import contextlib
import re
import sqlite3
import time

from core_lane_relation import normalize_worktree_path  # noqa: F401 -- re-exported by core.py


class LedgerTaskQueriesMixin:
    def get_task(self, task_id):
        with contextlib.closing(self._connect()) as connection:
            return self._dict(connection.execute("SELECT * FROM tasks WHERE id = ?", (task_id,)).fetchone())

    def get_open_task_for_lane(self, lane):
        """The single outstanding row that occupies a lane, whatever its id shape.

        agent-supervisor#36 (second issue comment): a lane's outstanding row
        is not always a dispatched task -- `claim_lane` writes a
        `ledger-claim:<lane>:<token>` row under the same `tasks` table, and an
        operator recovering a stranded lane by hand does not always know
        which shape it is, only the lane. Same SELECT `_find_open_task_for_cancel`
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
        """A NAME-GUESSING FALLBACK, not the intended mechanism.

        agent-supervisor#640: whether a task is a review is a structural
        fact known exactly at dispatch time (`dispatch.sh --reviews-pr` vs
        `--pr`/issue-scoped) and recorded, for `source_kind='pull'` rows, in
        `source_tasks.is_review` -- see that column's own comment on
        `source_tasks`' `CREATE TABLE`. `get_contributor_tasks_for_pr`
        reads that recorded column FIRST and only calls this when it reads
        `NULL` (a row written before the column existed). Every other
        caller here (`_non_review_tasks_for_issue`, `get_task_for_worktree`)
        still uses this as its only mechanism -- they resolve
        `source_kind='issue'` / plain `tasks` rows, which never carry
        `is_review` at all (`--reviews-pr` always writes `source_kind=
        'pull'`), so there is no recorded fact for them to prefer.

        This regex is demonstrably unreliable in BOTH directions -- proven
        by agent-supervisor#640's own measurement, not asserted: it never
        matches "rerev636" or "rereview" (no `-`/`_` separates "re" from
        "rev", so the required "right after `^`/`-`/`_`" boundary never
        fires) and it DOES match "revamp-parser" and "reverse-index" (`rev`
        occurs at the string's own start, satisfying that same boundary,
        then `[-_0-9a-z]*` consumes the rest of the word). Do not widen it
        to fix one of those without re-checking the other -- the two
        failure directions pull in opposite directions on the same pattern.
        Left exactly as measured, on purpose: this issue's own instruction
        is not to fix the guess, but to stop needing it.
        """
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

        agent-supervisor#640: review-exclusion now prefers the RECORDED
        fact, `source_tasks.is_review`, over the name-guessing regex this
        method used exclusively before. `is_review = 1` excludes; `= 0`
        (an explicit "not a review", written for a `--pr` fix-pass) keeps,
        whatever the task happens to be named -- a fix-pass named
        `revamp-parser` or `reverse-index` is never excluded once
        `dispatch.sh` records it this way, even though
        `_task_looks_like_review`'s own regex would misfire on both names.
        Only `is_review IS NULL` -- a row written before this column
        existed -- falls back to `_task_looks_like_review`, exactly the
        (imperfect, measured) behaviour every caller of this method already
        lived with before this column existed.
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT tasks.*, source_tasks.source_url AS _source_url,
                       source_tasks.is_review AS _is_review
                FROM tasks
                JOIN source_tasks ON source_tasks.id = tasks.id
                WHERE source_tasks.source_kind = 'pull' AND source_tasks.source_ref = ?
                ORDER BY tasks.created_at ASC, tasks.id ASC
                """,
                (str(pr_ref),),
            ).fetchall()
        candidates = []
        for row in rows:
            is_review = row["_is_review"]
            is_review = bool(is_review) if is_review is not None else self._task_looks_like_review(
                row["id"], row["summary"]
            )
            if is_review:
                continue
            candidate = self._dict(row)
            candidate.pop("_is_review", None)
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

    def mark_pr_director_authored(self, *, repo, pr_number, note, chain_verified=False, now=None):
        """Record that `pr_number` in `repo` was authored DIRECTLY by the
        Director -- verified, no lane contributed -- a first-class,
        recordable state distinct from `mark_pr_external`'s "authored
        outside the lane system entirely" (agent-estate#741). The Director
        is an internal estate actor, not an external one, and not a lane
        either: `register-lane-self.sh` structurally excludes the
        supervisor's own window from ever registering as one. Once marked,
        this PR's contributor set resolves to KNOWN-EMPTY, same as
        `pr_external_authorship`: no lane wrote it, so no lane is excluded
        from reviewing it. `INSERT ... ON CONFLICT DO UPDATE`: idempotent,
        the note/timestamp of the most recent marking wins.

        GATED exactly like `mark_pr_external` (agent-supervisor#308 item 3 /
        #321's own review, item 5), for the identical reason: this is a
        write that widens a PR's reviewer pool, and a caller with shell
        access but no verification could launder its own PR as "director-
        authored" the same way it could launder one as "external".
        `scripts/supervisor/mark-pr-director-authored.sh` is the recommended
        entry point -- it runs the full exhaustive resolution chain before
        ever reaching this method, AND independently verifies the caller's
        own pane is the Director's window (the opposite identity check from
        `mark-pr-external.sh`'s `$TMUX_PANE`-unset requirement). This method
        itself refuses independently on the same two ledger-only sources
        `mark_pr_external` checks: an explicit `record_pr_for_task` row, and
        a PR-scoped `source_tasks` row (`get_contributor_tasks_for_pr`) --
        never overwrite a real, known contributor.

        `chain_verified` must be passed `True` by a caller that has actually
        run the exhaustive chain (`mark-pr-director-authored.sh` does, and
        only after `resolve_pr_contributors` completed clean); refused when
        false or omitted, regardless of what the two ledger-only checks
        below find -- same rationale as `mark_pr_external`'s own docstring:
        this converts an unsafe silent default into a caller having to
        explicitly claim the chain ran.
        """
        if not chain_verified:
            raise ValueError(
                f"refusing to mark PR #{pr_number} in {repo} director-authored -- "
                f"chain_verified was not set. This method can only check two "
                f"of the five resolution paths (an explicit record_pr_for_task "
                f"row, and a PR-scoped source_tasks row); the other three "
                f"(issue-linkage, worktree, legacy branch) need gh/git and "
                f"cannot run here. Call scripts/supervisor/mark-pr-director-authored.sh, "
                f"which runs the full exhaustive chain first and passes "
                f"chain_verified=True only once it completes clean -- a direct "
                f"call cannot silently skip that chain"
            )
        existing_task = self.get_task_for_pr_number(repo=repo, pr_number=pr_number)
        if existing_task is not None:
            raise ValueError(
                f"refusing to mark PR #{pr_number} in {repo} director-authored -- "
                f"the ledger already records task {existing_task['id']!r} (lane "
                f"{existing_task['lane']!r}) as having opened it; marking this "
                f"director-authored now would erase a known contributor, not record an "
                f"absent one"
            )
        contributor_tasks = self.get_contributor_tasks_for_pr(pr_number)
        if contributor_tasks:
            names = ", ".join(f"{t['id']!r} (lane {t['lane']!r})" for t in contributor_tasks)
            raise ValueError(
                f"refusing to mark PR #{pr_number} in {repo} director-authored -- "
                f"the ledger already records {names} dispatched directly against "
                f"it; marking this director-authored now would erase known contributor(s), "
                f"not record an absent one"
            )
        now = int(now if now is not None else self.clock())
        with self._transaction() as connection:
            connection.execute(
                """
                INSERT INTO pr_director_authorship (repo, pr_number, note, recorded_at)
                VALUES (?, ?, ?, ?)
                ON CONFLICT (repo, pr_number) DO UPDATE SET
                    note = excluded.note, recorded_at = excluded.recorded_at
                """,
                (repo, str(pr_number), note, now),
            )

    def get_pr_director_authored(self, *, repo, pr_number):
        """The director-authorship marking for `pr_number` in `repo`, if any
        recorded by `mark_pr_director_authored`."""
        with contextlib.closing(self._connect()) as connection:
            row = connection.execute(
                "SELECT * FROM pr_director_authorship WHERE repo = ? AND pr_number = ?",
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

        Compared through `normalize_worktree_path` on BOTH sides
        (agent-supervisor#624): `dispatch.sh`'s `record-dispatch` and
        `reconcile_worktree_paths.py`'s backfill sweep have been observed
        writing two different spellings of the same directory (resolved
        `/private/var/...` vs. the unresolved, sometimes doubled-separator
        `/var/...` a brief's own text carries) -- a bare `=` comparison
        cannot see they name the same place, so a correctly-dispatched
        lane reads as undispatched depending on which shape its row
        happened to get. This is a point lookup either way: `worktree.sh
        new` mints a fresh path per dispatch, so at most one row is
        expected to normalize to the same value.
        """
        normalized_query = normalize_worktree_path(worktree_path)
        if not normalized_query:
            return None
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                "SELECT * FROM tasks WHERE worktree_path != ''",
            ).fetchall()
        candidates = [
            self._dict(row) for row in rows
            if normalize_worktree_path(row["worktree_path"]) == normalized_query
            and (include_reviews or not self._task_looks_like_review(row["id"], row["summary"]))
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

    def list_terminal_tasks_missing_result(self):
        """Every `complete`/`failed`/`cancelled` row carrying no result.

        agent-supervisor#649: before this existed, the only way to see this
        state was to query `ledger.sqlite3` by hand -- which is how the
        issue's own measurement was taken (951 of 951 `cancelled` rows,
        3 of 942 `complete`, 17 of 198 `failed`). `cancel_open_task` no
        longer lets a caller land here silently (it now requires an explicit
        `result` or `abandoned=True`), but this stays a live query rather
        than a one-time count: a genuine `abandoned=True` cancellation is
        expected to show up here forever, and a `complete`/`failed` row
        missing a result is always worth a look regardless of how it got
        that way.
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT * FROM tasks
                WHERE status IN ('complete','failed','cancelled') AND result_path IS NULL
                ORDER BY completed_at, id
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

    def list_terminal_tasks_with_worktree(self):
        """Every already-terminal (`complete`/`failed`/`cancelled`) row that
        still names a `worktree_path` -- agent-estate#834.

        This is `list_open_worktrees` above with its status filter
        inverted: that query finds tasks a fresh dispatch could still
        collide with, this one finds tasks a completion-time reaper could
        still retire. Both exist because a task's `worktree_path` says
        nothing about WHEN it went terminal -- `record_completion` (`cli.py
        record-completion`, `dispatch.sh`'s own suggested recovery for a
        lane that finished without signalling) writes `status='complete'`
        directly, outside `reconcile_lane_completions.py`'s sweep, so a row
        can sit here indefinitely with nothing having ever revisited its
        worktree or its pane. `reconcile_lane_completions.py`'s own
        `_leaked_worktree_candidate_ids` is the one caller: it unions this
        query's ids with the ids the current sweep just transitioned, so a
        row that went terminal minutes, days, or months ago through ANY
        path is still offered to the same reap guard chain a same-sweep
        completion already goes through -- never a second, weaker check.

        `worktree_path != ''` excludes a claim-row placeholder and any task
        dispatched before agent-supervisor#117 added the column, same as
        `list_open_worktrees` already does -- neither names a directory
        there is anything to reap.
        """
        with contextlib.closing(self._connect()) as connection:
            rows = connection.execute(
                """
                SELECT * FROM tasks
                WHERE status IN ('complete','failed','cancelled')
                  AND worktree_path != ''
                ORDER BY updated_at, id
                """
            ).fetchall()
        return [self._dict(row) for row in rows]
