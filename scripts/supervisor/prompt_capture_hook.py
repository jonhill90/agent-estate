#!/usr/bin/env python3
"""`UserPromptSubmit` hook: capture and mechanically classify one prompt at
the moment it is submitted (agent-supervisor#687).

WHY THIS EXISTS. `Ledger.record_prompt()` (core.py) has existed since
agent-supervisor#280/#303, and `mine_prompts.py --store` has always been
able to call it -- but nothing called `mine_prompts.py --store` on a
schedule, so the corpus only grew when someone remembered to re-crawl
transcripts by hand. Capture stopped silently for four days (#687's own
measurement: 4165 prompts, newest four days old, zero unitemised backlog --
the JUDGING half was healthy the whole time; CAPTURE was the dead half) and
nothing noticed, because nothing watched the rate. This hook makes capture a
byproduct of submitting a prompt at all, so there is no "remember to run
the crawler" step left to forget.

WHAT THIS DOES, and just as importantly what it does NOT do:

1. Writes ONE `prompts` row via `Ledger.record_prompt` -- durable and
   mechanical, no model. `at` is the real submission instant (`time.time()`
   here is not a Workflow script and is the honest capture time -- more
   accurate than a transcript crawl's later re-derivation from a JSONL
   timestamp), `context` is the transcript's own last assistant turn
   (`mine_prompts.last_assistant_text`), or `mine_prompts.CONTEXT_UNDETERMINED`
   when there is none -- never invented (`context` is NOT NULL and
   load-bearing, core.py's own comment on that column).
2. If the prompt matches a known STRUCTURAL noise marker (dispatch-brief
   boilerplate, loop-tick cron text, harness-injected role=user shapes --
   the SAME marker lists `itemize_prompts.py`/`mine_prompts.py` already use,
   imported rather than re-typed so there is exactly one place these markers
   live), it immediately gets the standard dropped `items` row
   (kind=thought, weight=retracted, status=dropped, status_reason naming the
   marker) -- mechanical, no model, so the prompt leaves the itemisation
   queue for good instead of costing a model call to learn what a substring
   match already knew.
3. Anything else is left WITHOUT an `items` row -- exactly the shape
   `list_unitemised_prompts` / `itemize_prompts.py --extract` already expect
   and already surface (the issue's own "unitemised backlog" line). Judging
   what a real prompt MEANS needs a model, and per the brief's non-negotiable
   constraint that model runs ONCE, at write time -- but a hook is the wrong
   place to make that call synchronously: if the judging model call hangs,
   every prompt in the estate stalls waiting on this hook (#687's own listed
   failure mode). So judging stays exactly where it already lives --
   `itemize_prompts.py --extract`/`--load`, run separately -- and this hook's
   only job is to make sure that queue is never missing a row to work from.

NEVER BLOCKS, NEVER LEAKS TO STDOUT. Claude Code injects a `UserPromptSubmit`
hook's stdout into the model's own context on exit 0 -- printing anything
here would inject hook internals into every single conversation turn. This
prints nothing to stdout, ever, and always exits 0: a failure here must never
stall or reject a prompt (the same failure mode as #2 above, applied to
capture itself). Failures are NOT swallowed silently, though -- see
`_log_failure` below; "fail loudly" per the brief means visible somewhere a
human or a later check reads, not a bare `except: pass`.

IDEMPOTENT. `prompt_id` derives from `(session_id, prompt text)` -- the same
hook firing twice for one literal submission (a retry, a duplicate hook
registration) writes the identical id both times, so `Ledger.get_prompt`
short-circuits the second `record_prompt` and `Ledger.get_item` short-
circuits the second noise-drop `add_item`, the same "verify idempotency
after a load" contract `itemize_prompts.py --load` already carries. This is
a DIFFERENT id namespace (`hp-` vs `mine_prompts.py`'s `mp-`) than a
transcript crawl would derive for the same turn -- deliberately: backfilling
the four-day gap this issue reports is explicitly out of scope (a re-crawl
over that window may write a second `mp-...` row for a turn this hook also
captured live; reconciling the two is the backfill issue's problem, not
this hook's).

STALENESS. This hook fixes capture; it does not by itself make a dead hook
visible. That is `Ledger.PROMPT_VIEWS`' new `capture_health` view
(core.py) -- `cli.py prompts capture_health` reads `seconds_since_capture`
the same plain-SQL way `unacknowledged`/`open_questions` are already read
regularly, per the brief: "a signal belongs beside the views people already
read," not a new dashboard.

INPUT SHAPE (Claude Code's own `UserPromptSubmit` hook contract): one JSON
object on stdin --
  {"session_id": "...", "transcript_path": "...", "cwd": "...", "prompt": "..."}
Anything missing degrades gracefully (see `main` below) rather than raising.
"""
import hashlib
import json
import os
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))  # sibling core.py/mine_prompts.py/itemize_prompts.py

