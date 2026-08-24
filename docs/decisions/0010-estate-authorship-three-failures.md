---
type: Decision
description: Three attempts at ledger-proven PR authorship have each failed differently. Decided -- what changes, what's bounded, what's documented as a limit rather than solved.
generated:
  at: 2026-08-23T19:55:00-04:00
---

# 0010 — Estate-lane PR authorship, after three failures: redesign, bound, and say plainly what is not proven

`2026-08-23`. Decided by the director loop
(`estate-loop/director-third-failure.md`). Not implemented here.

## The pattern, read straight rather than summarized

| # | Attempt | Failure |
|---|---|---|
| 1 | `#539`, branch-name cross-check | **False positive** — a throwaway repo with a matching branch name registered successfully (`estate:2`'s demonstrated attack). |
| 2 | `#539`, commit-SHA comparison | **False negative** — a genuine cross-repo author (pane anchored in `agent-dotfiles`, committing to `agent-supervisor`) was refused; the fix could not land through its own mechanism (`#552`). |
| 3 | `#556`, dispatch-time recording | **False positive again, relocated** — `record_pr_for_task`/`attach-pr-to-open-task` will silently overwrite an existing correct record, with no check the attached PR postdates the dispatch or bears any relation to the task's content. |

**The sharpest fact, from `estate:5`'s review of `#556`, restated because it
is the actual finding this document answers:** the same absence of a
content check that fixes attempt 2's false negative is what reopens
attempt 1's false positive. The two failure modes are trading places, not
being eliminated, and `#552`'s own closure is proven by the identical test
suite that leaves `#556`'s gap open.

## Why all three failed the same way underneath different names

Read closely, all three attempts share one property regardless of what
they were called: **the recording call is invoked by the lane whose
authorship is in question, on itself.** Attempt 3 was named "dispatch-time
recording" and inherited that name's presumption of soundness — `0009`'s
own reasoning was "the row is written before any commit exists, so there
is nothing yet to falsely take credit for" — but `#556`'s actual
implementation still has the lane call `cli.py attach-pr-to-open-task`
*itself*, after the fact, whenever it gets around to it. That is not
dispatch-time recording in the sense `0009` meant (recorded by the
dispatcher, before the lane even begins); it is a self-report wearing the
name, and it inherits self-reports' actual weakness: **a lane fully
controls every input to a tool call it invokes on itself — the timing, the
target, the claimed relationship — no matter how the check on the other
end is written.** Three different checks (name match, SHA match,
existence-of-a-task-row) were each satisfiable by the reporting lane's own
actions. This is the actual root cause underneath all three failures, not
a property of any one check.

## Is this achievable at all? Yes — but not the way it's been tried

**True dispatch-time recording — written by the actual dispatching actor,
at the actual moment of dispatch, and structurally never invokable by the
lane whose work is being recorded — has not been tried.** `dispatch.sh`
already does this correctly for issue-based work (`agent-dotfiles#320`,
`skills#278` both merged today through exactly this path). The gap is
narrower than "authorship cannot be proven": it is that the estate-loop's
informal brief-dispatch (a director typing "Read `<brief>` and do exactly
what it says" into an already-existing pane) has never called
`cli.py record-dispatch` **at the point of sending**, and every attempt so
far has tried to patch around that gap from the lane's side instead of
closing it at the source.

This is option 1 (keep iterating), taken seriously rather than as
momentum, because it names the concrete, structural difference the fourth
attempt requires: **the recording call moves out of any script a lane can
invoke on itself, permanently, and into whatever process performs the
send.** For dispatches the director itself performs (`verified_send`/
`send.sh`), this means the director calls `cli.py record-dispatch` in the
same action as the send, before the brief is delivered — not delegated to
the lane to do on arrival.

**Named honestly, not glossed over:** the director does not have full
visibility into every path that has been delivering briefs to lanes this
session — several arrived at lanes without the director sending them
(the self-organizing pattern observed repeatedly tonight). This redesign
closes the director's own future dispatches definitively. It does **not**
by itself guarantee every dispatching path in this estate is covered,
and whoever builds it should scope that explicitly rather than assume
one send mechanism is the only one.

## Option 2, considered and set aside as the primary answer, not dismissed

Re-scoping the guard to prove only "the reviewer is not the author" (not
full authorship) is a real, different, weaker claim — and worth naming
why it doesn't resolve this on its own: the natural way to check it is
against the set of lanes that have ever had a worktree/task touching this
PR's branch. When the ledger has no record of *any* lane touching a
branch (true for every one of tonight's informally-dispatched PRs), that
set is empty, and an empty set is indistinguishable from "genuinely nobody
else touched it" — the identical unknown-means-allowed trap named as a
hard constraint. It does not eliminate the gap, it relocates it to the
same missing registry the primary fix targets. Kept as a candidate
**additional** signal once real dispatch-time records exist to populate
that set, not adopted as a replacement for building them.

## Decision

**Pursue the redesigned dispatch-time mechanism (structurally different
from all three prior attempts: recorded by the dispatcher, never
lane-invocable) as the real fix — this is not declared unachievable.**
Simultaneously, and not contingent on that work landing:

1. **Bounded, explicit, per-PR human-authorized exception for the nine
   currently approved-at-head PRs** (`#531`, `#533`, `#534`, `#536`,
   `#537`, `#543`, `#544`, `#551`, `agent-tui#142`) — this was already the
   fallback named in `agent-supervisor#555` for the *time* case ("the
   mechanism takes hours"); it now also applies as the answer for the
   *soundness* case, since none of these nine can wait on a mechanism that
   has failed three times and whose fourth attempt is real engineering,
   not a quick patch. **Stated expiry**: each exception is granted per PR,
   individually recorded, and does not roll forward — a tenth PR
   appearing tomorrow with the same gap does not inherit an already-
   granted exception; it needs its own.
2. **Document the epistemic status of every authorship row in this
   ledger — present and future — where the merge gate's own comments will
   show it, not just in a decision doc a future reader has to already
   know to check.** Whichever mechanism produced a row (a self-check that
   still exists for some class of PR, a genuinely-external dispatch-time
   record, or a human-authorized exception), the comment at the point
   `verdict-independence.sh`/`merge-pr.sh` consumes it should say plainly
   that the row records a **claim made under stated conditions**, not a
   mathematically proven fact — so the next reader does not trust it
   further than three consecutive failures have earned.

**Not chosen: silently weakening the gate to clear the board.** Three
reviewers have now refused to do this under exactly this pressure
(`estate:2` on `#539`, `estate:5` on `#552` and again on `#556`); this
decision does not become the one that does.

## Sequencing

1. **The nine PRs**: present each to Jon individually, per `#555`'s
   already-decided process — his sign-off, not the director's, recorded
   distinctly from both a `mark-pr-external` row and an observed
   dispatch-time row. Not blocked on anything else in this document.
2. **`#556` does not land as currently designed** — the same
   silent-overwrite/no-content-check gap `estate:5` found stands
   unaddressed; comment posted marking it superseded by this redesign
   rather than patched a fourth time by the same author.
3. **The redesigned mechanism** — real engineering, scoped honestly as
   such, not a quick follow-up patch. Delegate to whoever is free; per
   `0009`'s own standing constraint, not `estate:5`, which has now
   reviewed this mechanism twice and should stay in the reviewing seat,
   not the building one, for whoever picks this up next either.
4. **The comment-visibility documentation task** (item 2 above) can
   proceed in parallel — it does not depend on the redesign landing, only
   on stating plainly what already exists.

## What this does not decide

- The exact code shape of "recorded by the dispatcher, never
  lane-invocable" — real design work for whoever builds it.
- Whether every non-director dispatching path in this estate can be
  brought under this discipline, or only the director's own — named as an
  open scoping question, not resolved here.
