"""Command line interface for the portable supervisor ledger."""

from __future__ import annotations

import argparse
import json
import os
import re
import secrets
import sqlite3
import sys
from pathlib import Path

from acp_transport import ACPTransport
from adapter import ACPAdapter, ClaudePrintAdapter, PiRPCAdapter, TmuxAdapter, HARNESS_COMMANDS
from claude_print_transport import ClaudePrintTransport
from core import CLAIM_TASK_PREFIX, Ledger, claim_owner_token, lane_relation
from github_source import GithubTaskSource
from pi_transport import PiRPCTransport
from reconcile_lane_completions import LaneCompletionReconciler
from reconcile_sources import SourceTaskReconciler
from sensor import StateSensor
from transport import TmuxTransport


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


DEFAULT_REPOSITORIES = _repositories_from_env() or (
    {"name": "agent-dotfiles", "path": os.path.expanduser("~/source/repos/Personal/agent-dotfiles"), "github": "jonhill90/agent-dotfiles"},
    {"name": "agent-supervisor", "path": os.path.expanduser("~/source/repos/Personal/agent-supervisor"), "github": "jonhill90/agent-supervisor"},
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

    # agent-supervisor#159: the PR-scoped sibling of `issue-lane`, asked by
    # `dispatch.sh` BEFORE it selects a lane -- unlike `issue-lane`, which
    # answers the most recent row regardless of status (it has no live
    # caller in dispatch.sh today), this one filters to OPEN so a completed
    # or cancelled prior task on the same PR does not wrongly refuse a fresh
    # dispatch. See `Ledger.get_open_task_for_pr`.
    pr_lane_parser = sub.add_parser("pr-lane")
    pr_lane_parser.add_argument("--pr", required=True)

    # agent-supervisor#108: the ONE comparison of two lane ids in this system,
    # exposed so `dispatch.sh` (the guard) and `digest.sh` (the independence
    # report) cannot drift apart on what "the same lane" means -- they had two
    # separate string equalities before this, and both were wrong the same way
    # the morning the session was renamed. See `core.lane_relation`.
    lane_relation_parser = sub.add_parser("lane-relation")
    lane_relation_parser.add_argument("--lane", required=True)
    lane_relation_parser.add_argument("--other", required=True)

    author_issue_lane_parser = sub.add_parser("author-issue-lane")
    author_issue_lane_parser.add_argument("--issue", required=True)
    # agent-supervisor#77: the PR's own head branch, when the caller has it --
    # resolves authorship by what actually produced the branch instead of by
    # position in the issue's task list. See `Ledger.get_author_task_for_issue`.
    author_issue_lane_parser.add_argument("--head-ref", default=None)

    # agent-supervisor#190: the CONTRIBUTOR SET, not narrowed to one author --
    # see `Ledger.get_contributor_tasks_for_issue`. `dispatch.sh`'s
    # `--reviews-pr` guard unions this (over every candidate issue the PR
    # closes) with the worktree-path lookup below to build the full set of
    # lanes a review dispatch must exclude.
    contributor_issue_lanes_parser = sub.add_parser("contributor-issue-lanes")
    contributor_issue_lanes_parser.add_argument("--issue", required=True)

    # agent-supervisor#117: `dispatch.sh`'s `--reviews-pr` last resort, when
    # neither `issue-lane` nor `author-issue-lane` answers. Keyed by the
    # worktree path that currently has the PR's head branch checked out
    # (resolved by the caller from `git worktree list`, not here -- see
    # `Ledger.get_task_for_worktree` for why a branch name alone cannot be
    # trusted) rather than by reconstructing a task id from that branch name.
    worktree_lane_parser = sub.add_parser("worktree-lane")
    worktree_lane_parser.add_argument("--path", required=True)

    # agent-dotfiles#237: the read `restore.sh` runs after a tmux server loss.
    # Deliberately its own command rather than a flag on `status`: it must
    # work when there is no tmux server at all, so it touches no transport.
    sub.add_parser("restore-plan")

    sub.add_parser("delivered-open")

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
    return root


def _print(value):
    print(json.dumps(value, sort_keys=True, separators=(",", ":")))


# NARROW inference, for a command that is actually diagnostic on its own
# (agent-dotfiles#216). Every ledger and every lane bootstrapped before #216
# has no `HARNESS_OPTION` recorded anywhere -- if `lane_free` required that
# option unconditionally, EVERY existing claude/codex lane would go from
# dispatchable to permanently refused the moment this shipped, which is
# obviously not what "fail closed" is supposed to protect. This dict stays
# exactly as narrow as it always was (exact binary name only, no "node"
# entry: `node` names several harnesses and must never resolve from a name
# alone) -- it is what keeps every already-working lane working. It is
# consulted FIRST, in `lane_free`; the recorded `HARNESS_OPTION` is the
# fallback for a command this dict cannot place, e.g. `node`.
HARNESS_BY_COMMAND = {"codex": "codex", "claude": "claude", "claude.exe": "claude"}
FREE_WINDOW_NAME_RE = re.compile(r"^free-[0-9]+$")
# agent-dotfiles#216: the pane option `lane_free`'s backfill reads as the
# RECORDED harness, instead of inferring it from `#{pane_current_command}` --
# see `TmuxAdapter.HARNESS_OPTION` and `bootstrap-session.sh`, the two
# writers.
HARNESS_OPTION = TmuxAdapter.HARNESS_OPTION


# agent-supervisor#153. Three states, and `unknown` collapses to the SAME
# treatment as `unsupervised` in every caller -- this module deliberately
# returns them as distinct strings rather than a bool, because a future
# `supervisor release <session>` (issue #153's own vocabulary, not built
# here) needs to tell "known not ours" apart from "never told either way";
# nothing yet writes the former, so it is not reachable today, but the
# three-way shape is set up so adding it later is additive, not a rename of
# an existing state's meaning.
def session_state(ledger, transport, *, session):
    """supervised / unsupervised / unknown -- the one answer #153 exists for.

    Two independent checks, both required, neither trusted alone:

    * `transport.session_exists` -- does tmux actually have this session
      right now. A stale ledger row for a session that is gone (#153
      measured `agent-dotfiles` and `ad241repro-22535` exactly this way)
      must never read as `supervised` -- there is nothing to act on, and
      reporting one as actionable is worse than reporting nothing, so this
      collapses straight to `unknown`, without even asking the ledger.
    * `ledger.session_marked_supervised` -- did WE decide to adopt it. This
      is the one-way ratchet: unless this call plainly returns True, the
      result is `unknown`, never `supervised`. Caught broadly on purpose --
      a locked ledger, a corrupt file, an old ledger missing the `sessions`
      table (`session_marked_supervised` raising `sqlite3.OperationalError`
      rather than returning) are all "the marker could not be read", and
      #153's acceptance test is exactly this: break that read and confirm
      the result is `unknown`, never `supervised`. A caller-side try/except
      would work too, but every caller would have to remember it; failing
      closed belongs here, once, where it cannot be forgotten by a future
      caller that assumes a clean read.
    """
    if not transport.session_exists(session):
        return "unknown"
    try:
        marked = ledger.session_marked_supervised(session)
    except Exception:
        return "unknown"
    return "supervised" if marked else "unknown"


def lane_free(ledger, transport, *, lane, target, window_name):
    """Answer "is `lane` safe to dispatch to?" from the ledger, not the name.

    agent-dotfiles#174. Three outcomes:

    * The ledger already knows this lane (`lane_available` returns True or
      False) -- that answer wins outright, REGARDLESS of what the window is
      currently named. A hand-renamed window, or one still carrying a task
      name for a task the ledger has never heard finished, must not change
      this: the whole point of the change is that authority moved off the
      name.
    * The ledger has never heard of this lane, and the window is currently
      named by the `free-N` convention -- the one-time MIGRATION path for a
      lane whose availability today exists only as that name (every lane
      alive before this landed, and any future lane opened by hand). This
      registers it in the ledger, with no open task, so it reads free from
      here on without ever consulting the name again. This is "lazy backfill
      on first sight", the option agent-dotfiles#174 itself named: it only
      ever fires once per lane, because the second call finds the lane
      already known and takes the first branch above.
    * Neither -- an unregistered lane not currently named `free-N` is
      UNKNOWN, and unknown is not free. This is the fail-closed default: a
      lane this code cannot positively place is never offered, the same
      posture `lanes.sh`'s own whitelist (#126) already takes for pane state.

    agent-dotfiles#216: harness identity for the backfill branch above tries
    `HARNESS_BY_COMMAND` first -- unchanged, exact-binary-name inference,
    same as before #216 -- and only falls back to the pane's `HARNESS_OPTION`
    (a RECORDED fact, written by `bootstrap-session.sh` or `cli.py register`)
    for a command that dict cannot place. That ordering, not the option
    alone, is what keeps every lane bootstrapped before #216 dispatchable:
    none of them ever had the option written, and requiring it unconditionally
    would have refused all of them the moment this shipped. The option only
    matters for a command `HARNESS_BY_COMMAND` was never able to place --
    every Node-based harness's process reads "node", so copilot (and codex
    whenever its binary is not literally named `codex`) needs a written
    record to be identified at all. An unrecorded option, or one that names a
    harness the live pane's command visibly contradicts
    (`TmuxAdapter._command_matches`), still refuses: this closes the gap for
    a harness that WAS written down, it does not turn "cannot tell" into "go
    ahead". The success reply's `"harness"` key lets a caller (`dispatch.sh`)
    forward the same resolved value into `record-dispatch` instead of that
    command re-deriving one from the pane command on its own.

    WHAT THIS DOES NOT DO (agent-dotfiles#188 finding 2, in the terms
    `claim.sh`'s own header uses for its sub-second race): this is a QUERY,
    not a claim. It takes no lock, writes no assignment for the caller, and
    grants no exclusion. Two dispatchers that both call this for the same
    lane within the same tick both read `"free":true` -- measured, on one
    seeded ledger, two consecutive calls with nothing written in between
    returned the identical answer both times. Nothing re-checks between a
    caller picking this lane and its first `send-keys`, and that window is
    not sub-second the way `claim.sh`'s is: it spans claim, worktree
    creation and the send itself, and NOTHING here stops two dispatchers
    from both typing a competing brief into the same live pane during it.
    `record_dispatch`'s `one_open_task_per_lane` constraint does NOT catch a
    double-dispatch either, measured: `cli.record_dispatch` mints a fresh
    `nonce` on every call, so `_register_lane_tx`'s "changed identity ->
    new incarnation" test is always true, even for the same pane seconds
    apart. A second writer's call does not refuse or hold -- it succeeds,
    cancels the first writer's task, and installs its own as the lane's one
    open task. The ledger ends up recording one clean occupancy, with
    nothing left to show two briefs went into that pane (agent-dotfiles#188
    finding 2 / #183 round 3). So there is no bookkeeping honesty here
    either, only pane exclusion's absence: this function's "free" is honest
    only as of the instant it was asked, exactly as `claim.sh` says of its
    own assignee check. This estate runs two dispatchers on unrelated
    cadences and has already paid for a duplicate dispatch once (#70); the
    name of this function is not a claim that the gap is closed.
    """
    known = ledger.lane_available(lane)
    if known is not None:
        record = ledger.get_lane(lane)
        return {
            "lane": lane,
            "known": True,
            "free": known,
            "backfilled": False,
            "harness": record["harness"] if record else None,
        }
    if not FREE_WINDOW_NAME_RE.match(window_name):
        return {"lane": lane, "known": False, "free": False, "backfilled": False}
    metadata = transport.metadata(target)
    command = metadata["command"]
    # Narrow inference first (unchanged from before #216: exact binary name,
    # never guessed for an ambiguous one like `node`), THEN the recorded
    # option as the fallback for a command this dict cannot place. Checking
    # both, rather than the option alone, is what keeps every pre-#216 lane
    # (no option ever written for it) dispatchable exactly as before.
    inferred = HARNESS_BY_COMMAND.get(command)
    recorded = transport.get_option(target, HARNESS_OPTION)
    recorded = recorded if recorded in HARNESS_COMMANDS else None
    if inferred and recorded and inferred != recorded:
        return {
            "lane": lane,
            "known": False,
            "free": False,
            "backfilled": False,
            "reason": (
                f"recorded harness {recorded!r} does not match pane command {command!r}"
            ),
        }
    if inferred:
        harness = inferred
    elif recorded and TmuxAdapter._command_matches(recorded, command):
        harness = recorded
    elif recorded:
        return {
            "lane": lane,
            "known": False,
            "free": False,
            "backfilled": False,
            "reason": (
                f"recorded harness {recorded!r} does not match pane command {command!r}"
            ),
        }
    else:
        return {
            "lane": lane,
            "known": False,
            "free": False,
            "backfilled": False,
            "reason": (
                f"cannot tell which harness pane command {command!r} is "
                f"-- no {HARNESS_OPTION} recorded on the pane"
            ),
        }
    # No tmux options are set here (unlike `TmuxAdapter.register_lane`): this
    # mirrors `record_dispatch`'s own choice not to touch tmux beyond reading
    # it (see that function's docstring) -- a real dispatch re-registers this
    # lane with a fresh identity moments later anyway, so nothing here needs
    # to survive past this one query. The harness option itself was already
    # written by whoever recorded it (bootstrap or `register`); this function
    # only ever reads it.
    ledger.register_lane(
        lane=lane,
        pane_id=metadata["pane_id"],
        nonce=secrets.token_hex(16),
        harness=harness,
        repo=metadata["path"],
        server_id=metadata["server_id"],
        session_id=metadata["session_id"],
        command=metadata["command"],
    )
    return {"lane": lane, "known": True, "free": True, "backfilled": True, "harness": harness}


def record_dispatch(
    ledger,
    *,
    lane,
    task,
    summary,
    pane_id,
    pane_path,
    command,
    server_id,
    session_id,
    issues,
    github="",
    harness=None,
    harness_session_id="",
    harness_project_dir="",
    worktree_path="",
    pr=None,
    confirm_landed=False,
):
    """Record a dispatch that ALREADY happened. Writes; never sends.

    This is deliberately not `register` + `assign`, and the difference is the
    whole point of agent-dotfiles#140. Read what those two do before assuming
    this duplicates them:

    * `assign` is not a recorder. `TmuxAdapter.assign_task` classifies the
      pane, refuses unless it reads `idle`, and then SENDS a prompt with
      `send_literal`. Calling it from `dispatch.sh` would type a second,
      competing task prompt into a lane that has just been given its brief --
      one telling the worker to run `hill90-supervisor accept`, a binary that
      is not on PATH (docs/supervisor-disposition.md §1.3). It would also put
      `classify_capture` -- whose approval/blocked matching is a known defect,
      §3 of the same document -- back into the dispatch path that #131 just
      finished taking pane inference OUT of. Routed around on purpose.
    * `Ledger.assign` also refuses any task without a reconstructed, OPEN
      `source_tasks` row, and the only writer of those rows requires a
      `hill90-supervisor:v1` marker in the issue body. Measured across all
      four repos: zero issues carry one (§2.1). So the source row is written
      here instead, from what this dispatch itself just observed -- claim.sh
      confirmed the issue open and assigned it seconds ago -- rather than from
      a marker that does not exist. `source_ref` is the issue number, not the
      commit SHA `GithubTaskSource` would put there; nothing reads either yet.
    * No tmux. The pane identity is passed in, already observed by the caller
      that was talking to tmux anyway. A durable record that cannot be written
      without a live tmux server is not the portability fix #140 asks for, and
      it makes this testable without a transport stub.

    agent-dotfiles#174: `lane_free` now reads this record back to decide
    whether a lane is safe to dispatch to. "Nothing reads any of this yet"
    was true under #140 and is not true anymore -- update this paragraph,
    not just the callers, the next time this changes again. `lanes.sh`
    still classifies panes exactly as it did; only availability/ownership
    authority moved onto what this function writes.

    agent-dotfiles#144 finding 2: this used to make five independent `Ledger`
    calls -- register, reconstruct, assign, mark-pending, mark-delivered --
    each its own lock and transaction. A crash between any two left whatever
    had already committed, including an orphan `lanes` row claiming a lane
    occupied for a dispatch nothing else records. `Ledger.record_dispatch`
    does the same five writes in ONE transaction now; this function's job is
    just shaping `dispatch.sh`'s raw inputs into that call.

    agent-dotfiles#188 finding 1: a failure here used to just raise and let
    the caller (`dispatch.sh`) print a warning. The transaction's own
    rollback is not a safe failure mode by itself -- for a lane the ledger
    already knew as free, rollback restores exactly that free row, and the
    brief is already running in the pane. On ANY failure this now also calls
    `Ledger.mark_lane_held` before re-raising, so the lane reads occupied
    instead of whatever it read before this call, regardless of which of
    the five writes failed or why.

    agent-supervisor#159: `pr` is the PR-scoped sibling of the issue-keyed
    write below -- passed by `dispatch.sh` for a review or a fix pass on PR
    <N> whose underlying issue is deliberately left claimed by the in-flight
    work that opened it. When given, the `source_tasks` row this writes is
    keyed `source_kind='pull'`, `source_ref=str(pr)` instead of by the
    primary issue, so `Ledger.get_open_task_for_pr` can find it -- the same
    one-transaction write, the same `one_open_task_per_lane` uniqueness,
    nothing new. `issues` is still recorded in `evidence` either way: this
    dispatch still names which issue(s) it closes, it is only not claimed as
    the SOURCE of the task.

    agent-supervisor#169: step 0.6's `pr-lane` read (`dispatch.sh`, before a
    lane is even picked) is a TOCTOU by itself -- this write, seconds later,
    is the actual gate. `Ledger._migrate_source_tasks_pull_uniqueness`'s
    `one_open_pull_per_source_ref` trigger (a `BEFORE INSERT` trigger, not a
    plain partial index -- `source_tasks.status` never advances, so a
    same-table index cannot ask the real "is this PR still open" question;
    see that migration's own docstring) makes a second dispatcher's INSERT
    for the same open PR raise `sqlite3.IntegrityError`, atomically, no
    matter how close the two dispatchers' checks landed. When that happens
    for a PR-scoped call, this prints one recognisable `PR-DUPLICATE:` line
    (in addition to the generic `mark_lane_held` handling every other
    failure already gets) naming the lane that actually won, so
    `dispatch.sh` can tell this collision apart from an ordinary ledger
    failure and refuse loud instead of folding it into the silent,
    non-fatal `ledger_record_failed` path every other write failure takes.
    """
    try:
        harness = harness or HARNESS_BY_COMMAND.get(command)
        if harness is None:
            raise RuntimeError(f"cannot tell which harness pane command {command!r} is -- pass --harness")
        primary = issues[0]
        evidence = [f"claimed by dispatch.sh for lane {lane}", f"issues: {','.join(str(i) for i in issues)}"]
        if pr:
            source_kind = "pull"
            source_url = f"https://github.com/{github}/pull/{pr}" if github else f"pull:{pr}@{Path(pane_path).name}"
            source_ref = str(pr)
            evidence.append(f"pr: {pr}")
        else:
            source_kind = "issue"
            source_url = (
                f"https://github.com/{github}/issues/{primary}" if github else f"issue:{primary}@{Path(pane_path).name}"
            )
            source_ref = str(primary)
        return ledger.record_dispatch(
            lane=lane,
            pane_id=pane_id,
            nonce=secrets.token_hex(16),
            harness=harness,
            # The pane's own working directory, which is what
            # `TmuxAdapter._verified_lane` compares this column against. NOT
            # the lane's worktree -- that belongs to the task, and (as of
            # agent-supervisor#117) is passed separately below as
            # `worktree_path` rather than only living inside `summary` text.
            repo=pane_path,
            server_id=server_id,
            session_id=session_id,
            # agent-dotfiles#237: the harness conversation id the dispatcher
            # resolved (`harness-session.sh`), or "" when it could not. Never
            # guessed here -- this function has no way to observe a pane.
            harness_session_id=harness_session_id,
            # agent-supervisor#172: the directory `harness_session_id` was
            # resolved in, never guessed here either -- see the matching
            # argument's docstring above.
            harness_project_dir=harness_project_dir,
            command=command,
            task_id=task,
            source_kind=source_kind,
            source_url=source_url,
            source_ref=source_ref,
            summary=summary,
            source_state="OPEN",
            evidence=evidence,
            status_marker=None,
            worktree_path=worktree_path,
            accepted=confirm_landed,
        )
    except Exception as error:
        ledger.mark_lane_held(lane, note=f"record_dispatch failed for task {task}: {error}")
        # agent-supervisor#169: the write-time PR-duplicate collision is a
        # distinct, expected failure -- not a generic ledger error -- and the
        # caller (`dispatch.sh`) needs to be able to tell them apart to
        # refuse loud rather than fold this into the silent non-fatal path
        # every other record_dispatch failure takes. Detected by the index
        # name rather than a generic IntegrityError check, so an unrelated
        # constraint violation (a real bug) still surfaces as an ordinary,
        # unrecognised failure instead of being misreported as this one.
        if pr and isinstance(error, sqlite3.IntegrityError) and "source_tasks.source_ref" in str(error):
            holder = ledger.get_open_task_for_pr(pr)
            holder_lane = holder["lane"] if holder else "unknown"
            holder_task = holder["id"] if holder else "unknown"
            print(
                f"PR-DUPLICATE: PR #{pr} is already claimed by lane {holder_lane} (task {holder_task}) "
                f"-- the write refused this second open source_tasks row; lane {lane} (task {task}) "
                "is marked HELD",
                file=sys.stderr,
            )
        raise


def record_completion(ledger, *, task, lane, note):
    """Record that a dispatched task finished. Writes; never sends.

    Not `cli.py complete`: that path verifies `TMUX_PANE` belongs to the
    lane's own pane and takes a `--result-file`. `lane-done.sh` runs in the
    supervisor's pane, not the worker's, and holds no result artifact -- the
    only thing it knows is that the worker's `wait-for` channel fired for a
    window still carrying the expected name. It cannot know the window was
    renamed: since agent-dotfiles#194 this release runs BEFORE the rename and
    unconditionally, and the rename is cosmetic. So it authenticates with the
    task's own recorded `pane_nonce` and records that fact as the result.

    Note the one thing this does that is not inert: `Ledger.complete` inserts
    a `completion:<task>` event, and `cli.py notify`/`tick` would send those
    to the supervisor lane. Neither can fire from this wiring -- both go
    through `_verified_lane`, which requires a supervisor lane registered
    with matching tmux options, and nothing here registers one.

    agent-supervisor#36 (second issue comment): `--task` used to be the only
    way in, and `get_task` is an exact id match -- so a `ledger-claim:<lane>:
    <token>` row (how the codex harness's completions land) could not be
    recorded honestly by an operator who only had the bare token, and
    `cancel-open-task` was the only verb that worked, which records the wrong
    terminal outcome instead of completing it. Resolution order: an exact `--task` id match
    first (unchanged behaviour for an ordinary task); failing that, with
    `--lane` also given, the claim row that token would produce under that
    lane; failing that, with only `--lane` given, whichever single row is
    still open for it -- mirroring `cancel_open_task`'s own lookup, so the
    caller does not have to know the row's shape before asking to close it.
    """
    if not task and not lane:
        raise RuntimeError("record-completion requires --task or --lane")
    row = ledger.get_task(task) if task else None
    if row is None and lane and task:
        row = ledger.get_task(f"{CLAIM_TASK_PREFIX}{lane}:{task}")
    if row is None and lane and not task:
        row = ledger.get_open_task_for_lane(lane)
    if row is None:
        identity = f"task: {task}" if task else f"lane: {lane}"
        raise RuntimeError(f"unknown {identity}")
    allow_claim = row["id"].startswith(CLAIM_TASK_PREFIX)
    return ledger.complete(row["id"], note.encode("utf-8"), pane_nonce=row["pane_nonce"], allow_claim=allow_claim)


# as#132: the ledger's registered lane id can still be the pre-migration
# "architecture", the post-migration "supervisor", or whatever the caller
# passed via --supervisor-lane/--architecture-lane -- any of those must be
# recognised as THE supervisor lane so a lane-exclusion check does not fail
# open (agent-supervisor#132, cli.py's `observe` filter used to hardcode the
# literal "architecture" and silently stopped excluding anything once the
# flag's default moved). Drop the "architecture" alias once every estate has
# migrated its ledger rows and callers to "supervisor".
_SUPERVISOR_LANE_ALIASES = frozenset({"architecture", "supervisor"})


def _is_supervisor_lane(lane_id, configured):
    return lane_id == configured or lane_id in _SUPERVISOR_LANE_ALIASES


def _verify_caller(adapter, ledger, lane):
    record = adapter._verified_lane(lane)
    caller = os.environ.get("TMUX_PANE")
    if caller and caller != record["pane_id"]:
        raise RuntimeError(f"caller pane {caller} does not own lane {lane}")
    return record


def main(argv=None):
    args = parser().parse_args(argv)
    # agent-supervisor#108: answered BEFORE any ledger is opened. Comparing two
    # lane ids reads no row and writes none, and a comparison that needed a
    # readable database would make the author-exclusion guard fail on a state
    # directory it never had to touch -- including in a test harness, or on a
    # host where the default state dir is another estate's live ledger.
    if args.command == "lane-relation":
        _print({
            "lane": args.lane,
            "other": args.other,
            "relation": lane_relation(args.lane, args.other),
        })
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
    elif args.command == "cancel-open-task":
        value = {"lane": args.lane, "cancelled": ledger.cancel_open_task(args.lane)}
    elif args.command == "task-lane":
        row = ledger.get_task(args.task)
        value = {"task": args.task, "known": row is not None, "lane": row["lane"] if row is not None else None}
    elif args.command == "issue-lane":
        row = ledger.get_task_for_issue(args.issue)
        value = {
            "issue": args.issue,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
        }
    elif args.command == "pr-lane":
        row = ledger.get_open_task_for_pr(args.pr)
        value = {
            "pr": args.pr,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
        }
    elif args.command == "author-issue-lane":
        row = ledger.get_author_task_for_issue(args.issue, head_ref=args.head_ref)
        value = {
            "issue": args.issue,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
        }
    elif args.command == "contributor-issue-lanes":
        rows = ledger.get_contributor_tasks_for_issue(args.issue)
        value = {
            "issue": args.issue,
            "known": len(rows) > 0,
            "contributors": [{"lane": row["lane"], "task": row["id"]} for row in rows],
        }
    elif args.command == "worktree-lane":
        row = ledger.get_task_for_worktree(args.path)
        value = {
            "path": args.path,
            "known": row is not None,
            "lane": row["lane"] if row is not None else None,
            "task": row["id"] if row is not None else None,
        }
    elif args.command == "record-completion":
        value = record_completion(ledger, task=args.task, lane=args.lane, note=args.note)
    elif args.command == "accept":
        task = ledger.get_task(args.task)
        if task is None:
            raise ValueError("unknown task")
        lane = ledger.get_lane(task["lane"])
        _verify_caller(adapter, ledger, task["lane"])
        value = ledger.accept(args.task, pane_nonce=lane["nonce"])
    elif args.command == "complete":
        task = ledger.get_task(args.task)
        if task is None:
            raise ValueError("unknown task")
        _verify_caller(adapter, ledger, task["lane"])
        value = ledger.complete(args.task, args.result_file.read_bytes(), pane_nonce=task["pane_nonce"])
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
    elif args.command == "reconcile-lane-completions":
        lanes_bin = os.environ.get("AGENT_LANES_BIN", str(Path(__file__).resolve().parent / "lanes.sh"))
        value = LaneCompletionReconciler(
            ledger, lanes_bin=lanes_bin, idle_after=args.idle_after
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
        )
    elif args.command == "restore-plan":
        value = ledger.restore_plan()
    elif args.command == "delivered-open":
        value = {"tasks": ledger.list_delivered_open_tasks()}
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
    else:
        raise AssertionError(args.command)
    _print(value)
    return 0


# Without this, the module is unreachable as a program: `cli.py --help` printed
# nothing and exited 0, which reads as success to any wrapper checking $?. The
# import-based tests all passed throughout, because they call main() directly.
if __name__ == "__main__":
    sys.exit(main())
