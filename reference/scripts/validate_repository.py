#!/usr/bin/env python3
"""Repo-wide cross-file consistency checks that no single script can see on
its own.

agent-supervisor#105: `validate_laneview_state_maps` originally lived in
`agent-dotfiles` (the pre-#171-split repo) and was deleted there during the
Phase 1.5 split, correctly -- once `lanes.sh` left that repo, the check's own
`if not lanes_sh.is_file(): return []` guard would have made it pass silently
forever, a check that cannot see its subject. It was never re-homed to where
`lanes.sh` and the laneview renderers actually live, so the comments in
`scripts/supervisor/laneview/text.sh` and
`scripts/supervisor/laneview/opensessions.sh` kept citing a file that, in
this repo, never existed (`tui.sh` carried the same drifted state map
without even citing it). This module is that re-homing.

Run directly (`python3 scripts/validate_repository.py`) or via
`tests/supervisor/test_validate_repository.py`, which is what wires it into
`python -m unittest discover -s tests`.
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
LANES_SH = REPO_ROOT / "scripts" / "supervisor" / "lanes.sh"
LANEVIEW_DIR = REPO_ROOT / "scripts" / "supervisor" / "laneview"

# Every `state=<word>` assignment in lanes.sh is a terminal classification --
# no branch reassigns `state` after setting it, so this regex over the whole
# file is a complete enumeration of what lanes.sh can emit.
STATE_ASSIGNMENT_RE = re.compile(r"^\s*state=([a-z][a-z0-9-]*)\s*$", re.MULTILINE)

# A renderer names its states one of two ways today (laneview/README.md
# rule 4): a Python dict literal ("state-name": "glyph", as text.sh and
# tui.sh use) or a bash `case` statement (state-name) ..., as
# opensessions.sh uses. Both are tried against every renderer file --
# whichever shape it's actually written in is the one that matches.
DICT_KEY_RE = re.compile(r'"([a-z][a-z0-9-]*)"\s*:\s*"')
CASE_ARM_RE = re.compile(r"^\s*([a-z][a-z0-9|-]*)\)\s", re.MULTILINE)


def lanes_sh_states() -> set[str]:
    if not LANES_SH.is_file():
        return set()
    return set(STATE_ASSIGNMENT_RE.findall(LANES_SH.read_text()))


def renderer_scripts() -> list[Path]:
    # Mirrors laneview.sh's own `laneview/*.sh` glob (impl_list()) rather
    # than a list kept here -- a renderer added or removed shows up without
    # this file being edited in step with laneview/, the same reasoning
    # laneview.sh's own comment gives for re-enumerating the directory.
    if not LANEVIEW_DIR.is_dir():
        return []
    return sorted(LANEVIEW_DIR.glob("*.sh"))


def renderer_mapped_states(path: Path) -> set[str]:
    text = path.read_text()
    states = set(DICT_KEY_RE.findall(text))
    for arm in CASE_ARM_RE.findall(text):
        if arm == "*":
            continue
        states.update(arm.split("|"))
    return states


def validate_laneview_state_maps(warn=lambda msg: print(msg, file=sys.stderr)) -> list[str]:
    """Returns a list of human-readable errors, empty if every state
    `lanes.sh` can emit is named in every laneview renderer's state map.

    Mirrors the pre-#171-split contract: a state lanes.sh grows that a
    renderer's map does not name is an error, not a silent fallback --
    #231's `scrolled` gap and this issue's `broken`/`never-busy` gap are
    exactly the shape this exists to catch before it ships again.

    A renderer file this function cannot find a state map in at all (no
    dict-literal keys, no case-statement arms) gets a call to `warn`
    instead of an entry in the returned error list -- laneview/README.md
    rule 4 promises that a renderer legitimately built without one "stays
    mergeable and stays visible", not silently unchecked.
    """
    lanes_states = lanes_sh_states()
    if not lanes_states:
        # Same defensive shape the pre-split check used: nothing to check
        # against is not evidence of drift.
        return []

    errors = []
    for renderer in renderer_scripts():
        mapped = renderer_mapped_states(renderer)
        if not mapped:
            warn(
                f"validate_repository.py: {renderer.relative_to(REPO_ROOT)} "
                "has no state map this check can read -- unchecked, not "
                "confirmed correct"
            )
            continue
        missing = sorted(lanes_states - mapped)
        if missing:
            errors.append(
                f"laneview/{renderer.name} is missing state(s) lanes.sh "
                f"emits: {', '.join(missing)}"
            )

    return errors


def main() -> int:
    errors = validate_laneview_state_maps()
    for error in errors:
        print(f"validate_repository.py: {error}", file=sys.stderr)
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
