"""Backfill sweep for `tasks.worktree_path` -- historical rows only, agent-
supervisor#611.

Same posture as `reconcile_sources.py`'s `SourceTaskReconciler` (read that
module's docstring first -- this one is deliberately modeled on it): a bulk,
unattended *projection* from facts that already exist, not a verdict of its
own, and it holds no authority to assert anything the evidence in front of it
does not independently corroborate.

## Why this exists

`agent-supervisor#611`: `tasks.worktree_path` was left empty for every
completion that went through `assign_task` (`ClaudePrintAdapter`/
`ACPAdapter`/`PiRPCAdapter`, and `TmuxAdapter` if called directly) rather
than `dispatch.sh`'s `record-dispatch` -- that gap is fixed forward in
`adapter.py`. This module is the SEPARATE, non-targeted sweep #611 also
asked for: historical rows this codebase already has real disk evidence for,
brought forward the same way `reconcile-source-tasks` brings `source_tasks`
forward, without guessing at a single one of them.

## Why this is not `cli.py record-pr-for-task` wearing a different name

#611's own issue is explicit about the shape this must NOT be: a bare
`--task`/`--pr` pair accepted on trust. `worktree_path` feeds directly into
`verdict-independence.sh`'s authorship resolution (`Ledger.
get_task_for_worktree`) -- an independence-gate-facing fact, not a cosmetic
one. So this sweep never accepts an assertion; it derives its own evidence,
in two independent steps, and refuses (leaves the row untouched) the moment
either one comes up short:

1. **The candidate path.** Read straight off the task's own `summary` --
   every dispatch shape that ever wrote one names the worktree it built,
   either `"...worktree at <path>..."` (the `assign_task` message every
   headless adapter sends) or `"...worktree=<path>;..."` (`dispatch.sh`'s
   `record-dispatch` summary, for callers that predated agent-supervisor
   #117's `--worktree` flag and so still only wrote the path into text).
   This alone is self-reported by the same lane whose worktree_path is
   missing -- worth reading, not worth trusting on its own.
2. **Independent disk corroboration.** The candidate must (a) still exist as
   a git worktree on disk, AND (b) carry a `git reflog` entry for a commit
   that is genuinely reachable from a PR this task is independently on
   record as having produced -- via `Ledger.get_pr_for_task`, i.e. an
   explicit prior `record_pr_for_task` call (`lane-done.sh`'s own
   self-report at completion, made from the branch its own worktree built,
   long before this sweep runs). No `record_pr_for_task` row for a task
   means there is nothing here to corroborate the candidate path against,
   and the row is left untouched -- "unknown stays unknown", same as
   `SourceTaskReconciler`. This sweep never resolves a task's PR by
   guessing from an issue number, a branch name, or which PR "should"
   belong to it; that guesswork is exactly what #611's issue measured three
   prior authorship mechanisms failing on (#539/#552/#556).

Blind to which PR benefits, by construction: nothing here special-cases a
task id, an issue number, or a PR number. Every row is judged by the same
two checks, and #531 (or any other issue) is not in this module's code at
all.

Idempotent: `Ledger.list_complete_tasks_missing_worktree_path()` only ever
returns rows with `worktree_path=''`, so a row this sweep already fixed is
never a candidate again -- a second run against an already-fixed set of
rows performs zero additional writes without needing its own dedup logic.
"""

from __future__ import annotations

import json
import re
import subprocess
from pathlib import Path

from core import normalize_worktree_path

# Matches both dispatch shapes that ever wrote a worktree path into task
# summary text (see this module's own docstring, point 1):
#   "...Do all of your work in the worktree at /var/.../ad-531-... -- ..."
#   "...worktree=/var/.../ad-605-...; brief=..."
# `\S+` stops at the first whitespace either shape's own trailing text
# supplies; the second shape immediately follows the path with `;`, which
# `_extract_candidate_path` strips along with any other trailing punctuation
# `\S+` swallowed.
WORKTREE_SUMMARY_RE = re.compile(r"(?:worktree at|worktree=)\s*(/\S+)")

_TRAILING_PUNCTUATION = ".,;:)"


def subprocess_runner(command):
    return subprocess.run(command, check=True, capture_output=True, text=True).stdout


