"""Argument parser and default configuration for cli.py.

Split out of cli.py (agent-supervisor#722, following core.py's split in
#706/#708) -- this module has no behaviour of its own, only the subcommand
surface every dispatcher/test parses. Pure move: every subcommand, flag,
default and help string is byte-for-byte what cli.py carried before the
split. `cli.py` imports `parser`, `DEFAULT_STATE` and `DEFAULT_REPOSITORIES`
back under their original names, so nothing outside this pair of files can
tell the split happened.
"""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

from core import Ledger


DEFAULT_STATE = Path(
    os.environ.get("AGENT_SUPERVISOR_STATE_DIR", Path.home() / ".local/state/agent-dotfiles-supervisor")
)
# The env var exists so the shell dispatcher (`dispatch.sh`, `lane-done.sh`,
# agent-dotfiles#140) and its test suites can aim the recorder at a scratch
# directory without every caller spelling `--state-dir` out. Unset, the
# default is exactly what it was.
# The four harness repos, and only these. This subsystem was ported from the
# estate it was written for (see README.md), and its defaults came with it --
# `tick` runs GitHub sensors against every entry here. That was harmless only
# for as long as the module had no `__main__` and could not be run at all.
# Adding the entry point makes the default list live, so it has to name the
# repos this supervisor actually drives.
def _repositories_from_env():
    """SUPERVISOR_REPOSITORIES overrides the built-in list.

    Format: colon-separated `name=path=owner/repo` entries, e.g.
        SUPERVISOR_REPOSITORIES="dots=$HOME/src/agent-dotfiles=jonhill90/agent-dotfiles"

    #179 §3: the built-in list below hardcodes four absolute `/Users/jon/...`
    paths. That was survivable while this tree lived inside the repo it names;
    in a standalone repo it is the difference between "runs on one laptop" and
    "runs anywhere". The default is unchanged so nothing that works today moves,
    and a malformed entry is SKIPPED LOUDLY rather than silently dropped -- a
    supervisor that quietly drives fewer repos than you configured looks exactly
    like a supervisor with nothing to do.
    """
    raw = os.environ.get("SUPERVISOR_REPOSITORIES", "").strip()
    if not raw:
        return None
    out = []
    for entry in raw.split(":"):
        entry = entry.strip()
        if not entry:
            continue
        parts = entry.split("=")
        if len(parts) != 3 or not all(parts):
            print(
                f"cli.py: SUPERVISOR_REPOSITORIES entry ignored, want name=path=owner/repo: {entry!r}",
                file=sys.stderr,
            )
            continue
        name, path, github = parts
        out.append({"name": name, "path": os.path.expanduser(path), "github": github})
    return out or None


def _first_existing_path(*candidates):
    """agent-estate#729: this repo's own entry below named a directory that
    no longer exists once agent-supervisor was renamed to agent-estate
    (#728). Prefer whichever candidate is actually on disk rather than
    hardcoding just the new name -- swapping one hardcoded literal for
    another rebuilds the exact defect this issue is about, one rename early.
    """
    for candidate in candidates:
        path = os.path.expanduser(candidate)
        if os.path.isdir(path):
            return path
    return os.path.expanduser(candidates[0])


DEFAULT_REPOSITORIES = _repositories_from_env() or (
    {"name": "agent-dotfiles", "path": os.path.expanduser("~/source/repos/Personal/agent-dotfiles"), "github": "jonhill90/agent-dotfiles"},
    {
        "name": "agent-estate",
        "path": _first_existing_path("~/source/repos/Personal/agent-estate", "~/source/repos/Personal/agent-supervisor"),
        "github": "jonhill90/agent-estate",
    },
    {"name": "skills", "path": os.path.expanduser("~/source/repos/Personal/Skills"), "github": "jonhill90/skills"},
    {"name": "skills-private", "path": os.path.expanduser("~/source/repos/Personal/skills-private"), "github": "jonhill90/skills-private"},
    {"name": "agent-evals", "path": os.path.expanduser("~/source/repos/Personal/agent-evals"), "github": "jonhill90/agent-evals"},
)