STATE_DIR = os.environ.get(
    "AGENT_SUPERVISOR_STATE_DIR", os.path.expanduser("~/.local/state/agent-dotfiles-supervisor")
)
FAILURE_LOG = os.path.join(STATE_DIR, "prompt-capture-hook-failures.log")

# agent-supervisor#693 (fix pass). `Ledger._locked()`'s flock is a blocking
# call with no timeout by default -- fine for the CLI and the Director loop,
# fatal here: this hook sits on every prompt submission, so a lock held by
# any other writer (a concurrent `itemize_prompts.py --load`, a process that
# died holding it) would otherwise hang the operator's terminal forever, past
# the point the `try/except` in `main()` ever gets a chance to fail open.
# `Ledger(..., lock_timeout=...)` bounds both the flock and sqlite's own
# busy-wait to this many seconds; `main()`'s existing broad except already
# catches the `LockTimeout` this raises and logs it as an ordinary failure.
LOCK_TIMEOUT_SECONDS = 2.0


def _log_failure(message):
    """Fail loudly without failing the prompt. stderr is Claude Code's own
    hook-debug channel (never injected into model context, unlike stdout);
    the log file makes the failure survive past a single `--debug` session
    so a later check (or a human reading `$STATE_DIR`) can see capture broke
    without needing to have been watching at the moment it did."""
    line = f"{int(time.time())} {message}\n"
    sys.stderr.write(f"prompt-capture-hook: {message}\n")
    try:
        os.makedirs(STATE_DIR, exist_ok=True)
        with open(FAILURE_LOG, "a") as fh:
            fh.write(line)
    except OSError:
        pass  # stderr above is the fallback; a log write failing is not this hook's to escalate further


def _prompt_id(session_id, text):
    """`hp-` (hook-prompt) namespace -- see module docstring for why this is
    deliberately distinct from `mine_prompts.py`'s `mp-` ids. Same session +
    identical text hashes to the same id, which is the idempotency contract
    a duplicate hook firing needs."""
    digest = hashlib.sha1(f"{session_id}|{text}".encode("utf-8", errors="ignore")).hexdigest()
    return f"hp-{digest[:16]}"


def capture(payload, ledger):
    """Do the actual work. Returns a short status string for tests; never
    raises for anything the caller should treat as "the prompt was fine, the
    hook just couldn't handle it" -- see `main`'s try/except for the one
    layer that actually enforces that."""
    from itemize_prompts import _item_id, noise_reason
    from mine_prompts import CONTEXT_UNDETERMINED, last_assistant_text

    text = (payload.get("prompt") or "").strip()
    if not text:
        return "empty-prompt: nothing to capture"

    session_id = payload.get("session_id") or "unknown-session"
    transcript_path = payload.get("transcript_path") or ""

    prompt_id = _prompt_id(session_id, text)
    if ledger.get_prompt(prompt_id) is None:
        context = last_assistant_text(transcript_path) if transcript_path else None
        context = (context or CONTEXT_UNDETERMINED)[:400]
        ledger.record_prompt(
            prompt_id,
            at=int(time.time()),
            text_raw=text,
            context=context,
            session=session_id,
            source_file=os.path.basename(transcript_path) if transcript_path else None,
        )
        wrote_prompt = True
    else:
        wrote_prompt = False

    reason = noise_reason(text)
    if reason is not None:
        item_id = _item_id(prompt_id, 0, f"noise:{reason}")
        if ledger.get_item(item_id) is None:
            ledger.add_item(
                item_id,
                prompt_id=prompt_id,
                kind="thought",
                body=f"[excluded: non-Jon text -- {reason}]",
                weight="retracted",
                status="dropped",
                status_reason=reason,
            )
            return f"captured (prompt {'written' if wrote_prompt else 'already present'}), dropped as noise: {reason}"
        return f"captured (prompt {'written' if wrote_prompt else 'already present'}), noise item already present"

    return f"captured (prompt {'written' if wrote_prompt else 'already present'}), left unitemised for judging"


def main():
    raw = sys.stdin.read()
    try:
        payload = json.loads(raw) if raw.strip() else {}
    except ValueError as exc:
        _log_failure(f"unparseable stdin payload: {exc}")
        return 0

    try:
        from core import Ledger

        ledger = Ledger(STATE_DIR, lock_timeout=LOCK_TIMEOUT_SECONDS)
        status = capture(payload, ledger)
    except Exception as exc:  # noqa: BLE001 -- a hook must never block or crash a prompt on any failure shape
        _log_failure(f"capture failed: {type(exc).__name__}: {exc}")
        return 0

    if os.environ.get("PROMPT_CAPTURE_HOOK_DEBUG"):
        sys.stderr.write(f"prompt-capture-hook: {status}\n")
    return 0  # never block, never reject a prompt -- see module docstring


if __name__ == "__main__":
    sys.exit(main())
