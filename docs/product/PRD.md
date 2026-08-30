---
type: PRD
description: What the estate is for and the parameters it must be built to. Rewritten 2026-08-30 against the tree, after the shell and Python supervisor was deleted.
verified: 2026-08-30
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

1. **The implementation language is Go.** Not shell, not Python, at any size,
   for any reason. `reference/` holds the deleted shell and Python supervisor as
   material to read when recovering a rule; recovering a rule means
   reimplementing it in Go. `src/langguard` enforces this in CI.
2. **Every guard fails closed.** A limit that cannot be measured refuses. "Could
   not measure" is never reported as clean, and blindness is never capacity.
3. **Delivery is observed, not inferred.** An agent turn is a subprocess whose
   exit status and parsed output are the result. Nothing is concluded from what a
   terminal appears to show.
4. **Unknown is not failed.** A turn that timed out or could not be parsed stays
   non-terminal and keeps occupying its slot until something establishes
   otherwise.
5. **No work is ever lost.** Destructive operations look at the target first,
   and anything removed is recoverable.
6. **Report capability, not process.** Merged is not delivered. PR counts, issue
   counts and sweep counts are not progress.

## What it refuses to do

- Drive an agent by typing into a terminal pane and reading pixels back.
- Free a slot for a turn it did not observe finish.
- Delete a branch, worktree or file without a recoverable copy.
- Extend a layer that a standing directive has ruled out — the correct response
  is to stop and say so, not to do it well.

## Who it serves

One operator with limited time and a hard token budget, who needs to see estate
state at a glance and needs the machine to stay usable while agents run.

## How this document stays true

Every claim here is either a parameter (a decision, true because it was decided)
or a fact about the tree (true because it was checked on the date above). When
the code moves past a fact, the code wins and this file is wrong — fix it.
