"""Command line interface for the portable supervisor ledger."""

from __future__ import annotations

import argparse
import json
import os
import secrets
import sys
from pathlib import Path

from acp_transport import ACPTransport
from adapter import ACPAdapter, TmuxAdapter
from core import Ledger
from github_source import GithubTaskSource
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
DEFAULT_REPOSITORIES = (
    {"name": "agent-dotfiles", "path": "/Users/jon/source/repos/Personal/agent-dotfiles", "github": "jonhill90/agent-dotfiles"},
    {"name": "skills", "path": "/Users/jon/source/repos/Personal/Skills", "github": "jonhill90/skills"},
    {"name": "skills-private", "path": "/Users/jon/source/repos/Personal/skills-private", "github": "jonhill90/skills-private"},
    {"name": "agent-evals", "path": "/Users/jon/source/repos/Personal/agent-evals", "github": "jonhill90/agent-evals"},
)


def parser():
    root = argparse.ArgumentParser()
    root.add_argument("--state-dir", type=Path, default=DEFAULT_STATE)
    root.add_argument("--tmux-bin", default=os.environ.get("AGENT_TMUX_BIN", "tmux"))
    sub = root.add_subparsers(dest="command", required=True)

    register = sub.add_parser("register")
    register.add_argument("--lane", required=True)
    register.add_argument("--target", required=True)
    register.add_argument("--harness", choices=("codex", "claude", "copilot-acp"), required=True)
    register.add_argument("--repo", required=True)
    register.add_argument("--nonce")

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
    record_dispatch_parser.add_argument("--issue", action="append", required=True)
    record_dispatch_parser.add_argument("--github", default="")
    record_dispatch_parser.add_argument("--harness", choices=("codex", "claude", "copilot-acp"))

    record_completion_parser = sub.add_parser("record-completion")
    record_completion_parser.add_argument("--task", required=True)
    record_completion_parser.add_argument("--note", required=True)

    for name in ("accept", "complete"):
        command = sub.add_parser(name)
        command.add_argument("--task", required=True)
        if name == "complete":
            command.add_argument("--result-file", type=Path, required=True)

    reconcile = sub.add_parser("reconcile")
    reconcile.add_argument("--task", required=True)
    reconcile.add_argument("--outcome", choices=("delivered", "failed"), required=True)

    observe = sub.add_parser("observe")
    observe.add_argument("--lane", action="append")

    notify = sub.add_parser("notify")
    notify.add_argument("--architecture-lane", default="architecture")
    notify.add_argument("--retry-after", type=int, default=900)

    tick = sub.add_parser("tick")
    tick.add_argument("--architecture-lane", default="architecture")
    tick.add_argument("--retry-after", type=int, default=900)
    tick.add_argument("--no-sensors", action="store_true")
    tick.add_argument("--sensor-timeout", type=int, default=30)

    sub.add_parser("sensor")

    events = sub.add_parser("events")
    events.add_argument("--due", action="store_true")

    ack = sub.add_parser("ack")
    ack.add_argument("--event", action="append", required=True)
    ack.add_argument("--architecture-lane", default="architecture")

    reconstruct = sub.add_parser("reconstruct")
    reconstruct.add_argument("--source-url", required=True)
    reconstruct.add_argument("--source-ref", required=True)

    sub.add_parser("status")
    return root


def _print(value):
    print(json.dumps(value, sort_keys=True, separators=(",", ":")))


