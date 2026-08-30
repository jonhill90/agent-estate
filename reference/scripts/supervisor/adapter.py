"""Harness-specific tmux adapters for the portable supervisor ledger."""

from __future__ import annotations

import re
import time
import uuid
from pathlib import Path


# agent-supervisor#362: every lane prompt used to tell the worker to run
# `hill90-supervisor accept|complete`. That shim is Hill90's launchd adapter
# and STAYED BEHIND in that repository during the split -- scripts/supervisor/
# README.md:13 says so, and README.md:139 gives the portable form: invoke the
# subcommands directly against `cli.py`. cli.py's own docstring already noted
# the shim is "a binary that is not on PATH (docs/supervisor-disposition.md
# §1.3)".
#
# The consequence was not cosmetic. A lane did its work, then BOTH of its
# reporting calls failed `command not found`, so the task sat at `delivered`
# with `completed_at` null forever and its claim was never released. That is
# the source of the stale-claim floods (#359), the phantom in-flight tasks
# (21 `delivered` rows against zero running processes), and lanes that look
# like they produced nothing.
#
# An absolute path, not a bare name: a lane's cwd is its own worktree, so a
# relative path does not resolve, and re-adding a `hill90-supervisor` shim
# here would re-couple the two repositories the split deliberately separated.
SUPERVISOR_CLI = Path(__file__).resolve().parent / "cli.py"

BLOCKED_RE = re.compile(r"hit your (?:weekly |usage )?limit|usage limit", re.IGNORECASE)
APPROVAL_RE = re.compile(r"\[Y/n\]|\(y/N\)|(?:Allow|Approve|Continue|Proceed).*[?]", re.IGNORECASE)
CODEX_ACTIVE_RE = re.compile(r"^[•◦]\s+(?:Working|Running|Thinking)(?:\s|\()", re.MULTILINE)
CLAUDE_ACTIVE_RE = re.compile(
    r"^✻\s+(?!(?:Crunched|Cooked|Worked|Brewed)\b)(?:Thinking|Churning|Working|Running|[A-Za-z]+ing)(?:…|\.{3}|\s|$)",
    re.MULTILINE,
)


# Harness identity, once known, is a RECORDED fact -- written down at the one
# place it is actually decided: `bootstrap-session.sh` (the operator names
# the harness a lane's window starts with), `cli.py register` (an operator
# names it by hand), or `lane_free`'s own first-sight backfill of a pane
# option one of those two already set. It is never solely inferred here
# (agent-dotfiles#216).
#
# This table is the PLAUSIBILITY CHECK `_command_matches` runs against a
# harness already on record; it does not decide what a lane IS. Every
# Node-based harness's process reads "node" in `#{pane_current_command}` --
# confirmed live for both copilot and codex (#216's own measurement, window 8
# ran codex under `node`) -- so a bare command name cannot tell them apart
# from each other, and the table says so by listing "node" for both. What it
# still catches: a lane recorded "claude" whose pane is running anything
# other than the claude binary -- exactly the case #216's mutation-check
# asks for (register the wrong harness, confirm something refuses).
HARNESS_COMMANDS = {
    "claude": ("claude", "claude.exe"),
    "codex": ("codex", "codex.exe", "node"),
    "copilot": ("copilot", "copilot.exe", "node"),
    "pi": ("pi", "pi.exe"),
}


