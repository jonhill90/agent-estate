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
  itemize_prompts.py --extract [--limit N] > candidates.json
  itemize_prompts.py --load judged.json
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
    ap.add_argument("--limit", type=int, default=None)
    ap.add_argument("--load", metavar="FILE")
    args = ap.parse_args()

    if not args.extract and not args.load:
        ap.error("one of --extract or --load is required")

    ledger = Ledger(args.state_dir)

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
