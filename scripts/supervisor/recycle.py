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

# Written verbatim by a maintainer (or the supervisor itself) to state,
# explicitly, that the section was read and no lane is armed. Its presence
# is what makes an empty result trustworthy -- see the module docstring on
# `ChannelsSectionUnparseable` for why silence is never treated the same way.
EMPTY_CHANNELS_MARKER = "_No lanes armed._"

_REQUIRED_FIELDS = ("channel", "lane", "brief", "description")

# Table header cells, case-insensitively, mapped to `ArmedChannel` fields.
# The live brief's column headers ("Working from", "Task") read better to a
# human than the field names they carry; both the field name and the human
# label are accepted so either spelling parses.
_TABLE_HEADER_ALIASES = {
    "channel": "channel",
    "lane": "lane",
    "brief": "brief",
    "working from": "brief",
    "description": "description",
    "task": "description",
}


class ChannelsSectionMissing(ValueError):
    """Raised when a brief has no `## Live lanes and armed channels` section.

    Callers must treat this as a refusal reason, not as "zero lanes running"
    -- an absent section means the successor cannot know which channels to
    re-arm, which is exactly the case recycling must refuse.
    """


class ChannelsSectionUnparseable(ValueError):
    """Raised when the section exists but neither a table nor the explicit
    empty marker (`EMPTY_CHANNELS_MARKER`) could be found in it.

    This is not the same fact as "zero lanes armed". A section that reads
    "no lanes armed" in as many words and a section whose format the parser
    simply cannot read must not collapse into the same empty list -- the
    first is a legitimate idle state, the second is a parser that cannot do
    its job and must say so, not answer "nothing running" by default.
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


def _split_table_row(line: str) -> list[str]:
    """Split a GitHub-flavored markdown table row into its cell contents."""
    stripped = line.strip()
    if stripped.startswith("|"):
        stripped = stripped[1:]
    if stripped.endswith("|"):
        stripped = stripped[:-1]
    return stripped.split("|")


_SEPARATOR_ROW_RE = re.compile(r"^\|?(\s*:?-{1,}:?\s*\|)+\s*:?-{1,}:?\s*\|?$")


def _parse_table(lines: list[str]) -> list[ArmedChannel] | None:
    """Parse a markdown table starting at `lines[0]` into `ArmedChannel`s.

    Returns `None` if `lines` does not start with a table whose header
    names every required field -- callers fall through to "unparseable" in
    that case rather than guessing at a partial table.
    """
    if not lines or not lines[0].strip().startswith("|"):
        return None

    header_cells = [cell.strip().lower() for cell in _split_table_row(lines[0])]
    fields = [_TABLE_HEADER_ALIASES.get(cell) for cell in header_cells]
    if None in fields or set(fields) != set(_REQUIRED_FIELDS):
        return None

    if len(lines) < 2 or not _SEPARATOR_ROW_RE.match(lines[1].strip()):
        return None

    channels: list[ArmedChannel] = []
    for line in lines[2:]:
        stripped = line.strip()
        if not stripped:
            continue
        if not stripped.startswith("|"):
            break
        cells = _split_table_row(line)
        if len(cells) != len(fields):
            continue
        values = {field: cell.strip().strip("`").strip() for field, cell in zip(fields, cells)}
        channels.append(ArmedChannel(**{field: values[field] for field in _REQUIRED_FIELDS}))
    return channels


def parse_armed_channels(text: str) -> list[ArmedChannel]:
    """Parse the brief's `## Live lanes and armed channels` section.

    The section is a markdown table -- `| Channel | Lane | Working from |
    Task |` header, a separator row, then one row per armed channel (see
    `scripts/supervisor/README.md` for the documented format and the grep
    that reads it directly). A heading present whose body is exactly the
    literal `EMPTY_CHANNELS_MARKER` is a legitimate empty list -- "no lanes
    in flight, and the section says so explicitly". A heading present with
    neither a readable table nor that marker raises
    `ChannelsSectionUnparseable`: the parser could not do its job, and an
    empty list is not an honest report of that. A heading entirely absent
    raises `ChannelsSectionMissing`. All three are different facts and must
    not collapse into one.
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

    body = lines[start + 1 : end]

    if any(EMPTY_CHANNELS_MARKER in line for line in body):
        return []

    for i, line in enumerate(body):
        if line.strip().startswith("|"):
            table = _parse_table(body[i:])
            if table is not None:
                return table
            break

    raise ChannelsSectionUnparseable(
        f"'{CHANNELS_HEADING}' section present but no table or "
        f"'{EMPTY_CHANNELS_MARKER}' marker could be read from it"
    )


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
    channels section cannot be found or cannot be read -- an empty
    `channels` tuple is only ever returned when the section is present and
    explicitly, legibly empty.
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
    except ChannelsSectionUnparseable as error:
        return RecycleDecision(False, f"channels section unparseable: {error}")

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
