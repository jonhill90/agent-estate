---
type: Decision
description: Reversing 0008 -- the self-run authorship stopgap is retired, not patched a fourth time, because it just proved it cannot unblock even the PR that introduces it.
generated:
  at: 2026-08-23T19:30:00-04:00
---

# 0009 — Estate-lane PR authorship: the stopgap is retired; dispatch-time recording is the only remaining option

`2026-08-23`. Decided by the director loop
(`estate-loop/director-552-crossrepo.md`), reversing part of
`0008-estate-lane-pr-authorship-evidence.md` on new information that
document did not have. Not implemented here — see "Sequencing."

## What changed since 0008

0008 accepted `agent-supervisor#539`'s self-run cross-check as the current
mechanism, specifically because the alternative (dispatch-time recording,
`agent-supervisor#546`) "does not unblock today." That was the deciding
fact. **It is no longer true, and the thing that made it untrue is that
the stopgap tried to unblock itself and failed:**

`estate:5`, attempting to register its own PR `#539` — the fix the entire
board is blocked behind — hit a refusal from the tool `#539` itself
introduces:

    95b53058... is not jonhill90/agent-supervisor#539's real head commit (fc945860...)

`95b530588` is not a fabrication — it is **`agent-dotfiles`'s** worktree
`HEAD`. `estate:5`'s pane has been anchored in the `agent-dotfiles`
worktree all session; every commit to `agent-supervisor` today, including
`#539`'s own, was written through absolute-path tool calls from that
anchor, never by relocating the pane. The cross-check assumes pane-cwd ==
work-repo. A cross-repo estate lane — a real, currently-running shape,
not a hypothetical — is one where those structurally differ. Full
write-up: `agent-supervisor#552`.

**The tool has now failed in both directions, discovered in the same
review cycle:**

1. **False positive** (`estate:2`'s attack, fixed): a fabricated branch
   sharing a name registered successfully.
2. **False negative** (this issue): a genuine author, doing genuine work,
   cannot register because its pane is anchored elsewhere.

## What this means for 0008's own reasoning

0008 weighed "ship the safe part now, defer the hard part" against this
repo's own `worktree.sh gc` precedent (designed 2026-08-11, unwired 12
days) and concluded the self-check was the more disciplined choice
*because it actually worked and unblocked something today*, unlike a
deferred poller. That distinction is what made the self-check the
non-lazy option. **It no longer holds a genuine advantage over dispatch-
time recording on that axis, because it cannot unblock `#539` — the one
PR the entire board's forward progress depends on.** Both options are
now equally "does not unblock today." Once that tie is broken, the
remaining question is which is structurally sound, and every analysis
this loop has produced — the original council, the devils-advocate pass,
`estate:5`'s independent assessment on `#549`, and now this — has reached
the same answer: an actor cannot honestly attest to its own authorship
in a way that generalizes across every real shape of lane this estate
runs. Three patches in one review cycle (numeric-only → branch-name →
commit-SHA) each closed one demonstrated hole and the SHA-comparison
version, the most careful of the three, still failed on first real use —
not evidence the pattern needs one more patch, evidence the pattern
itself does not generalize.

## Testing the three options `#552` raised, not just picking one

**Option 2 (commit authorship, `gh pr view --json commits`, the same
evidence `#472`/`#495` cite) — already disproven this session, cited
here rather than re-derived.** `agent-supervisor#549`'s own Q3 measured
this directly: `#533`'s commits (`Jon Hill` + `Claude Sonnet 5`) are
byte-identical in shape to `#539`'s own (a known-genuine, ledger-
registered lane's PR). The signal does not discriminate between a
registered lane and an unregistered internal actor. Dead on arrival,
not reconsidered here.

**Option 1 (resolve the worktree by the PR's repo, not the pane's cwd) —
looks promising, fails the same way once actually verified.** `#552`
itself names the cost: the lane would have to name which checkout it
used, and that claim needs its own verification. Worked through what
verifying it actually requires: the natural check is this repo's own
Invariant 10 pattern (`cli.py worktree-lane --path ... --include-reviews`)
— confirm the named path is one the ledger already associates with this
lane. **That lookup requires a `tasks`/`source_tasks` row for the named
worktree, which is exactly the row that does not exist for informally-
dispatched work** — the same absence that makes the self-check's original
resolution chain return "nobody" for the director in `#549`. Properly
verified, option 1 does not avoid needing a dispatch-time-recorded row;
it just relocates the same requirement one level down while adding a
second bespoke mechanism (a named-path claim plus its own verification)
to build and maintain. Not worth building on its own.

**Option 3 (record authorship at dispatch time) — the only one of the
three that doesn't converge back onto its own prerequisite.** It was
raised when this design was first debated (`0008`) and set aside for
timing, not for being wrong — nothing in this document or `#552` disputes
its soundness, only its speed. That speed advantage is gone (see above).
It also closes `agent-supervisor#538` (the director's own missing `lanes`
row — the exact defect `#549` traced the false-positive incident to) with
the same mechanism, rather than needing a separate fix for that case.
**Corrected citation, caught by review**: earlier drafts of this document
cited `#532` for this fact — `#532` is the tmux-server-crash incident
report; the "`estate:1` has no `lanes` row" claim is `#538`'s own body
text, not `#532`'s. Verified by reading both directly rather than
re-trusting the earlier citation.

