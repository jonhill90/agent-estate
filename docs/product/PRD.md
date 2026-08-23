# agent-supervisor — PRD

Historical record, not a live spec. It describes the boundaries this project
was designed against as of the date below; if the code has moved and this
hasn't, the code wins. `README.md` already states what the system *is* ("a
long-lived agent reads GitHub issues, hands each one to a disposable agent in
its own terminal and its own git worktree, and records who is doing what in
SQLite"). This document does not restate that. It exists for the half that
isn't in the README: who this serves, and what it refuses to do.

`Verified 2026-08-14` on every claim below means: read from the cited file in
this repository on that date, or produced by running the cited command and
reading its actual output. Nothing here is inferred from a setting or a
prediction.

## Who this is for

Two roles, stated in `README.md`'s "three roles" table:

- **The Director** — a human, or a long-lived agent acting for one — decides
  what matters next and whether a PR is good. This project serves that role
  by making the estate legible: one table (`digest.sh`, `lanes.sh`) instead of
  N terminals to individually check.
- **The Supervisor** — mechanical upkeep: is everything alive, is the ledger
  consistent. This project *is* mostly this role's code.

It does not serve an end user of whatever the workers are building. There is
no product surface here beyond the estate itself.

## What this is for

Turning "N terminals running agents" into one thing a human can read the
state of and hand work to, without either terminal or human having to
remember anything the other already knows. `README.md`, "The one idea that
matters", frames the mechanism: two places state can live — the pane (a
screen) and `ledger.sqlite3` (the record) — and confusing them is named as the
cause of "nearly every serious failure this system has had."

## What this refuses to do

These are already decided, and are gathered here from where they already
live — `AGENTS.md`, `README.md`, and the header comments of the scripts that
enforce them — not invented for this document.

### tmux is not a database

The authorship test, stated in `AGENTS.md` ("Invariants", item 1) and in
`README.md` ("The one idea that matters"): did *this system* write the value,
or did tmux produce it as a byproduct? Availability, ownership, and which
conversation belongs to which lane are decided-and-remembered facts and
belong in `ledger.sqlite3`. Busy, blocked, and scrolled are observed-right-now
facts and are read from the pane. A window name may be a *projection* of a
ledger record — `lane-done.sh` renames a window only after the ledger already
holds the fact — but it may never be the record itself. `README.md`'s
"Order of operations" section explains why: releasing the ledger before
renaming the window means a crash between the two leaves a stale *label*;
renaming first would leave the ledger silently wrong.

### Refuse, never invent

`AGENTS.md` (Invariants, item 3) and the header of `restore.sh`: a lane whose
harness session id is missing, whose harness has no resume dialect, or whose
transcript is gone from disk is reported `UNRECOVERABLE` (exit 2) and left
alone. `restore.sh`'s own header calls the alternative "the worst outcome
available — it wears the right window name, looks fully recovered, and has
none of the context." (Checked 2026-08-23 against `restore.sh:19-22`
directly: the exact quoted clause order is `README.md`'s own "Recovery"
section, line 109 — `restore.sh`'s header states the same claim with the
first two clauses transposed, "it looks fully recovered, wears the right
window name, and has none of the context." Not a substantive difference,
but the quote above is `README.md`'s wording, not a verbatim excerpt of
`restore.sh` as attributed.) This project would rather tell an operator "I
can't" than hand them something that looks like a successful recovery and
isn't one.

### Fail closed

`scripts/supervisor/dispatch.sh`, in the block guarding step 0 ("the ledger
must be readable before any lane is trusted"), states the rule directly: an
unreadable ledger "must mean 'cannot tell what is free', never 'assume
everything is free'." Nothing may ever be the reason a lane becomes
*available* — an inability to read state degrades to "ask a human"
(`lanes.sh`'s `unknown` state, `AGENTS.md` item 6), never to "assume free."

**Added `Verified 2026-08-15`, the same rule applied to merging:**
`merge-pr.sh` — the only path meant to merge a PR here — refuses when either
of its two gates cannot be evaluated, not just when either evaluates false.
An unreadable CI status, an unresolved PR author, or a verdict with no lane
to compare against all refuse the merge (`AGENTS.md`, "Invariants" item 9 on
lane identity, and its Conventions section on the merge gate). This is the
same instrument as `dispatch.sh`'s ledger check, applied one step later in
the lifecycle: "cannot tell" degrades to a refusal, never to "proceed."

### One fix pass

`AGENTS.md`, Conventions: one independent review per PR, by someone who did
not write it. If a PR fails a second review, it is closed and what remains is
filed as a new issue, rather than iterated on indefinitely in place. This
project trades thoroughness-per-PR for keeping the review queue itself
legible.

### The renderer consumes the supervisor; it does not decide for it

`scripts/supervisor/laneview/README.md` states the contract for every lane
viewer: "Read, never write. The only state a renderer may treat as ground
truth is the json it was handed... A renderer must never decide 'this lane is
free' and act on that decision (dispatch, claim, rename a window) — that is
`dispatch.sh` and `claim.sh`'s job, not a viewer's." Any human-facing surface
(a TUI, a sidebar, a chat bot) is a client of the supervisor's read surface,
never a second writer with its own opinion about lane state.

### The poller is a service, not a lane

`AGENTS.md`, Invariants item 8: `inbox-poll.sh` is never dispatched to and
never "restarted" as if it were a worker. It consumes Telegram messages by
acking an offset, so running it by hand to check for messages returns nothing
— that is not evidence nobody wrote, only evidence the offset already moved.

## What this is not

- **Not a general workflow engine.** It hands one GitHub issue to one
  disposable agent in one worktree and records the outcome. It does not
  orchestrate multi-step pipelines beyond that.
- **Not a guess when it cannot see.** `AGENTS.md` item 6: `unknown` means "not
  offered", never "probably fine." A harness `lanes.sh` cannot read is left
  alone, not guessed at.
- **Not a place secrets or judgement calls live.** Judgement (is this PR
  good, what matters next) is centralized in the Director, per `README.md`'s
  "three roles" table; the supervisor's own code stays mechanical.
