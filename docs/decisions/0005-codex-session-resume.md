---
type: Decision
description: Decision record: Codex lanes are resumable after a tmux server loss, the same way Claude lanes already were.
generated:
  at: 2026-08-23T12:25:06-04:00
---

# 0005 — Codex lanes are resumable, the same as Claude's

`2026-08-23` (scoping pass on "the daemon is meant to be genuinely
multi-harness and today it is not"), `agent-supervisor#446` (adjacent, see
"What this PR is not" below).

## Decision

`harness_session.py`'s `resolve()` now finds a codex lane's own session id
from codex's real on-disk rollout files
(`~/.codex/sessions/<Y>/<M>/<D>/rollout-<ts>-<uuid>.jsonl`), the same way it
already found Claude's from `~/.claude/projects/*/<uuid>.jsonl`.
`harness/codex.sh` records `HARNESS_RESUME_CMD='codex resume %s'` and
`HARNESS_TRANSCRIPT_GLOB` (its own on-disk shape, for the "is this
transcript really still there" check `restore.sh` already runs before
trusting a recorded id). `HARNESS_TRANSCRIPT_GLOB` is a new,
harness-registry field; `harness/claude.sh` grew one too, to replace what
was previously a Claude-only literal path hardcoded inside `restore.sh`
itself.

## Why this was scoped this way, not as "fix #446"

