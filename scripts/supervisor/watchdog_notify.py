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

import calendar
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


def _deliver(decision: NotifyDecision, *, episode_state_path: Path, sender, log, attempts: int) -> NotifyDecision:
    """Shared actuator tail for every episode-gated alarm in this module:
    persist the episode flag on delivery only (#91), retry unbounded on
    failure, and never swallow a send error. `check_and_notify` (the
    loop-restart escalation) and `check_and_notify_heartbeat` (#163, the
    stale inbox-poll heartbeat) both reduce to this once each has read its
    own status file and produced a `NotifyDecision` -- the delivery
    semantics are identical, only what triggers the decision differs."""
    if not decision.should_notify:
        # Nothing was sent, so nothing about delivery changed. A silent tick
        # (or a deduped one) persists next_episode_notified as-is.
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
    return _deliver(decision, episode_state_path=episode_state_path, sender=sender, log=log, attempts=attempts)


# --- #163: the inbox-poll heartbeat, the death report_stop() cannot make ---
#
# `inbox-poll.sh` pages Jon itself, from `trap report_stop EXIT` -- but a
# trap only runs for exits bash can still act on. SIGKILL, an OOM kill, or
# the machine losing power leave `inbox-poll.status`'s `checked:` timestamp
# frozen at whatever it last wrote, with no exit record at all. This is the
# external observer for exactly that case: it reads the heartbeat, not the
# process, so it does not need the dying process's cooperation.
#
# Reuses the same episode-gated delivery this module already has for the
# loop-restart escalation (`_deliver`, `_load_episode`, `_save_episode`,
# `send_via_notify_skill`) rather than inventing a second notify path -- the
# only new thing here is the DECISION: what makes an inbox-poll heartbeat
# worth paging about, which is a different question from what makes a
# supervisor-loop restart count worth paging about.


def classify_heartbeat(fields: dict[str, str] | None, *, now: float) -> tuple[str, float | None]:
    """Read facts out of `inbox-poll.status`'s parsed fields; apply no
    threshold and make no notify decision -- that split keeps this half
    testable without touching the clock and `decide_notify_heartbeat` pure
    of file I/O.

    Returns `(kind, age_seconds)`:
      "missing"    -- no status file. Never-started and died-hard are
                      different operator problems (#163): a poller that has
                      never run yet is not evidence of a SIGKILL.
      "unreadable" -- a status file exists but `checked:` cannot be parsed.
                      Not the same as missing -- something IS there, it is
                      just not trustworthy -- so the caller folds this
                      toward "stale" rather than toward "fresh": a heartbeat
                      this module cannot read proves nothing about liveness,
                      and silence is the wrong side to fail toward.
      "stopped"    -- `state: stopped`, `inbox-poll.sh`'s own report_stop()
                      writing its LAST heartbeat on the way out (#155's
                      deliberate/too-young gates already decided whether
                      THAT paged Jon). Exempt from staleness on purpose: the
                      brief is explicit that alarming here too pages him
                      twice for one event.
      "alive"      -- anything else. `age_seconds` is how long ago `checked:`
                      was written; the threshold comparison happens in
                      `decide_notify_heartbeat`, not here.
    """
    if fields is None:
        return "missing", None
    checked_raw = fields.get("checked", "")
    try:
        checked_epoch = calendar.timegm(time.strptime(checked_raw, "%Y-%m-%dT%H:%M:%SZ"))
    except ValueError:
        return "unreadable", None
    age_seconds = now - checked_epoch
    if fields.get("state") == "stopped":
        return "stopped", age_seconds
    return "alive", age_seconds


def build_heartbeat_message(*, age_seconds: float | None, threshold_seconds: int) -> str:
    """A message a human can act on from a lock screen: how stale, against
    what threshold, and the one command to look deeper."""
    age_desc = "unreadable" if age_seconds is None else f"{int(age_seconds)}s"
    return (
        f"watchdog: inbox-poll heartbeat stale — last checked {age_desc} ago, "
        f"threshold {threshold_seconds}s. "
        f"cat ~/.local/state/agent-dotfiles-supervisor/inbox-poll.status"
    )


