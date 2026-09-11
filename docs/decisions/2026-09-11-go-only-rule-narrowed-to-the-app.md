# 2026-09-11 — the go-only rule is narrowed to the app; the retired shell/Python test tree is deleted, not repaired

**Status:** decided by Jon, reverses a hard rule stated in `CLAUDE.md` since
2026-08-22. Recorded because reversing a documented hard rule needs its
reasoning to survive the reversal, not just the new text.

## The question that was actually asked, and the answer that came back wider

agent-estate#1412 asked one narrow question: repair, keep frozen, or drop the
157-of-165 failing shell suites under `reference/tests-supervisor/` (all
broken on one stale pre-`reference/`-move path, none of them citing anything
live). Jon's answer, quoted in full because it settles more than the
question asked:

> Fix the guard. Move the reference test stuff to other directories as needed
> if that will help, or remove the reference folder — I don't know. Just
> don't start writing the app in shell and Python again and we will be fine.
> That was the whole reason for doing that. But I feel now we are good and
> don't need you to be that way, and can use the scripts as needed — make
> them as needed. But delete what we don't need, and just reference from old
> commits, browse files if needed.

Two separate decisions are in there, and they don't share one justification:

1. **Drop the test tree**, not repair it first. #1412 had recommended
   repair-then-decide (repair is small — one 409-line mechanical
   substitution — and reversible; deletion is neither). That recommendation
   was overtaken, not wrong: Jon answered the actual disposition question
   directly, and repairing something about to be deleted spends real time
   proving a number ("158/165 green") that the decision no longer needed.
2. **The go-only rule itself no longer covers helper scripts.** "That was the
   whole reason for doing that" names the rule's actual scope in his own
   words — stopping the app from drifting back into shell and Python. Scripts
   *outside* the app were never what the rule was protecting; he is now
   saying so directly instead of leaving it to be inferred from "guidance,
   not a gate" (docs/orientation/go-only.md's 2026-09-02 CI-blocker removal).

## Why these are one decision and not two coincidental ones

Both turn on the same distinction: git history versus a second executable
copy. "Just reference from old commits, browse files if needed" is the
through-line — old commits are the record of how the retired shell
supervisor worked; a frozen-but-runnable test tree is a second, redundant
record of the same thing, kept live only so it could be read later. Once
scripts are allowed to be written and used as needed going forward, keeping
a large body of tests for a specific retired implementation stops being about
the *rule* (nothing in the retired tree was ever going to be un-retired) and
becomes pure archival weight — which git already carries for free.

## What was verified before executing this, not assumed from the quote alone

The quote authorizes deletion; it does not by itself tell an agent which
files are safe to delete. Two corrections to #1412's own scope were made
before anything was removed (agent-estate#1412's own follow-up comment):

- **`reference/scripts/` is not uniformly droppable — it is live.**
  `src/estate/internal/isolate` names `reference/scripts/supervisor/` by
  path; `.claude/settings.json` registers
  `reference/scripts/supervisor/prompt_capture_hook.py` as a real
  `UserPromptSubmit` hook. Nothing under `reference/scripts/` was deleted by
  this decision.
- **The Python half of the test tree needed a per-file check, not a blanket
  deletion.** 49 of the 118 `test_*.py` files exercise the live 17-file
  subset (`core.py`, its 11 `core_ledger_*.py` mixins, `itemize_prompts.py`,
  `mine_prompts.py`, `prompt_capture_hook.py`,
  `migrate_dead_capture_1357.py`, `migrate_dead_items_1362.py`) directly, as
  their own subject under test — not merely importing `core.Ledger` as
  shared DB plumbing for a retired CLI, which several dozen of the deleted
  ones also did and which does not make the retired CLI itself live.
  `test_adapter.py` is the one exception to that criterion, kept for a
  narrower reason, not the same one: `test_prompt_capture_hook.py` (one of
  the 17-subset suites) imports `TmuxAdapter`/`classify_capture` from
  `adapter.py` directly to build its own fixtures, so `adapter.py` — itself
  retired, the same module `cli.py`'s dead CLI imports it for — has to keep
  importing cleanly for that live suite to even collect. Testing it in its
  own right, rather than only incidentally through another suite's fixture
  setup, is the more honest way to keep that import from becoming a silent
  assumption. Those 49 moved to `reference/scripts/supervisor/tests/`, next
  to the code they test, and were repaired to actually run — they carried
  the same stale-path defect as the shell suites, plus a
  `tests.supervisor.*`-qualified import left over from before the
  `reference/` move that made every one of them fail collection with
  `ModuleNotFoundError: No module named 'tests'`, independent of the path
  bug. All 165 shell suites and the other 69 Python suites test retired
  subjects only (`dispatch.sh`, `cli.py`, `watchdog.sh`, `merge-pr.sh`,
  `mcp_server.py`, `verdict.py`, and the rest) and were deleted.

`reference/scripts/` itself was **not** renamed in this same change, despite
now being live infrastructure rather than reference material by definition.
`src/estate/internal/isolate` names it by literal path string, and
`.claude/settings.json`'s hook registration does too — a rename touches Go
source, its tests, and a machine-read JSON config together, which is
real, separable risk this PR did not need to take on to land the two
decisions above. Tracked separately rather than folded in silently.

## What this does not decide

The app (`src/estate`, `src/tui`) is still Go, with no exception — that half
of the original rule is unchanged and unrelaxed. Nothing here authorizes
writing new application logic in shell or Python, only tooling and scripts
that sit outside the app boundary, which was already permitted in practice
(docs/orientation/go-only.md's "guidance, not a gate" section, standing since
2026-09-02) and is now permitted by direct instruction too.

## References

- agent-estate#1412 — the measurement (population, cause, citations, what
  each retired subject's live Go/tests counterpart is) and Jon's decision
  comment, quoted above
- `docs/orientation/go-only.md` — the rule's full current text
- `CLAUDE.md`'s hard-rules section — the one-line restatement every task
  reads first
