#!/usr/bin/env python3
"""Turn `prompts` rows into `items` rows -- the ONE step in the prompt-corpus
pipeline (agent-supervisor#280, #303) that needs a model, and it runs ONCE
per prompt, at write time. Every read against `items`/`links` after this is
plain SQL (`Ledger.read_prompt_view`, `cli.py prompts <view>`); nothing
downstream ever calls a model again. That boundary is the operator's rule
and is not negotiable: "this at some point should be a tool not something
that relies on inference (ai)."

Two modes, deliberately separate, so the model boundary is an explicit file
this script hands off across rather than a network call it makes on your
own behalf:

  --extract        dump prompts with no `items` row yet, as JSON, for a
                    model to read and judge. Pure SQL read -- it picks
                    nothing, decides nothing.
  --load FILE       read a JSON array of judgements (produced BY A MODEL
                    from --extract's output, in the vocabulary below) and
                    write them via `Ledger.add_item` / `Ledger.link_items`.

--load's expected shape, one entry per JUDGED prompt (a prompt with nothing
worth recording is simply omitted -- there is no "no-op" item kind):
  {
    "prompt_id": "mp-...",
    "items": [
      {"kind": "parameter|question|directive|thought|correction",
       "body": "...", "weight": "hard|preference|retracted",
       "status": "open|acknowledged|acted|resolved|dropped",   # optional, default "open"
       "status_reason": "...",                                  # required if status=dropped
       "resolved_to": "..."}                                    # optional
    ]
  }
Item ids are derived deterministically from (prompt_id, index, body), so
re-running --load over the same judgements is a no-op the second time --
the same idempotency shape `mine_prompts.py --store` uses for `prompts`.

`links` (conflicts_with/supersedes/depends_on) are NOT produced here.
agent-supervisor#303's brief is explicit: "Do NOT make `conflicts` infer
anything. It reports recorded links only." A link is recorded separately,
by whoever (human or a later, deliberate pass) actually decided two items
relate -- never guessed from co-occurring in one --load batch.

Usage:
  itemize_prompts.py --drop-noise [--limit N]           strip agent/system text first (see below)
  itemize_prompts.py --extract [--limit N] > candidates.json
  itemize_prompts.py --load judged.json

FILTER NON-JON TEXT FIRST (agent-supervisor#313): the 3,584-row corpus mine_prompts.py
harvested is every role=user turn that survived HARNESS_NOISE, not just what Jon typed
-- dispatch briefs (`dispatch-claude-print.sh`'s "Read FILE and do exactly what it
says... That file is your complete brief."), loop-tick cron text, skill routers, and
tool-generated reports ("## Context Usage") are role=user by transcript shape but
agent/system-authored by origin. Paying a model to itemise text an agent wrote is pure
waste -- and it would also corrupt `unacknowledged`/`possibility_count` with judgements
about nobody's intent.

`--drop-noise` is the same shape as `claude-vault`'s strip-noise-at-import: a fixed list
of structural substrings (NOISE_MARKERS below), matched mechanically, no model involved
-- the same posture `mine_prompts.py`'s HARNESS_NOISE tuple already takes one layer up.
A match gets an `items` row immediately (kind='thought', weight='retracted',
status='dropped', status_reason naming which marker matched) so it leaves the
`list_unitemised_prompts` queue for good and never reaches `unacknowledged` or
`live_parameters` -- `dropped`/`retracted` are excluded from both by the views'
own WHERE clauses (core.py). This is NOT a judgement that the text is meaningless;
it is a record of why nobody paid a model to read it, kept in the same table results
live in rather than silently vanishing from `--extract`'s output.
"""
import argparse
import hashlib
import json
import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from core import Ledger  # noqa: E402


def _item_id(prompt_id, index, body):
    digest = hashlib.sha1(f"{prompt_id}|{index}|{body}".encode("utf-8", errors="ignore")).hexdigest()
    return f"it-{digest[:16]}"