def decide_notify_heartbeat(
    *,
    kind: str,
    age_seconds: float | None,
    threshold_seconds: int,
    episode_notified: bool,
) -> NotifyDecision:
    """Pure decision: should this tick page Jon about the inbox-poll
    heartbeat? `kind` is `classify_heartbeat`'s output ("missing",
    "unreadable", "stopped", "alive"); staleness itself -- `age_seconds >
    threshold_seconds` -- is decided here, not in the classifier, so the
    threshold stays a decision-time parameter instead of baked into the fact
    read off disk.

    Same one-per-episode shape as `decide_notify`: only a genuinely stale,
    still-unexplained heartbeat notifies, and only once per episode. Missing,
    stopped, and fresh are all silent AND reset the episode, so a later
    stale reading -- even a recurrence of the same underlying cause -- is
    treated as a new episode and pages again.
    """
    if kind == "missing":
        return NotifyDecision(
            should_notify=False,
            reason="inbox-poll.status missing — poller never started or state was wiped, not a stale-heartbeat page",
            next_episode_notified=False,
        )
    if kind == "stopped":
        return NotifyDecision(
            should_notify=False,
            reason="poller reported its own stop (state: stopped) — its EXIT trap already decided whether to page, not double-paging here",
            next_episode_notified=False,
        )
    stale = kind == "unreadable" or (age_seconds is not None and age_seconds > threshold_seconds)
    if not stale:
        return NotifyDecision(
            should_notify=False,
            reason=f"heartbeat {int(age_seconds)}s old, within the {threshold_seconds}s threshold — alive",
            next_episode_notified=False,
        )
    if episode_notified:
        return NotifyDecision(
            should_notify=False,
            reason="stale-heartbeat episode already notified — deduped",
            next_episode_notified=True,
        )
    return NotifyDecision(
        should_notify=True,
        reason="new stale-heartbeat episode — notify",
        next_episode_notified=True,
        message=build_heartbeat_message(age_seconds=age_seconds, threshold_seconds=threshold_seconds),
    )


def check_and_notify_heartbeat(
    *, heartbeat_status_path, episode_state_path, sender, threshold_seconds: int, log_path=None, now: float | None = None
) -> NotifyDecision:
    """Actuator for the inbox-poll heartbeat alarm: read
    `inbox-poll.status`, decide via `decide_notify_heartbeat`, and deliver
    through the same episode-gated path the loop-restart escalation uses.
    `now` is injectable so tests never depend on the wall clock.
    """
    heartbeat_status_path = Path(heartbeat_status_path)
    episode_state_path = Path(episode_state_path)
    log = (lambda line: _log_local(Path(log_path), line)) if log_path is not None else (lambda line: None)
    now = time.time() if now is None else now

    fields = None
    if heartbeat_status_path.exists():
        try:
            fields = parse_status(heartbeat_status_path.read_text())
        except OSError:
            fields = None

    kind, age_seconds = classify_heartbeat(fields, now=now)
    episode_notified, attempts = _load_episode(episode_state_path)
    decision = decide_notify_heartbeat(
        kind=kind,
        age_seconds=age_seconds,
        threshold_seconds=threshold_seconds,
        episode_notified=episode_notified,
    )
    return _deliver(decision, episode_state_path=episode_state_path, sender=sender, log=log, attempts=attempts)


