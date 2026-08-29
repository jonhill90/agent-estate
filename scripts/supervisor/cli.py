"""Command line interface for the portable supervisor ledger.

agent-supervisor#722: split behind this composition root, same shape as
core.py's split in #706/#708 -- the argument parser lives in
cli_parser.py, lane-availability/session reads in cli_lane_ops.py,
dispatch-recording writes in cli_dispatch_record.py, record-completion in
cli_completion.py, and caller/supervisor-lane identity checks in
cli_auth.py. This module re-exports every name those carried under its
original name (`parser`, `DEFAULT_STATE`, `DEFAULT_REPOSITORIES`,
`lane_free`, `session_state`, `HARNESS_OPTION`, `HARNESS_BY_COMMAND`,
`FREE_WINDOW_NAME_RE`, `record_dispatch`, `_unique_redispatch_task_id`,
`_release_issue_claim_for_task`, `record_completion`, `_verify_caller`,
`_is_supervisor_lane`, `_SUPERVISOR_LANE_ALIASES`) so nothing outside this
file set -- a test importing `cli.<name>`, a shell script shelling out to
`cli.py <subcommand>` -- can tell the split happened. Pure move: no
subcommand's behaviour, flags, messages or exit codes changed.
"""

from __future__ import annotations

import json
import os
import secrets
import sys
from pathlib import Path

from acp_transport import ACPTransport
from adapter import ACPAdapter, ClaudePrintAdapter, PiRPCAdapter, TmuxAdapter
from claude_print_transport import ClaudePrintTransport
from core import (
    CLAIM_TASK_PREFIX,
    Ledger,
    TERMINAL_STATUSES,
    claim_owner_token,
    cross_namespace_lane_relation,
    lane_or_task_row,
    lane_population,
    lane_relation,
    lane_relation_from_rows,
)
from github_source import GithubTaskSource
from lane_orphan_reap import LaneOrphanReaper
from lane_worktree_reap import LaneWorktreeReaper
from pi_transport import PiRPCTransport
from reconcile_lane_completions import LaneCompletionReconciler
from reconcile_sources import SourceTaskReconciler
from reconcile_worktree_paths import WorktreePathReconciler
from sensor import StateSensor
from transport import TmuxTransport

from cli_auth import _SUPERVISOR_LANE_ALIASES, _is_supervisor_lane, _verify_caller
from cli_completion import record_completion
from cli_dispatch_record import (
    _release_issue_claim_for_task,
    _unique_redispatch_task_id,
    record_dispatch,
)
from cli_lane_ops import FREE_WINDOW_NAME_RE, HARNESS_BY_COMMAND, HARNESS_OPTION, lane_free, session_state
from cli_parser import DEFAULT_REPOSITORIES, DEFAULT_STATE, parser


def _print(value):
    print(json.dumps(value, sort_keys=True, separators=(",", ":")))


# agent-supervisor#303: "cli.py prompts <view> -- print any of the five views
# as a table" (the brief's own words). Every other command here prints one
# JSON line for a script to parse; this one is for a human -- Jon reading
# `unacknowledged` directly -- so it is deliberately not `_print`.
def _print_table(rows):
    if not rows:
        print("(no rows)")
        return
    columns = list(rows[0].keys())
    widths = {c: max(len(c), max(len(str(row[c])) for row in rows)) for c in columns}
    print("  ".join(c.ljust(widths[c]) for c in columns))
    print("  ".join("-" * widths[c] for c in columns))
    for row in rows:
        print("  ".join(str(row[c]).ljust(widths[c]) for c in columns))