# Matches on a quoted phrase anywhere in a 25-line capture window, the same
# wide-window anti-pattern #65 fixed lanes.sh to avoid (docs/supervisor-
# disposition.md §3). Measured there: a healthy lane quoting "Should I
# proceed?" or a PR body's own text reads back as approval/blocked.
#
# It has real callers, right here: TmuxAdapter.assign_task, .observe_lane,
# and .notify_supervisor -- the last of which calls it twice, once to
# check the pane is idle before sending and once after to confirm the send
# landed. What routes around it is one level up -- dispatch.sh's dispatch
# path (via cli.py's `record_dispatch` command) deliberately calls the
# ledger's `dispatch` recorder instead of `assign_task`, precisely to keep
# this function's defect out of the dispatch flow. See `record_dispatch`'s
# own docstring in cli.py, which names #131 as the change that took pane
# inference OUT of that path. "Routed around on purpose."
#
# That makes this the opposite of the nothing-calls-it defect dispatch.sh's
# header describes: the implementation is unfit to call, not merely uncalled.
# See dispatch.sh's header for the refined test this instance motivated.
# Do not wire a new caller through here without first rebuilding this to
# lanes.sh's standard (docs/supervisor-disposition.md:359, agent-dotfiles#222).
#
# agent-dotfiles#215 is the first issue to take that instruction as written.
# It reported watchdog.sh's Claude-only busy literal as "the general solution
# exists and nothing calls it", and pointed here. watchdog.sh's probe was
# rebuilt per-harness and fail-closed WITHOUT routing through this function --
# deliberately, and recorded at both ends: this function's 25-line quoted-
# phrase window is the #65 anti-pattern the watchdog must not inherit, and it
# raises on any harness other than claude/codex, which inside a bash `if` is a
# non-zero exit, i.e. "not busy" -- reintroducing the exact failure direction
# #215 exists to remove. It reads the same harness/*.sh adapters lanes.sh
# reads, via harness-registry.sh. See watchdog.sh's own #215 section.
def classify_capture(harness, capture):
    tail = "\n".join(capture.splitlines()[-25:])
    if BLOCKED_RE.search(tail):
        return "blocked"
    if APPROVAL_RE.search(tail):
        return "approval"
    if harness == "codex":
        if CODEX_ACTIVE_RE.search(tail):
            return "active"
        if any(line.startswith("›") for line in tail.splitlines()[-5:]):
            return "idle"
    elif harness == "claude":
        if CLAUDE_ACTIVE_RE.search(tail):
            return "active"
        if any(line.lstrip().startswith("❯") for line in tail.splitlines()[-5:]):
            return "idle"
    else:
        raise ValueError(f"unsupported harness: {harness}")
    return "unknown"