## Decision

**Retire the self-run cross-check. Do not patch `register-pr-dispatch-
self.sh` a fourth time.** `#539`, as currently designed, is superseded,
not merged as a stopgap — it cannot unblock itself, so nothing is lost by
not landing it in its current form. Build dispatch-time recording
(option 3) as the real, only remaining mechanism.

**The residual gap already disclosed on `#539`/`0008` still applies and
is restated here rather than assumed carried over:** possession of a
commit — under any of these three options — proves the worktree *has*
it, never that this lane *produced* it. Dispatch-time recording sidesteps
this specific gap differently than the other two: it never asks "does
this worktree have the commit," it asks "did I, the dispatcher, hand this
lane this piece of work before any commit existed" — a different question
that the worktree-possession gap does not apply to at all, which is
additional, not previously stated, reason to prefer it over options 1/2
rather than merely tying on soundness.

**No merge should rely on the self-check while it exists in its current
form.** Fail-closed stays fail-closed: an unresolved author lane is
reported unresolved, not treated as permitted. This is not a new
constraint — `0008` already stated it — restated because "the stopgap
turned out to be more broken than thought" is exactly the moment that
constraint is most likely to be quietly relaxed, and it is not being
relaxed here.

## Sequencing

1. **`#539` does not land as currently designed.** Comment posted on it
   (see below) marking it superseded by this decision rather than left to
   be patched a fourth time.
2. **Build dispatch-time recording**, scoped at minimum to close the gap
   that actually blocked `#539`: when a lane's dispatch happens (today,
   informally — a director types a brief-file path into an already-
   existing pane), record a `tasks`/`source_tasks` row for that dispatch
   at that moment, keyed to the lane's registered identity
   (`register-lane-self.sh`'s existing, trusted `$TMUX_PANE` anchor), not
   to the pane's cwd. This is real engineering, not a one-line fix — full
   design is the implementer's scope, not fixed here. **Delegate to
   whichever of `estate:2`, `estate:3`, `estate:4` is free first — not
   `estate:5`**, which has now patched this exact mechanism three times
   in one review cycle and should not be the one to also build its
   replacement.
3. **Retroactive resolution for the 14 currently-open PRs is its own
   decision, not a free consequence of step 2 — corrected here before the
   builder reached it** (`director-retroactive-tension.md`). Writing a
   dispatch-time-shaped row for a dispatch nobody actually observed is
   the identical evidentiary hazard this document just retired, wearing
   the new mechanism's name. A retroactive row, if written at all, must
   be (a) backed by real evidence — the session transcript that actually
   shows the dispatch, not a lane's bare say-so, (b) confirmed by a lane
   independent of both the one being resolved and the one that built the
   mechanism, and (c) permanently, visibly distinguishable in the ledger
   from a real dispatch-time-observed row (a distinct `dispatch_kind`,
   never a bare boolean easy to drop later). A PR whose dispatch cannot
   be found or confirmed this way stays unresolved — fail-closed applies
   to the reconstruction exactly as to everything else. This also
   resolves `agent-supervisor#538`'s recovery gap for dispatches from
   here forward; it does not retroactively grant the director a `lanes`
   row for past work by the same reasoning.
4. **`agent-supervisor#546`** (the independent poller, filed against the
   self-check's residual gap) is folded into this work rather than
   pursued separately — a poller answering "does this worktree have the
   right commits" is solving a weaker question than dispatch-time
   recording answers directly. Note this on `#546` rather than silently
   abandoning a filed, tracked issue.
5. **`agent-supervisor#550`** (the cheap interim guard — refuse
   `mark-pr-external` when the caller is an estate participant) still
   stands, unaffected by this reversal; it addresses a different tool.

## The residual — added because this document, retiring the mechanism, owes a path for the work stuck behind it

This gap was found by review, not self-caught: a decision record retiring
a mechanism has to say what happens to the work that was depending on it,
not just that the mechanism is retired. **`agent-supervisor#555`** is
that path — a per-PR, human-authorized exception, live and active now,
not a future contingency. As measured directly (`merge-pr.sh` re-run
against every open PR, not estimated): **seven** PRs currently have no
honest `dispatch.sh` route and sit on `#555` — `#531`, `#533`, `#534`,
`#535`, `#536`, `#537`, `#557`. Three others that were briefly thought to
be in the same boat (`#541`, `#547`, `#553` — this document itself) are
blocked on their own review content, not on authorship, and are not part
of this accounting. This section will go stale the moment the seven
change; check `#555` directly for the current count rather than trusting
the number restated here.

## What this does not decide

- The exact schema/mechanism for dispatch-time recording of an
  informal estate-loop brief-dispatch — real design work, scoped to
  whoever picks up step 2 above.
- Whether `#539`'s existing test suite
  (`test_merge_pr_estate_lane_identity.sh`, etc.) is reusable for the
  new mechanism or needs rewriting — for the implementer to assess.