def main(argv=None):
    args = parser().parse_args(argv)
    # agent-supervisor#108: answered BEFORE any ledger is opened, for the
    # overwhelming majority of comparisons -- both ids parse as
    # `<session>:<index>` (a tmux lane, the common case) and the string-shape
    # check alone gets a positive answer. A comparison that needed a readable
    # database for THAT case would make the author-exclusion guard fail on a
    # state directory it never had to touch -- including in a test harness,
    # or on a host where the default state dir is another estate's live
    # ledger.
    #
    # agent-supervisor#292: `unknown` from the shape check is not the end of
    # it anymore. A claude-print or pi-rpc lane id has no window to index, so
    # it can never satisfy `LANE_ID_RE` and the shape check answers `unknown`
    # for EVERY comparison involving one -- see `core.lane_relation_from_rows`
    # own comment for the measurement. ONLY when the shape check could not
    # decide does this open the ledger, to widen through the registry's
    # `pane_id` instead -- so the common tmux-vs-tmux case still never pays
    # for a database open, and the claude-print/pi-rpc case finally can be
    # established rather than reflexively refused.
    if args.command == "lane-relation":
        # agent-supervisor#631: identical STRINGS are `same`, full stop --
        # checked before any pane-id reasoning (frozen or live) ever runs.
        # `core.lane_relation` already holds this as its own first rule
        # (`if one == other: return "same"`), but the `--lane-pane-id`
        # branch below never consulted it before deciding from pane ids --
        # harmless pre-#631, because both sides were always resolved LIVE at
        # the same instant, so an identical string could only ever compare
        # equal to itself. #631 broke that symmetry: `--other-pane-id` lets
        # a caller supply a FROZEN historical snapshot for `--other` while
        # `--lane-pane-id` stays a fresh live measurement, and a lane that
        # legitimately re-registered itself since that snapshot was taken
        # (a claude-print re-registration over the same lane string,
        # `test_merge_pr.sh`'s own live case) then compares its OLD pane
        # against its NEW one and wrongly answers `different` for a literal
        # self-review. No pane fact can ever outrank the two callers naming
        # the exact same lane id.
        if isinstance(args.lane, str) and isinstance(args.other, str) and args.lane.strip() and args.lane.strip() == args.other.strip():
            _print({"lane": args.lane, "other": args.other, "relation": "same"})
            return 0
        # agent-supervisor#235: `--lane-pane-id` -- a LIVE measurement the
        # caller took off tmux itself, not a lookup -- is reconciled BEFORE
        # the string-shape check gets to answer at all, not only when that
        # check says `unknown`. The shape check would happily answer
        # `different` for two `<session>:<index>` strings whose indices
        # differ even when a renumber means they now name the SAME window;
        # that positive-looking `different` is wrong in exactly the
        # self-review direction the guard exists to prevent, so it must
        # never be trusted over a live measurement that can settle the
        # question directly against the ledger's `pane_id` registry (#292).
        # `args.other`'s row is still the ledger's own record -- there is no
        # live pane to re-measure for a lane this call is not the candidate
        # for -- unchanged from #292's reasoning.
        if args.lane_pane_id:
            # agent-supervisor#631: `--other-pane-id`, when supplied, IS
            # `other_row` -- no ledger lookup at all, so a `lanes` row this
            # string was reused to overwrite can never be consulted for this
            # comparison. See the flag's own argparse help above.
            if args.other_pane_id:
                other_row = {"pane_id": args.other_pane_id}
            else:
                try:
                    relation_ledger = Ledger(args.state_dir)
                    # agent-supervisor#689: `--other` may name a TASK id
                    # (a pane-having lane's own review dispatched itself with
                    # its task id, not a registered `lanes` row -- see
                    # `core.lane_or_task_row`'s own comment) as well as a
                    # registered lane -- try the lane first, the task's
                    # frozen `pane_id` snapshot second.
                    other_row = lane_or_task_row(relation_ledger, args.other)
                except Exception:
                    other_row = None
            lane_row = {"pane_id": args.lane_pane_id}
            relation = lane_relation_from_rows(lane_row, other_row)
            if relation == "unknown":
                # agent-supervisor#605: the shape/pane-id check above cannot
                # place a daemon-shaped id positive of anything -- its
                # ledger row carries pane_id='' (EnsureLane's own write), so
                # `lane_relation_from_rows` answers `unknown` for it every
                # time, same as it did for every daemon-authored PR before
                # this. Tried only when that check has already given up.
                cross = cross_namespace_lane_relation(args.lane, lane_row, args.other, other_row)
                if cross is not None:
                    relation = cross
            result = {"lane": args.lane, "other": args.other, "relation": relation}
            if relation != "different":
                result["lane_population"] = lane_population(args.lane, lane_row)
                result["other_population"] = lane_population(args.other, other_row)
            _print(result)
            return 0
        relation = lane_relation(args.lane, args.other)
        result = {"lane": args.lane, "other": args.other, "relation": relation}
        if relation == "unknown":
            try:
                relation_ledger = Ledger(args.state_dir)
                # agent-supervisor#689: same task-id fallback as the
                # `--lane-pane-id` branch above, for BOTH sides here -- this
                # branch runs when the shape check found neither id looks
                # like `<session>:<index>` at all, exactly the case a
                # task-style reviewer or author id lands in.
                lane_row = lane_or_task_row(relation_ledger, args.lane)
                # agent-supervisor#631: same widening as the `--lane-pane-id`
                # branch above -- `--other-pane-id` applies regardless of
                # whether `--lane-pane-id` was also given.
                if args.other_pane_id:
                    other_row = {"pane_id": args.other_pane_id}
                else:
                    other_row = lane_or_task_row(relation_ledger, args.other)
            except Exception:
                lane_row = other_row = None
            relation = lane_relation_from_rows(lane_row, other_row)
            if relation == "unknown":
                # agent-supervisor#605: same widening as the --lane-pane-id
                # branch above, for the case where neither side arrived
                # with a live-measured pane id and both rows came straight
                # from the ledger.
                cross = cross_namespace_lane_relation(args.lane, lane_row, args.other, other_row)
                if cross is not None:
                    relation = cross
            result["relation"] = relation
            if relation != "different":
                # agent-supervisor#292 item 3: named only on a refusing
                # answer (same/unknown) -- the actionable detail a caller
                # needs to explain WHY it refused, e.g. dispatch.sh's
                # author-exclusion skip message. The admitting path
                # (`different`) never needed an explanation and does not
                # pay for one.
                result["lane_population"] = lane_population(args.lane, lane_row)
                result["other_population"] = lane_population(args.other, other_row)
        _print(result)
        return 0
    ledger = Ledger(args.state_dir)
    # tmux stays the default transport for every existing lane (codex,
    # claude) and is never replaced -- Jon requires the persistent, watchable
    # terminals it gives him. ACP is opt-in per lane, selected by the lane's
    # registered harness: only harness=copilot-acp dispatches through
    # ACPTransport (SPEC §15.2 -- Copilot is the only harness that ships an
    # ACP server today). pi RPC (agent-supervisor#58) is opt-in the same way,
    # but by TRANSPORT rather than harness alone -- unlike copilot-acp, a
    # `pi` lane may be registered either `send-keys` or `pi-rpc`
    # (`core.py`'s `_TRANSPORTS_BY_HARNESS`), so harness alone cannot decide
    # which adapter drives it; the lane's own recorded transport must.
    #
    # agent-supervisor#171: `claude-print` is the same shape as pi RPC's
    # opt-in, one level down -- a `claude` lane may be registered either
    # `send-keys` (the standing, watched lanes, untouched) or `claude-print`
    # (a headless dispatch-and-collect lane over `claude -p`), so this too is
    # decided by the lane's recorded TRANSPORT, never by `harness` alone.
    adapter = TmuxAdapter(ledger, TmuxTransport(args.tmux_bin))
    acp_adapter = ACPAdapter(ledger, ACPTransport.spawn)
    pi_adapter = PiRPCAdapter(ledger, PiRPCTransport.spawn)
    # `--model sonnet`, same alias `harness/claude.sh` launches every other
    # claude lane with (CLAUDE.md's own convention: cheaper tiers for
    # workers) -- a headless claude-print lane must not silently default to
    # whatever `claude -p` resolves on its own, which measured opus on this
    # host.
    claude_print_adapter = ClaudePrintAdapter(
        ledger, lambda **kwargs: ClaudePrintTransport.spawn(**{"model": "sonnet", **kwargs})
    )

    def adapter_for_harness(harness, transport=None):
        if harness == "copilot-acp":
            return acp_adapter
        if harness == "pi" and transport == "pi-rpc":
            return pi_adapter
        if harness == "claude" and transport == "claude-print":
            return claude_print_adapter
        return adapter

    def adapter_for_lane(lane):
        record = ledger.get_lane(lane)
        if record is None:
            raise ValueError(f"unknown lane: {lane}")
        return adapter_for_harness(record["harness"], record.get("transport"))

    if args.command == "register":
        value = adapter_for_harness(args.harness, args.transport).register_lane(
            lane=args.lane,
            target=args.target,
            harness=args.harness,
            repo=args.repo,
            nonce=args.nonce or secrets.token_hex(16),
        )
    elif args.command == "assign":
        value = adapter_for_lane(args.lane).assign_task(lane=args.lane, task_id=args.task, summary=args.summary)
    elif args.command == "record-dispatch":
        value = record_dispatch(
            ledger,
            lane=args.lane,
            task=args.task,
            summary=args.summary,
            pane_id=args.pane_id,
            pane_path=args.pane_path,
            command=args.pane_command,
            server_id=args.server_id,
            session_id=args.session_id,
            harness_session_id=args.harness_session_id,
            harness_project_dir=args.harness_project_dir,
            issues=args.issue,
            github=args.github,
            harness=args.harness,
            worktree_path=args.worktree,
            pr=args.pr,
            is_review=args.is_review,
            confirm_landed=args.confirm_landed,
        )
    elif args.command == "lane-free":
        value = lane_free(
            ledger, adapter.transport, lane=args.lane, target=args.target, window_name=args.window_name
        )
    elif args.command == "lane-diagnostic":
        lane = ledger.get_lane(args.lane)
        task = ledger.open_task_for_lane(args.lane)
        value = {
            "lane": args.lane,
            "known": lane is not None,
            "task": task["id"] if task is not None else None,
            "status": task["status"] if task is not None else None,
            "summary": task["summary"] if task is not None else None,
            "created_at": task["created_at"] if task is not None else None,
            "updated_at": task["updated_at"] if task is not None else None,
            "delivered_at": task["delivered_at"] if task is not None else None,
            # agent-supervisor#615: `lane-retire.sh` needs the LEDGER's
            # recorded worktree for this lane, not whatever directory its
            # pane happens to be sitting in (`tasks.worktree_path`, written
            # by `record-dispatch` at dispatch time). Exposed as `""` rather
            # than `None` for a task row that predates the column or that
            # never had a worktree recorded (a claim-lane placeholder row) --
            # matches the column's own `NOT NULL DEFAULT ''`, so a caller can
            # treat "blank" and "column absent" identically instead of
            # branching on two different falsy shapes.
            "worktree_path": (task.get("worktree_path") or "") if task is not None else "",
        }
    elif args.command == "claim-lane":
        owner = None if args.owner_pid is None else claim_owner_token(args.owner_pid)
        value = ledger.claim_lane(args.lane, token=args.token, owner=owner)
    elif args.command == "commit-lane-claim":
        value = ledger.commit_lane_claim(args.lane, token=args.token)
    elif args.command == "release-lane-claim":
        # `released` reports whether a row actually went away, not whether the
        # command ran (agent-dotfiles#209 round 2). It is `false` for a claim
        # that never existed AND for one already marked live by
        # `commit-lane-claim`, which this deliberately will not free -- and an
        # operator following the refusal's recovery steps has to be able to
        # see that from the output rather than by re-reading `status`.
        released = ledger.release_lane_claim(args.lane, token=args.token)
        value = {"lane": args.lane, "token": args.token, "released": released}
        if not released:
            # agent-supervisor#174: "no reserved claim matched" was written
            # for exactly one case -- a live brief behind the claim -- and
            # said it regardless of why the DELETE actually matched nothing.
            # A row still exists but is uncommitted (contradicts this
            # method's own contract) is not a real case; the two that are:
            # a claim already LIVE (still needs cancel-open-task, unchanged),
            # and no row at all under this id -- which after #174's fix to
            # `claim_lane` means either nobody ever claimed under this token,
            # or a previous claim under it already closed. The second used to
            # be exactly the state that left a lane stuck; it no longer
            # blocks anything (the next `claim-lane` for this token revives
            # a closed row itself), so the hint says that instead of
            # asserting a live brief that is not there.
            existing = ledger.get_task(f"{CLAIM_TASK_PREFIX}{args.lane}:{args.token}")
            if existing is None:
                value["hint"] = "no claim by this token exists; nothing to release"
            elif existing["status"] not in ("complete", "failed", "cancelled"):
                value["hint"] = "a claim with a live brief behind it needs cancel-open-task"
            else:
                value["hint"] = (
                    f"this claim already closed ({existing['status']}); "
                    "it is not blocking dispatch, and a retry under the same token will reclaim it"
                )
    elif args.command == "reap-lane-claims":
        reaped = ledger.reap_stale_lane_claims()
        value = {"reaped": reaped, "count": len(reaped)}
    elif args.command == "take-supervisor-lease":
        value = ledger.take_supervisor_lease(owner=claim_owner_token(args.owner_pid), note=args.note)
    elif args.command == "supervisor-lease":
        row = ledger.supervisor_lease()
        value = {"held": row is not None, "owner": row["owner"] if row is not None else None, "lease": row}
    elif args.command == "release-supervisor-lease":
        released = ledger.release_supervisor_lease(owner=claim_owner_token(args.owner_pid))
        value = {"released": released}
    elif args.command == "reap-supervisor-lease":
        reaped = ledger.reap_stale_supervisor_lease()
        value = {"reaped": reaped}
    elif args.command == "cancel-open-task":
        # agent-supervisor#649: pick exactly one of the three ways a caller
        # can answer "was there a result?" -- argparse cannot express this
        # mutual exclusion (each flag is independently optional) without
        # losing the specific error message, so it is checked here, the same
        # way `record_completion` checks its own --task/--lane requirement.
        given = [name for name, value in (
            ("--result-file", args.result_file), ("--note", args.note), ("--abandoned", args.abandoned or None)
        ) if value]
        if len(given) == 0:
            raise RuntimeError("cancel-open-task requires --result-file, --note, or --abandoned")
        if len(given) > 1:
            raise RuntimeError(f"cancel-open-task takes exactly one of --result-file/--note/--abandoned, got {given}")
        if args.result_file is not None:
            result = args.result_file.read_bytes()
        elif args.note is not None:
            result = args.note.encode("utf-8")
        else:
            result = None
        cancelled = ledger.cancel_open_task(args.lane, result=result, abandoned=args.abandoned)
        # agent-supervisor#359: this is an operator recovering a lane a
        # normal completion never reached (the crash path) -- exactly the
        # shape #359's own "Done this tick" workaround released by hand.
        # cancel_open_task always mints a freshly-cancelled row here (never a
        # replay of one already terminal -- its own SELECT excludes
        # 'complete'/'failed'/'cancelled'), so no already-terminal guard is
        # needed the way complete()'s idempotent short-circuit requires one.
        if cancelled is not None:
            _release_issue_claim_for_task(ledger, cancelled["id"])
        value = {"lane": args.lane, "cancelled": cancelled}
    elif args.command == "task-lane":
        row = ledger.get_task(args.task)
        value = {
            "task": args.task,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            # agent-supervisor#631: this task's frozen pane_id snapshot (see
            # `core.pane_id_for_task` and the `tasks.pane_id` column
            # comment) -- '' for a task predating the column, never None, so
            # a caller can pass it straight through to
            # `lane-relation --other-pane-id` without a truthiness surprise.
            "pane_id": (row.get("pane_id") or "") if row is not None else None,
        }
    elif args.command == "issue-lane":
        row = ledger.get_task_for_issue(args.issue, repo=args.repo)
        value = {
            "issue": args.issue,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
            # agent-supervisor#480: `claim.sh stale` has no tmux window to
            # check for a claude-print lane (it runs as a plain subprocess,
            # not a tmux pane) -- these two let it ask the ledger instead
            # whether the most recent dispatch for this issue is still
            # genuinely open (`status` in `delivered`/`accepted` and
            # `completed_at` unset) before reporting the issue stale.
            # `row["status"]`/`row["completed_at"]` were already fetched by
            # `get_task_for_issue`'s `SELECT tasks.*, ...`; this just exposes
            # them the same way `lane`/`task` already are.
            "status": row["status"] if row is not None else None,
            "completed_at": row["completed_at"] if row is not None else None,
        }
    elif args.command == "pr-lane":
        row = ledger.get_open_task_for_pr(args.pr, repo=args.repo)
        value = {
            "pr": args.pr,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
        }
    elif args.command == "author-issue-lane":
        row = ledger.get_author_task_for_issue(args.issue, head_ref=args.head_ref, repo=args.repo)
        value = {
            "issue": args.issue,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
        }
    elif args.command == "contributor-issue-lanes":
        rows = ledger.get_contributor_tasks_for_issue(args.issue, repo=args.repo)
        value = {
            "issue": args.issue,
            "known": len(rows) > 0,
            "contributors": [{"lane": row["lane"], "task": row["id"]} for row in rows],
        }
    elif args.command == "worktree-lane":
        row = ledger.get_task_for_worktree(args.path, include_reviews=args.include_reviews)
        value = {
            "path": args.path,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
        }
    elif args.command == "contributor-pr-lanes":
        rows = ledger.get_contributor_tasks_for_pr(args.pr, repo=args.repo)
        value = {
            "pr": args.pr,
            "known": len(rows) > 0,
            "contributors": [{"lane": row["lane"], "task": row["id"]} for row in rows],
        }
    elif args.command == "record-pr-for-task":
        ledger.record_pr_for_task(task_id=args.task, repo=args.repo, pr_number=args.pr)
        value = {"task": args.task, "repo": args.repo, "pr": args.pr, "recorded": True}
    elif args.command == "pr-task":
        row = ledger.get_task_for_pr_number(repo=args.repo, pr_number=args.pr)
        value = {
            "repo": args.repo,
            "pr": args.pr,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
        }
    elif args.command == "mark-pr-external":
        ledger.mark_pr_external(
            repo=args.repo, pr_number=args.pr, note=args.note, chain_verified=args.chain_verified
        )
        value = {"repo": args.repo, "pr": args.pr, "marked_external": True}
    elif args.command == "pr-external":
        row = ledger.get_pr_external(repo=args.repo, pr_number=args.pr)
        value = {"repo": args.repo, "pr": args.pr, "known": row is not None, "note": row["note"] if row else None}
    elif args.command == "mark-pr-director-authored":
        ledger.mark_pr_director_authored(
            repo=args.repo, pr_number=args.pr, note=args.note, chain_verified=args.chain_verified
        )
        value = {"repo": args.repo, "pr": args.pr, "marked_director_authored": True}
    elif args.command == "pr-director":
        row = ledger.get_pr_director_authored(repo=args.repo, pr_number=args.pr)
        value = {"repo": args.repo, "pr": args.pr, "known": row is not None, "note": row["note"] if row else None}
    elif args.command == "record-completion":
        value = record_completion(ledger, task=args.task, lane=args.lane, note=args.note)
    elif args.command == "accept":
        task = ledger.get_task(args.task)
        if task is None:
            raise ValueError("unknown task")
        lane = ledger.get_lane(task["lane"])
        _verify_caller(adapter_for_lane(task["lane"]), ledger, task["lane"])
        value = ledger.accept(args.task, pane_nonce=lane["nonce"])
    elif args.command == "complete":
        task = ledger.get_task(args.task)
        if task is None:
            raise ValueError("unknown task")
        _verify_caller(adapter_for_lane(task["lane"]), ledger, task["lane"])
        # agent-supervisor#359: see record_completion's own comment on
        # already_complete -- an idempotent replay of an already-complete
        # task must not re-release a claim that legitimately belongs to
        # whatever later work has since re-claimed the same issue.
        already_complete = task["status"] == "complete"
        value = ledger.complete(args.task, args.result_file.read_bytes(), pane_nonce=task["pane_nonce"])
        if not already_complete:
            _release_issue_claim_for_task(ledger, args.task)
    elif args.command == "reconcile":
        # Deliberately not caller-verified and deliberately not the lane's
        # *current* nonce: this is the human-operator path for an ambiguous
        # delivery, run from outside the (possibly stuck, dead, or since
        # re-registered) pane after inspecting it directly. Authentication
        # uses the task's own recorded pane_nonce from send time - see
        # Ledger._reconcile_transition. It never infers its answer from tmux
        # capture.
        task = ledger.get_task(args.task)
        if task is None:
            raise ValueError("unknown task")
        value = ledger.reconcile_delivery(args.task, pane_nonce=task["pane_nonce"], outcome=args.outcome)
    elif args.command == "reconcile-source-tasks":
        value = SourceTaskReconciler(
            ledger, gh_bin=os.environ.get("AGENT_GH_BIN", "gh")
        ).sweep()
    elif args.command == "reconcile-worktree-paths":
        value = WorktreePathReconciler(
            ledger, gh_bin=os.environ.get("AGENT_GH_BIN", "gh")
        ).sweep(dry_run=args.dry_run)
    elif args.command == "reconcile-lane-completions":
        lanes_bin = os.environ.get("AGENT_LANES_BIN", str(Path(__file__).resolve().parent / "lanes.sh"))
        # agent-estate#800: the one real production caller of this sweep --
        # wired here, not defaulted on inside `LaneCompletionReconciler`
        # itself, so every existing test constructing that class directly
        # keeps its prior behaviour (see that class's own `__init__`
        # docstring). `AGENT_REAP_LANE_ORPHANS=0` is the escape hatch if
        # this ever needs to be disabled in production without a code
        # change -- opt-out, not opt-in, because the whole point of #800
        # was that nothing was reaping these by default.
        orphan_reaper = None
        if os.environ.get("AGENT_REAP_LANE_ORPHANS", "1") != "0":
            orphan_reaper = LaneOrphanReaper(ledger)
        # agent-estate#804: same opt-out convention as AGENT_REAP_LANE_
        # ORPHANS above -- wired here, not defaulted on inside
        # LaneCompletionReconciler itself, for the identical reason (every
        # existing test constructing that class directly keeps its prior
        # behaviour). Opt-out, not opt-in, because the whole point of #804
        # was that nothing was reaping these worktrees by default.
        worktree_reaper = None
        if os.environ.get("AGENT_REAP_LANE_WORKTREES", "1") != "0":
            worktree_reaper = LaneWorktreeReaper(ledger)
        value = LaneCompletionReconciler(
            ledger,
            lanes_bin=lanes_bin,
            gh_bin=os.environ.get("AGENT_GH_BIN", "gh"),
            idle_after=args.idle_after,
            stale_after=args.stale_after,
            orphan_reaper=orphan_reaper,
            worktree_reaper=worktree_reaper,
        ).sweep()
    elif args.command == "observe":
        lanes = args.lane or [
            item["lane"] for item in ledger.list_lanes() if not _is_supervisor_lane(item["lane"], args.supervisor_lane)
        ]
        value = [
            event for lane in lanes if (event := adapter_for_lane(lane).observe_lane(lane)) is not None
        ]
    elif args.command == "notify":
        value = {"notified": adapter.notify_supervisor(lane=args.supervisor_lane, retry_after=args.retry_after)}
    elif args.command == "tick":
        with ledger.operation_lock():
            sensor_result = {"events": [], "errors": [], "recoveries": []}
            if not args.no_sensors:
                sensor_result = StateSensor(
                    ledger, repositories=DEFAULT_REPOSITORIES, timeout=args.sensor_timeout
                ).collect_all()
            sensor_blockers = sorted(
                error["component"] for error in sensor_result["errors"] if error["component"].startswith("github-")
            )
            if args.no_sensors:
                sensor_blockers.append("github-sensor-disabled")
            gated = bool(sensor_blockers)
            observations = []
            errors = []
            notified = False
            if not gated:
                for lane in ledger.list_lanes():
                    if _is_supervisor_lane(lane["lane"], args.supervisor_lane):
                        continue
                    try:
                        event = adapter_for_harness(lane["harness"], lane.get("transport")).observe_lane(lane["lane"])
                        if event is not None:
                            observations.append(event["key"])
                        ledger.record_component(f"lane:{lane['lane']}", snapshot=b"reachable", healthy=True)
                    except Exception as error:  # a bad worker lane must not blind the others
                        ledger.record_component(f"lane:{lane['lane']}", healthy=False, error=str(error))
                        errors.append({"lane": lane["lane"], "error": str(error)})
                try:
                    notified = adapter.notify_supervisor(lane=args.supervisor_lane, retry_after=args.retry_after)
                    # as#132: "architecture" here is a ledger COMPONENT KEY, not
                    # the lane name or the window name -- record_component's own
                    # rows are keyed by this literal and changing it would start
                    # a fresh health history and orphan everything recorded
                    # before this PR, for zero behavioural gain. Left unchanged
                    # deliberately; the flag/lane rename above does not migrate
                    # this key. See agent-supervisor#132 (Director's comment).
                    ledger.record_component("architecture", snapshot=b"reachable", healthy=True)
                except Exception as error:
                    ledger.record_component("architecture", healthy=False, error=str(error))
                    errors.append({"lane": args.supervisor_lane, "error": str(error)})
                    notified = False
        value = {
            "sensor_events": sensor_result["events"],
            "sensor_recoveries": sensor_result["recoveries"],
            "sensor_blockers": sensor_blockers,
            "gated": gated,
            "observations": observations,
            "notified": notified,
            "errors": sensor_result["errors"] + errors,
        }
    elif args.command == "sensor":
        value = StateSensor(ledger, repositories=DEFAULT_REPOSITORIES).collect_all()
    elif args.command == "events":
        value = ledger.events_due() if args.due else ledger.list_events()
    elif args.command == "ack":
        _verify_caller(adapter, ledger, args.supervisor_lane)
        ledger.ack(args.event)
        value = {"acked": args.event}
    elif args.command == "reconstruct":
        value = GithubTaskSource().reconstruct(
            ledger, source_url=args.source_url, source_ref=args.source_ref
        )
    elif args.command == "reconstruct-task":
        value = ledger.reconstruct_task(
            task_id=args.task,
            source_kind=args.source_kind,
            source_url=args.source_url,
            source_ref=args.source_ref,
            summary=args.summary,
            source_state="OPEN",
            status="created",
            evidence=args.evidence,
            status_marker=None,
            is_review=args.is_review,
        )
    elif args.command == "restore-plan":
        value = ledger.restore_plan()
    elif args.command == "delivered-open":
        value = {"tasks": ledger.list_delivered_open_tasks()}
    elif args.command == "missing-results":
        value = {"tasks": ledger.list_terminal_tasks_missing_result()}
    elif args.command == "open-worktrees":
        value = {"tasks": ledger.list_open_worktrees()}
    elif args.command == "adopt-session":
        value = ledger.adopt_session(args.session, source=args.source)
    elif args.command == "session-state":
        value = {"session": args.session, "state": session_state(ledger, adapter.transport, session=args.session)}
    elif args.command == "record-session-event":
        value = ledger.record_session_event(args.session, event=args.event, detail=json.loads(args.detail))
    elif args.command == "status":
        value = {
            "lanes": ledger.list_lanes(),
            "source_tasks": ledger.list_source_tasks(),
            "tasks": ledger.list_tasks(),
            "events": ledger.list_events(),
            # agent-supervisor#153: raw ledger rows, not the tri-state read --
            # `status` is the offline, ledger-only view every other section
            # here already is (compare `lanes`, `source_tasks`); cross-
            # checking against a live tmux server belongs to `session-state`,
            # which takes a target and actually queries transport.
            "sessions": ledger.list_sessions(),
        }
    elif args.command == "prompts":
        _print_table(ledger.read_prompt_view(args.view))
        return 0
    else:
        raise AssertionError(args.command)
    _print(value)
    return 0


