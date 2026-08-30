"""Reconciliation sweep for `source_tasks` -- the write-once-table fix.

agent-supervisor#127: `source_tasks` is the only link between intent
(GitHub issues/PRs) and execution (the ledger's `tasks` table), and every
one of its rows is written exactly once, at dispatch, and never touched
again. `source_state` and `status` look like a small state machine but
nothing advances them -- 314/314 rows measured stuck at `OPEN|created`.

This is not `cli.py reconcile` (`Ledger.reconcile_delivery`). That is a
single-task, human-supplied verdict about one ambiguous *delivery*
(`--outcome delivered|failed`), authenticated by the task's own
`pane_nonce`. This is a bulk, unattended *projection*: it derives every
row's two facts from data that already exists elsewhere and holds no
verdict of its own. Kept separate rather than overloaded onto `reconcile`
because the two have nothing in common except touching the same table --
different callers (operator vs. schedule), different authority (a human's
judgement vs. read-only facts), different failure mode (a bad `reconcile`
call resolves one stuck task; a bad sweep could silently mismark 300).

Design, matching this table's own rule ("do not add a third store" --
agent-tui#6) and this codebase's fail-closed convention (`lanes.sh`'s
"`unknown` means not offered, not broken"):

* `source_state` <- the issue/PR's real state on GitHub, fetched in two
  batched calls PER REPO (`gh issue list --state all` and `gh pr list
  --state all`), never per row. 314 rows against 4-5 repos is 314 API
  calls done wrong and under 10 done right. Both calls run for every repo,
  not just the row's own recorded `source_kind`: `record_dispatch`
  hardcodes every row's `source_kind` to `'issue'` regardless of whether
  the dispatched number was actually a PR, and a PR number never appears
  in `gh issue list`'s answer (unlike `gh issue view <n>`, which resolves
  either) -- the other table is this sweep's fallback for exactly that
  case, not a guess.
* `status` <- the `tasks` row of the same id, which IS already maintained
  (`record_completion` sets it terminal). This needs no GitHub call at all,
  and is derived independently of whether GitHub answered.
* Unknown stays unknown. A row is left alone -- not guessed into CLOSED,
  not guessed into a status -- whenever the fact it would need cannot be
  obtained right now:
    - `source_url` is not a canonical `github.com/<owner>/<repo>/(issues|pull)/<n>`
      URL (agent-dotfiles's pre-#127 fallback shape, `issue:<n>@<dir>`, has
      no resolvable repo) -- `source_state` is set to the literal
      `"UNKNOWN"` once, then stays there; this IS a write, not a skip,
      because "I cannot tell" is itself a fact worth recording over the
      stale hardcoded `"OPEN"` every such row currently carries.
    - a repo's `gh ... list` call itself fails (network, auth, rate limit)
      -- every row for that repo is left completely untouched, not marked
      `"UNKNOWN"`; a transient fetch failure must not overwrite a
      previously-good value with "I give up", only report it.
    - GitHub's answer doesn't include this row's number at all (more than
      `--limit` issues+PRs in one repo, or the number never existed) --
      left untouched, same reasoning.
* Idempotent: `Ledger.update_source_task_state` only writes when a value
  actually changes, so a second sweep with no new facts performs zero
  writes.
"""

from __future__ import annotations

import json
import subprocess

from github_source import SOURCE_URL_RE


# tasks.status has one value source_tasks.status has no room for:
# `delivery_pending` ("send attempted, not confirmed"). Mapped to `created`
# rather than `delivered` -- the whole point of that tasks-table state is
# that delivery is NOT yet confirmed, and this sweep must not be the thing
# that turns an unconfirmed send into a confirmed one.
_TASK_STATUS_TO_SOURCE_STATUS = {
    "created": "created",
    "delivery_pending": "created",
    "delivered": "delivered",
    "accepted": "accepted",
    "running": "running",
    "complete": "complete",
    "failed": "failed",
    "cancelled": "cancelled",
}

UNKNOWN_SOURCE_STATE = "UNKNOWN"


def subprocess_runner(command):
    return subprocess.run(command, check=True, capture_output=True, text=True).stdout


