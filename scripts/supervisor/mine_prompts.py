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

STORING (agent-supervisor#303): --store writes matched rows into the
`prompts` table of the same ledger `cli.py` reads (`Ledger.record_prompt`).
This step is STILL no model -- it writes `text_raw` and a `context` derived
mechanically from transcript shape (the nearest preceding assistant turn in
the same file), never an interpretation of what the prompt meant. Turning a
prompt into `items` rows is the one place a model belongs (agent-supervisor's
brief for #303), and it happens once, later, reading from `prompts` --
never here.

Idempotent by construction: `prompt_id` is derived from the row's own
(source, at, text) shape, so re-running over the same transcripts hashes to
the same ids and `Ledger.get_prompt` short-circuits before any write.
`text_raw` is therefore written at most once per turn, ever.

Usage:
  mine_prompts.py                      every turn, oldest first
  mine_prompts.py --since 2026-08-14
  mine_prompts.py --grep theme
  mine_prompts.py --typed-only         heuristic: drop long/pasted blocks
  mine_prompts.py --stats              counts per day
  mine_prompts.py --json               machine-readable, for the memory pipeline
  mine_prompts.py --store              write matched rows into the ledger's `prompts` table
"""
import argparse
import datetime
import glob
import hashlib
import json
import os
import re
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # sibling `core.py`, only needed for --store

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


# `context` is required (NOT NULL, core.py's `prompts` table) but nothing
# here reasons about what a turn meant -- this is the honest label for a
# turn with no preceding assistant record to point at (first line of a
# file, or a transcript this extractor could not otherwise place), used
# instead of inventing a plausible-looking string. See `--store` docstring.
CONTEXT_UNDETERMINED = "[context undetermined: no prior assistant turn in this transcript file]"


def _assistant_text(rec):
    """Best-effort text of an assistant record -- same shape logic as the
    user branch below, mirrored. Used only to build `context`, never stored
    as a prompt itself."""
    msg = rec.get("message") or {}
    if msg.get("role") != "assistant":
        return None
    content = msg.get("content")
    if isinstance(content, str):
        return content.strip() or None
    if isinstance(content, list):
        text = " ".join(b.get("text", "") for b in content
                        if isinstance(b, dict) and b.get("type") == "text")
        return text.strip() or None
    return None


def harvest(paths, excludes):
    seen, out = set(), []
    for path in paths:
        try:
            fh = open(path, errors="ignore")
        except OSError:
            continue
        last_assistant_text = None
        with fh:
            for line in fh:
                is_user = '"role":"user"' in line or '"role": "user"' in line
                is_assistant = '"role":"assistant"' in line or '"role": "assistant"' in line
                if not is_user and not is_assistant:
                    continue
                try:
                    rec = json.loads(line)
                except ValueError:
                    continue
                msg = rec.get("message") or {}
                role = msg.get("role")
                if role == "assistant":
                    text = _assistant_text(rec)
                    if text:
                        last_assistant_text = text
                    continue
                if role != "user":
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
                    "context": last_assistant_text[:400] if last_assistant_text else CONTEXT_UNDETERMINED,
                })
    out.sort(key=lambda r: r["at"])
    return out


def _epoch(iso_ts):
    """Transcript timestamps are ISO 8601 UTC; `prompts.at` is INTEGER unix
    seconds, matching every other `at`/`created_at` column in this ledger.
    Returns None on anything unparseable -- `store_rows` skips such a row
    loudly rather than fabricate a time for it."""
    if not iso_ts:
        return None
    try:
        return int(datetime.datetime.fromisoformat(iso_ts.replace("Z", "+00:00")).timestamp())
    except ValueError:
        return None


def _prompt_id(row):
    """Deterministic id from the row's own shape (source, at, text) -- the
    same transcript turn hashes to the same id every run, which is what
    makes `store_rows` idempotent without a second, separate dedup table."""
    digest = hashlib.sha1(
        f"{row['source']}|{row['at']}|{row['text']}".encode("utf-8", errors="ignore")
    ).hexdigest()
    return f"mp-{digest[:16]}"


def store_rows(rows, ledger):
    """Write matched rows into `prompts`. Returns (written, skipped, no_time)
    counts. Idempotent: a row whose id `get_prompt` already finds is left
    alone, `text_raw` included -- see module docstring."""
    written = skipped = no_time = 0
    for row in rows:
        at = _epoch(row["at"])
        if at is None:
            no_time += 1
            print(f"SKIPPED (unparseable timestamp {row['at']!r}): "
                  f"{row['text'][:80]!r}", file=sys.stderr)
            continue
        prompt_id = _prompt_id(row)
        if ledger.get_prompt(prompt_id) is not None:
            skipped += 1
            continue
        ledger.record_prompt(
            prompt_id,
            at=at,
            text_raw=row["text"],
            context=row["context"],
            session=row["source"],
            source_file=row["source"],
        )
        written += 1
    return written, skipped, no_time


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
    ap.add_argument("--store", action="store_true",
                    help="write matched rows into the ledger's prompts table (idempotent)")
    ap.add_argument("--state-dir", default=os.environ.get(
        "AGENT_SUPERVISOR_STATE_DIR", os.path.expanduser("~/.local/state/agent-dotfiles-supervisor")),
                    help="ledger directory; same default and env var as cli.py")
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

    if args.store:
        from core import Ledger  # deferred: only --store needs the ledger at all
        ledger = Ledger(args.state_dir)
        written, skipped, no_time = store_rows(rows, ledger)
        print(f"stored: {written} written, {skipped} already present, "
              f"{no_time} skipped (unparseable timestamp)")
        return 0

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