class TmuxAdapter:
    NONCE_OPTION = "@hill90_lane_nonce"
    LANE_OPTION = "@hill90_lane"
    # agent-dotfiles#216: the record `lane_free`'s backfill (cli.py) and this
    # class's own `register_lane` both read authority from, instead of
    # guessing it back out of the pane's process name every time.
    HARNESS_OPTION = "@hill90_lane_harness"

    def __init__(self, ledger, transport, *, clock=None):
        self.ledger = ledger
        self.transport = transport
        self.clock = clock or time.time

    @staticmethod
    def _command_matches(harness, command):
        """Plausibility check, NOT identity (agent-dotfiles#216). `harness`
        must already be a recorded fact by the time this is called; this
        only refuses a recording that the live pane visibly contradicts."""
        return command in HARNESS_COMMANDS.get(harness, ())

    def register_lane(self, *, lane, target, harness, repo, nonce):
        metadata = self.transport.metadata(target)
        if metadata["path"] != repo:
            raise RuntimeError(f"lane repo mismatch: {metadata['path']}")
        if not self._command_matches(harness, metadata["command"]):
            raise RuntimeError(f"lane harness mismatch: {metadata['command']}")
        self.transport.set_option(target, self.NONCE_OPTION, nonce)
        self.transport.set_option(target, self.LANE_OPTION, lane)
        self.transport.set_option(target, self.HARNESS_OPTION, harness)
        return self.ledger.register_lane(
            lane=lane,
            pane_id=metadata["pane_id"],
            nonce=nonce,
            harness=harness,
            repo=repo,
            server_id=metadata["server_id"],
            session_id=metadata["session_id"],
            command=metadata["command"],
            # Recorded explicitly rather than left to `register_lane`'s
            # per-harness default (agent-supervisor#58): this class IS the
            # send-keys transport by construction, so its lanes must never
            # read as unlabelled even if the default ever changes.
            transport="send-keys",
        )

    def _verified_lane(self, lane):
        record = self.ledger.get_lane(lane)
        if record is None:
            raise RuntimeError(f"unknown lane: {lane}")
        metadata = self.transport.metadata(record["pane_id"])
        identity = (
            metadata["pane_id"] == record["pane_id"]
            and metadata["path"] == record["repo"]
            and metadata["server_id"] == record["server_id"]
            and metadata["session_id"] == record["session_id"]
            and self._command_matches(record["harness"], metadata["command"])
            and self.transport.get_option(record["pane_id"], self.NONCE_OPTION) == record["nonce"]
            and self.transport.get_option(record["pane_id"], self.LANE_OPTION) == lane
        )
        if not identity:
            raise RuntimeError("pane incarnation does not match registered lane")
        return record

    def assign_task(self, *, lane, task_id, summary):
        with self.ledger.operation_lock():
            record = self._verified_lane(lane)
            existing = self.ledger.get_task(task_id)
            if existing is not None and existing["status"] == "delivery_pending":
                raise RuntimeError(
                    f"delivery already attempted for task {task_id} and is unconfirmed; "
                    "reconcile the task before it can be assigned again"
                )
            state = classify_capture(record["harness"], self.transport.capture(record["pane_id"], lines=25))
            if state != "idle":
                raise RuntimeError(f"lane is {state}; assignment not sent")

            # agent-supervisor#611: `worktree_path` was left at `assign`'s
            # own default ("") on every path that reaches `Ledger.assign`
            # through an adapter's own `assign_task` rather than through
            # `dispatch.sh`'s `record-dispatch` (the only OTHER writer of
            # this column -- see `record_dispatch`'s call into
            # `Ledger._assign_tx`). `record["repo"]` is the worktree: every
            # `register_lane` above requires it (`TmuxAdapter.register_lane`
            # checks `metadata["path"] != repo`; the headless adapters pass
            # the worktree `worktree.sh` just built straight through as
            # `repo`), so it is already sitting on the lane record this
            # call just verified -- not guessed, not re-derived, the exact
            # value `register_lane` recorded for THIS lane.
            task = self.ledger.assign(
                task_id=task_id,
                lane=lane,
                pane_nonce=record["nonce"],
                summary=summary,
                worktree_path=record["repo"],
            )
            incoming = self.ledger.root / "incoming"
            incoming.mkdir(mode=0o700, exist_ok=True)
            result_file = incoming / f"{task_id}.md"
            prompt = (
                f"[Hill90 task {task_id}] {summary}\n\n"
                "Do not begin unrelated work. Record commands and actual outputs in a compact result. "
                f"Before working run: python3 {SUPERVISOR_CLI} accept --task {task_id}. "
                f"At completion write {result_file} and run: "
                f"python3 {SUPERVISOR_CLI} complete --task {task_id} --result-file {result_file}"
            )
            # Persist the ambiguous, non-resendable state before the physical
            # send. If send_literal raises, or the ledger write below fails,
            # the task is left here rather than silently eligible for retry -
            # see assign_task's guard above and mark_delivered's own allowed
            # source state. Nothing after this point trusts echoed pane text
            # to decide whether the send actually reached the harness.
            self.ledger.mark_delivery_pending(task_id, pane_nonce=record["nonce"])
            self.transport.send_literal(record["pane_id"], prompt)
            return self.ledger.mark_delivered(task_id, pane_nonce=record["nonce"])

    def observe_lane(self, lane):
        record = self._verified_lane(lane)
        state = classify_capture(record["harness"], self.transport.capture(record["pane_id"], lines=25))
        if state == "active":
            return None
        return self.ledger.observe_attention(lane, pane_nonce=record["nonce"], reason=state)

    def notify_supervisor(self, *, lane, retry_after):
        with self.ledger.operation_lock():
            record = self._verified_lane(lane)
            pre_state = classify_capture(record["harness"], self.transport.capture(record["pane_id"], lines=25))
            if pre_state != "idle":
                return False
            events = self.ledger.events_due()
            if not events:
                return False
            event_lines = []
            for event in events:
                detail = event["key"]
                if event["payload_path"]:
                    detail += f" result={event['payload_path']}"
                event_lines.append(detail)
            keys = [event["key"] for event in events]
            prompt = (
                "Hill90 supervisor events:\n- "
                + "\n- ".join(event_lines)
                + "\nRead only these event artifacts, verify evidence, continue bounded work, then acknowledge with: "
                # agent-supervisor#362, found by the independent review of
                # #381: the FOURTH live call site of the missing shim, and the
                # one that matters most here. `ACPAdapter`, `PiRPCAdapter` and
                # `ClaudePrintAdapter` all short-circuit `notify_supervisor`
                # to `return False` (SPEC §15.2 -- only tmux-hosted lanes
                # carry the standing supervisor lane), so THIS is the only
                # implementation that ever sends the prompt for real. `ack` is
                # a real cli.py subcommand, so this is live code, not a stale
                # doc string: every notified lane was being told to run a
                # binary that is not on PATH, exactly as accept/complete were.
                + f"python3 {SUPERVISOR_CLI} ack --event "
                + " --event ".join(keys)
            )
            self.transport.send_literal(record["pane_id"], prompt)
            post_state = classify_capture(record["harness"], self.transport.capture(record["pane_id"], lines=25))
            if post_state != "active":
                return False
            self.ledger.mark_notified(keys, retry_after=retry_after)
            return True