# --- as#151: director-inbox staleness, independent of the loop's own tick --
#
# director-route.sh already pages Jon when a queued message crosses
# DIRECTOR_INBOX_STALE_SECONDS (as#34/#42) -- but that check only runs
# inside inbox-poll.sh's own flush loop, once per Telegram long-poll
# iteration. Measured on this issue (as#151): the flush loop can run for
# hours logging "pane not idle, nothing sent" without the underlying
# escalate ever having been proven to fire in production (notify.log's
# entire history has no "Director inbox has undelivered message(s)" line,
# despite at least one earlier incident -- as#34's own filing -- that should
# have crossed the threshold). And if inbox-poll.sh itself is the thing that
# is down, nothing calls director-route.sh --flush at all, so that escalate
# never runs regardless of how stale the queue gets.
#
# This is the same fix #163 already applied to the poller's own heartbeat:
# an external observer that reads the fact off disk (here, via
# `director-inbox.sh stats`, not a status file) from watchdog.sh's own
# unattended LaunchAgent tick, rather than depending on the cooperation of
# the exact subsystem that is failing. Reuses the same episode-gated
# delivery (`_deliver`, `_load_episode`, `_save_episode`) as every other
# alarm in this module -- the only new thing is the decision.
def classify_director_inbox(stats: dict | None, *, binary_missing: bool = False) -> tuple[str, float | None, int]:
    """Read facts out of `director-inbox.sh stats`'s parsed JSON; apply no
    threshold and make no notify decision -- same split as
    `classify_heartbeat`.

    Returns `(kind, age_seconds, pending)`:
      "missing"    -- `director-inbox.sh` itself could not be found or run
                      (`binary_missing=True`, from a FileNotFoundError /
                      PermissionError trying to exec it). Same reasoning as
                      `classify_heartbeat`'s "missing": a scratch copy of
                      this tree that only carries the two or three scripts a
                      given test needs (as#57/#75's `copy_dir`, for one) is
                      not evidence the real queue is stuck -- it is evidence
                      this check has nothing to read yet, which must not
                      page any more than a poller that has never started
                      does.
      "unreadable" -- the binary WAS found and ran, but exited non-zero,
                      timed out, or produced output that is not the JSON
                      `stats` promises. Something is there and it is not
                      trustworthy -- unlike "missing", this folds toward
                      "worth paging", the same direction `classify_heartbeat`
                      already takes for its own "unreadable".
      "empty"      -- `pending == 0`. The ordinary, healthy state.
      "pending"    -- `pending > 0`. `age_seconds` is `oldest_age_s`; the
                      threshold comparison happens in
                      `decide_notify_director_inbox`, not here.
    """
    if binary_missing:
        return "missing", None, 0
    if not isinstance(stats, dict):
        return "unreadable", None, 0
    pending = stats.get("pending")
    if not isinstance(pending, int):
        return "unreadable", None, 0
    if pending == 0:
        return "empty", None, 0
    age = stats.get("oldest_age_s")
    if not isinstance(age, (int, float)):
        return "unreadable", None, pending
    return "pending", float(age), pending


def build_director_inbox_message(*, pending: int, age_seconds: float | None, threshold_seconds: int) -> str:
    """A message a human can act on from a lock screen: how many, how
    stale, against what threshold, and the one command to look deeper."""
    age_desc = "unreadable" if age_seconds is None else f"{int(age_seconds)}s"
    return (
        f"watchdog: director inbox has {pending} undelivered message(s), oldest {age_desc} old, "
        f"threshold {threshold_seconds}s. "
        f"cat ~/.local/state/agent-dotfiles-supervisor/director-inbox.jsonl"
    )


def decide_notify_director_inbox(
    *,
    kind: str,
    age_seconds: float | None,
    pending: int,
    threshold_seconds: int,
    episode_notified: bool,
) -> NotifyDecision:
    """Pure decision: should this tick page Jon about the director inbox?
    Same one-per-episode shape as `decide_notify_heartbeat`: missing, empty,
    and fresh are all silent and reset the episode, so a later stale reading
    -- even a recurrence of the same underlying cause -- is treated as a new
    episode and pages again.
    """
    if kind == "missing":
        return NotifyDecision(
            should_notify=False,
            reason="director-inbox.sh not found — nothing to measure yet, not a stale-inbox page",
            next_episode_notified=False,
        )
    if kind == "empty":
        return NotifyDecision(
            should_notify=False,
            reason="director inbox empty — nothing pending",
            next_episode_notified=False,
        )
    stale = kind == "unreadable" or (age_seconds is not None and age_seconds > threshold_seconds)
    if not stale:
        return NotifyDecision(
            should_notify=False,
            reason=f"{pending} pending, oldest {int(age_seconds)}s old, within the {threshold_seconds}s threshold",
            next_episode_notified=False,
        )
    if episode_notified:
        return NotifyDecision(
            should_notify=False,
            reason="stale director-inbox episode already notified — deduped",
            next_episode_notified=True,
        )
    return NotifyDecision(
        should_notify=True,
        reason="new stale director-inbox episode — notify",
        next_episode_notified=True,
        message=build_director_inbox_message(pending=pending, age_seconds=age_seconds, threshold_seconds=threshold_seconds),
    )


