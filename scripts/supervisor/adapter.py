"""Harness-specific tmux adapters for the portable supervisor ledger."""

from __future__ import annotations

import re
import time
import uuid
from pathlib import Path


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

            task = self.ledger.assign(
                task_id=task_id,
                lane=lane,
                pane_nonce=record["nonce"],
                summary=summary,
            )
            incoming = self.ledger.root / "incoming"
            incoming.mkdir(mode=0o700, exist_ok=True)
            result_file = incoming / f"{task_id}.md"
            prompt = (
                f"[Hill90 task {task_id}] {summary}\n\n"
                "Do not begin unrelated work. Record commands and actual outputs in a compact result. "
                f"Before working run: hill90-supervisor accept --task {task_id}. "
                f"At completion write {result_file} and run: "
                f"hill90-supervisor complete --task {task_id} --result-file {result_file}"
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
                + "hill90-supervisor ack --event "
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
            self.ledger.assign(task_id=task_id, lane=lane, pane_nonce=record["nonce"], summary=summary)
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
            self.ledger.assign(task_id=task_id, lane=lane, pane_nonce=record["nonce"], summary=summary)
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
            # agent-supervisor: for claude-print the harness session id IS this
            # uuid -- it is what `claude -p --session-id <uuid>` minted and what
            # `claude -p --resume <uuid>` takes. Omitting it left all 33 migrated
            # lanes UNRECOVERABLE: restore.sh refuses rather than invents, so a
            # tmux death would have reported every one of them UNRECOVERABLE
            # while the conversations sat resumable on disk. The old send-keys
            # transport stranded prompts but survived a crash; this one delivered
            # reliably and lost everything, which is the worse trade.
            harness_session_id=session_id,
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
            self.ledger.assign(task_id=task_id, lane=lane, pane_nonce=record["nonce"], summary=summary)
            prompt = (
                f"[Hill90 task {task_id}] {summary}\n\n"
                "Do not begin unrelated work. Record commands and actual outputs in a compact result."
            )
            # Same ambiguous-state-before-physical-send ordering as
            # ACPAdapter/PiRPCAdapter.assign_task: if the resumed call
            # raises, the task is left `delivery_pending` rather than
            # silently eligible for an automatic resend.
            self.ledger.mark_delivery_pending(task_id, pane_nonce=record["nonce"])
            transport = self.transport_factory(cwd=record["repo"], session_id=record["session_id"])
            try:
                result = transport.run(prompt)
            finally:
                transport.terminate()
            self.ledger.mark_delivered(task_id, pane_nonce=record["nonce"])
            message = (result.get("result") or "").strip()
            if not message:
                message = f"claude -p subtype={result.get('subtype')}"
            return self.ledger.complete(task_id, message.encode("utf-8"), pane_nonce=record["nonce"])

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
