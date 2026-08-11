#!/usr/bin/env python3
"""Regenerate the `## Resume point` block in brief.md from live state.

WHY THIS IS A SCRIPT AND NOT A RULE
-----------------------------------
`brief.md` is what a cold session reads first, and its resume block is the part
a reader trusts most: shas, test counts, what is open. It went stale three times
on 2026-08-11 alone --

  08:04  block claimed `40a349a` / 301 tests while sections beneath were current
  11:39  block still said "state as of 08:11 UTC" -- 3.5 hours old -- and the
         file had been WRITTEN nine minutes earlier
  11:49  rewritten by hand at 11:42, stale again within ten minutes

-- each time because someone edited a section below it and left the heading
alone. The file already carried a prominent warning about the first occurrence
when the second and third happened. A warning that has been ignored three times
is not a control; it is a note about a control that does not exist.

So this block is now derived rather than remembered. Everything it prints is
read at run time; nothing is carried forward from the previous copy.

WHAT IT DELIBERATELY DOES NOT DO
--------------------------------
It does not touch anything outside the resume block. The rest of `brief.md` is
prose written by whoever knew something, and regenerating that would destroy the
judgement it holds. If the markers are missing, this refuses rather than guessing
where the block starts -- a mangled brief is worse than a stale one, because the
staleness is at least visible.

It does not run the test suites. Counting tests means running them, which takes
~40s and is the caller's decision, not this script's; pass the numbers in with
--tests if you have just run them, and the line says so honestly when you have
not.

Usage:
    python3 scripts/supervisor/refresh_brief_resume.py [--brief PATH] [--tests N]
    python3 scripts/supervisor/refresh_brief_resume.py --check   # non-zero if stale
"""

from __future__ import annotations

import argparse
import datetime
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

BEGIN = "<!-- resume-point:begin -->"
END = "<!-- resume-point:end -->"

REPOS = ("agent-dotfiles", "skills", "skills-private", "agent-evals")
REPO_ROOT = Path.home() / "source" / "repos" / "Personal"


def _run(args: list[str], cwd: Path | None = None) -> str | None:
    """Return stdout, or None if the command could not be run or failed.

    None means "could not measure" and is rendered as such. It is never
    collapsed into an empty string or a zero -- that conflation is the defect
    this estate has hit most often (agent-dotfiles #59, #92, #95, #100).
    """
    try:
        proc = subprocess.run(args, cwd=cwd, capture_output=True, text=True, timeout=30)
    except (OSError, subprocess.SubprocessError):
        return None
    if proc.returncode != 0:
        return None
    return proc.stdout.strip()


def repo_head(name: str) -> str:
    path = REPO_ROOT / name
    if not path.is_dir():
        # A missing checkout is not a repo at an unknown sha; say which it is.
        return "no local checkout"
    _run(["git", "fetch", "-q", "origin"], cwd=path)
    sha = _run(["git", "rev-parse", "--short", "origin/main"], cwd=path)
    return sha or "unreadable"


def open_items(repo: str, kind: str) -> str:
    """`kind` is "pr" or "issue". Returns a rendered list or an explicit failure."""
    # Request one MORE than we will show. A bare --limit renders a truncated
    # list identically to a complete one -- the same "incomplete presented as
    # complete" family as reporting a failed query as "none", which this
    # function is otherwise careful about.
    shown = 40
    out = _run(["gh", kind, "list", "-R", f"jonhill90/{repo}", "--state", "open",
                "--limit", str(shown + 1), "--json", "number,title"])
    if out is None:
        return "could not read (gh failed) — NOT the same as none"
    try:
        rows = json.loads(out)
    except ValueError:
        return "could not parse (gh returned malformed JSON) — NOT the same as none"
    if not rows:
        return "none"
    ordered = sorted(rows, key=lambda r: -r["number"])
    truncated = len(ordered) > shown
    listed = ", ".join(f"#{r['number']}" for r in ordered[:shown])
    return f"{listed} … and more (showing {shown})" if truncated else listed


def build_block(tests: str | None) -> str:
    now = datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    heads = {name: repo_head(name) for name in REPOS}
    lines = [
        BEGIN,
        f"## Resume point — generated {now}",
        "",
        "**Generated, not written.** `scripts/supervisor/refresh_brief_resume.py`",
        "derives this block from live state. Do not hand-edit it: run the script.",
        "Everything outside the markers is prose and is yours to edit.",
        "",
        "```",
    ]
    width = max(len(n) for n in REPOS)
    for name in REPOS:
        lines.append(f"{name.ljust(width)}  main {heads[name]}")
    if tests:
        lines.append("")
        lines.append(f"agent-dotfiles suite: {tests}")
    else:
        lines.append("")
        lines.append("agent-dotfiles suite: not run by this refresh — run it and pass --tests")
    lines.append("```")
    lines.append("")
    for repo in REPOS:
        lines.append(f"- **{repo}** — PRs: {open_items(repo, 'pr')} · issues: {open_items(repo, 'issue')}")
    lines.extend([
        "",
        "A `could not read` above means the query failed, which is **not** the same",
        "as nothing being open. Re-run before concluding the estate is quiet.",
        END,
    ])
    return "\n".join(lines)