# Structural markers of text an AGENT or the HARNESS produced, not Jon.
# Substring match against the raw prompt, same shape as mine_prompts.py's
# HARNESS_NOISE. Each entry is (marker, reason) -- the reason is recorded on
# the dropped item so a later reviewer can see WHY, not just THAT it was cut.
NOISE_MARKERS = (
    ("That file is your complete brief.", "dispatch brief (claude-print contract line)"),
    ("do exactly what it says", "dispatch brief (send-keys/claude-print template)"),
    ("carry it out exactly as written", "dispatch brief (alternate template wording)"),
    ("Supervisor loop tick.", "loop-tick cron text (scripts/supervisor/loop-tick.md)"),
    ("## Context Usage", "harness-generated /context report, not typed"),
    ("Base directory for this skill:", "skill router boilerplate"),
    ("Your turn ended without shipping.", "watchdog/lane-done nudge text, scripted"),
    ("I'm answering on Jon's behalf", "agent explicitly relaying, not Jon"),
    ("Hill90 lane supervisor.", "Hill90 loop-tick template"),
    ("Hill90 sweep. Jon is", "Hill90 loop-tick template"),
)


def noise_reason(text):
    """Return the matched reason string, or None if `text` looks Jon-authored.
    First match wins; markers are structural, not stylistic -- this never
    reads tone or content, only fixed boilerplate shapes."""
    for marker, reason in NOISE_MARKERS:
        if marker in text:
            return reason
    return None


def drop_noise(ledger, limit=None):
    """Mechanically exclude agent/system-authored rows from the itemisation
    queue. Returns (dropped, kept) counts. No model call -- see module
    docstring. Idempotent via the same `add_item`/`get_item` check `load`
    uses: a prompt already itemised (dropped or otherwise) is left alone."""
    dropped = kept = 0
    for prompt in ledger.list_unitemised_prompts(limit=limit):
        reason = noise_reason(prompt["text_raw"])
        if reason is None:
            kept += 1
            continue
        item_id = _item_id(prompt["id"], 0, f"noise:{reason}")
        if ledger.get_item(item_id) is not None:
            continue
        ledger.add_item(
            item_id,
            prompt_id=prompt["id"],
            kind="thought",
            body=f"[excluded: non-Jon text -- {reason}]",
            weight="retracted",
            status="dropped",
            status_reason=reason,
        )
        dropped += 1
    return dropped, kept


def extract(ledger, limit=None):
    return ledger.list_unitemised_prompts(limit=limit)


def load(judged, ledger):
    """Write judgements. Returns (written, skipped) item counts."""
    written = skipped = 0
    for entry in judged:
        prompt_id = entry["prompt_id"]
        for index, item in enumerate(entry.get("items", [])):
            item_id = _item_id(prompt_id, index, item["body"])
            if ledger.get_item(item_id) is not None:
                skipped += 1
                continue
            ledger.add_item(
                item_id,
                prompt_id=prompt_id,
                kind=item["kind"],
                body=item["body"],
                weight=item["weight"],
                status=item.get("status", "open"),
                status_reason=item.get("status_reason"),
                resolved_to=item.get("resolved_to"),
            )
            written += 1
    return written, skipped


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--state-dir", default=os.environ.get(
        "AGENT_SUPERVISOR_STATE_DIR", os.path.expanduser("~/.local/state/agent-dotfiles-supervisor")))
    ap.add_argument("--extract", action="store_true")
    ap.add_argument("--drop-noise", action="store_true",
                     help="mechanically exclude agent/system-authored rows (no model) before --extract")
    ap.add_argument("--limit", type=int, default=None)
    ap.add_argument("--load", metavar="FILE")
    args = ap.parse_args()

    if not args.extract and not args.load and not args.drop_noise:
        ap.error("one of --extract, --drop-noise or --load is required")

    ledger = Ledger(args.state_dir)

    if args.drop_noise:
        dropped, kept = drop_noise(ledger, limit=args.limit)
        print(f"drop-noise: {dropped} rows excluded as non-Jon, {kept} rows kept as candidates")
        return 0

    if args.extract:
        json.dump(extract(ledger, limit=args.limit), sys.stdout, indent=2)
        return 0

    with open(args.load) as fh:
        judged = json.load(fh)
    written, skipped = load(judged, ledger)
    print(f"itemized: {written} items written, {skipped} already present")
    return 0


if __name__ == "__main__":
    sys.exit(main())
