"""Decide whether the watchdog's `escalate` state should reach a human.

Split per `docs/SPEC.md` §15, mirroring `recycle.py` -- a pure decision core
and a thin actuator. `decide_notify` takes the watchdog's current state and
whether the current escalate *episode* has already been notified, and
returns whether to send, why, and the episode flag for the next tick. It
does no I/O and sends nothing. `check_and_notify` is the actuator: it reads
`watchdog.status`, persists the episode flag across ticks, and calls an
injected `sender` -- the real one shells out to the `notify` skill
(jonhill90/skills#146); tests inject a fake that only records calls.

`watchdog.sh` (untracked, jonhill90/agent-dotfiles#51) calls this module as
a subprocess after every `report()`. Only the `escalate` state ever
notifies -- `working`, `waiting_on_jon`, `cooling_down`, and `restarted` are
all normal ticks and must stay silent, and each of them also resets the
episode so a later escalate notifies again.
"""

from __future__ import annotations

import json
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


def _load_episode_notified(path: Path) -> bool:
    if not path.exists():
        return False
    try:
        return bool(json.loads(path.read_text()).get("notified", False))
    except (OSError, ValueError):
        return False


def _save_episode_notified(path: Path, notified: bool) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + f".{__name__.rsplit('.', 1)[-1]}.tmp")
    tmp.write_text(json.dumps({"notified": notified}))
    tmp.replace(path)


def _log_local(log_path: Path, line: str) -> None:
    log_path.parent.mkdir(parents=True, exist_ok=True)
    timestamp = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    with log_path.open("a", encoding="utf-8") as f:
        f.write(f"{timestamp} {line}\n")


def check_and_notify(*, status_path, episode_state_path, sender, log_path=None) -> NotifyDecision:
    """Actuator: read `watchdog.status`, decide, persist the episode flag,
    and call `sender(message)` when the decision says to notify.

    `sender` is injected so tests never touch a real channel: production
    wires `send_via_notify_skill`, tests wire a recorder. A send failure is
    logged locally (when `log_path` is given) and re-raised -- the episode
    flag is still saved as notified first, because a failed *attempt* is
    still an attempt; retrying every tick the moment a channel is flaky
    would be the message-storm failure this exists to avoid.
    """
    status_path = Path(status_path)
    episode_state_path = Path(episode_state_path)

    fields = parse_status(status_path.read_text())
    state = fields.get("state", "")
    detail = fields.get("detail", "")
    restarts, window_seconds = parse_restarts(fields.get("restarts", "0 in the last 0s"))

    episode_notified = _load_episode_notified(episode_state_path)
    decision = decide_notify(
        state=state,
        detail=detail,
        restarts=restarts,
        window_seconds=window_seconds,
        episode_notified=episode_notified,
    )
    _save_episode_notified(episode_state_path, decision.next_episode_notified)

    if decision.should_notify:
        try:
            sender(decision.message)
        except Exception as error:  # noqa: BLE001 - re-raised immediately, never swallowed
            if log_path is not None:
                _log_local(Path(log_path), f"SEND-FAILED {error!r}")
            raise

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
    """
    if notify_script.endswith(".sh"):
        argv = [notify_script, "Supervisor escalation", message]
    else:
        argv = [sys.executable, notify_script, "--message", message, "--send"]
    result = subprocess.run(
        argv,
        capture_output=True,
        text=True,
        timeout=30,
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