HARNESS_BY_COMMAND = {"codex": "codex", "claude": "claude", "claude.exe": "claude"}


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

    Nothing reads any of this yet. That is the safety property, not an
    oversight: `lanes.sh` still classifies panes exactly as it did.

    agent-dotfiles#144 finding 2: this used to make five independent `Ledger`
    calls -- register, reconstruct, assign, mark-pending, mark-delivered --
    each its own lock and transaction. A crash between any two left whatever
    had already committed, including an orphan `lanes` row claiming a lane
    occupied for a dispatch nothing else records. `Ledger.record_dispatch`
    does the same five writes in ONE transaction now; this function's job is
    just shaping `dispatch.sh`'s raw inputs into that call.
    """
    harness = harness or HARNESS_BY_COMMAND.get(command)
    if harness is None:
        raise RuntimeError(f"cannot tell which harness pane command {command!r} is -- pass --harness")
    primary = issues[0]
    source_url = (
        f"https://github.com/{github}/issues/{primary}" if github else f"issue:{primary}@{Path(pane_path).name}"
    )
    return ledger.record_dispatch(
        lane=lane,
        pane_id=pane_id,
        nonce=secrets.token_hex(16),
        harness=harness,
        # The pane's own working directory, which is what
        # `TmuxAdapter._verified_lane` compares this column against. NOT the
        # lane's worktree -- that belongs to the task and is carried in its
        # summary, because the tasks table has no column for it.
        repo=pane_path,
        server_id=server_id,
        session_id=session_id,
        command=command,
        task_id=task,
        source_kind="issue",
        source_url=source_url,
        source_ref=str(primary),
        summary=summary,
        source_state="OPEN",
        evidence=[f"claimed by dispatch.sh for lane {lane}", f"issues: {','.join(str(i) for i in issues)}"],
        status_marker=None,
    )


def record_completion(ledger, *, task, note):
    """Record that a dispatched task finished. Writes; never sends.

    Not `cli.py complete`: that path verifies `TMUX_PANE` belongs to the
    lane's own pane and takes a `--result-file`. `lane-done.sh` runs in the
    supervisor's pane, not the worker's, and holds no result artifact -- the
    only thing it knows is that the worker's `wait-for` channel fired and the
    window was renamed. So it authenticates with the task's own recorded
    `pane_nonce` and records that fact as the result.

    Note the one thing this does that is not inert: `Ledger.complete` inserts
    a `completion:<task>` event, and `cli.py notify`/`tick` would send those
    to the architecture lane. Neither can fire from this wiring -- both go
    through `_verified_lane`, which requires an architecture lane registered
    with matching tmux options, and nothing here registers one.
    """
    row = ledger.get_task(task)
    if row is None:
        raise RuntimeError(f"unknown task: {task}")
    return ledger.complete(task, note.encode("utf-8"), pane_nonce=row["pane_nonce"])


def _verify_caller(adapter, ledger, lane):
    record = adapter._verified_lane(lane)
    caller = os.environ.get("TMUX_PANE")
    if caller and caller != record["pane_id"]:
        raise RuntimeError(f"caller pane {caller} does not own lane {lane}")
    return record


def main(argv=None):
    args = parser().parse_args(argv)
    ledger = Ledger(args.state_dir)
    # tmux stays the default transport for every existing lane (codex,
    # claude) and is never replaced -- Jon requires the persistent, watchable
    # terminals it gives him. ACP is opt-in per lane, selected by the lane's
    # registered harness: only harness=copilot-acp dispatches through
    # ACPTransport (SPEC §15.2 -- Copilot is the only harness that ships an
    # ACP server today).
    adapter = TmuxAdapter(ledger, TmuxTransport(args.tmux_bin))
    acp_adapter = ACPAdapter(ledger, ACPTransport.spawn)

    def adapter_for_harness(harness):
        return acp_adapter if harness == "copilot-acp" else adapter

    def adapter_for_lane(lane):
        record = ledger.get_lane(lane)
        if record is None:
            raise ValueError(f"unknown lane: {lane}")
        return adapter_for_harness(record["harness"])

    if args.command == "register":
        value = adapter_for_harness(args.harness).register_lane(
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
            issues=args.issue,
            github=args.github,
            harness=args.harness,
        )
    elif args.command == "record-completion":
        value = record_completion(ledger, task=args.task, note=args.note)
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
    elif args.command == "observe":
        lanes = args.lane or [item["lane"] for item in ledger.list_lanes() if item["lane"] != "architecture"]
        value = [
            event for lane in lanes if (event := adapter_for_lane(lane).observe_lane(lane)) is not None
        ]
    elif args.command == "notify":
        value = {"notified": adapter.notify_architecture(lane=args.architecture_lane, retry_after=args.retry_after)}
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
                    if lane["lane"] == args.architecture_lane:
                        continue
                    try:
                        event = adapter_for_harness(lane["harness"]).observe_lane(lane["lane"])
                        if event is not None:
                            observations.append(event["key"])
                        ledger.record_component(f"lane:{lane['lane']}", snapshot=b"reachable", healthy=True)
                    except Exception as error:  # a bad worker lane must not blind the others
                        ledger.record_component(f"lane:{lane['lane']}", healthy=False, error=str(error))
                        errors.append({"lane": lane["lane"], "error": str(error)})
                try:
                    notified = adapter.notify_architecture(lane=args.architecture_lane, retry_after=args.retry_after)
                    ledger.record_component("architecture", snapshot=b"reachable", healthy=True)
                except Exception as error:
                    ledger.record_component("architecture", healthy=False, error=str(error))
                    errors.append({"lane": args.architecture_lane, "error": str(error)})
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
        _verify_caller(adapter, ledger, args.architecture_lane)
        ledger.ack(args.event)
        value = {"acked": args.event}
    elif args.command == "reconstruct":
        value = GithubTaskSource().reconstruct(
            ledger, source_url=args.source_url, source_ref=args.source_ref
        )
    elif args.command == "status":
        value = {
            "lanes": ledger.list_lanes(),
            "source_tasks": ledger.list_source_tasks(),
            "tasks": ledger.list_tasks(),
            "events": ledger.list_events(),
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