The task that opened this PR named a specific, real, previously-filed
defect (agent-supervisor#446: `verified_type`/`verified_preclear` cannot
read a codex pane's input box) as "the codex adapter gap." Before writing
any code, the brief's own instruction was followed: check what the
existing abstraction already provides.

**#446, read against the CURRENT `dispatch.sh`, is very likely already
moot.** `harness/codex.sh` sets `HARNESS_LAUNCH_TAKES_PROMPT=1`
unconditionally (added by #451, merged after #446 was filed) — every
codex dispatch, fresh lane or reused one, respawns the codex process with
the brief folded into its own launch argv (`codex -a never
-s danger-full-access '<brief>'`) and verifies it through
`verified_launch_prompt`, which checks codex's own failure/blocked
signatures on the pane, never `input_box_state`/`input_box_text`. Reading
`dispatch.sh`'s control flow directly: `PROMPT_IN_LAUNCH` gates every call
to `verified_preclear`/`verified_type`/`verified_submit` off for a harness
with `H_LAUNCH_TAKES_PROMPT` set, and codex is the only harness that sets
it. `tests/supervisor/test_dispatch.sh`'s own codex coverage (issues #30,
#255, #256) confirms this: every codex case in that suite goes through
`codex-launch-prompt`/`verified_launch_prompt`, never through
`verified_type`. The Claude-only input-box reader #446 named is a real,
narrower gap (it still leaves `lanes.sh`'s own `--unsent` classification
under-informed for a codex lane holding unsubmitted text outside a
dispatch — see "What this does not fix" below) — but it is not, as far as
this pass could reproduce, currently reachable from a real `dispatch.sh`
codex dispatch. Closing it as this PR's headline finding would have fixed
a path nothing in the shipped code walks into anymore.

**What IS live, reproduced directly, on every codex dispatch today:**
`dispatch.sh` calls `harness_session.py` after every successful dispatch,
regardless of harness, to record `harness_session_id` — and that module's
`resolve()` refused unconditionally for any harness but `claude`
("no session resolver for harness 'codex' -- only claude is
implemented"). Non-fatal (the dispatch still succeeds), but the effect
compounds silently: **no codex lane in this estate has ever had a
resumable session id recorded**, which means `restore.sh` — after any
tmux server loss — reports every codex lane `UNRECOVERABLE` unconditionally,
regardless of whether codex itself still has the conversation. Codex does:
verified live (isolated tmux socket, private `TMUX_TMPDIR`, real
`codex-cli 0.149.0`, 2026-08-23), `codex resume <session-id>` reopens the
exact prior conversation — earlier turns and all — with no picker shown.
The capability existed on the vendor's side the whole time; nothing in
this repository had ever looked for the id it needed to use it.

This is the more central instance of "the daemon is meant to be genuinely
multi-harness and today it is not, for codex specifically": the corpus's
own standing requirement (`it-f7a2bdf48192eb46`,
`it-087332d39903dda8` — persistent, visible, steerable sessions, for every
harness, not just Claude) names *persistence* as the property that
matters, and a lane that cannot survive a tmux server loss is not a
persistent session in the sense that requirement means — it is a
best-effort one that happens to work until the tmux server dies, same as
if it had never been dispatched with a durable identity at all.

## What the existing abstraction already provided

Everything except the session-id resolver and the two new registry fields
was already correct and needed no change:

- `restore.sh`'s own refusal-first control flow (missing id / no resume
  dialect / transcript not on disk / missing project dir → `UNRECOVERABLE`,
  never a guess) applies to codex exactly as written, once it has a real
  id and a real glob to check.
- `harness-registry.sh`'s parallel-array loader pattern
  (`H_RESUME_CMD`, now `H_TRANSCRIPT_GLOB`) needed one more field, not a
  new mechanism.
- `dispatch.sh`'s unconditional, non-fatal call into `harness_session.py`
  after every dispatch needed no change at all — it was already
  harness-agnostic; the resolver behind it was not.
- Codex's own CLI already ships the resume dialect this needed
  (`codex resume [SESSION_ID] [PROMPT]`) and its own on-disk session
  format already carries everything the resolver needs (`session_id`,
  `cwd`, `timestamp`) in a single first-line `session_meta` record — an
  easier read than Claude's own transcript format, which scatters the
  equivalent facts across many lines with no single authoritative header.

## What this does not fix

- **#446 itself** is left open, named here rather than closed, because
  this pass did not reproduce it against the current `dispatch.sh` and
  closing an issue on "could not reproduce" without a live codex dispatch
  to confirm against would be a claim this pass cannot back. It should be
  re-verified (or explicitly closed with this doc's reasoning cited)
  rather than carried forward silently.
- **`lanes.sh`'s own `--unsent` classification** calls
  `input_box_state` directly, independent of `dispatch.sh`'s send
  verification, and is Claude-only for the same underlying reason #446
  named — a codex lane holding unsubmitted typed text (outside of a
  dispatch, e.g. from a human or a watchdog nudge) is not detected as
  `unsent` today. Not fixed here: `lanes.sh` resolves its harness index
  (`hidx`) later in its own per-lane loop than the point `box` is read,
  so wiring it through means restructuring that ordering, not just
  threading one more registry field — a distinct, separately-scoped
  change.
- **`daemon/internal/agent/codex.go`** (the Go `supervisord` binary) is a
  separate, already-complete codex adapter (`#497`/`#499`) that runs
  `codex exec`/`codex exec resume` as a one-shot subprocess per turn, not
  a tmux lane. It was not touched by this PR and is not "the codex
  adapter gap" this PR closes — see this doc's own note that the corpus's
  persistent-session requirement is explicitly about tmux-lane behavior,
  not this binary's.

## Verified

- `tests/supervisor/test_harness_session.py` (new): 14 cases for the codex
  resolver, mutation-checked both directions (reverting `resolve()`'s
  dispatch to the old claude-only check fails 3 of them).
- `tests/supervisor/test_restore.sh`: 43/43, including two new codex cases
  (a codex lane with a resolved id restores via `codex resume`, not
  `claude --resume`; a codex lane with no matching rollout file on disk is
  still refused, not resumed against nothing) — mutation-checked by
  removing `HARNESS_RESUME_CMD`/`HARNESS_TRANSCRIPT_GLOB` from
  `harness/codex.sh` and confirming those two cases go red.
- `tests/supervisor/test_dispatch.sh`: 531/531, unaffected.
- `tests/supervisor/test_lanes.sh`: 182/182, unaffected.
- `tests/supervisor/test_send.sh`: 59/59, unaffected (this PR does not
  touch `send.sh`).
- `scripts/validate_repository.py`: clean.
- Live, against a real `codex-cli 0.149.0` process on an isolated tmux
  socket (never a live lane): a killed pane's prior conversation
  (a real prompt and its real reply) reappeared verbatim after
  `codex resume <id>`, no picker, matching this doc's own claim above.