class ACPAdapter:
    """ACP-driven sibling to `TmuxAdapter` for the single `copilot-acp` harness.

    Per SPEC §15.1 the core owns the ledger and lifecycle; this class only
    owns "deliver this prompt to this lane and report what came back." Where
    `TmuxAdapter` scrapes pane scrollback to guess a lane's state and relies
    on the agent to self-report completion via a separate `complete` CLI
    call, ACP's `session/prompt` already blocks until the agent reaches a
    structured `stopReason` -- so assignment and completion happen in one
    round trip here, and there is no pane to classify as idle/active between
    calls (`observe_lane` is a deliberate no-op).

    The CLI dispatches one process per command, so there is no long-lived
    transport to hold onto between calls the way `TmuxAdapter` holds a
    `TmuxTransport` for the process lifetime. `transport_factory` is called
    fresh for every operation (production wiring: `ACPTransport.spawn`) and
    the resulting transport is always closed before returning -- a later
    call resumes the same agent-side session via `ACPTransport.load_session`
    using the session id stored as the lane's `pane_id` at registration.
    """

    def __init__(self, ledger, transport_factory, *, clock=None):
        self.ledger = ledger
        self.transport_factory = transport_factory
        self.clock = clock or time.time

    def register_lane(self, *, lane, target, harness, repo, nonce):
        if harness != "copilot-acp":
            raise RuntimeError(f"ACPAdapter only supports copilot-acp lanes, got {harness!r}")
        transport = self.transport_factory()
        try:
            transport.initialize()
            session_id = transport.new_session(repo)
        finally:
            transport.close()
        return self.ledger.register_lane(
            lane=lane,
            pane_id=session_id,
            nonce=nonce,
            harness=harness,
            repo=repo,
            server_id="acp",
            session_id=session_id,
            command="copilot",
            # Explicit for the same reason as `TmuxAdapter.register_lane`:
            # this class IS the ACP transport by construction, so its own
            # lanes must never rely on `register_lane`'s default to say so.
            transport="acp",
        )

    def _verified_lane(self, lane):
        record = self.ledger.get_lane(lane)
        if record is None:
            raise RuntimeError(f"unknown lane: {lane}")
        if record["harness"] != "copilot-acp":
            raise RuntimeError(f"lane {lane} is not an ACP lane: {record['harness']}")
        return record

    def assign_task(self, *, lane, task_id, summary):
        with self.ledger.operation_lock():
            record = self._verified_lane(lane)
            existing = self.ledger.get_task(task_id)
            if existing is not None and existing["status"] == "delivery_pending":
                raise RuntimeError(
                    f"delivery already attempted for task {task_id} and is unconfirmed; "
                    "reconcile the task before it can be assigned again"
                )
            # agent-supervisor#611: see the matching comment on
            # `TmuxAdapter.assign_task` -- `record["repo"]` is the worktree
            # `register_lane` recorded for this lane, not a guess.
            self.ledger.assign(
                task_id=task_id,
                lane=lane,
                pane_nonce=record["nonce"],
                summary=summary,
                worktree_path=record["repo"],
            )
            prompt = (
                f"[Hill90 task {task_id}] {summary}\n\n"
                "Do not begin unrelated work. Record commands and actual outputs in a compact result."
            )
            # Same ambiguous-state-before-physical-send ordering as
            # TmuxAdapter.assign_task: if the resumed prompt raises, the task
            # is left `delivery_pending` rather than silently eligible for
            # an automatic resend.
            self.ledger.mark_delivery_pending(task_id, pane_nonce=record["nonce"])
            transport = self.transport_factory()
            try:
                transport.initialize()
                transport.load_session(record["session_id"], cwd=record["repo"])
                result = transport.send_literal(record["session_id"], prompt)
            finally:
                transport.close()
            self.ledger.mark_delivered(task_id, pane_nonce=record["nonce"])
            message = (result.get("message") or "").strip()
            if not message:
                message = f"ACP stopReason={result.get('stop_reason')}"
            return self.ledger.complete(task_id, message.encode("utf-8"), pane_nonce=record["nonce"])

    def observe_lane(self, lane):
        """Always None: a prompt either already returned (and completed the
        task inline in `assign_task`) or the call is still in flight -- there
        is no pane to poll between the two."""
        self._verified_lane(lane)
        return None

    def notify_supervisor(self, *, lane, retry_after):
        """copilot-acp workers never host the supervisor lane -- only
        codex/claude do (SPEC §15.2) -- so there is nothing for this adapter
        to notify."""
        return False


