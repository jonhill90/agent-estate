"""Decide whether the watchdog's `escalate` state should reach a human.

Split per `docs/SPEC.md` §15, mirroring `recycle.py` -- a pure decision core
and a thin actuator. `decide_notify` takes the watchdog's current state and
whether the current escalate *episode* has already been notified, and
returns whether to send, why, and the episode flag for the next tick. It
does no I/O and sends nothing. `check_and_notify` is the actuator: it reads
`watchdog.status`, calls an injected `sender` -- the real one shells out to
the `notify` skill (jonhill90/skills#146); tests inject a fake that only
records calls -- and persists the episode flag across ticks according to
whether that send actually succeeded. The flag means "a human has been
told", never "we tried" (#91).

`watchdog.sh` (untracked, jonhill90/agent-dotfiles#51) calls this module as
a subprocess after every `report()`. Only the `escalate` state ever
notifies -- `working`, `waiting_on_jon`, `cooling_down`, and `restarted` are
all normal ticks and must stay silent, and each of them also resets the
episode so a later escalate notifies again.
"""

from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import time
from dataclasses import dataclass
from pathlib import Path


class SendError(Exception):
    """A send was attempted and failed. Always logged locally before this
    propagates -- an unreachable channel must not look like a healthy
    system, the same discipline the `notify` skill itself follows."""


_RESTARTS_RE = re.compile(r"^(\d+)\s+in the last\s+(\d+)s$")


@dataclass(frozen=True)
class NotifyDecision:
    """`next_episode_notified` is the flag to persist *if the send succeeds*.

    On the `should_notify` branch it is the actuator's job, not this pure
    core's, to know whether the message actually went out -- a decision made
    before the attempt cannot describe its outcome (#91).
    """

    should_notify: bool
    reason: str
    next_episode_notified: bool
    message: str | None = None


def parse_status(text: str) -> dict[str, str]:
    """Parse `watchdog.status`'s `key:   value` lines into a dict.

    The file is written by `report()` in `watchdog.sh` -- see #51. Values
    may be empty (`detail:` on a healthy tick); missing keys are simply
    absent rather than raising, since a caller who needs a key checks for
    it explicitly.
    """
    fields: dict[str, str] = {}
    for line in text.splitlines():
        if ":" not in line:
            continue
        key, _, value = line.partition(":")
        fields[key.strip()] = value.strip()
    return fields


def parse_restarts(value: str) -> tuple[int, int]:
    """Parse the `restarts:` field's `"N in the last Ws"` shape."""
    match = _RESTARTS_RE.match(value.strip())
    if not match:
        raise ValueError(f"unparseable restarts field: {value!r}")
    return int(match.group(1)), int(match.group(2))


def build_message(*, detail: str, restarts: int, window_seconds: int) -> str:
    """A message a human can act on from a lock screen: how many restarts,
    over what window, and the one command to look deeper."""
    hours = window_seconds / 3600
    window = f"{hours:g}h" if hours == int(hours) else f"{window_seconds}s"
    return (
        f"watchdog: escalate — {restarts} restarts/{window}, stopped. "
        f"cat ~/.local/state/agent-dotfiles-supervisor/watchdog.status"
    )


def decide_notify(
    *,
    state: str,
    detail: str,
    restarts: int,
    window_seconds: int,
    episode_notified: bool,
) -> NotifyDecision:
    """Pure decision: should this tick send a message?

    Only `state == "escalate"` ever notifies, and only once per episode --
    an episode is the run of consecutive `escalate` ticks starting from the
    first one after any non-escalate tick. Every other state is silent and
    resets `episode_notified` to False, so a later escalate is treated as a
    new episode and notifies again.

    "Once per episode" means once *delivered* per episode. `episode_notified`
    is only ever True because a send succeeded, so a tick whose send failed
    arrives here looking exactly like the first tick of the episode -- which
    is precisely what makes it retry (#91).
    """
    if state != "escalate":
        return NotifyDecision(
            should_notify=False,
            reason=f"state={state!r} is not escalate — silent",
            next_episode_notified=False,
        )
    if episode_notified:
        return NotifyDecision(
            should_notify=False,
            reason="escalate episode already notified — deduped",
            next_episode_notified=True,
        )
    return NotifyDecision(
        should_notify=True,
        reason="new escalate episode — notify",
        next_episode_notified=True,
        message=build_message(detail=detail, restarts=restarts, window_seconds=window_seconds),
    )