class WorktreePathReconciler:
    """Sweep every eligible `tasks` row forward from disk evidence that
    already exists -- never from an assertion."""

    def __init__(self, ledger, runner=None, gh_bin="gh", git_bin="git"):
        self.ledger = ledger
        self.runner = runner or subprocess_runner
        self.gh_bin = gh_bin
        self.git_bin = git_bin

    @staticmethod
    def _extract_candidate_path(summary):
        match = WORKTREE_SUMMARY_RE.search(summary or "")
        if match is None:
            return None
        return match.group(1).rstrip(_TRAILING_PUNCTUATION)

    @staticmethod
    def _worktree_exists(path):
        """A real git worktree, not merely a directory that happens to be
        there -- `.git` (file, for a worktree, pointing at the shared repo's
        `worktrees/<name>` gitdir; a plain directory only for a primary
        checkout) is what makes this a worktree rather than any other path a
        summary's text could coincidentally contain."""
        candidate = Path(path)
        return candidate.is_dir() and (candidate / ".git").exists()

    def _pr_head_sha(self, repo, pr_number):
        out = self.runner(
            [self.gh_bin, "pr", "view", str(pr_number), "--repo", repo, "--json", "headRefOid"]
        )
        return json.loads(out)["headRefOid"]

    def _reflog_shas(self, path):
        out = self.runner([self.git_bin, "-C", path, "reflog", "--format=%H"])
        return [line.strip() for line in out.splitlines() if line.strip()]

    def _is_ancestor(self, path, sha, pr_head_sha):
        try:
            self.runner([self.git_bin, "-C", path, "merge-base", "--is-ancestor", sha, pr_head_sha])
            return True
        except subprocess.CalledProcessError:
            return False

    def sweep(self, *, dry_run=False):
        """Advance every eligible row once. Safe to call on a schedule.

        Returns a report dict: `backfilled`/`would_backfill` (real disk
        evidence for each, not just the task id -- the candidate path, the
        PR it was corroborated against, and the reflog commit that proved
        it), `unresolved` (left alone, with the specific reason), and
        `errors` (a `gh`/`git` call that itself failed -- distinct from
        `unresolved`: this is "could not check", not "checked and it did
        not corroborate").
        """
        rows = self.ledger.list_complete_tasks_missing_worktree_path()
        report = {"backfilled": [], "would_backfill": [], "unresolved": [], "errors": []}

        for row in rows:
            task_id = row["id"]
            candidate = self._extract_candidate_path(row["summary"])
            if candidate is None:
                report["unresolved"].append(
                    {"task": task_id, "reason": "no worktree path found in this task's own summary text"}
                )
                continue

            if not self._worktree_exists(candidate):
                report["unresolved"].append(
                    {"task": task_id, "reason": f"not a git worktree on disk: {candidate}"}
                )
                continue

            pr_link = self.ledger.get_pr_for_task(task_id)
            if pr_link is None:
                report["unresolved"].append(
                    {
                        "task": task_id,
                        "reason": (
                            f"worktree {candidate} exists on disk, but no record_pr_for_task link is "
                            "recorded for this task -- nothing to corroborate the candidate path against"
                        ),
                    }
                )
                continue

            try:
                pr_head_sha = self._pr_head_sha(pr_link["repo"], pr_link["pr_number"])
            except Exception as error:
                report["errors"].append({"task": task_id, "error": f"gh pr view failed: {error}"})
                continue

            try:
                reflog = self._reflog_shas(candidate)
            except Exception as error:
                report["errors"].append({"task": task_id, "error": f"git reflog failed: {error}"})
                continue

            corroborating_sha = next(
                (sha for sha in reflog if self._is_ancestor(candidate, sha, pr_head_sha)), None
            )
            if corroborating_sha is None:
                report["unresolved"].append(
                    {
                        "task": task_id,
                        "reason": (
                            f"no commit in {candidate}'s own reflog is reachable from "
                            f"{pr_link['repo']}#{pr_link['pr_number']}'s head ({pr_head_sha}) -- "
                            "the candidate path does not independently corroborate"
                        ),
                    }
                )
                continue

            # agent-supervisor#624: write the CANONICAL spelling, not the raw
            # text the summary happened to carry. The candidate above came
            # from a brief's own "worktree at <path>" / "worktree=<path>"
            # text, written before this sweep ever ran and never resolved --
            # on macOS that is `/var/folders/...`, sometimes with a doubled
            # separator, never `/private/var/...`. `dispatch.sh`'s own
            # `record-dispatch` writes the `pwd -P`-resolved form (see its
            # own comment on agent-supervisor#117); a sweep writing the
            # unresolved form into the same column is exactly the "two
            # writers disagree" defect #624 measured, not a hygiene nit --
            # `normalize_worktree_path` is the one already used to READ this
            # column back (`Ledger.get_task_for_worktree`), so writing
            # through it here keeps every row in the shape reads expect
            # without needing reads to keep compensating for writes forever.
            canonical = normalize_worktree_path(candidate)
            evidence = {
                "task": task_id,
                "worktree_path": canonical,
                "pr": f"{pr_link['repo']}#{pr_link['pr_number']}",
                "pr_head_sha": pr_head_sha,
                "reflog_sha": corroborating_sha,
            }
            if dry_run:
                report["would_backfill"].append(evidence)
                continue

            self.ledger.backfill_task_worktree_path(task_id, canonical)
            report["backfilled"].append(evidence)

        return report
