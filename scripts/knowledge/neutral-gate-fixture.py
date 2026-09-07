#!/usr/bin/env python3
"""Create a NEW private fixture baseline after recording the unchanged gate.
Never edits the input vault/index. Output is test data, never real memory.
Usage: python3 scripts/knowledge/neutral-gate-fixture.py NEW_DIRECTORY
"""
import datetime
import pathlib
import sys

root = pathlib.Path(sys.argv[1])
root.mkdir(exist_ok=False)
facts = root / 'agent' / 'facts'
facts.mkdir(parents=True)
now = datetime.datetime.now(datetime.timezone.utc).isoformat(timespec='seconds')
(facts / 'estate-turn-budget-ceiling.md').write_text(f'''---
type: project
title: A dispatched turn is capped at 120 tool calls
description: Current dispatched-turn budget and when to split a brief.
created: {now}
updated: {now}
source: Turn-budget reference
reviewer: process:reference-builder
memory_status: promoted
memory_revision: current-120
supersedes: previous-40
---

A dispatched lane turn is capped at 120 tool calls, raised from the earlier 40.
Briefs no longer need splitting at 40; split only when acceptance genuinely
needs more than 120 calls. Any guidance still citing 40 is superseded.
''')
(root / 'agent' / 'index.md').write_text('# Facts\n\n- [Turn budget](facts/estate-turn-budget-ceiling.md) — Current dispatched-turn budget.\n')
(root / 'BASELINE.md').write_text('Isolated synthetic acceptance data. New wording baseline; never compare its scores directly to the old gate. The 120-call value is not an operator instruction.\n')
print(root)