def splice(text: str, block: str) -> str:
    """Replace the single marker-delimited block. Refuse anything ambiguous.

    Every check here is a REFUSAL, not a repair. This writes to the document a
    cold session reads first, and the failure modes below were found by an
    adversarial review that constructed each one:

    - two marker pairs: `re.sub` replaced BOTH with the same block. If they had
      ever held different content -- a bad merge, a copy-paste -- both were
      clobbered silently.
    - nested markers: the non-greedy match ran from the first BEGIN to the
      NEAREST END, replaced that, and left a dangling orphan END behind.
    - markers out of order (END before BEGIN): the old presence check passed,
      because both literals existed *somewhere*. The regex then matched nothing
      and returned the text byte-for-byte unchanged -- while the CLI printed
      "rewrote the resume block" and exited 0, and --check reported "current".
      **A malformed brief was actively certified as fresh.** That is strictly
      worse than the staleness this tool exists to fix: staleness was visible.

    Counting occurrences and checking order catches all four, including a marker
    pasted into a fenced code block as documentation -- which this repo's own
    docstrings and PR bodies do, so it is not hypothetical.
    """
    n_begin = text.count(BEGIN)
    n_end = text.count(END)
    if n_begin == 0 or n_end == 0:
        raise SystemExit(
            f"refresh_brief_resume: markers not found (BEGIN x{n_begin}, END x{n_end}).\n"
            f"Add {BEGIN} / {END} around the resume block first. Refusing to guess "
            f"where it starts -- a mangled brief is worse than a stale one."
        )
    if n_begin > 1 or n_end > 1:
        raise SystemExit(
            f"refresh_brief_resume: expected exactly one marker pair, found "
            f"BEGIN x{n_begin}, END x{n_end}. Refusing: replacing several blocks "
            f"with identical content destroys whichever one differed."
        )
    if text.index(BEGIN) > text.index(END):
        raise SystemExit(
            "refresh_brief_resume: END appears before BEGIN. Refusing: the "
            "previous version silently changed nothing here and reported success."
        )
    start = text.index(BEGIN)
    end = text.index(END) + len(END)
    return text[:start] + block + text[end:]


def _without_stamp(text: str) -> str:
    """The block minus its generation timestamp, for change detection."""
    return re.sub(r"^## Resume point — generated .*$", "## Resume point", text, flags=re.M)


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--brief", type=Path,
                    default=Path.home() / ".local/state/agent-dotfiles-supervisor/brief.md")
    ap.add_argument("--tests", default=None,
                    help='e.g. "399 python, 15 lanes, 26 watchdog, 0 failed"')
    ap.add_argument("--check", action="store_true",
                    help="exit 1 if the block would change; write nothing")
    args = ap.parse_args(argv)

    if not args.brief.is_file():
        print(f"refresh_brief_resume: no brief at {args.brief}", file=sys.stderr)
        return 2

    text = args.brief.read_text()
    updated = splice(text, build_block(args.tests))
    if args.check:
        # Compare with the generation timestamp stripped. The block embeds
        # "generated <when>", which differs on every run, so a naive equality
        # check reports STALE unconditionally and can never report current --
        # a check that always fires carries exactly as much information as one
        # that never does. Found by running it, not by reading it.
        if _without_stamp(updated) == _without_stamp(text):
            print("resume block is current")
            return 0
        print("resume block is STALE — run without --check to regenerate", file=sys.stderr)
        return 1
    # Atomic: write a sibling temp file and rename over the target. A partial
    # write here truncates the cold-start document, and nothing else on the
    # machine holds a copy of the prose it contains.
    tmp = args.brief.with_suffix(args.brief.suffix + ".tmp")
    tmp.write_text(updated)
    # Carry the target's existing mode onto the replacement. os.replace moves
    # the temp file's mode with it, so a brief at 0600 came back 0644 -- the
    # write silently widened permissions on the file it is meant to maintain.
    # #103 is hardening this state directory to 0700/0600; this would have
    # quietly undone the file half of that.
    shutil.copymode(args.brief, tmp)
    os.replace(tmp, args.brief)
    print(f"refresh_brief_resume: rewrote the resume block in {args.brief}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
