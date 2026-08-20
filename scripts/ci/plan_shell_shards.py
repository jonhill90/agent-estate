#!/usr/bin/env python3
"""Bin-pack tests/supervisor/test_*.sh into N balanced CI shards (agent-supervisor#440).

CI took 22 minutes because validate.yml ran 801 Python tests plus 89 bash
integration suites *serially in a single Python test*
(test_shell_suites.py::test_shell_suites_pass). Measured directly against a
real CI run (jonhill90/agent-supervisor run 32415643900, instrumented with
per-suite timing prints): the 89 suites took 1306.1s total, concentrated
with a long tail -- the top 10 suites are 68% of the time, but the
remaining 79 are not negligible (32%, ~5.4s average each). That distribution
calls for balanced-by-time bin-packing across several parallel shards, not
carving out "the slow ones" into their own job while the rest stay serial.

Suites discovered on disk are always the source of truth (`glob`, not the
timing file) -- a newly added suite with no timing hint still gets a shard,
using the mean of known suites as its weight, so it is never silently
dropped from CI. `shell_suite_timings.json` is a *hint* for balancing, not a
membership list.
"""
from __future__ import annotations

import glob
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
REPO_ROOT = os.path.dirname(os.path.dirname(HERE))
SUITE_DIR = os.path.join(REPO_ROOT, "tests", "supervisor")
TIMINGS_FILE = os.path.join(SUITE_DIR, "shell_suite_timings.json")


def discover_suites():
    return sorted(os.path.basename(p) for p in glob.glob(os.path.join(SUITE_DIR, "test_*.sh")))


def load_hints():
    with open(TIMINGS_FILE) as f:
        return json.load(f)


def plan(shard_count, suites, hints):
    if not suites:
        raise ValueError(f"no test_*.sh found under {SUITE_DIR}")
    known = [t for t in hints.values() if isinstance(t, (int, float))]
    fallback = (sum(known) / len(known)) if known else 15.0

    # Longest-processing-time-first greedy bin packing: sort heaviest first,
    # always drop the next suite into the currently lightest shard. Simple,
    # deterministic, and close enough to optimal for this suite count.
    weighted = sorted(((hints.get(name, fallback), name) for name in suites), reverse=True)
    shards = [[] for _ in range(shard_count)]
    loads = [0.0] * shard_count
    for weight, name in weighted:
        i = loads.index(min(loads))
        shards[i].append(name)
        loads[i] += weight

    assigned = sum(len(s) for s in shards)
    if assigned != len(suites):
        raise AssertionError(f"bin-packing assigned {assigned} suites, expected {len(suites)}")
    if sorted(name for s in shards for name in s) != sorted(suites):
        raise AssertionError("bin-packing dropped or duplicated a suite name")

    matrix = [{"shard": i, "suites": shards[i]} for i in range(shard_count) if shards[i]]
    return {"total_suites": len(suites), "matrix": matrix, "loads": loads}


def main(argv=None):
    argv = argv if argv is not None else sys.argv[1:]
    shard_count = int(argv[0]) if argv else 5
    suites = discover_suites()
    hints = load_hints()
    result = plan(shard_count, suites, hints)
    print(json.dumps(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
