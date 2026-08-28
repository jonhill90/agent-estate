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
  itemize_prompts.py --drop-noise [--limit N]            strip agent/system text, and flag
                                                          candidate synthetic eval fixtures for
                                                          review, first (see below)
  itemize_prompts.py --reclassify-synthetic [--limit N]  flag already-itemised open items that
                                                          predate the #583 synthetic-fixture marker
                                                          for review (status=needs_review, #652)
  itemize_prompts.py --reclassify-director-pane [--limit N]
                                                          flag already-itemised open items whose
                                                          prompt was captured from a known
                                                          machine-only pane for review (#755)
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
# ONLY turn of its own throwaway transcript file, so it always lands here.
#
# agent-supervisor#652: this marker is a CANDIDATE signal, not proof, and
# #583's original PR (58ccee4) got that wrong -- it dropped straight to
# `status='dropped'` on this marker alone. Re-measured on the live ledger:
# a real operator directive ("Update the stale defect note in AGENTS.md
# referencing commit b00db9b", `it-b7e0b317eb2ce85b`) carries the identical
# marker, because it is the first turn of a session that opened with `/clear`
# -- an ordinary way this operator restarts mid-task, verified against the
# raw transcript (`cwd /Users/jon/source/repos/Personal/agent-tui`, branch
# `feat/secrets-storage-decision-101`). `context` does not separate "no prior
# assistant turn in THIS FILE" from "no prior turn at all" -- a `/clear`
# produces the former for a turn that is entirely real.
#
# `prompts.project` was tried as a second, corroborating signal and rejected:
# on the 37 items the pre-#652 code actually dropped, EVERY ONE -- both the
# confirmed-synthetic rows and the one confirmed-real one -- carries
# `project IS NULL`. It does not merely fail to add rows (the #583 PR's
# claim); it is unable to tell the real row from the synthetic ones within
# this exact batch, so keying a drop on "context marker AND empty project"
# would still have dropped the AGENTS.md directive. (Separately, `project`
# is NOT globally inert as the #583 PR claimed -- 811 of the 1020
# CONTEXT_UNDETERMINED rows across the whole corpus carry a non-empty
# `project` -- but non-emptiness elsewhere doesn't help distinguish real
# from synthetic on the specific rows this marker flags.)
#
# Content-shape was tried too and is the thing #583 already rejected: reading
# the 37 dropped bodies side by side, the real AGENTS.md directive is not
# distinguishable from the fixtures by phrasing, length, or specificity --
# it names a file and a commit exactly the way the fixtures name files.
#
# With no reliable second signal in the current schema, this marker alone
# can flag a CANDIDATE but must never itself decide `dropped` -- see
# `Ledger.flag_needs_review` / the `needs_review` view (core.py). A human or
# a later pass with an actual corroborating signal promotes a `needs_review`
# item onward from there (`Ledger.drop_item` if confirmed synthetic, or back
# to 'open' if confirmed real); this module only ever produces the
# unconfirmed intermediate state.
NEEDS_REVIEW_REASON = (
    f"agent-supervisor#652: candidate synthetic eval-scenario fixture, unconfirmed "
    f"(context={CONTEXT_UNDETERMINED!r}) -- context alone also matches a real post-/clear "
    f"operator turn; needs a second signal or human confirmation before dropping"
)
# Kept as an alias so a diff reader following #583's original name from the
# issue thread lands here; nothing in this module writes `status='dropped'`
# from it directly any more (see `NEEDS_REVIEW_REASON` above).
SYNTHETIC_REASON = NEEDS_REVIEW_REASON


def synthetic_provenance_reason(context):
    """Return NEEDS_REVIEW_REASON if `context` is the exact no-prior-turn
    marker mine_prompts.py stamps, else None. Structural: reads a column
    mine_prompts.py derived from transcript SHAPE, never the prompt's own
    text -- and, per agent-supervisor#652, a CANDIDATE reason only: the
    caller must route a match to `needs_review`, never straight to
    `dropped` (see this module's docstring above)."""
    return NEEDS_REVIEW_REASON if context == CONTEXT_UNDETERMINED else None


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


# agent-supervisor#755 part B: the Director's own pane (and, by the same
# reasoning, a lane's) has no fixed-boilerplate marker `noise_reason` above
# can key on -- a tick-loop status report or an escalation relayed by
# `director-route.sh`/`director-inbox.sh` reads exactly like an operator
# directive, the same "well-written fixture is indistinguishable from a
# real one by content alone" trap #583/#652 already document. The
# separation has to be structural: WHICH PANE submitted the prompt, from
# `prompts.tmux_pane_target` (`core_ledger_schema.py`'s
# `_migrate_prompts_pane_columns`, `prompt_capture_hook.py`'s
# `_resolve_tmux_pane`), never the prompt's own text.
#
# What the corpus itself showed before this was built (agent-supervisor#755
# brief's own required check, re-run against the live ledger 2026-08-28):
# every one of the 18 prompts captured from the Director's currently-live
# session (`session=34dacb27-dafc-4afd-b976-8aea16b68545`, pane `estate:1`)
# is either director-loop.sh's own fixed "Director decision required. Your
# complete brief is ..." tick template, or a status/escalation report about
# a specific issue/PR relayed into the pane by another script -- never
# Jon's own conversational voice. Two structural facts explain why, read
# directly from this tree rather than assumed:
#   1. Claude Code's own transcript metadata cannot tell a real keystroke
#      from a `tmux send-keys` injection -- both land with identical
#      `origin.kind: "human"` / `promptSource: "typed"` fields (measured
#      directly against the live session's own `.jsonl`). So no signal
#      *inside* a submitted prompt, structural or otherwise, can ever
#      distinguish the two -- the brief's own warning confirmed, not just
#      assumed.
#   2. `director-route.sh`/`director-inbox.sh`'s own doc comments establish
#      that Jon's real messages (Telegram replies) are deliberately NEVER
#      typed into the Director's pane as literal text: they are queued to
#      `director-inbox.jsonl` and drained by a tool call inside the
#      Director's own tick turn, never resubmitted as a fresh
#      `UserPromptSubmit` event. Every committed script that DOES write
#      into that pane (`director-loop.sh`, `director-route.sh`'s idle-nudge,
#      `watchdog.sh`) sends a fixed template, never Jon's raw words.
# Neither fact makes pane identity PROOF that a given prompt is
# machine-authored -- nothing stops Jon from `tmux attach`-ing to that
# session and typing into window 1 by hand, and no committed script here
# could ever observe that if he did. So, same discipline as
# `synthetic_provenance_reason` (#652): this is a CANDIDATE signal. A match
# routes to `needs_review`, never straight to `dropped` -- see
# `drop_noise`'s own comment.
#
# `known_targets` is deliberately NOT a hardcoded literal like `estate:1`.
# Window indices are not stable across a tmux server restart
# (director-loop.sh's own #466 comment: `director:@3` came back as `@35`),
# and the window NAME is configurable and has already been renamed once on
# this estate (Jon renamed the Director's own window away from the
# `supervisor` convention `LANES_SUPERVISOR_NAME` documents, to `director`,
# measured directly against the live tmux server 2026-08-28) -- a
# hardcoded target would go stale the same way a hardcoded `@id` already
# did for `director-loop.sh` itself. The allowlist is read from
# `$AGENT_SUPERVISOR_MACHINE_PANES` (comma-separated `session:window`
# entries, the exact `#{session_name}:#{window_index}` shape
# `lane-whoami.sh` already produces) so it is set once, by whoever operates
# this estate, from the SAME resolution `director-loop.sh`'s own
# `resolve_director_target` performs -- never guessed here.
def director_pane_reason(tmux_pane_target, known_targets=None):
    """Return a CANDIDATE reason string if `tmux_pane_target` names a known
    estate-managed (machine-only) pane, else None. See the module-level
    comment above this function for the corpus evidence and why this can
    never be proof by itself."""
    if not tmux_pane_target:
        return None
    if known_targets is None:
        raw = os.environ.get("AGENT_SUPERVISOR_MACHINE_PANES", "")
        known_targets = {entry.strip() for entry in raw.split(",") if entry.strip()}
    if tmux_pane_target in known_targets:
        return (
            f"agent-supervisor#755: candidate machine-only pane {tmux_pane_target!r} -- "
            f"structurally the Director's own (or a lane's) pane, not proof Jon never "
            f"typed here directly; needs human confirmation before dropping"
        )
    return None


def noise_reason(text):
    """Return the matched reason string, or None if `text` looks Jon-authored.
    First match wins; markers are structural, not stylistic -- this never
    reads tone or content, only fixed boilerplate shapes."""
    for marker, reason in NOISE_MARKERS:
        if marker in text:
            return reason
    return None


def drop_noise(ledger, limit=None):
    """Mechanically exclude agent/system-authored rows, and flag
    candidate-synthetic-fixture rows for review, from the itemisation queue.
    Returns (dropped, needs_review, kept) counts. No model call -- see
    module docstring. Idempotent via the same `add_item`/`get_item` check
    `load` uses: a prompt already itemised (dropped, needs_review, or
    otherwise) is left alone.

    Three independent structural checks, tried in order, first match wins:
    text-shape (`noise_reason`, agent-supervisor#313 -- boilerplate an agent
    or the harness produced) drops outright, because that marker IS proof --
    "That file is your complete brief." is never something Jon typed.
    Provenance-shape (`synthetic_provenance_reason`, agent-supervisor#583)
    and pane-identity (`director_pane_reason`, agent-supervisor#755 part B)
    are NOT proof by themselves (see each function's own comment) and so
    land the item in `needs_review`, not `dropped`. None of the three reads
    what the prompt is ABOUT."""
    dropped = needs_review = kept = 0
    for prompt in ledger.list_unitemised_prompts(limit=limit):
        noise = noise_reason(prompt["text_raw"])
        if noise is not None:
            item_id = _item_id(prompt["id"], 0, f"noise:{noise}")
            if ledger.get_item(item_id) is None:
                ledger.add_item(
                    item_id,
                    prompt_id=prompt["id"],
                    kind="thought",
                    body=f"[excluded: non-Jon text -- {noise}]",
                    weight="retracted",
                    status="dropped",
                    status_reason=noise,
                )
            dropped += 1
            continue
        candidate = synthetic_provenance_reason(prompt["context"])
        if candidate is None:
            candidate = director_pane_reason(prompt.get("tmux_pane_target"))
        if candidate is not None:
            item_id = _item_id(prompt["id"], 0, f"noise:{candidate}")
            if ledger.get_item(item_id) is None:
                ledger.add_item(
                    item_id,
                    prompt_id=prompt["id"],
                    kind="thought",
                    body=f"[flagged: candidate synthetic fixture -- {candidate}]",
                    weight="retracted",
                    status="needs_review",
                    status_reason=candidate,
                )
            needs_review += 1
            continue
        kept += 1
    return dropped, needs_review, kept


def reclassify_synthetic(ledger, limit=None):
    """Re-judge items itemised BEFORE `drop_noise` learned the #583 marker.
    New prompts being filtered at itemisation time does not clean rows
    already sitting in `unacknowledged` from a prior run -- this walks every
    currently-open item, applies the same `synthetic_provenance_reason`
    check to its originating prompt's `context`, and corrects the item's
    status in place. Returns (reclassified, kept) item counts. Idempotent:
    a second run finds nothing left with status='open' to reclassify.

    agent-supervisor#652: corrects to `status='needs_review'`
    (`Ledger.flag_needs_review`), never straight to `status='dropped'` --
    `synthetic_provenance_reason` is a candidate signal, not proof (see its
    own comment); this function's job is only to move a stale-classified
    OPEN item into the confirmation queue, same as a freshly-itemised one
    gets from `drop_noise`."""
    reclassified = kept = 0
    for item in ledger.list_open_items(limit=limit):
        reason = synthetic_provenance_reason(item["prompt_context"])
        if reason is None:
            kept += 1
            continue
        ledger.flag_needs_review(item["id"], reason)
        reclassified += 1
    return reclassified, kept


def reclassify_director_pane(ledger, limit=None):
    """Re-judge items itemised BEFORE `drop_noise` learned the pane-identity
    marker (agent-supervisor#755 part B) -- same shape as
    `reclassify_synthetic` (#583/#652) for the same reason: a prompt
    captured before `tmux_pane_target` existed, or before this check was
    wired in, is sitting in `unacknowledged`/`open` with no chance to have
    been flagged at itemisation time. Returns (reclassified, kept) item
    counts. Idempotent: a second run finds nothing left with status='open'
    to reclassify. Always lands on `needs_review`, never `dropped` -- see
    `director_pane_reason`'s own comment."""
    reclassified = kept = 0
    for item in ledger.list_open_items(limit=limit):
        reason = director_pane_reason(item.get("prompt_tmux_pane_target"))
        if reason is None:
            kept += 1
            continue
        ledger.flag_needs_review(item["id"], reason)
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
                     help="mechanically exclude agent/system-authored rows (no model), and flag "
                          "candidate-synthetic-fixture rows for review, before --extract")
    ap.add_argument("--reclassify-synthetic", action="store_true",
                     help="agent-supervisor#583/#652: correct already-itemised open items whose prompt "
                          "carries the synthetic eval-fixture context marker to status=needs_review")
    ap.add_argument("--reclassify-director-pane", action="store_true",
                     help="agent-supervisor#755: correct already-itemised open items whose prompt was "
                          "captured from a known machine-only pane ($AGENT_SUPERVISOR_MACHINE_PANES) "
                          "to status=needs_review")
    ap.add_argument("--limit", type=int, default=None)
    ap.add_argument("--load", metavar="FILE")
    args = ap.parse_args()

    if not any((args.extract, args.load, args.drop_noise, args.reclassify_synthetic,
                args.reclassify_director_pane)):
        ap.error("one of --extract, --drop-noise, --reclassify-synthetic, "
                  "--reclassify-director-pane or --load is required")

    ledger = Ledger(args.state_dir)

    if args.drop_noise:
        dropped, needs_review, kept = drop_noise(ledger, limit=args.limit)
        print(f"drop-noise: {dropped} rows excluded as non-Jon text, "
              f"{needs_review} rows flagged as candidate synthetic fixtures (needs_review), "
              f"{kept} rows kept as candidates")
        return 0

    if args.reclassify_synthetic:
        reclassified, kept = reclassify_synthetic(ledger, limit=args.limit)
        print(f"reclassify-synthetic: {reclassified} open items flagged needs_review as candidate "
              f"synthetic fixtures, {kept} open items left as-is")
        return 0

    if args.reclassify_director_pane:
        reclassified, kept = reclassify_director_pane(ledger, limit=args.limit)
        print(f"reclassify-director-pane: {reclassified} open items flagged needs_review as candidate "
              f"machine-only-pane prompts, {kept} open items left as-is")
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
