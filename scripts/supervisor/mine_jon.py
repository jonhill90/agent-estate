#!/usr/bin/env python3
"""Extract JON'S OWN prompts from the harness transcripts. Nothing else.

WHY THIS EXISTS. Jon has asked repeatedly for his transcripts to be reviewed so
nothing he said is lost, and it kept not happening -- `mine-transcripts` existed
for days and was never run. His framing on 2026-08-16 is sharper than the
original ask:

    "i feel like we need to look at the transcripts. My prompts not yours so you
     can really determine context for this project."

HIS prompts. Not the assistant's, not tool output. The signal is what the human
asked for; everything else in a transcript is the estate talking to itself, and
it drowns the thing worth reading by three orders of magnitude.

NO MODEL IN THE EXTRACTION. Pulling human turns out of JSONL is filtering, not
reasoning -- Jon's standing rule is that AI is only for the reasoning part. A
model reads the OUTPUT of this; it does not do the gathering. That also makes
the result reproducible: same input, same output, no sampling.

WHAT COUNTS AS A REAL PROMPT. Transcripts store many things with role=user that
Jon never typed: tool results, system reminders, hook output, queued-command
echoes, and the watchdog's own cron prompts. Those are filtered by shape, and
the filter is deliberately visible so it can be argued with -- a silent filter
is how you lose the thing you were mining for.

Usage:
  mine_jon.py                    every prompt, oldest first
  mine_jon.py --since 2026-08-14
  mine_jon.py --grep theme       only prompts matching a pattern
  mine_jon.py --stats            counts per day, no bodies
"""
import argparse
import glob
import json
import os
import re
import sys

# Shapes that arrive as role=user but are not Jon typing. Each one is here
# because it was observed polluting the output, not from imagination.
NOISE = (
    "<system-reminder>",
    "[SYSTEM NOTIFICATION",
    "<task-notification>",
    "<command-name>",
    "<local-command-stdout>",
    "Director watchdog (hourly)",   # the watchdog's own cron prompt
    "QUOTA RESUME —",               # ditto
    "Caveat: The messages below",
    "This session is being continued",
)


def is_jon(text: str) -> bool:
    if not text or not text.strip():
        return False
    stripped = text.strip()
    for marker in NOISE:
        if marker in stripped[:400]:
            return False
    # A pasted image reference alone is not a prompt.
    if stripped.startswith("[Image") and len(stripped) < 200:
        return False
    return True


def harvest(paths):
    seen = set()
    out = []
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
                    # A list containing a tool_result is never a human turn.
                    if any(isinstance(b, dict) and b.get("type") == "tool_result"
                           for b in content):
                        continue
                    text = " ".join(b.get("text", "") for b in content
                                    if isinstance(b, dict) and b.get("type") == "text")
                else:
                    continue
                if not is_jon(text):
                    continue
                key = text.strip()[:300]
                if key in seen:          # the same prompt appears in forks/resumes
                    continue
                seen.add(key)
                out.append((rec.get("timestamp", ""), text.strip()))
    out.sort(key=lambda r: r[0])
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--since", default="")
    ap.add_argument("--grep", default="")
    ap.add_argument("--stats", action="store_true")
    ap.add_argument("--root", default=os.path.expanduser("~/.claude/projects"))
    args = ap.parse_args()

    prompts = harvest(glob.glob(os.path.join(args.root, "*", "*.jsonl")))
    if args.since:
        prompts = [p for p in prompts if p[0] >= args.since]
    if args.grep:
        rx = re.compile(args.grep, re.I)
        prompts = [p for p in prompts if rx.search(p[1])]

    if not prompts:
        # Absence is reported as a possible blindness, never as a clean result.
        print("NO PROMPTS MATCHED. Verify the instrument before believing this: "
              "run without --since/--grep and confirm it returns anything at all.",
              file=sys.stderr)
        return 2

    if args.stats:
        days = {}
        for ts, _ in prompts:
            days[ts[:10]] = days.get(ts[:10], 0) + 1
        for day in sorted(days):
            print(f"  {day}  {days[day]:>4} prompts")
        print(f"  TOTAL {len(prompts)}")
        return 0

    for ts, text in prompts:
        print(f"\n=== {ts[:16].replace('T', ' ')} ===")
        print(text)
    return 0


if __name__ == "__main__":
    sys.exit(main())
