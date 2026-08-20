"""One-time repair for agent-supervisor#401's existing wrong results.

`reconcile_lane_completions.py`'s fix (see its own module docstring) stops
NEW stamps from claiming `results/<task_id>.md` and from asserting a
failure the sweep never actually observed. It does nothing for the 133
results already written before the fix landed -- those files are on disk,
not reachable by re-running the sweep (`list_delivered_open_tasks` no
longer returns a task once it is terminal), and the issue's acceptance
block explicitly requires them "repaired... where evidence survives"
(agent-supervisor#401, point 4).

This script is that one-time repair, not a service:

* Scope: every `results/*.md` containing the literal phrase this codebase
  used to stamp, `"failed, not completed"`.
* Evidence: the SAME cheap check `LaneCompletionReconciler._lane_log_pr_url`
  now runs before stamping -- does `lane-logs/<task_id>.log` name a pull
  request? A result with no lane-log, or a lane-log with no PR URL, is left
  untouched: this script repairs from evidence, it does not manufacture it.
* Never destroys the original. The file's original bytes are copied to a
  sibling `<task_id>.md.pre-401-repair` (skipped, not overwritten, if that
  sibling already exists -- this run already repaired it) before the
  canonical file is rewritten, so a human can always see exactly what the
  reconciler originally wrote.
* Idempotent: a task whose backup sibling already exists is reported
  `already_repaired` and left alone.

Deliberately does NOT touch the ledger DB. A results/*.md file is an
immutable historical artifact by convention (`Ledger._write_result`); the
DB row behind it is already terminal (`failed`) and changing that
retroactively means re-deriving `completed_at`, `result_sha256`, and every
downstream `events` row an already-observed completion implies -- a
separate, higher-risk piece of work than restoring the record a human or
`git log`-adjacent tool actually reads. `results/*.md` is what
agent-supervisor#401's own acceptance block checks, and what PR #400's
brief-feeding mechanism reads (see the issue).

Usage:
    python3 repair_401_reconcile_stamps.py --state-dir /path/to/state [--apply]

Without `--apply`, prints what WOULD change and does not touch anything on
disk -- the default is a dry run.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

_PR_URL_RE = re.compile(r'https://github\.com/[^\s",]+/pull/[0-9]+')
_WRONG_PHRASE = "failed, not completed"
_BACKUP_SUFFIX = ".pre-401-repair"


def find_pr_url(lane_logs_dir, task_id):
    log_path = lane_logs_dir / f"{task_id}.log"
    try:
        text = log_path.read_text(errors="replace")
    except OSError:
        return None
    match = _PR_URL_RE.search(text)
    return match.group(0) if match else None


def repair(results_dir, lane_logs_dir, *, apply):
    report = {"repaired": [], "already_repaired": [], "no_evidence": []}
    for result_path in sorted(results_dir.glob("*.md")):
        original = result_path.read_bytes()
        if _WRONG_PHRASE.encode("utf-8") not in original:
            continue
        task_id = result_path.stem
        backup_path = result_path.with_name(result_path.name + _BACKUP_SUFFIX)
        if backup_path.exists():
            report["already_repaired"].append(task_id)
            continue
        pr_url = find_pr_url(lane_logs_dir, task_id)
        if pr_url is None:
            report["no_evidence"].append(task_id)
            continue
        # Deliberately does NOT reproduce the original wording verbatim here
        # (only the backup sibling does): the literal phrase this repair
        # exists to retract must not survive in the canonical file, or a
        # human/tool reading `results/<task_id>.md` -- including
        # agent-supervisor#401's own acceptance script -- would still find
        # the false "failed, not completed" claim sitting right next to its
        # correction.
        redacted_original = original.decode("utf-8", errors="replace").replace(
            _WRONG_PHRASE, "[retracted -- see correction below]"
        )
        corrected = (
            "reconcile-lane-completions: complete (agent-supervisor#401 repair)\n\n"
            f"This task's lane-log names {pr_url}, cheap evidence available at stamp "
            "time that the original sweep below did not check before asserting a "
            "failure. Corrected verdict: complete, not failed. The original stamp's "
            f"exact wording is preserved verbatim at `{backup_path.name}`; the text "
            "below is that same stamp with only its retracted claim redacted:\n\n"
            "---\n"
            f"{redacted_original}\n"
        ).encode("utf-8")
        if apply:
            backup_path.write_bytes(original)
            backup_path.chmod(0o600)
            result_path.write_bytes(corrected)
            result_path.chmod(0o600)
        report["repaired"].append(task_id)
    return report


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--state-dir", required=True, type=Path)
    parser.add_argument("--apply", action="store_true", help="write changes; default is dry-run")
    args = parser.parse_args(argv)

    results_dir = args.state_dir / "results"
    lane_logs_dir = args.state_dir / "lane-logs"
    report = repair(results_dir, lane_logs_dir, apply=args.apply)

    mode = "APPLIED" if args.apply else "DRY RUN"
    print(f"[{mode}] repaired: {len(report['repaired'])} {sorted(report['repaired'])}")
    print(f"[{mode}] already_repaired: {len(report['already_repaired'])}")
    print(f"[{mode}] no_evidence (left untouched): {len(report['no_evidence'])}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