class PiRPCAdapter:
    """pi-RPC-driven sibling to `ACPAdapter`, for `pi` lanes registered with
    `transport='pi-rpc'` (agent-supervisor#58). `pi` is the one harness that
    may be driven either way -- plain `send-keys` via `TmuxAdapter`, same as
    every other harness, or its own documented RPC here -- so unlike
    `ACPAdapter` this class does not own the harness outright; `cli.py`
    chooses between the two by the lane's recorded `transport`, not its
    `harness` alone.

    Shares ACPAdapter's shape for the reason `pi_transport.py`'s module
    docstring gives: pi's `prompt` command only acks acceptance, so
    `PiRPCTransport.send_literal` itself waits out the async
    agent_start -> ... -> agent_settled stream before returning -- from this
    adapter's perspective that is the same "one blocking call, get back a
    structured result" surface ACP's synchronous `session/prompt` gives
    `ACPAdapter`. Assignment and completion happen in one round trip here for
    the same reason: there is no pane to classify as idle/active in between.

    The CLI dispatches one process per command, so, exactly like ACPAdapter,
    there is no long-lived transport to hold onto between calls.
    `transport_factory` is called fresh for every operation (production
    wiring: `PiRPCTransport.spawn`) with `cwd` and, to resume a session a
    prior process already started, `session=<recorded session id>` -- and the
    resulting transport is always terminated before returning.
    """

    def __init__(self, ledger, transport_factory, *, clock=None):
        self.ledger = ledger
        self.transport_factory = transport_factory
        self.clock = clock or time.time

    def register_lane(self, *, lane, target, harness, repo, nonce):
        if harness != "pi":
            raise RuntimeError(f"PiRPCAdapter only supports pi lanes, got {harness!r}")
        transport = self.transport_factory(cwd=repo)
        try:
            state = transport.get_state()
            session_id = state.get("sessionId")
            if not session_id:
                raise RuntimeError(f"pi RPC get_state returned no sessionId: {state!r}")
        finally:
            transport.terminate()
        return self.ledger.register_lane(
            lane=lane,
            pane_id=session_id,
            nonce=nonce,
            harness=harness,
            repo=repo,
            server_id="pi-rpc",
            session_id=session_id,
            command="pi",
            # Explicit, same reasoning as TmuxAdapter/ACPAdapter: this class
            # IS the pi-rpc transport by construction.
            transport="pi-rpc",
        )

    def _verified_lane(self, lane):
        record = self.ledger.get_lane(lane)
        if record is None:
            raise RuntimeError(f"unknown lane: {lane}")
        if record["harness"] != "pi" or record["transport"] != "pi-rpc":
            raise RuntimeError(
                f"lane {lane} is not a pi-rpc lane: harness={record['harness']!r} "
                f"transport={record.get('transport')!r}"
            )
        return record

    def assign_task(self, *, lane, task_id, summary):
        with self.ledger.operation_lock():
            record = self._verified_lane(lane)
            existing = self.ledger.get_task(task_id)
            if existing is not None and existing["status"] == "delivery_pending":
                raise RuntimeError(
                    f"delivery already attempted for task {task_id} and is unconfirmed; "
                    "reconcile the task before it can be assigned again"
                )
            # agent-supervisor#611: see the matching comment on
            # `TmuxAdapter.assign_task` -- `record["repo"]` is the worktree
            # `register_lane` recorded for this lane, not a guess.
            self.ledger.assign(
                task_id=task_id,
                lane=lane,
                pane_nonce=record["nonce"],
                summary=summary,
                worktree_path=record["repo"],
            )
            prompt = (
                f"[Hill90 task {task_id}] {summary}\n\n"
                "Do not begin unrelated work. Record commands and actual outputs in a compact result."
            )
            # Same ambiguous-state-before-physical-send ordering as
            # ACPAdapter.assign_task: if the resumed prompt raises, the task
            # is left `delivery_pending` rather than silently eligible for
            # an automatic resend.
            self.ledger.mark_delivery_pending(task_id, pane_nonce=record["nonce"])
            transport = self.transport_factory(cwd=record["repo"], session=record["session_id"])
            try:
                result = transport.send_literal(record["session_id"], prompt)
            finally:
                transport.terminate()
            self.ledger.mark_delivered(task_id, pane_nonce=record["nonce"])
            message = (result.get("message") or "").strip()
            if not message:
                message = f"pi RPC stop_reason={result.get('stop_reason')}"
            return self.ledger.complete(task_id, message.encode("utf-8"), pane_nonce=record["nonce"])

    def observe_lane(self, lane):
        """Always None, for the same reason as `ACPAdapter.observe_lane`:
        `send_literal` already blocks until the turn settles, so a task
        either completed inline in `assign_task` or the call is still in
        flight -- there is no pane to poll between the two."""
        self._verified_lane(lane)
        return None

    def notify_supervisor(self, *, lane, retry_after):
        """pi-rpc workers never host the supervisor lane -- only
        codex/claude do (SPEC §15.2) -- so there is nothing for this adapter
        to notify."""
        return False


