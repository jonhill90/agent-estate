#!/usr/bin/env python3
"""Extract the OPERATOR'S OWN turns from harness transcripts. Nothing else.

Replaces `mine_jon.py`, which had the operator baked into its filename, its
docstring and its noise filter. Jon: "mine_jon.py seems catered to me. dont
forget agnostisism and adapatbility and modularability." He is right -- an
extractor named after one person is a tool nobody else can run, and this estate
is meant to be portable to another operator, another harness, another machine.

WHAT IS OPERATOR-SPECIFIC AND WHAT IS NOT:
  - Universal (stays in code): transcripts store many role=user records the human
    never typed -- tool results, system reminders, task notifications, hook
    output, context-continuation summaries. Those are a property of the HARNESS,
    not of who is using it.
  - Site-specific (moves to config): the recurring prompts THIS estate injects on
    a schedule, e.g. its watchdog cron text. Another estate has different ones.
    These live in --exclude-file, defaulting to $SUPERVISOR_STATE/mine-exclude.txt,
    one substring per line, `#` for comments. Missing file = no site excludes,
    which is the correct default for a fresh clone.

NO MODEL IN THE EXTRACTION. Filtering JSONL by shape is not reasoning; the
operator's rule is that AI is only for the reasoning part. A model reads the
OUTPUT of this. That also makes it reproducible: same input, same output.

REPORTING ABSENCE: if nothing matches, this says so on stderr and exits 2 rather
than printing an empty, confident-looking result. An instrument that cannot see a
thing looks exactly like the thing being absent, which is this estate's most
expensive recurring failure.

KNOWN LIMITATION, measured 2026-08-16 and not yet fixed: of 3,205 extracted
turns, roughly 1,473 are PASTED documents rather than typed prompts -- skill
files, briefs, API docs. A grep for a word therefore hits pasted material. Use
--typed-only for a rough split (short, few newlines, no code fences); it is a
heuristic, not a guarantee, and separating typed from pasted properly is open
work. Do not treat this corpus as clean until that lands.

Usage:
  mine_prompts.py                      every turn, oldest first
  mine_prompts.py --since 2026-08-14
  mine_prompts.py --grep theme
  mine_prompts.py --typed-only         heuristic: drop long/pasted blocks
  mine_prompts.py --stats              counts per day
  mine_prompts.py --json               machine-readable, for the memory pipeline
"""
import argparse
import glob
import json
import os
import re
import sys

# Harness-level noise. These are shapes the HARNESS produces with role=user, not
# anything a human typed. Universal, so they stay in code.
HARNESS_NOISE = (
    "<system-reminder>",
    "[SYSTEM NOTIFICATION",
    "<task-notification>",
    "<command-name>",
    "<local-command-stdout>",
    "Caveat: The messages below",
    "This session is being continued",
)

FENCE = re.compile(r"^```", re.M)


def load_site_excludes(path):
    """Site-specific recurring prompts. Absent file is fine and means none."""
    if not path or not os.path.exists(path):
        return ()
    out = []
    with open(path, errors="ignore") as fh:
        for line in fh:
            line = line.strip()
            if line and not line.startswith("#"):
                out.append(line)
    return tuple(out)


def looks_typed(text):
    """Heuristic split of a typed prompt from a pasted document. Not exact."""
    return (
        len(text) < 600
        and text.count("\n") < 12
        and not FENCE.search(text)
        and not text.lstrip().startswith(("#", "---"))
    )


def is_operator_turn(text, excludes):
    if not text or not text.strip():
        return False
    head = text.strip()[:400]
    for marker in HARNESS_NOISE + excludes:
        if marker in head:
            return False
    if text.strip().startswith("[Image") and len(text.strip()) < 200:
        return False
    return True


def harvest(paths, excludes):
    seen, out = set(), []
    for path in paths:
        try:
            fh = open(path, errors="ignore")
        except OSError:
            continue
        with fh:
            for line in fh:
                if '"role":"user"' not in line and '"role": "user"' not in line:
                    continue
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                msg = rec.get("message") or {}
                if msg.get("role") != "user":
                    continue
                content = msg.get("content")
                if isinstance(content, str):
                    text = content
                elif isinstance(content, list):
                    if any(isinstance(b, dict) and b.get("type") == "tool_result"
                           for b in content):
                        continue
                    text = " ".join(b.get("text", "") for b in content
                                    if isinstance(b, dict) and b.get("type") == "text")
                else:
                    continue
                if not is_operator_turn(text, excludes):
                    continue
                key = text.strip()[:300]
                if key in seen:      # same turn reappears in forks and resumes
                    continue
                seen.add(key)
                out.append({
                    "at": rec.get("timestamp", ""),
                    "text": text.strip(),
                    "typed": looks_typed(text.strip()),
                    "source": os.path.basename(path),
                })
    out.sort(key=lambda r: r["at"])
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=os.path.expanduser("~/.claude/projects"),
                    help="transcript root; override for another harness")
    ap.add_argument("--exclude-file", default=os.path.join(
        os.environ.get("SUPERVISOR_STATE",
                       os.path.expanduser("~/.local/state/agent-dotfiles-supervisor")),
        "mine-exclude.txt"))
    ap.add_argument("--since", default="")
    ap.add_argument("--grep", default="")
    ap.add_argument("--typed-only", action="store_true")
    ap.add_argument("--stats", action="store_true")
    ap.add_argument("--json", action="store_true")
    args = ap.parse_args()

    rows = harvest(glob.glob(os.path.join(args.root, "*", "*.jsonl")),
                   load_site_excludes(args.exclude_file))
    if args.since:
        rows = [r for r in rows if r["at"] >= args.since]
    if args.typed_only:
        rows = [r for r in rows if r["typed"]]
    if args.grep:
        rx = re.compile(args.grep, re.I)
        rows = [r for r in rows if rx.search(r["text"])]

    if not rows:
        print("NO TURNS MATCHED. Verify the instrument before believing it: "
              "re-run with no --since/--grep/--typed-only and confirm it returns "
              "anything at all.", file=sys.stderr)
        return 2

    if args.json:
        json.dump(rows, sys.stdout, indent=2)
        return 0
    if args.stats:
        days, typed = {}, 0
        for r in rows:
            days[r["at"][:10]] = days.get(r["at"][:10], 0) + 1
            typed += 1 if r["typed"] else 0
        for day in sorted(days):
            print(f"  {day}  {days[day]:>4}")
        print(f"  TOTAL {len(rows)}   typed-looking {typed}   pasted-looking "
              f"{len(rows) - typed}")
        return 0
    for r in rows:
        print(f"\n=== {r['at'][:16].replace('T', ' ')} ===")
        print(r["text"])
    return 0


if __name__ == "__main__":
    sys.exit(main())
