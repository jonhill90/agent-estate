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
  itemize_prompts.py --drop-noise [--limit N]            strip agent/system text and synthetic
                                                          eval fixtures first (see below)
  itemize_prompts.py --reclassify-synthetic [--limit N]  correct already-itemised open items that
                                                          predate the #583 synthetic-fixture marker
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
from mine_prompts import CONTEXT_UNDETERMINED  # noqa: E402


def _item_id(prompt_id, index, body):
    digest = hashlib.sha1(f"{prompt_id}|{index}|{body}".encode("utf-8", errors="ignore")).hexdigest()
    return f"it-{digest[:16]}"


# agent-supervisor#583: eval-scenario fixture prompts (`skills/*/references/
# eval-scenario*/`) were being itemised as if Jon had typed them -- `weight=
# hard, status=open` directives naming files (`finalize.py`, `reconcile.py`)
# that exist nowhere outside the fixture. They are DELIBERATELY written to
# read exactly like a real operator directive, so no heuristic on how the
# text reads can separate them (issue #583's own point) -- the separation
# has to be structural, keyed on where the text came from, never on topic.
#
# The structural fact: mine_prompts.py stamps `context` with
# CONTEXT_UNDETERMINED when a transcript turn has no prior assistant turn to
# derive context from. A synthetic eval fixture is invoked as the FIRST and
# ONLY turn of its own throwaway transcript file, so it always lands here;
# a real operator prompt sits inside a longer working session and almost
# always carries real prior-turn context instead.
#
# Measured against the live corpus (issue #583, `unacknowledged`, 198 rows):
# this marker flags 37, a 12-of-12 hand-read sample of the 37 is entirely
# fixture filenames, and it selects ZERO of three known-real operator
# directives (a resource-usage directive, the worktree-sweep question
# `it-332b79a4dd086f0a`, and a transcript-mining directive).
#
# `prompts.project` (empty-string session cwd) was tried alongside `context`
# and is INERT on this population: every row `context` catches already has
# empty `project` too, so it adds nothing -- and unlike `context`, nothing
# in this pipeline (`mine_prompts.py record_prompt`) writes `project` for a
# newly-mined prompt, so keying on it would silently stop catching anything
# the day today's already-populated rows age out. `context` is written at
# mine time by code in this repo; that is the property this filter needs
# and the only one of the two that has it.
#
# This is a FLOOR, not a full classifier: an eval fixture delivered inside a
# longer transcript would carry real context and be missed. Extending it to
# a filename-existence check was tried and rejected (issue #583 comment) --
# it has a 100% false-positive rate, because real directives routinely name
# files in repos this estate's four-repo scan can't see, and briefs living
# under `~/.local/state/estate-loop/` look exactly like directives about a
# file that doesn't exist.
SYNTHETIC_REASON = f"agent-supervisor#583: synthetic eval-scenario fixture (context={CONTEXT_UNDETERMINED!r})"


def synthetic_provenance_reason(context):
    """Return SYNTHETIC_REASON if `context` is the exact no-prior-turn marker
    mine_prompts.py stamps, else None. Structural: reads a column mine_prompts.py
    derived from transcript SHAPE, never the prompt's own text."""
    return SYNTHETIC_REASON if context == CONTEXT_UNDETERMINED else None


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
    """Mechanically exclude agent/system-authored AND synthetic-fixture rows
    from the itemisation queue. Returns (dropped, kept) counts. No model call
    -- see module docstring. Idempotent via the same `add_item`/`get_item`
    check `load` uses: a prompt already itemised (dropped or otherwise) is
    left alone.

    Two independent structural checks, tried in order, first match wins:
    text-shape (`noise_reason`, agent-supervisor#313 -- boilerplate an agent
    or the harness produced) and provenance-shape (`synthetic_provenance_reason`,
    agent-supervisor#583 -- an eval-scenario fixture with no real prior-turn
    context). Neither reads what the prompt is ABOUT."""
    dropped = kept = 0
    for prompt in ledger.list_unitemised_prompts(limit=limit):
        reason = noise_reason(prompt["text_raw"])
        excluded_as = "non-Jon text"
        if reason is None:
            reason = synthetic_provenance_reason(prompt["context"])
            excluded_as = "synthetic fixture"
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
            body=f"[excluded: {excluded_as} -- {reason}]",
            weight="retracted",
            status="dropped",
            status_reason=reason,
        )
        dropped += 1
    return dropped, kept


def reclassify_synthetic(ledger, limit=None):
    """Re-judge items itemised BEFORE `drop_noise` learned the #583 marker.
    New prompts being filtered at itemisation time does not clean rows
    already sitting in `unacknowledged` from a prior run -- this walks every
    currently-open item, applies the same `synthetic_provenance_reason`
    check to its originating prompt's `context`, and corrects the item's
    status in place (`Ledger.drop_item`) rather than deleting or duplicating
    it. Returns (reclassified, kept) item counts. Idempotent: a second run
    finds nothing left with status='open' to reclassify."""
    reclassified = kept = 0
    for item in ledger.list_open_items(limit=limit):
        reason = synthetic_provenance_reason(item["prompt_context"])
        if reason is None:
            kept += 1
            continue
        ledger.drop_item(item["id"], reason)
        reclassified += 1
    return reclassified, kept


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
                     help="mechanically exclude agent/system-authored and synthetic-fixture rows "
                          "(no model) before --extract")
    ap.add_argument("--reclassify-synthetic", action="store_true",
                     help="agent-supervisor#583: correct already-itemised open items whose prompt "
                          "carries the synthetic eval-fixture context marker to status=dropped")
    ap.add_argument("--limit", type=int, default=None)
    ap.add_argument("--load", metavar="FILE")
    args = ap.parse_args()

    if not args.extract and not args.load and not args.drop_noise and not args.reclassify_synthetic:
        ap.error("one of --extract, --drop-noise, --reclassify-synthetic or --load is required")

    ledger = Ledger(args.state_dir)

    if args.drop_noise:
        dropped, kept = drop_noise(ledger, limit=args.limit)
        print(f"drop-noise: {dropped} rows excluded (non-Jon text or synthetic fixture), "
              f"{kept} rows kept as candidates")
        return 0

    if args.reclassify_synthetic:
        reclassified, kept = reclassify_synthetic(ledger, limit=args.limit)
        print(f"reclassify-synthetic: {reclassified} open items dropped as synthetic fixtures, "
              f"{kept} open items left as-is")
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
