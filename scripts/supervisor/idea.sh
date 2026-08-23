#!/usr/bin/env bash
# Capture an idea into the corpus, fast, without derailing anything.
# agent-supervisor#341.
#
# WHY. Jon, 2026-08-18: "I want to be able to drop ideas in here without it
# derailing things. I need a safe place to drop ideas and make sure they get
# set as parameters / added to corpus."
#
# The failure this replaces: he said "(Backlog)" and got a repo investigation,
# a dependency audit and a 60-line issue. Twice. Dropping an idea should cost
# him one line and cost the estate one row -- the whole point is that having an
# idea does not start a project.
#
# WHAT IT DOES, and deliberately nothing more:
#   1. writes the idea verbatim to `prompts.text_raw` -- immutable, timestamped
#   2. writes ONE `items` row, kind=thought, weight=preference, status=open
#   3. prints the ids and exits
#
# It does NOT file an issue, dispatch a lane, or start research. Those are
# decisions; capture is not. `unacknowledged` will surface it later, which is
# the point of that view.
#
# NO MODEL IN THIS PATH. Judging an idea into parameters is the corpus's
# once-at-write-time model step and it runs in the batch pass, not here --
# because inference at capture time is what makes capture slow enough to avoid.
set -uo pipefail

STATE="${SUPERVISOR_STATE:-$HOME/.local/state/agent-dotfiles-supervisor}"
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTEXT=""
RAW=""
TAG="idea"

while [ $# -gt 0 ]; do
  case "$1" in
    --context) CONTEXT="${2:-}"; shift 2 ;;
    --raw)     RAW="${2:-}"; shift 2 ;;
    --tag)     TAG="${2:-idea}"; shift 2 ;;
    --) shift; break ;;
    *) break ;;
  esac
done

BODY="$*"
[ -n "$BODY" ] || BODY="$(cat)"   # accept stdin so it can be piped
if [ -z "${BODY// }" ]; then
  echo "usage: idea.sh [--context TEXT] [--tag TAG] <the idea>   (or pipe it on stdin)" >&2
  exit 2
fi

BODY="$BODY" RAW="$RAW" CONTEXT="$CONTEXT" TAG="$TAG" STATE="$STATE" python3 - <<'PY'
import os, sys, time, hashlib
sys.path.insert(0, os.path.join(os.path.dirname(os.environ.get("STATE","")), ""))
sys.path.insert(0, "/Users/jon/source/repos/Personal/agent-supervisor/scripts/supervisor")
from core import Ledger

body    = os.environ["BODY"].strip()
raw     = os.environ.get("RAW","").strip()
context = os.environ.get("CONTEXT") or (
    "Idea dropped by Jon for later. Captured verbatim at the moment it was said; "
    "NOT investigated, filed or dispatched -- capture is not a decision. It will "
    "surface in `unacknowledged` until someone judges it.")
led = Ledger(os.path.expanduser(os.environ["STATE"]))
now = int(time.time())
pid = "mp-" + hashlib.sha256((body + str(now)).encode()).hexdigest()[:16]

# text_raw IS JON'S VERBATIM WORDS. NOTHING ELSE GOES IN IT.
#
# This was got wrong and it mattered. Until 2026-08-19 this script wrote the
# CALLER'S paraphrase into text_raw -- an agent's summary of what Jon meant,
# stored in the field the whole corpus treats as "what he actually typed".
# Four such rows referred to "Jon" in the third person, which is precisely the
# signature a fidelity audit uses to detect agent text masquerading as his.
#
# The audit that found this measured ~8.3% of parameters as agent words
# misattributed to Jon, and warned the figure was a FLOOR. This script was
# actively adding to it, while the estate was diagnosing it.
#
# So: --raw carries his words and lands in text_raw. Everything the caller
# wants to say about them -- why it matters, what it relates to -- goes in
# `context`, which is what that column is for.
if not raw:
    raise SystemExit(
        "idea.sh: refusing to write an agent paraphrase into text_raw.\n"
        "  Pass Jon's own words with --raw, and put your framing in --context.\n"
        "  text_raw is the verbatim record; a summary there is a fabricated quote."
    )
led.record_prompt(pid, at=now, text_raw=raw, context=(context + "\n\nCaptured framing: " + body) if body else context,
                  session="idea-capture", source_file="idea.sh")

iid = "it-" + hashlib.sha256(f"{pid}\x000\x00{body}".encode()).hexdigest()[:16]
led.add_item(iid, prompt_id=pid, kind="thought", body=body[:900],
             weight="preference", status="open")

print(f"captured  prompt={pid}  item={iid}")
print(f"  tag={os.environ.get('TAG')}")
print("  status=open -> it will appear in `unacknowledged` until judged")
PY