class ClaudePrintAdapter:
    """`claude -p`-driven sibling to `PiRPCAdapter`/`ACPAdapter`, for `claude`
    lanes registered with `transport='claude-print'` (agent-supervisor#171).
    `claude` is the one harness that may be driven either way -- plain
    `send-keys` via `TmuxAdapter`, the same as every standing, watched lane
    Jon relies on, or this headless dispatch-and-collect surface -- so, like
    `PiRPCAdapter` does for `pi`, this class does not own the harness
    outright; `cli.py` still chooses between the two by the lane's recorded
    `transport`, never by `harness` alone.

    Unlike `ACPAdapter`/`PiRPCAdapter`, there is no long-lived protocol
    session to resume mid-call: `claude -p` is a single process that runs a
    turn to completion and exits (see `claude_print_transport.py`'s module
    docstring). So `assign_task` does not "load" a session before sending --
    it hands the recorded `session_id` to a fresh transport as the `--resume`
    target and lets `ClaudePrintTransport.run` do the one subprocess call.
    Assignment and completion still happen in one round trip, for the same
    reason as its siblings: `claude -p` already blocks until the turn is
    genuinely done, so there is no pane to poll in between.
    """

    def __init__(self, ledger, transport_factory, *, clock=None):
        self.ledger = ledger
        self.transport_factory = transport_factory
        self.clock = clock or time.time

    def register_lane(self, *, lane, target, harness, repo, nonce):
        if harness != "claude":
            raise RuntimeError(f"ClaudePrintAdapter only supports claude lanes, got {harness!r}")
        session_id = str(uuid.uuid4())
        transport = self.transport_factory(cwd=repo)
        try:
            # Real fail-closed handshake, the same role `PiRPCAdapter.
            # register_lane`'s `get_state` call and `ACPAdapter.register_
            # lane`'s `new_session` call both play: a `claude` that cannot
            # start, crashes, or returns a malformed result makes THIS call
            # raise, and the lane is never registered.
            result = transport.start_session(session_id, "Reply with exactly the single word: ready.")
        finally:
            transport.terminate()
        if result.get("is_error"):
            raise RuntimeError(f"claude -p handshake reported an error: {result!r}")
        return self.ledger.register_lane(
            lane=lane,
            pane_id=session_id,
            nonce=nonce,
            harness=harness,
            repo=repo,
            server_id="claude-print",
            session_id=session_id,
            # agent-supervisor#302, reintroduced by #320 (agent-supervisor#311):
            # for claude-print the harness session id IS this uuid -- it is
            # what `claude -p --session-id <uuid>` minted and what
            # `claude -p --resume <uuid>` takes. Omitting it leaves every
            # migrated lane UNRECOVERABLE: restore.sh refuses rather than
            # invents, so a tmux death reports each one UNRECOVERABLE while
            # the conversation sits resumable on disk. #320's rewrite of the
            # surrounding assign_task method silently dropped this line and
            # the one below along with it -- the population #311 was meant to
            # backfill kept growing (41 -> 47) after #302 was already merged
            # and deployed, because new registrations went right back to
            # writing nothing here.
            harness_session_id=session_id,
            # core.py's register_lane never lets this diverge from
            # harness_session_id (agent-supervisor#172): a caller resolving a
            # fresh session id must pass the directory it was resolved in
            # alongside it. `repo` IS that directory -- what
            # `transport_factory(cwd=repo)` above actually launched
            # `claude -p` in. Leaving this out fails restore.sh's independent
            # harness_project_dir check even when harness_session_id is set.
            harness_project_dir=repo,
            command="claude",
            # Explicit, same reasoning as TmuxAdapter/ACPAdapter/PiRPCAdapter:
            # this class IS the claude-print transport by construction.
            transport="claude-print",
        )

    def _verified_lane(self, lane):
        record = self.ledger.get_lane(lane)
        if record is None:
            raise RuntimeError(f"unknown lane: {lane}")
        if record["harness"] != "claude" or record["transport"] != "claude-print":
            raise RuntimeError(
                f"lane {lane} is not a claude-print lane: harness={record['harness']!r} "
                f"transport={record.get('transport')!r}"
            )
        return record

    def assign_task(self, *, lane, task_id, summary):
        with self.ledger.operation_lock():
            record = self._verified_lane(lane)
            existing = self.ledger.get_task(task_id)
            if existing is not None and existing["status"] == "delivery_pending":
                raise RuntimeError(
                    f"delivery already attempted for task {task_id} and is unconfirmed; "
                    "reconcile the task before it can be assigned again"
                )
            # agent-supervisor#611: `worktree_path` used to fall through to
            # `assign`'s own default (""), the same as every other adapter's
            # `assign_task` -- `dispatch.sh`'s tmux flow is the only OTHER
            # writer of this column (`record_dispatch`'s call into
            # `Ledger._assign_tx`, forwarding the `--worktree` it was given),
            # and this class never went through it. `record["repo"]` is the
            # worktree `register_lane` recorded for this lane (`worktree.sh
            # new`'s own output, passed straight through as `repo` by
            # `dispatch-claude-print.sh`'s `register` call) -- not guessed,
            # not re-derived, the exact value this lane was registered with.
            self.ledger.assign(
                task_id=task_id,
                lane=lane,
                pane_nonce=record["nonce"],
                summary=summary,
                worktree_path=record["repo"],
            )
            # agent-supervisor#278: the lane must now report its OWN
            # completion, because nothing waits around to observe it any
            # more. These are the same `accept`/`complete` instructions
            # TmuxAdapter has always issued -- claude-print was the odd one
            # out, inferring completion from a blocking call returning, which
            # is why a timeout left the task at `delivery_pending` forever
            # instead of at any state a human or a query could act on.
            incoming = self.ledger.root / "incoming"
            incoming.mkdir(mode=0o700, exist_ok=True)
            result_file = incoming / f"{task_id}.md"
            prompt = (
                f"[Hill90 task {task_id}] {summary}\n\n"
                "Do not begin unrelated work. Record commands and actual outputs in a compact result. "
                f"Before working run: python3 {SUPERVISOR_CLI} accept --task {task_id}. "
                f"At completion write {result_file} and run: "
                f"python3 {SUPERVISOR_CLI} complete --task {task_id} --result-file {result_file}"
            )
            # Same ambiguous-state-before-physical-send ordering as
            # ACPAdapter/PiRPCAdapter.assign_task: if the resumed call
            # raises, the task is left `delivery_pending` rather than
            # silently eligible for an automatic resend.
            self.ledger.mark_delivery_pending(task_id, pane_nonce=record["nonce"])
            transport = self.transport_factory(cwd=record["repo"], session_id=record["session_id"])
            # agent-supervisor#278. This USED to be `result = transport.run(prompt)`
            # -- one blocking call covering delivery, the work, AND completion,
            # capped at 900s. Three consequences, all measured on 2026-08-17:
            #
            #   1. `dispatch.sh` did not return until the WORK finished, so a
            #      caller could not tell "working" from "hung". Every timeout
            #      the watchdog imposed then SIGKILLed a healthy dispatch.
            #   2. `mark_delivered` sat AFTER the work, so a timeout stranded
            #      the task at `delivery_pending` forever. `as308-fix-author-
            #      resolution` sat there over two hours.
            #   3. The work died with the call. The corpus loader, the #313
            #      batch and 411 uncommitted lines in two #308 worktrees were
            #      all lost or hand-salvaged this way.
            #
            # Now: start the turn, record delivery, RETURN. Exit means
            # DISPATCHED, not COMPLETED -- which is what dispatch.sh's usage
            # text always claimed ("Exit 0 only when a lane has been sent a
            # brief") and what the code never honoured. The lane reports its
            # own completion via `hill90-supervisor complete`, the same
            # mechanism every send-keys lane already uses.
            #
            # `transport.terminate()` is deliberately NOT called: it would
            # kill the child we just started. The child is in its own process
            # group, so it survives this process exiting -- that is the point.
            # The pid and log path are written to the lane log itself rather
            # than to the `events` table: that table is a notification queue
            # with a fixed shape (key/type/task_id/status/payload_path), not a
            # general audit log, and bending it into one would give two
            # meanings to one column. The log file is where someone looking
            # for "what is this detached child doing" will actually look.
            log_path = self.ledger.root / "lane-logs" / f"{task_id}.log"
            # The ADAPTER ensures the directory, not the transport. Depending
            # on the transport to have created it made this crash with
            # FileNotFoundError against any transport that does not -- caught
            # by the test double, which is exactly what a test double is for.
            # Whoever writes to a path is responsible for its parent.
            log_path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            proc = transport.run_detached(prompt, log_path=log_path)
            with open(log_path, "a") as handle:
                handle.write(f"\n--- dispatched detached: task={task_id} lane={lane} pid={proc.pid} ---\n")
            return self.ledger.mark_delivered(task_id, pane_nonce=record["nonce"])

    def observe_lane(self, lane):
        """Always None, for the same reason as `ACPAdapter.observe_lane`/
        `PiRPCAdapter.observe_lane`: `run` already blocks until the turn
        settles, so a task either completed inline in `assign_task` or the
        call is still in flight -- there is no pane to poll in between."""
        self._verified_lane(lane)
        return None

    def notify_supervisor(self, *, lane, retry_after):
        """claude-print workers never host the supervisor lane -- it is
        headless by construction, with no pane for a human to attach to, and
        the standing supervisor lane needs exactly that (SPEC §15.2) -- so
        there is nothing for this adapter to notify."""
        return False