def parser():
    root = argparse.ArgumentParser()
    root.add_argument("--state-dir", type=Path, default=DEFAULT_STATE)
    root.add_argument("--tmux-bin", default=os.environ.get("AGENT_TMUX_BIN", "tmux"))
    sub = root.add_subparsers(dest="command", required=True)

    register = sub.add_parser("register")
    register.add_argument("--lane", required=True)
    register.add_argument("--target", required=True)
    register.add_argument("--harness", choices=("codex", "claude", "copilot", "copilot-acp", "pi"), required=True)
    register.add_argument("--repo", required=True)
    register.add_argument("--nonce")
    # Only `pi` lanes have a real choice here (agent-supervisor#58): every
    # other harness has exactly one transport it may ever record
    # (`core.py`'s `_TRANSPORTS_BY_HARNESS`), so passing this for them would
    # either be redundant or rejected by that same allow-list. Omit it to get
    # `pi`'s default, `send-keys` -- `pi-rpc` is never silently assumed.
    register.add_argument("--transport", choices=("send-keys", "acp", "pi-rpc", "claude-print"), default=None)

    assign = sub.add_parser("assign")
    assign.add_argument("--lane", required=True)
    assign.add_argument("--task", required=True)
    assign.add_argument("--summary", required=True)

    # The write-only recording pair (agent-dotfiles#140). See `record_dispatch`
    # for why these exist next to `register`/`assign`/`complete` rather than
    # reusing them.
    record_dispatch_parser = sub.add_parser("record-dispatch")
    record_dispatch_parser.add_argument("--lane", required=True)
    record_dispatch_parser.add_argument("--task", required=True)
    record_dispatch_parser.add_argument("--summary", required=True)
    record_dispatch_parser.add_argument("--pane-id", required=True)
    record_dispatch_parser.add_argument("--pane-path", required=True)
    # `dest` spelled out: the subparser dispatch itself uses `command`, and
    # letting the pane's command land there would overwrite the subcommand
    # name argparse just parsed.
    record_dispatch_parser.add_argument("--command", dest="pane_command", required=True)
    record_dispatch_parser.add_argument("--server-id", required=True)
    record_dispatch_parser.add_argument("--session-id", required=True)
    # agent-dotfiles#237. NOT required: the resolver only speaks Claude Code
    # today, and a dispatch to a codex or copilot lane must still record
    # everything else it knows rather than fail. Empty means "not resolved",
    # and `restore.sh` refuses such a lane instead of starting a fresh agent
    # in it -- the failure direction #237 names as its primary constraint.
    record_dispatch_parser.add_argument("--harness-session-id", default="")
    # agent-supervisor#172. The directory `harness-session-id` was resolved
    # IN, recorded by dispatch.sh at the same moment as the id itself --
    # never independently, and never NOT required just because
    # `--harness-session-id` isn't: a caller that passes one without the
    # other would let `restore.sh` pair a real id with the wrong directory,
    # which is the exact defect this issue exists to close. Empty means the
    # same thing an empty `--harness-session-id` means: not resolved, or a
    # pre-#172 caller.
    record_dispatch_parser.add_argument("--harness-project-dir", default="")
    record_dispatch_parser.add_argument("--issue", action="append", required=True)
    # agent-supervisor#159: a PR-scoped dispatch (a review, or a fix pass, on
    # PR <N> while the issue it closes stays claimed by the in-flight work
    # that opened it) records itself AGAINST THE PR, not the issue -- see
    # `Ledger.get_open_task_for_pr`. `--issue` above is still sent and still
    # required: it is what names the worktree/window and still belongs in
    # the evidence trail, but when `--pr` is given it is no longer what this
    # dispatch's `source_tasks` row is keyed by.
    record_dispatch_parser.add_argument("--pr", default=None)
    # agent-supervisor#640: `dispatch.sh` passes this ONLY when `--reviews-pr`
    # (explicit or inferred) is what actually put `--pr` on this call --
    # never for a plain `--pr`-scoped fix pass. Meaningless without `--pr`
    # (an issue-scoped dispatch has no `source_kind='pull'` row for
    # `is_review` to describe); `record_dispatch` below only consults it
    # when `--pr` is also present. See `Ledger.record_dispatch`'s own
    # docstring for what the recorded value means downstream.
    record_dispatch_parser.add_argument("--is-review", action="store_true")
    record_dispatch_parser.add_argument("--github", default="")
    record_dispatch_parser.add_argument("--harness", choices=("codex", "claude", "copilot", "copilot-acp", "pi"))
    # agent-supervisor#117: the worktree `worktree.sh new` built for this
    # dispatch, structured now instead of only living inside `--summary`
    # text -- see `Ledger.get_task_for_worktree`. Not required: a caller
    # that predates this flag (or genuinely has no worktree) still records
    # everything else, same as `--harness-session-id` above.
    record_dispatch_parser.add_argument("--worktree", default="")
    # agent-supervisor#193: NOT the agent's own self-report (`accept`, below,
    # is that -- and it is caller-verified against the lane's own pane_id).
    # This is `dispatch.sh`'s OWN evidence that its send actually landed --
    # a position-anchored proof check (`verified_type --proof-head`) plus a
    # confirmed-empty box (`verified_submit`) -- passed straight through so
    # the ledger can tell "typed and submitted, verified" apart from "the
    # lane went quiet" the moment the dispatch itself already knows the
    # difference. Omitted (the default) leaves the task `delivered`, exactly
    # today's behaviour.
    record_dispatch_parser.add_argument("--confirm-landed", action="store_true")

    # agent-supervisor#36 (second issue comment): a stranded lane's open row
    # is not always a task id an operator has on hand -- `claim_lane` writes
    # a `ledger-claim:<lane>:<token>` row, and the codex harness's
    # completions land as exactly that shape. `--lane` alone resolves
    # whichever row shape currently occupies the lane, the same way
    # `cancel-open-task` does for the cancellation path; `--task` alone keeps the
    # exact-id behaviour this command has always had. At least one is
    # required -- enforced in `record_completion`, not here, so the error
    # names the actual gap instead of argparse's generic mutex message.
    record_completion_parser = sub.add_parser("record-completion")
    record_completion_parser.add_argument("--task")
    record_completion_parser.add_argument("--lane")
    record_completion_parser.add_argument("--note", required=True)

    for name in ("accept", "complete"):
        command = sub.add_parser(name)
        command.add_argument("--task", required=True)
        if name == "complete":
            command.add_argument("--result-file", type=Path, required=True)

    reconcile = sub.add_parser("reconcile")
    reconcile.add_argument("--task", required=True)
    reconcile.add_argument("--outcome", choices=("delivered", "failed"), required=True)

    # agent-supervisor#127: NOT the single-task verb above. `reconcile`
    # resolves one human-supplied delivery verdict; this sweeps every
    # `source_tasks` row forward from GitHub state and the local `tasks`
    # table -- see `reconcile_sources.py`'s module docstring for why they
    # are separate commands rather than one overloaded.
    sub.add_parser("reconcile-source-tasks")

    # agent-supervisor#611: a third sibling sweep, same read-only-projection
    # shape as `reconcile-source-tasks`, a different column -- see
    # `reconcile_worktree_paths.py`'s module docstring for the two
    # independent checks this refuses to backfill a row without.
    reconcile_worktree_paths_parser = sub.add_parser("reconcile-worktree-paths")
    reconcile_worktree_paths_parser.add_argument(
        "--dry-run",
        action="store_true",
        help="report what would be backfilled without writing anything",
    )

    # agent-supervisor#155: a sibling sweep, same shape, a different table.
    # `reconcile-source-tasks` advances `source_tasks` from GitHub state this
    # process can observe; this advances `tasks.status` for a lane that
    # finished without ever running `lane-done.sh`'s `wait-for -S`, from
    # `lanes.sh --json`'s observed pane state instead of trusting the worker
    # to announce it. See `reconcile_lane_completions.py`'s module docstring.
    reconcile_lane_completions_parser = sub.add_parser("reconcile-lane-completions")
    reconcile_lane_completions_parser.add_argument(
        "--idle-after", type=int, default=int(os.environ.get("AGENT_LANE_IDLE_AFTER", "300"))
    )
    # agent-supervisor#374: a claude-print/pi-rpc lane has no pane for
    # --idle-after's observation to ever apply to -- see
    # reconcile_lane_completions.py's DEFAULT_STALE_AFTER_SECONDS comment for
    # why this needs its own, longer, default.
    reconcile_lane_completions_parser.add_argument(
        "--stale-after", type=int, default=int(os.environ.get("AGENT_LANE_STALE_AFTER", "3600"))
    )

    observe = sub.add_parser("observe")
    observe.add_argument("--lane", action="append")
    observe.add_argument("--supervisor-lane", default="supervisor", dest="supervisor_lane")
    observe.add_argument("--architecture-lane", dest="supervisor_lane", help=argparse.SUPPRESS)

    notify = sub.add_parser("notify")
    # as#132: --architecture-lane is a leftover name that misleads every new
    # reader. --supervisor-lane is the name going forward; the old flag is
    # kept as a hidden (help=SUPPRESS) alias sharing the same dest, so a
    # caller that was not migrated in lockstep keeps working.
    notify.add_argument("--supervisor-lane", default="supervisor", dest="supervisor_lane")
    notify.add_argument("--architecture-lane", dest="supervisor_lane", help=argparse.SUPPRESS)
    notify.add_argument("--retry-after", type=int, default=900)

    tick = sub.add_parser("tick")
    tick.add_argument("--supervisor-lane", default="supervisor", dest="supervisor_lane")
    tick.add_argument("--architecture-lane", dest="supervisor_lane", help=argparse.SUPPRESS)
    tick.add_argument("--retry-after", type=int, default=900)
    tick.add_argument("--no-sensors", action="store_true")
    tick.add_argument("--sensor-timeout", type=int, default=30)

    sub.add_parser("sensor")

    events = sub.add_parser("events")
    events.add_argument("--due", action="store_true")

    ack = sub.add_parser("ack")
    ack.add_argument("--event", action="append", required=True)
    ack.add_argument("--supervisor-lane", default="supervisor", dest="supervisor_lane")
    ack.add_argument("--architecture-lane", dest="supervisor_lane", help=argparse.SUPPRESS)

    reconstruct = sub.add_parser("reconstruct")
    reconstruct.add_argument("--source-url", required=True)
    reconstruct.add_argument("--source-ref", required=True)

    # agent-supervisor#160: `reconstruct` above is gated on a
    # `hill90-supervisor:v1` marker in the issue body -- `GithubTaskSource`'s
    # only writer of `source_tasks` rows. Measured across all four repos at
    # the time #160 was filed: zero issues carry that marker, which is
    # exactly why `record_dispatch`'s own docstring says the tmux dispatch
    # path writes its `source_tasks` row itself rather than calling
    # `reconstruct`. `Ledger.reconstruct_task` was always generic -- task id,
    # source facts and a summary, no marker requirement -- it just had no
    # caller that did not also register a lane, assign a task and mark it
    # delivered in the same breath (`record_dispatch`'s five-write bundle,
    # built for "record what already physically happened via send-keys").
    # `dispatch-pi-rpc.sh` needs the opposite shape: create the ledger's
    # record of the WORK before any RPC call is attempted, then let
    # `assign` (routed to `PiRPCAdapter`) perform the real, blocking send --
    # so the two writes cannot be bundled the way `record_dispatch` bundles
    # them for a delivery that already happened. This subcommand is that
    # missing direct caller, exposed standalone rather than folded into
    # `reconstruct` -- widening `reconstruct`'s marker gate was rejected
    # because the gate is a defensible READ filter (which GitHub issues
    # this supervisor treats as tasks), not a write restriction, and this
    # subcommand's caller has already confirmed the issue itself (claim.sh)
    # before ever reaching here.
    reconstruct_task_parser = sub.add_parser("reconstruct-task")
    reconstruct_task_parser.add_argument("--task", required=True)
    reconstruct_task_parser.add_argument("--source-kind", default="issue")
    reconstruct_task_parser.add_argument("--source-url", required=True)
    reconstruct_task_parser.add_argument("--source-ref", required=True)
    reconstruct_task_parser.add_argument("--summary", required=True)
    reconstruct_task_parser.add_argument("--evidence", action="append", default=[])
    # agent-estate#838: tri-state, default None ("not recorded") -- unchanged
    # for every caller that omits it. Only `dispatch-claude-print.sh`'s new
    # `--reviews-pr` reroute passes `--is-review`, the same fact
    # `record-dispatch`'s own `--is-review` already records for the tmux
    # flow (see that flag's own comment above).
    reconstruct_task_parser.add_argument("--is-review", action="store_true", default=None)

    # agent-dotfiles#174: the read side of the seam #140 opened. `dispatch.sh`
    # calls this once per idle-looking candidate instead of trusting the
    # window name. See `lane_free` for the migration story (first-sight
    # backfill).
    lane_free_parser = sub.add_parser("lane-free")
    lane_free_parser.add_argument("--lane", required=True)
    lane_free_parser.add_argument("--target", required=True)
    lane_free_parser.add_argument("--window-name", required=True)

    lane_diagnostic_parser = sub.add_parser("lane-diagnostic")
    lane_diagnostic_parser.add_argument("--lane", required=True)

    # agent-dotfiles#184: the claim side `lane-free` (a query) never had. See
    # `Ledger.claim_lane`'s docstring for why a read-then-write pair of
    # separate calls does not close the race and this is one atomic write.
    claim_lane_parser = sub.add_parser("claim-lane")
    claim_lane_parser.add_argument("--lane", required=True)
    claim_lane_parser.add_argument("--token", required=True)
    # agent-dotfiles#209: the claiming process's pid, so a claim stranded by a
    # kill the shell could not trap can be told from one still in flight. The
    # HOST half is composed here (`claim_owner_token`), not passed in, so it
    # matches what `reap-lane-claims` compares against on the way back out.
    claim_lane_parser.add_argument("--owner-pid", type=int, default=None)

    # agent-dotfiles#209 round 2: the point of no return, called by
    # `dispatch.sh` immediately before the `send-keys Enter` that submits the
    # brief. See `Ledger.commit_lane_claim` for why this is a ledger fact
    # written BEFORE the send rather than a flag in the dispatcher set after
    # it. No `--owner-pid`: this does not change who owns the claim, only
    # whether a cleanup path is still allowed to free it.
    commit_lane_claim_parser = sub.add_parser("commit-lane-claim")
    commit_lane_claim_parser.add_argument("--lane", required=True)
    commit_lane_claim_parser.add_argument("--token", required=True)

    release_lane_claim_parser = sub.add_parser("release-lane-claim")
    release_lane_claim_parser.add_argument("--lane", required=True)
    release_lane_claim_parser.add_argument("--token", required=True)

    # agent-dotfiles#209: the untrappable half of claim cleanup. Called by
    # `dispatch.sh` at startup, before it picks a lane -- see that script's
    # step 0.5 for why the dispatcher itself is the right caller.
    sub.add_parser("reap-lane-claims")

    # agent-dotfiles#238: the supervisor ROLE, not a lane -- see
    # `Ledger.take_supervisor_lease` for why this is its own table rather
    # than a `claim-lane` placeholder. `--owner-pid` is required (unlike
    # `claim-lane`'s optional one): an unowned lease can never be reaped, so
    # taking one without an owner would be permanent until a human cleared it
    # by hand.
    take_lease_parser = sub.add_parser("take-supervisor-lease")
    take_lease_parser.add_argument("--owner-pid", type=int, required=True)
    take_lease_parser.add_argument("--note", default="supervisor loop")

    sub.add_parser("supervisor-lease")

    release_lease_parser = sub.add_parser("release-supervisor-lease")
    release_lease_parser.add_argument("--owner-pid", type=int, required=True)

    sub.add_parser("reap-supervisor-lease")

    # agent-dotfiles#209, from the #144 finding that never got a caller:
    # `Ledger.cancel_open_task` had no CLI wiring at all, so the recovery it
    # exists for -- an operator freeing a lane held by something the automatic
    # reap will not touch (a `ledger-hold:` row from #188, or a claim whose
    # owner pid has been recycled) -- could not be performed with the tools
    # this estate ships. Broader than `release-lane-claim` on purpose: it
    # cancels whatever outstanding task owns the lane, without needing to know
    # its id. Reach for `release-lane-claim` first; it is the scoped one.
    cancel_open_task_parser = sub.add_parser("cancel-open-task")
    cancel_open_task_parser.add_argument("--lane", required=True)
    # agent-supervisor#649: exactly one of these three is required (enforced
    # in `main`, not argparse, so the error names the actual gap the same
    # way `record-completion --task/--lane` already does). `--result-file`/
    # `--note` let a caller who has recovered what the lane actually
    # delivered -- before the pane died -- record that instead of losing it;
    # `--abandoned` is the explicit "there genuinely is nothing" this issue
    # asked for. No default: a caller that does not say which is not allowed
    # to have this silently guessed for them, which is exactly how all 951
    # of the ledger's cancelled rows ended up with a null result.
    cancel_open_task_parser.add_argument("--result-file", type=Path, default=None)
    cancel_open_task_parser.add_argument("--note", default=None)
    cancel_open_task_parser.add_argument("--abandoned", action="store_true")

    # agent-dotfiles#212: the read `dispatch.sh` needs to refuse a review
    # dispatched back to the lane that wrote the code under review. The
    # ledger already records each task's `lane` permanently (`tasks.id` is
    # its primary key, and `Ledger._assign_tx` raises rather than let a
    # second lane claim the same task id) -- this just exposes that lookup,
    # the same way `lane-free` exposes `lane_available` rather than making
    # dispatch.sh touch the database directly.
    task_lane_parser = sub.add_parser("task-lane")
    task_lane_parser.add_argument("--task", required=True)

    # agent-supervisor#35: the same lookup as `task-lane`, but keyed by the
    # GitHub issue a PR closes rather than by a task id parsed out of a
    # branch name -- see `Ledger.get_task_for_issue`. `dispatch.sh`'s
    # `--reviews-pr` authorship check asks this FIRST, before it ever looks
    # at a branch.
    issue_lane_parser = sub.add_parser("issue-lane")
    issue_lane_parser.add_argument("--issue", required=True)
    # agent-supervisor#146: optional -- when given, narrows to that repo's
    # dispatch of this issue number; omitted and the number resolves in more
    # than one repo, this refuses (`known:false`) rather than guess. See
    # `Ledger.get_task_for_issue`.
    issue_lane_parser.add_argument("--repo", default=None)

    # agent-supervisor#159: the PR-scoped sibling of `issue-lane`, asked by
    # `dispatch.sh` BEFORE it selects a lane -- unlike `issue-lane`, which
    # answers the most recent row regardless of status (it has no live
    # caller in dispatch.sh today), this one filters to OPEN so a completed
    # or cancelled prior task on the same PR does not wrongly refuse a fresh
    # dispatch. See `Ledger.get_open_task_for_pr`.
    pr_lane_parser = sub.add_parser("pr-lane")
    pr_lane_parser.add_argument("--pr", required=True)
    # agent-supervisor#146: optional repo scope, same reasoning as
    # `issue-lane --repo`. See `Ledger.get_open_task_for_pr`.
    pr_lane_parser.add_argument("--repo", default=None)

    # agent-supervisor#108: the ONE comparison of two lane ids in this system,
    # exposed so `dispatch.sh` (the guard) and `digest.sh` (the independence
    # report) cannot drift apart on what "the same lane" means -- they had two
    # separate string equalities before this, and both were wrong the same way
    # the morning the session was renamed. See `core.lane_relation`.
    lane_relation_parser = sub.add_parser("lane-relation")
    lane_relation_parser.add_argument("--lane", required=True)
    lane_relation_parser.add_argument("--other", required=True)
    # agent-supervisor#235: the caller's own freshly-measured, LIVE pane id
    # for `--lane` -- dispatch.sh has this for free (it already resolved
    # `--lane`'s tmux target to pick a candidate) and it is the one fact a
    # renumber cannot make stale, unlike the `--lane` STRING itself, whose
    # index half is exactly what a renumber rewrites. Optional and
    # positional-neutral: a caller that cannot supply it (an offline test,
    # `digest.sh`'s independence report reasoning about lanes with no pane to
    # re-measure) gets the pre-#235 behaviour unchanged.
    lane_relation_parser.add_argument("--lane-pane-id", default=None)
    # agent-supervisor#631: the caller's own already-known, FROZEN pane id
    # for `--other` -- e.g. a contributor task's `tasks.pane_id` snapshot
    # (`core.pane_id_for_task`), immune to a later dispatch overwriting the
    # `lanes` row that string names. Without this, `--other`'s pane id is
    # always re-resolved live from `Ledger.get_lane(args.other)` -- exactly
    # the mutable-by-string lookup a reused lane id silently corrupts (see
    # `tasks.pane_id`'s column comment in `core.py`). Optional and
    # independent of `--lane-pane-id`: a caller may supply either, both, or
    # neither, and each defaults to the pre-#631 live lookup when omitted.
    lane_relation_parser.add_argument("--other-pane-id", default=None)

    author_issue_lane_parser = sub.add_parser("author-issue-lane")
    author_issue_lane_parser.add_argument("--issue", required=True)
    # agent-supervisor#77: the PR's own head branch, when the caller has it --
    # resolves authorship by what actually produced the branch instead of by
    # position in the issue's task list. See `Ledger.get_author_task_for_issue`.
    author_issue_lane_parser.add_argument("--head-ref", default=None)
    # agent-supervisor#146: the fix. Issue numbers collide across the repos
    # this estate tracks in parallel (session-per-repo, #111) -- without
    # this, `#181` in `skills` and `#181` in `agent-dotfiles` were
    # indistinguishable and the resolver silently answered for whichever
    # repo's row won its ordering. Optional so existing single-repo callers
    # are unaffected; ambiguous-and-omitted still fails closed inside
    # `Ledger.get_author_task_for_issue`.
    author_issue_lane_parser.add_argument("--repo", default=None)

    # agent-supervisor#190: the CONTRIBUTOR SET, not narrowed to one author --
    # see `Ledger.get_contributor_tasks_for_issue`. `dispatch.sh`'s
    # `--reviews-pr` guard unions this (over every candidate issue the PR
    # closes) with the worktree-path lookup below to build the full set of
    # lanes a review dispatch must exclude.
    contributor_issue_lanes_parser = sub.add_parser("contributor-issue-lanes")
    contributor_issue_lanes_parser.add_argument("--issue", required=True)
    # agent-supervisor#146: optional repo scope. See `issue-lane --repo`.
    contributor_issue_lanes_parser.add_argument("--repo", default=None)

    # agent-supervisor#117: `dispatch.sh`'s `--reviews-pr` last resort, when
    # neither `issue-lane` nor `author-issue-lane` answers. Keyed by the
    # worktree path that currently has the PR's head branch checked out
    # (resolved by the caller from `git worktree list`, not here -- see
    # `Ledger.get_task_for_worktree` for why a branch name alone cannot be
    # trusted) rather than by reconstructing a task id from that branch name.
    worktree_lane_parser = sub.add_parser("worktree-lane")
    worktree_lane_parser.add_argument("--path", required=True)
    # agent-supervisor#212: off by default, so this stays the AUTHOR-FINDING
    # answer `dispatch.sh --reviews-pr` needs (a review task never counts,
    # #76). A caller asking "which task is THIS worktree, whatever it is" --
    # a lane confirming its own identity, AGENTS.md invariant 10 -- passes
    # this so its own review-shaped task id is not filtered out from under
    # it. See `Ledger.get_task_for_worktree` for the two questions this
    # flag distinguishes.
    worktree_lane_parser.add_argument("--include-reviews", action="store_true")

    # agent-supervisor#308 item 2: the fifth resolution path -- the PR's own
    # `source_tasks` rows (`source_kind='pull'`), asked DIRECTLY by PR number
    # rather than by the issue(s) it closes. See
    # `Ledger.get_contributor_tasks_for_pr`.
    contributor_pr_lanes_parser = sub.add_parser("contributor-pr-lanes")
    contributor_pr_lanes_parser.add_argument("--pr", required=True)
    # agent-supervisor#146: optional repo scope. See `issue-lane --repo`.
    contributor_pr_lanes_parser.add_argument("--repo", default=None)

    # agent-supervisor#308 item 1: the explicit "task X's work opened PR N"
    # record -- see `Ledger.record_pr_for_task` / `Ledger.get_task_for_pr_number`.
    # Written after the fact (`lane-done.sh`, best effort), not at dispatch
    # time -- the PR does not exist yet when an issue-keyed dispatch starts.
    record_pr_for_task_parser = sub.add_parser("record-pr-for-task")
    record_pr_for_task_parser.add_argument("--task", required=True)
    record_pr_for_task_parser.add_argument("--repo", required=True)
    record_pr_for_task_parser.add_argument("--pr", required=True)

    pr_task_parser = sub.add_parser("pr-task")
    pr_task_parser.add_argument("--repo", required=True)
    pr_task_parser.add_argument("--pr", required=True)

    # agent-supervisor#308 item 3: "authored outside the lane system" as a
    # first-class, recordable state -- see `Ledger.mark_pr_external` /
    # `Ledger.get_pr_external`.
    mark_pr_external_parser = sub.add_parser("mark-pr-external")
    mark_pr_external_parser.add_argument("--repo", required=True)
    mark_pr_external_parser.add_argument("--pr", required=True)
    mark_pr_external_parser.add_argument("--note", required=True)
    # PR #331 review, finding 2: this method's own backstop can only check
    # two of the five resolution paths (the three needing `gh`/`git` cannot
    # live here) -- so a direct `cli.py mark-pr-external` call, without ever
    # going through `mark-pr-external.sh`'s exhaustive chain, sailed through
    # for the most common (issue-linked) contributor shape. This flag is the
    # explicit, unmissable claim "the exhaustive chain ran and found nobody"
    # -- `mark-pr-external.sh` is the only caller that passes it, once its
    # own `resolve_pr_contributors` chain has actually completed clean.
    # Omitting it is refused outright (see `Ledger.mark_pr_external`); it is
    # not a permission check (nothing stops a caller from lying), it is the
    # difference between an unsafe silent default and a caller having to say
    # so out loud.
    mark_pr_external_parser.add_argument("--chain-verified", action="store_true")

    pr_external_parser = sub.add_parser("pr-external")
    pr_external_parser.add_argument("--repo", required=True)
    pr_external_parser.add_argument("--pr", required=True)

    # agent-estate#741: "authored directly by the Director, verified, no
    # lane contributed" as its OWN first-class, recordable state -- see
    # `Ledger.mark_pr_director_authored` / `Ledger.get_pr_director_authored`.
    # Same shape as mark-pr-external/pr-external above, on purpose -- see
    # core_ledger_schema.py's own comment on pr_director_authorship for why
    # this is a sibling table, not a reuse of pr_external_authorship.
    mark_pr_director_authored_parser = sub.add_parser("mark-pr-director-authored")
    mark_pr_director_authored_parser.add_argument("--repo", required=True)
    mark_pr_director_authored_parser.add_argument("--pr", required=True)
    mark_pr_director_authored_parser.add_argument("--note", required=True)
    # Same rationale as mark-pr-external's own --chain-verified: the explicit,
    # unmissable claim "the exhaustive chain ran and found nobody" --
    # `mark-pr-director-authored.sh` is the only caller that passes it.
    mark_pr_director_authored_parser.add_argument("--chain-verified", action="store_true")

    pr_director_parser = sub.add_parser("pr-director")
    pr_director_parser.add_argument("--repo", required=True)
    pr_director_parser.add_argument("--pr", required=True)

    # agent-dotfiles#237: the read `restore.sh` runs after a tmux server loss.
    # Deliberately its own command rather than a flag on `status`: it must
    # work when there is no tmux server at all, so it touches no transport.
    sub.add_parser("restore-plan")

    # agent-supervisor#291: the ledger half of the pre-dispatch collision
    # check -- which worktrees are currently in flight, so collision-
    # check.sh can `git diff` each one for files without maintaining any
    # graph of its own. See `Ledger.list_open_worktrees` for what counts as
    # "in flight" and why blank worktree_path rows are excluded.
    sub.add_parser("open-worktrees")

    sub.add_parser("delivered-open")

    # agent-supervisor#649: the discoverable half of the fix. Before this, a
    # terminal row with no result -- what every `cancelled` row in the ledger
    # turned out to be -- was invisible unless someone queried
    # ledger.sqlite3 by hand, which is how the issue's own measurement was
    # taken. See `Ledger.list_terminal_tasks_missing_result`.
    sub.add_parser("missing-results")

    # agent-supervisor#153: the write side. `bootstrap-session.sh` is the
    # only caller today -- called once, at the moment it creates a session,
    # never for a session it merely --add-lanes'd into (that session was
    # either already adopted, or predates this feature and stays unknown
    # until someone decides otherwise; --add-lanes has no opinion on
    # supervision, only on window count).
    adopt_session_parser = sub.add_parser("adopt-session")
    adopt_session_parser.add_argument("--session", required=True)
    adopt_session_parser.add_argument("--source", default="bootstrap-session.sh")

    # agent-supervisor#153: the read side, and the one three-state answer
    # every caller (dispatch.sh, a future window-kill guard, agent-tui) is
    # meant to use instead of re-deriving this from lanes.lane strings the
    # way #153 measured drifting. Touches tmux (has-session) as well as the
    # ledger on purpose -- see `session_state` below for why a ledger-only
    # answer is not enough.
    session_state_parser = sub.add_parser("session-state")
    session_state_parser.add_argument("--session", required=True)

    # agent-tui#14: the write `session_remove` calls BEFORE killing anything --
    # see `Ledger.record_session_event`. `--detail` is a JSON string (the full
    # `session_guard.remove_guard` payload that authorized the removal), not
    # a set of flags: it is structured evidence this command only carries
    # through to the ledger, not something it interprets.
    record_session_event_parser = sub.add_parser("record-session-event")
    record_session_event_parser.add_argument("--session", required=True)
    record_session_event_parser.add_argument("--event", required=True)
    record_session_event_parser.add_argument("--detail", required=True)

    sub.add_parser("status")

    # agent-supervisor#303: the read side of the prompt corpus (#280/#297).
    # `view` is restricted to the named views (`Ledger.PROMPT_VIEWS`,
    # `needs_review` added by #652) -- there is no free-form SQL entry point
    # here, same posture as `Ledger.read_prompt_view` itself.
    prompts_parser = sub.add_parser("prompts")
    prompts_parser.add_argument("view", choices=Ledger.PROMPT_VIEWS)
    return root