# Without this, the module is unreachable as a program: `cli.py --help` printed
# nothing and exited 0, which reads as success to any wrapper checking $?. The
# import-based tests all passed throughout, because they call main() directly.
if __name__ == "__main__":
    # agent-supervisor#788: `RuntimeError`/`ValueError` are this file's own
    # vocabulary for an expected refusal -- "unknown task", "unknown lane",
    # "cannot complete failed task" and the like (see record_completion's own
    # `raise RuntimeError(f"unknown {identity}")`, and the many
    # `ValueError`s core.py's `Ledger` raises for an illegal transition).
    # In-process callers (`cli.main([...])` from a test, or from another
    # module) still see the real exception -- several tests assert exactly
    # that with `assertRaises(RuntimeError)` -- so this catches only at the
    # process boundary, where an uncaught one prints a full Python traceback
    # to stderr that a caller checking $? never asked for and a caller
    # checking stderr for problems reads as a crash. `lane-retire.sh` hits
    # this on an already-free lane (a known-benign, expected refusal, exit 1
    # either way) and was printing exactly that traceback before this fix.
    # Anything NOT one of these two types (a KeyError, an AttributeError --
    # an actual bug, not a modelled refusal) still tracebacks, deliberately:
    # this narrows what gets a clean line, it does not silence failures.
    try:
        sys.exit(main())
    except (RuntimeError, ValueError) as error:
        print(f"cli.py: {error}", file=sys.stderr)
        sys.exit(1)