def _load_episode(path: Path) -> tuple[bool, int]:
    """`(notified, failed_attempts)` for the current episode.

    An unreadable or absent file reads as "not notified", which fails toward
    paging a human rather than toward silence -- the direction every choice
    on this path is made in.
    """
    if not path.exists():
        return False, 0
    try:
        data = json.loads(path.read_text())
        return bool(data.get("notified", False)), int(data.get("attempts", 0))
    except (OSError, ValueError, TypeError):
        return False, 0


def _save_episode(path: Path, *, notified: bool, attempts: int = 0) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + f".{__name__.rsplit('.', 1)[-1]}.tmp")
    tmp.write_text(json.dumps({"notified": notified, "attempts": attempts}))
    tmp.replace(path)


def _log_local(log_path: Path, line: str) -> None:
    log_path.parent.mkdir(parents=True, exist_ok=True)
    timestamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    with log_path.open("a", encoding="utf-8") as f:
        f.write(f"{timestamp} {line}\n")


def check_and_notify(*, status_path, episode_state_path, sender, log_path=None) -> NotifyDecision:
    """Actuator: read `watchdog.status`, decide, call `sender(message)` when
    the decision says to notify, and persist the episode flag according to
    what actually happened.

    `sender` is injected so tests never touch a real channel: production
    wires `send_via_notify_skill`, tests wire a recorder.

    **The episode flag records DELIVERY, not attempt** (#91). It used to be
    written before the send, so one unreachable moment -- Telegram down, the
    laptop off wifi, an expired token -- consumed the escalation: the next
    tick read `notified: true`, deduped itself, and nobody was ever paged.
    The loop stayed down and `watchdog.status` said `escalate` to an empty
    room. That is the estate's recurring failure -- a thing that could not
    be measured recorded as a thing that was not there -- landing in the one
    path whose whole job is to be the backstop.

    **Retry semantics: every tick, unbounded, no backoff.** A failed send
    leaves the flag unset, so the next tick tries again, and the one after
    that, for as long as the escalation stands. That cannot become a burst,
    because there is no queue: the flag is per *episode*, not per attempt,
    so however many ticks failed first, a recovered channel delivers exactly
    one message -- and it describes the live state at the moment it lands,
    not a stale backlog. Backoff was considered and rejected: its only
    effect here would be to delay the single message that matters. The
    ceiling on pages to Jon is one per escalate episode either way, which is
    the property that makes retrying freely safe.
    """
    status_path = Path(status_path)
    episode_state_path = Path(episode_state_path)
    log = (lambda line: _log_local(Path(log_path), line)) if log_path is not None else (lambda line: None)

    fields = parse_status(status_path.read_text())
    state = fields.get("state", "")
    detail = fields.get("detail", "")
    restarts, window_seconds = parse_restarts(fields.get("restarts", "0 in the last 0s"))

    episode_notified, attempts = _load_episode(episode_state_path)
    decision = decide_notify(
        state=state,
        detail=detail,
        restarts=restarts,
        window_seconds=window_seconds,
        episode_notified=episode_notified,
    )

    if not decision.should_notify:
        # Nothing was sent, so nothing about delivery changed. A non-escalate
        # tick resets the episode (and its attempt count); a deduped tick
        # keeps the `notified: true` it just read.
        _save_episode(episode_state_path, notified=decision.next_episode_notified)
        return decision

    try:
        sender(decision.message)
    except Exception as error:  # noqa: BLE001 - re-raised immediately, never swallowed
        attempts += 1
        _save_episode(episode_state_path, notified=False, attempts=attempts)
        # The only record left when the channel is the thing that is broken.
        # It has to say, unambiguously, that NOBODY WAS TOLD -- the same
        # distinction `notify.sh` draws with UNREACHABLE.
        log(f"NOTIFY-FAILED (attempt {attempts}) nobody was told, will retry next tick: {error!r}")
        raise

    _save_episode(episode_state_path, notified=True)
    log(f"NOTIFY-SENT after {attempts + 1} attempt(s): {decision.message}")
    return decision


