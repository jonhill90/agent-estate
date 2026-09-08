---
type: PRD
description: What the estate is for and the parameters it must be built to. Verified against the tree 2026-09-08, correcting nine days of drift from the 2026-08-30 rewrite.
verified: 2026-09-08
---

# agent-estate — PRD

**Read this before starting any work.** The previous version of this document
described a supervisor written in shell and Python. That supervisor has been
deleted. Everything below is checked against the tree on the date above.

## What this is for

One person runs a fleet of agents on one Mac. The estate is the machinery that
lets that happen without the machine falling over and without work being lost:
it decides whether there is room to start an agent, starts it, records what
happened, and shows the operator the state of everything.

It is a product, not a pile of automation. The measure of a change is what a
human can do after it that they could not before.

## Parameters — these are not negotiable

1. **The app is Go.** Not shell, not Python, at any size, for any reason.
   `reference/` holds the deleted shell and Python supervisor as material to
   read when recovering a rule; recovering a rule means reimplementing it in
   Go. This binds the app; it does not bind tooling — `scripts/` runs Python
   today (docs-lint, evidence, knowledge tooling), and that is correct, not a
   violation (Jon, 2026-09-07). No CI job enforces this today, and that is a
   decision, not a gap: a hard CI block on new shell/Python was tried and
   deliberately reverted on 2026-09-02, an over-extreme reading that could
   wedge an agent legitimately needing a script for tooling, a sandbox or an
   experiment. See [`docs/orientation/go-only.md`](../orientation/go-only.md)
   for the full reasoning — this is guidance the operator checks by hand,
   not something waiting to be built.
2. **Every guard fails closed.** A limit that cannot be measured refuses. "Could
   not measure" is never reported as clean, and blindness is never capacity.
3. **Delivery is observed, not inferred.** An agent turn is a subprocess whose
   exit status and parsed output are the result. Nothing is concluded from what a
   terminal appears to show.
4. **Unknown is not failed.** A turn that timed out or could not be parsed stays
   non-terminal and keeps occupying its slot until something establishes
   otherwise.
5. **No work is ever lost.** Destructive operations look at the target first,
   and anything removed is recoverable — verified: `Worktree.Remove` refuses
   unless the content is either clean or byte-identical to what origin already
   holds durably.
6. **Report capability, not process.** Merged is not delivered. PR counts, issue
   counts and sweep counts are not progress.
7. **A rule the estate states is never invented.** What the operator remembers
   having decided is a fact recorded once, verbatim, not composed from what
   sounds right — see "Knowledge" below.

## What it refuses to do

- Drive an agent by typing into a terminal pane and reading pixels back.
- Free a slot for a turn it did not observe finish.
- Delete a branch, worktree or file without a recoverable copy.
- Extend a layer that a standing directive has ruled out — the correct response
  is to stop and say so, not to do it well.
- Regenerate the shared knowledge index, or write to the memory vault, as a
  side effect of anything other than the one sanctioned write path.

## Who it serves

One operator with limited time and a hard token budget, who needs to see estate
state at a glance and needs the machine to stay usable while agents run.

## Knowledge

The estate remembers what the operator has decided and lets an agent ask it,
rather than re-deriving a rule from scratch or asking Jon something already on
the record. What it refuses: a rule is never composed — its text is verbatim
from what he said, never a paraphrase that merely sounds like something he'd
say. See [`docs/canonical/knowledge.md`](../canonical/knowledge.md) for the
three-layer design and [`docs/canonical/knowledge-workflow.md`](../canonical/knowledge-workflow.md)
for how a fact moves from said to citable.

## How this document stays true

Every claim here is either a parameter (a decision, true because it was decided)
or a fact about the tree (true because it was checked on the date above). When
the code moves past a fact, the code wins and this file is wrong — fix it.