def check_and_notify_director_inbox(
    *, director_inbox_bin, episode_state_path, sender, threshold_seconds: int, log_path=None, timeout: int = 15
) -> NotifyDecision:
    """Actuator: run `director-inbox.sh stats` (read-only, no lock, safe to
    call every tick), decide via `decide_notify_director_inbox`, and deliver
    through the same episode-gated path every other alarm in this module
    uses. Unlike the heartbeat check, there is no injectable `now` -- the
    age comes precomputed from `stats`'s own `oldest_age_s`, itself measured
    against `director-inbox.sh`'s own clock read at call time.
    """
    episode_state_path = Path(episode_state_path)
    log = (lambda line: _log_local(Path(log_path), line)) if log_path is not None else (lambda line: None)

    stats = None
    binary_missing = False
    try:
        result = subprocess.run(
            [str(director_inbox_bin), "stats"],
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        if result.returncode == 0:
            stats = json.loads(result.stdout)
    except OSError:
        # director-inbox.sh could not even be exec'd -- not found, not
        # executable, a dangling symlink. Distinct from a binary that ran
        # and misbehaved (see classify_director_inbox's "missing" vs
        # "unreadable"): a scratch copy of this tree that does not carry
        # director-inbox.sh (as#57/#75's copy_dir, for one) must not read as
        # a stuck queue.
        binary_missing = True
    except (subprocess.TimeoutExpired, ValueError):
        stats = None

    kind, age_seconds, pending = classify_director_inbox(stats, binary_missing=binary_missing)
    episode_notified, attempts = _load_episode(episode_state_path)
    decision = decide_notify_director_inbox(
        kind=kind,
        age_seconds=age_seconds,
        pending=pending,
        threshold_seconds=threshold_seconds,
        episode_notified=episode_notified,
    )
    return _deliver(decision, episode_state_path=episode_state_path, sender=sender, log=log, attempts=attempts)


#: The notifier ships beside this module. Resolved from `__file__` rather
#: than from any absolute path so that moving the tree -- a repository split,
#: a relocated live worktree, a test running from a worktree -- moves the
#: notifier with it. #118: the operator's `NOTIFY_SCRIPT` still named the
#: pre-split `agent-dotfiles` copy months after it stopped existing, and
#: nothing noticed because nothing checked.
DEFAULT_NOTIFY_SCRIPT = Path(__file__).resolve().parent / "notify.sh"


def _notifier_is_usable(path: str) -> bool:
    """A `*.sh` notifier is exec'd directly, so it must be executable; anything
    else is handed to `sys.executable`, which only needs to be able to read it."""
    if path.endswith(".sh"):
        return os.access(path, os.X_OK)
    return os.path.isfile(path)


def resolve_notify_script(configured: str) -> tuple[str, str | None]:
    """Decide which notifier to actually run, and say so when it is not the
    configured one.

    Returns `(path, note)`; `note` is None when the configured value was used
    as given, and otherwise a line for `watchdog-notify.log` naming what was
    overridden. A relocation that invalidates the configured path must not be
    able to happen quietly -- that is the whole of #118 -- so the fallback is
    never silent.

    An EMPTY configured value is deliberately left alone rather than defaulted
    here. `main()` already treats "nothing configured" as a loud SendError, and
    that is the branch every test exercising an unconfigured notifier takes:
    defaulting it would make those tests execute the real `notify.sh`, which
    reaches a real phone. A wrong path is a bug to route around; no path at all
    is a decision the operator has not made, and guessing at a live channel is
    not this function's call.
    """
    if not configured:
        return configured, None
    if _notifier_is_usable(configured):
        return configured, None
    fallback = str(DEFAULT_NOTIFY_SCRIPT)
    return fallback, (
        f"NOTIFY-PATH-STALE: configured notifier {configured!r} does not resolve; "
        f"falling back to the one shipped beside this module: {fallback}"
    )


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
    try:
        result = subprocess.run(
            argv,
            capture_output=True,
            text=True,
            timeout=30,
            env=env,
        )
    except OSError as error:
        # A notifier that cannot be RUN is a delivery failure like any other,
        # and has to arrive through the same channel as one (#118). Left
        # uncaught, `subprocess.run` raised a bare FileNotFoundError past
        # `main()`, which only catches SendError: the process died with a
        # traceback instead of producing the NOTIFY-FAILED line, the rc=1, and
        # the "escalation did NOT reach a human" line `watchdog.sh` writes
        # into `watchdog.status` from it. The estate's loudest failure came
        # out as its quietest. Catching OSError covers the whole family --
        # missing, not executable, dangling symlink, unusable interpreter --
        # rather than only the path shape that happened to get reported.
        raise SendError(f"could not run notifier {notify_script!r}: {error}") from error
    except subprocess.TimeoutExpired as error:
        # Same reasoning, other way to not deliver: a channel that hangs is a
        # channel that reached nobody, and must not exit through a traceback
        # either.
        raise SendError(f"notifier {notify_script!r} timed out after 30s; nobody was told") from error
    if result.returncode != 0:
        raise SendError(
            f"notify.py exited {result.returncode}: {result.stderr.strip() or result.stdout.strip() or '(no output)'}"
        )


def main(argv: list[str] | None = None) -> int:
    import argparse
    import os

    parser = argparse.ArgumentParser(description=__doc__)
    default_state = Path(os.environ.get("SUPERVISOR_STATE_DIR", "~/.local/state/agent-dotfiles-supervisor")).expanduser()
    parser.add_argument(
        "--mode",
        choices=["escalate", "heartbeat", "director-inbox"],
        default="escalate",
        help=(
            "'escalate' (default) is the loop-restart check; 'heartbeat' is #163's "
            "inbox-poll staleness check; 'director-inbox' is as#151's director-inbox "
            "staleness check"
        ),
    )
    parser.add_argument("--status-path", default=str(default_state / "watchdog.status"), help="escalate mode: watchdog.status")
    parser.add_argument(
        "--heartbeat-status-path", default=str(default_state / "inbox-poll.status"), help="heartbeat mode: inbox-poll.status"
    )
    parser.add_argument(
        "--director-inbox-bin",
        default=str(Path(__file__).resolve().parent / "director-inbox.sh"),
        help="director-inbox mode: path to director-inbox.sh",
    )
    parser.add_argument(
        "--threshold-seconds", type=int, default=0, help="heartbeat/director-inbox mode: staleness threshold, in seconds"
    )
    # Default depends on --mode (each check must not read or reset the
    # other's episode file) so it is resolved below, after parsing, not here.
    parser.add_argument("--episode-state-path", default=None)
    parser.add_argument("--log-path", default=str(default_state / "watchdog-notify.log"))
    parser.add_argument(
        "--notify-script",
        default=os.environ.get("NOTIFY_SCRIPT", ""),
        help="path to the notify skill's scripts/notify.py",
    )
    args = parser.parse_args(argv)
    _EPISODE_FILENAMES = {
        "heartbeat": ".watchdog-heartbeat-episode.json",
        "director-inbox": ".watchdog-director-inbox-episode.json",
    }
    episode_state_path = args.episode_state_path or str(
        default_state / _EPISODE_FILENAMES.get(args.mode, ".watchdog-escalate-episode.json")
    )

    def sender(message: str) -> None:
        # Checked lazily, at send time, not up front: a missing
        # --notify-script must never keep the process from running quietly
        # on the every-tick, non-escalate/non-stale path -- it only matters
        # the instant there is actually something to send.
        if not args.notify_script:
            raise SendError(
                "--notify-script (or $NOTIFY_SCRIPT) is not configured "
                f"(the notifier shipped with this tree is {DEFAULT_NOTIFY_SCRIPT})"
            )
        script, note = resolve_notify_script(args.notify_script)
        if note is not None:
            # Written before the attempt, not after: if the send then fails
            # too, the log still says which notifier was tried.
            _log_local(Path(args.log_path), note)
        send_via_notify_skill(message, notify_script=script)

    try:
        if args.mode == "heartbeat":
            if args.threshold_seconds <= 0:
                print("ERROR: --threshold-seconds must be > 0 for --mode heartbeat", file=sys.stderr)
                return 2
            decision = check_and_notify_heartbeat(
                heartbeat_status_path=args.heartbeat_status_path,
                episode_state_path=episode_state_path,
                sender=sender,
                threshold_seconds=args.threshold_seconds,
                log_path=args.log_path,
            )
        elif args.mode == "director-inbox":
            if args.threshold_seconds <= 0:
                print("ERROR: --threshold-seconds must be > 0 for --mode director-inbox", file=sys.stderr)
                return 2
            decision = check_and_notify_director_inbox(
                director_inbox_bin=args.director_inbox_bin,
                episode_state_path=episode_state_path,
                sender=sender,
                threshold_seconds=args.threshold_seconds,
                log_path=args.log_path,
            )
        else:
            decision = check_and_notify(
                status_path=args.status_path,
                episode_state_path=episode_state_path,
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