def send_via_notify_skill(message: str, *, notify_script: str) -> None:
    """Real sender: shells out to whichever notifier is configured.

    Two call shapes, chosen by extension, because two notifiers exist and
    only one of them can currently reach Jon:

    - `*.sh` -> `notify.sh "<subject>" "<body>"`. This is
      `scripts/supervisor/notify.sh`, which delivers over Telegram and is
      the only path proven to land on Jon's phone (first real message
      2026-08-11 05:52Z). An earlier version of this docstring called it
      "the untracked reference"; it is tracked as of #55.
    - anything else -> `python <script> --message <msg> --send`, the
      `notify` skill (jonhill90/skills#148), which is iMessage-only today.

    The skill becomes the single canonical sender once it grows Telegram
    (jonhill90/skills#146); until then, routing escalations through the
    iMessage-only path would mean escalations that reach nobody.

    `AGENT_NOTIFY_CALLER=supervisor` is set on the child's environment
    because `notify.sh` refuses to touch any channel without it
    (agent-dotfiles#52) -- this is the one process in the estate allowed
    to identify itself that way; nothing else should.
    """
    env = dict(os.environ, AGENT_NOTIFY_CALLER="supervisor")
    if notify_script.endswith(".sh"):
        argv = [notify_script, "Supervisor escalation", message]
    else:
        argv = [sys.executable, notify_script, "--message", message, "--send"]
    result = subprocess.run(
        argv,
        capture_output=True,
        text=True,
        timeout=30,
        env=env,
    )
    if result.returncode != 0:
        raise SendError(
            f"notify.py exited {result.returncode}: {result.stderr.strip() or result.stdout.strip() or '(no output)'}"
        )


def main(argv: list[str] | None = None) -> int:
    import argparse
    import os

    parser = argparse.ArgumentParser(description=__doc__)
    default_state = Path(os.environ.get("SUPERVISOR_STATE_DIR", "~/.local/state/agent-dotfiles-supervisor")).expanduser()
    parser.add_argument("--status-path", default=str(default_state / "watchdog.status"))
    parser.add_argument("--episode-state-path", default=str(default_state / ".watchdog-escalate-episode.json"))
    parser.add_argument("--log-path", default=str(default_state / "watchdog-notify.log"))
    parser.add_argument(
        "--notify-script",
        default=os.environ.get("NOTIFY_SCRIPT", ""),
        help="path to the notify skill's scripts/notify.py",
    )
    args = parser.parse_args(argv)

    def sender(message: str) -> None:
        # Checked lazily, at send time, not up front: a missing
        # --notify-script must never keep the process from running quietly
        # on the every-tick, non-escalate path -- it only matters the
        # instant there is actually something to send.
        if not args.notify_script:
            raise SendError("--notify-script (or $NOTIFY_SCRIPT) is not configured")
        send_via_notify_skill(message, notify_script=args.notify_script)

    try:
        decision = check_and_notify(
            status_path=args.status_path,
            episode_state_path=args.episode_state_path,
            sender=sender,
            log_path=args.log_path,
        )
    except SendError as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1

    print(decision.reason)
    return 0


if __name__ == "__main__":
    sys.exit(main())
