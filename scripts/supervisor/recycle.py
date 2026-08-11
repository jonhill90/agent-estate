"""Scheduled supervisor session recycling: decision core and thin actuator.

Split per `docs/SPEC.md` §15 -- the core decides, a transport adapter acts.
`decide_recycle` is pure and unit-tested without tmux: given a brief path, a
session-started timestamp, a max session age, and a max brief staleness, it
returns recycle / don't-recycle with a named reason. `respawn_supervisor` is
the thin actuator -- it knows nothing but "respawn this pane, then seed it
with this prompt" -- and is never exercised against a live pane in tests.

Recycling on a stale or missing brief converts a long transcript into a lost
one, which is strictly worse than not recycling: both fail closed.
"""

from __future__ import annotations

import re
import time
from dataclasses import dataclass
from pathlib import Path


CHANNELS_HEADING = "## Live lanes and armed channels"
_FIELD_RE = re.compile(r"^(channel|lane|brief|description):\s*(.*)$")
_REQUIRED_FIELDS = ("channel", "lane", "brief", "description")


class ChannelsSectionMissing(ValueError):
    """Raised when a brief has no `## Live lanes and armed channels` section.

    Callers must treat this as a refusal reason, not as "zero lanes running"
    -- an absent section means the successor cannot know which channels to
    re-arm, which is exactly the case recycling must refuse.
    """


@dataclass(frozen=True)
class ArmedChannel:
    channel: str
    lane: str
    brief: str
    description: str


@dataclass(frozen=True)
class RecycleDecision:
    allowed: bool
    reason: str
    channels: tuple[ArmedChannel, ...] = ()


def parse_armed_channels(text: str) -> list[ArmedChannel]:
    """Parse the brief's `## Live lanes and armed channels` section.

    The section is a sequence of `key: value` blocks separated by blank
    lines (see `scripts/supervisor/README.md` for the documented format and
    the grep that reads it directly). A heading present with no blocks under
    it is a legitimate empty list -- "no lanes in flight". A heading that is
    entirely absent raises `ChannelsSectionMissing`; the two are not the
    same fact and must not collapse into one.
    """
    lines = text.splitlines()
    try:
        start = next(i for i, line in enumerate(lines) if line.strip() == CHANNELS_HEADING)
    except StopIteration:
        raise ChannelsSectionMissing(f"no '{CHANNELS_HEADING}' section in brief") from None

    end = len(lines)
    for i in range(start + 1, len(lines)):
        if lines[i].startswith("## "):
            end = i
            break

    channels: list[ArmedChannel] = []
    block: dict[str, str] = {}

    def flush():
        if not block:
            return
        missing = [field for field in _REQUIRED_FIELDS if field not in block]
        if missing:
            raise ValueError(f"armed channel block missing field(s): {', '.join(missing)}")
        channels.append(ArmedChannel(**{field: block[field] for field in _REQUIRED_FIELDS}))
        block.clear()

    for line in lines[start + 1 : end]:
        stripped = line.strip()
        if not stripped or stripped == "---":
            flush()
            continue
        match = _FIELD_RE.match(stripped)
        if match is None:
            continue
        key, value = match.groups()
        block[key] = value.strip("`").strip()
    flush()
    return channels


def decide_recycle(
    *,
    brief_path,
    session_started_at: float,
    max_session_age_seconds: float,
    max_brief_staleness_seconds: float,
    now: float | None = None,
) -> RecycleDecision:
    """Decide whether the supervisor session may recycle now.

    Pure and tmux-free: takes a brief path, a "session started at" clock
    reading, a max session age, and a max brief staleness, and returns a
    `RecycleDecision` with a named reason. Every refusal path fails closed:
    a missing or stale brief refuses outright, and so does a brief whose
    channels section cannot be found -- an empty list is only ever returned
    when the section is present and genuinely empty.
    """
    now = time.time() if now is None else now
    brief_path = Path(brief_path)

    try:
        mtime = brief_path.stat().st_mtime
    except FileNotFoundError:
        return RecycleDecision(False, f"brief missing: {brief_path}")

    staleness = now - mtime
    if staleness > max_brief_staleness_seconds:
        return RecycleDecision(
            False,
            f"brief stale: {brief_path} last written {staleness:.0f}s ago "
            f"(max {max_brief_staleness_seconds:.0f}s)",
        )

    try:
        channels = tuple(parse_armed_channels(brief_path.read_text()))
    except ChannelsSectionMissing as error:
        return RecycleDecision(False, f"channels section missing: {error}")

    session_age = now - session_started_at
    if session_age < max_session_age_seconds:
        return RecycleDecision(
            False,
            f"session too young: age {session_age:.0f}s < max {max_session_age_seconds:.0f}s",
            channels,
        )

    return RecycleDecision(
        True,
        f"recycle allowed: session age {session_age:.0f}s >= max "
        f"{max_session_age_seconds:.0f}s, brief fresh ({staleness:.0f}s old)",
        channels,
    )


def respawn_supervisor(transport, *, target, tick_prompt: str) -> None:
    """Replace the supervisor pane's session with a fresh one.

    Thin actuator: all protocol knowledge stays inside `transport`. This
    respawns the pane and seeds it with the tick prompt -- nothing here
    decides *whether* to recycle, that is `decide_recycle`'s job. Never
    exercised against a live pane in tests; see `tests/supervisor/`.
    """
    transport.respawn_pane(target)
    transport.send_literal(target, tick_prompt)