class SourceTaskReconciler:
    """Sweep every `source_tasks` row forward from facts that already exist."""

    def __init__(self, ledger, runner=None, gh_bin="gh"):
        self.ledger = ledger
        self.runner = runner or subprocess_runner
        self.gh_bin = gh_bin

    def _fetch_repo_states(self, owner, repo, kind):
        """One batched call for every issue or PR in one repo, open or closed.

        agent-supervisor#144: REST core (`gh api --paginate repos/.../issues`
        or `.../pulls`), not GraphQL (`gh issue list`/`gh pr list`) -- this
        sweep's own docstring already names this as "314 rows against 4-5
        repos is 314 API calls done wrong and under 10 done right"; those ten
        used to all draw on the empty budget. `--paginate` walks every page
        rather than relying on one generous `--limit`, so nothing is silently
        omitted the way a fixed `--limit` could; a row whose number is still
        missing from the result is treated as unresolved (fail-closed), not
        as evidence the number never existed.
        """
        path = "issues" if kind == "issue" else "pulls"
        command = [
            self.gh_bin, "api", "--paginate",
            f"repos/{owner}/{repo}/{path}?state=all&per_page=100",
        ]
        payload = json.loads(self.runner(command))
        if kind == "issue":
            # REST's /issues answers for PRs too (they carry a `pull_request`
            # key) -- filtered out here to match `gh issue list`'s
            # issues-only answer; the /pulls fetch above is the fallback this
            # sweep already has for a PR-numbered row (see sweep()'s own
            # comment on `record_dispatch` hardcoding `source_kind`).
            payload = [item for item in payload if "pull_request" not in item]
        return {str(item["number"]): str(item["state"]).upper() for item in payload}

    def sweep(self):
        """Advance every `source_tasks` row once. Safe to call on a schedule.

        Returns a report dict: `updated` (task ids actually written),
        `unchanged` (already correct -- the idempotence case), `unresolved`
        (left alone; source_state could not be determined this run), and
        `errors` (repo/kind fetches that failed, each affecting every row
        that depended on it).
        """
        rows = self.ledger.list_source_tasks()

        by_repo = {}
        unresolvable = []
        for row in rows:
            match = SOURCE_URL_RE.fullmatch(row["source_url"])
            if match is None:
                unresolvable.append(row)
                continue
            kind = "issue" if match["kind"] == "issues" else "pull"
            by_repo.setdefault((match["owner"], match["repo"]), []).append(
                (row, kind, match["number"])
            )

        report = {"updated": [], "unchanged": [], "unresolved": [], "errors": []}

        for row in unresolvable:
            self._apply(row, source_state=UNKNOWN_SOURCE_STATE, report=report)

        for (owner, repo), entries in by_repo.items():
            # Both tables, always -- `record_dispatch` hardcodes every row's
            # `source_kind` to 'issue' regardless of whether the number it
            # dispatched against was actually a PR (agent-supervisor#127
            # measured: `gh issue view <n>` answers for a PR number too, but
            # the batched `gh issue list` this sweep relies on does not
            # return PR numbers at all -- a real PR-numbered row would
            # otherwise sit unresolved forever). The row's own recorded
            # `source_kind` is tried first; the other table is a fallback,
            # not a guess -- it is still GitHub's own authoritative state for
            # that exact number, just reached via the other endpoint.
            states = {}
            for kind in ("issue", "pull"):
                try:
                    states[kind] = self._fetch_repo_states(owner, repo, kind)
                except Exception as error:
                    states[kind] = None
                    report["errors"].append({"repo": f"{owner}/{repo}", "kind": kind, "error": str(error)})

            for row, kind, number in entries:
                other = "pull" if kind == "issue" else "issue"
                state = None
                if states[kind] is not None:
                    state = states[kind].get(number)
                if state is None and states[other] is not None:
                    state = states[other].get(number)
                if state is None:
                    report["unresolved"].append(row["task_id"])
                    continue
                self._apply(row, source_state=state, report=report)

        return report

    def _apply(self, row, *, source_state, report):
        """Write one row's `source_state` and, independently, its `status`.

        `status` is derived purely from the local `tasks` table -- it needs
        no GitHub call and is computed for every row, including one whose
        `source_state` just went to `UNKNOWN`. The two facts have different
        sources and neither's availability gates the other.
        """
        task_id = row["task_id"]
        local_task = self.ledger.get_task(task_id)
        status = _TASK_STATUS_TO_SOURCE_STATUS.get(local_task["status"]) if local_task else None

        if source_state == row["source_state"] and (status is None or status == row["status"]):
            report["unchanged"].append(task_id)
            return

        updated = self.ledger.update_source_task_state(task_id, source_state=source_state, status=status)
        if updated["source_state"] == row["source_state"] and updated["status"] == row["status"]:
            report["unchanged"].append(task_id)
        else:
            report["updated"].append(task_id)
