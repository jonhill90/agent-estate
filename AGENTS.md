# agent-supervisor — agent orientation

*(`AGENTS.md` and `CLAUDE.md` are the same file — one is a symlink, so there is
no second copy to drift.)*

## Before you ask Jon anything — read this first

Jon has stated this more than twenty times. It is a **hard** parameter in his
corpus and it keeps being broken, so it goes at the top of the file rather than
somewhere polite.

**Exhaust the record before a question reaches him**, in this order:

1. **Query the corpus.** `~/.local/state/agent-dotfiles-supervisor/ledger.sqlite3`
   — 3,700+ of his own prompts, 900+ live hard constraints. Views:
   `live_parameters`, `open_questions`, `unacknowledged`, `possibility_count`.
   If you have not queried it this session, you have not earned the question.
2. **Read the docs and the code.** `agent-dotfiles/docs/` carries ~2,467 lines
   of spec — PRD, SPEC, loop-engineering, supervisor-disposition, loop-signals.
   A loop was once declared "never planned" because someone searched the wrong
   repository.
3. **Convene a council** (`ask-a-council`) when the failure modes are plural.
4. **`sanity-check` or `devils-advocate`** when it is one decision that needs
   attacking rather than several lenses.

**Only INTENT questions reach him.** His words, weight hard: *"the right move is
to ask questions that determine intent, not to ask him to make your decisions."*
Architecture, sequencing, which-PR-next, how-to-implement — those are yours.
Deciding them is the job.

**Why this fails, so you can catch yourself:** asking is safe. If he picks, you
cannot have picked wrong. It is the same instinct that ships a stub reading
"not built yet" instead of a populated view — the defensible option over the
useful one. Choose the useful one.

Read this before changing anything. It is short on purpose. [`README.md`](README.md)
explains what the system is; this explains what will bite you.

## Layout

```
scripts/supervisor/          the system
  lanes.sh                   classify every pane into one of ELEVEN states
  dispatch.sh                hand an issue to a lane; records the ledger row
  lane-done.sh               completion, in the safe order
  claim.sh                   atomic lane claim
  worktree.sh                one git worktree per lane
  restore.sh                 rebuild every lane after a tmux server loss
  cli.py / core.py           the SQLite ledger
  harness_session.py         resolve the agent's own conversation id
  adapter.py                 harness-neutral pane classification
  harness/{claude,codex,copilot}.sh   per-harness shapes
  send.sh                    the one verified-send primitive for typed text
                             into a pane's input box (#178/#186); nothing else
                             types a caller's message straight to `send-keys`
  merge-pr.sh                the only path that merges a PR here — chains
                             ci_gate.py (every check green at the live head
                             SHA) and verdict-independence.sh (author cannot
                             merge its own unreviewed PR, #179/#184)
  verdict.py                 read a PR's review verdict from GitHub or the
                             ledger, fail closed to "unknown", never invert
  verdict-independence.sh    is the reviewing lane really not the author —
                             see "Lane identity" below (#184/#192/#196/#198)
  tmux-isolation.sh          `assert_isolated_tmux`, the function Invariant 4
                             requires before any destructive tmux verb,
                             including session creation (#185)
  laneview/                  two viewers, neither required
  look.py                    let an agent SEE a pane: capture / png / navigate / frames
  termshot.py                ANSI-to-SVG rasteriser look.py's `png` renders through
  ui-evidence-gate.sh        CI check: a UI PR must carry a look.py frame
  mcp_server.py              read surface over MCP, plus four guarded
                             session-management writes (agent-tui#14)
  session_guard.py           the one place session removal is judged safe
  watchdog.sh                liveness, from OUTSIDE the loop
  inbox-poll.sh              Telegram poller (a service, never a lane)
tests/supervisor/            110 tracked files; the suite is the contract
                             (`Verified 2026-08-18` by `git ls-files
                             tests/supervisor/ | wc -l`; this line was last
                             updated to 92 during the 2026-08-16 docs sweep
                             and had gone stale again since — a count this
                             volatile is worth re-measuring, not trusting)
```

This list is a highlight, not an inventory — `ls scripts/supervisor/*.sh
scripts/supervisor/*.py` names more than fifty files. What is listed here is
what changing it without reading it first will bite you for; everything else
is discoverable by reading the directory.

## Invariants — do not break these without an explicit decision

1. **The ledger is the record; tmux is the screen.** Anything *decided and
   remembered* (availability, ownership, which conversation belongs to a lane)
   belongs in `ledger.sqlite3`. Anything *observed right now* (busy, blocked,
   scrolled) is read from the pane. The test is authorship: did this system
   write the value, or did tmux produce it as a byproduct?

2. **Write the durable fact before the pretty label.** `lane-done.sh` releases
   the ledger, then renames the window. The reverse order strands a lane
   permanently on a crash; this order leaves only a stale label.

3. **Restore refuses rather than invents.** A lane that cannot be brought back
   with its own conversation is reported `UNRECOVERABLE` (exit 2) and left
   alone. A fresh agent wearing a recovered lane's name looks fully healthy and
   has none of the context — it is the worst available outcome.

4. **Never address the default tmux socket in a test.** `kill-server`,
   `kill-session`, `kill-window` and `respawn-*` must be scoped with
   `TMUX_TMPDIR` and gated by `assert_isolated_tmux` (`tmux-isolation.sh`). A
   bare `tmux kill-server` from a lane destroyed the entire live estate three
   times in one day, including unrelated sessions belonging to the operator.
   The rule is not "never call it" — one test harness calls it sixteen times
   safely. `Verified 2026-08-15` (#185): the guard was extended to session
   *creation* too, not just the destructive verbs above — an isolated test
   that creates a session without `TMUX_TMPDIR` set was the same class of
   leak with a different verb.

5. **Address windows by `window_id` (`@7`), never by index.** Killing window 4
   renumbers 5 into 4. A loop killing indices hits shifting targets; that
   destroyed the Telegram poller.

6. **`unknown` means "not offered", not "broken".** `lanes.sh` is a whitelist:
   only a recognised idle shape is offered as free. Handing work to a lane you
   cannot read is worse than leaving it idle. Do not "improve" this into a guess.

7. **`lanes.sh` holds no harness-specific string.** Harness knowledge lives in
   `harness/*.sh`. Widening a regex to cover another harness is the wrong fix —
   it lets one harness's shapes falsely match another's.

8. **The poller is a service, not a lane.** Never dispatch to it, never "restart"
   it as a lane. It consumes Telegram messages by acking the offset, so running
   the inbox by hand returns nothing — that is not evidence nobody wrote.

9. **Lane identity is the string `<session>:<index>`, e.g. `agent-supervisor:3`
   — not the task, not the worktree, not the window name.** A lane that
   finishes one task and is dispatched a second, in a different worktree, is
   still the *same lane*. This was used to justify treating a review as
   independent on 2026-08-15 and was wrong: same `<session>:<index>` across a
   rename is a self-review, and `verdict-independence.sh`'s `lane_relation`
   check exists specifically to catch it — see its own comment on "the same
   window, renamed session" (#184/#192/#196/#198). Compare lane ids, never
   task ids or window names, when deciding whether two pieces of work were
   done by the same agent.

10. **A lane identifies itself by matching the ledger's `worktree_path`
    against its own `cwd`, not by asking tmux who it is.** `tmux
    display-message` **without an explicit `-t`** answers for the session's
    *currently active* window, not the caller's own — a background or
    non-focused pane gets someone else's answer. That produced six
    mis-stamped `Review-Lane:` trailers in one day (#187) before the merge
    gate's independence check caught them as suspicious self-reviews (see
    invariant 9) rather than silently trusting them.

    The correct self-lookup is `cli.py worktree-lane --path "$(pwd)"
    --include-reviews` (`Ledger.get_task_for_worktree(..., include_reviews=
    True)`). The `--include-reviews` flag matters: `worktree-lane` defaults
    to `False` because its real caller, `dispatch.sh --reviews-pr`, is
    asking a DIFFERENT question — "who could plausibly have AUTHORED this
    PR?" — and a review task can never be its own PR's author (#76), so the
    default filters review-shaped tasks out. A REVIEWING lane's own
    worktree is legitimately parked on a task that looks like a review, so
    that same filter answers `known:false` for a row the ledger actually
    has if the flag is left off — measured directly (#212), from the exact
    situation #187 was about, before this flag existed:

    ```
    $ cli.py worktree-lane --path "$(pwd)"
    {"known":false,"lane":null,"path":".../ad-211-rev212-14268","task":null}
    $ cli.py worktree-lane --path "$(pwd)" --include-reviews
    {"known":true,"lane":"agent-supervisor:6","path":"...","task":"as211-rev212"}
    ```

    Do not remove the default filter to "fix" this — that reintroduces #76
    (a review task answering as a PR's author). The flag exists so the two
    questions share one lookup without sharing one answer.

## The failure mode this codebase produces most

**An instrument that cannot see a thing looks exactly like the thing being
absent.** Before reporting "none", "empty", "never" or "not called":

- Check the whole tree, not one file. `grep`ing a single script and concluding
  "nothing calls this" was wrong when the callers were one file away.
- Test *tracking* (`git ls-files`), not directory existence. A gitignored
  `__pycache__` made a completed deletion look incomplete.
- Capture exit codes directly. `cmd | tail` gives you `tail`'s status.
- Verify a mutation applied before believing the result it produced.
- Cite **functions and behaviours, never line numbers.** A comment citing its own
  callers by line was already wrong in the diff that added it.

## Two more, learned expensively

**A tool that fails closed and that nothing calls is a documentation rule with a
binary attached.** After building anything protective, ask both *what calls it?*
and *is that caller something that survives the failure it guards against?* A
cleanup invoked from the crashing process is wired to the one thing that will not
be running.

**An abstraction can be present and correctly avoided.** Routing around a seam
looks identical to nobody having wired it up. Check whether the avoidance is
documented before "fixing" it — the reason belongs next to the seam.

**A merged fix that never reaches the process running it looks identical to an
unfixed defect.** `agent-supervisor#308` was reported broken three times
(#331, #333, then this same report a third time) with the same live
`bash -x` transcript: `cli.py pr-task` reported as an "invalid choice". Every repair
attempt found `pr-task` already implemented and working on `origin/main` —
because it was: added by #321/`a2d8a80`, before either later PR. The
transcripts were real; the repo was never the thing they were measuring.
The Director runs `scripts/supervisor/` out of the shared checkout at
`/Users/jon/source/repos/Personal/agent-supervisor`, which had fallen 13
commits behind `origin/main` (stuck at `876edb1`, predating #321 entirely)
and was carrying its own uncommitted staged/unstaged changes. A code-level
drift guard — #333's `test_resolve_pr_contributors_subcommands.sh` — can
only check that the tree it runs in is internally consistent; it has no way
to see that a *different* checkout, never pulled forward, is the one
actually executing production traffic. Before re-diagnosing a "still
broken" report against `origin/main` as a code defect, check what checkout
the report was actually measured against and how stale it is — a green
guard and a red transcript can both be telling the truth at once.

## Conventions

- Branch with a type prefix; never commit to `main`.
- One independent review per PR, by someone who did not write it — including
  fixup commits. `dispatch.sh --reviews-pr` asks the ledger, not the branch
  name, and `dispatch.sh --pr` supports dispatching a review or a fix pass
  scoped to a PR directly rather than only to the issue it closes (#169,
  `Verified 2026-08-15`).
- This is enforced at merge, not just at dispatch: `merge-pr.sh` is the only
  path that should merge a PR here, and it refuses to merge one whose author
  lane and reviewer lane are the same (invariant 9) or whose verdict cannot be
  read (`Verified 2026-08-15`, #184/#196/#198). `gh pr merge` run directly
  bypasses this — it is convention, not a platform block, same as the CI gate
  below.
- One fix pass. If a PR fails a second review, close it and file what remains.
- Cheaper model tiers for workers and reviewers; reserve the expensive tier for
  judgement.
- Anything touching tmux behaviour runs against an isolated socket or on a
  throwaway host — never the machine you are working on.
- A UI PR (touching `scripts/supervisor/laneview/`) needs a captured frame,
  not a description, as evidence — `look.py capture`/`look.py frames` (#110).
  This is enforced, not just written down here: `.github/workflows/ui-evidence.yml`
  runs `ui-evidence-gate.sh` on every PR and fails one that touches that path
  without a `<!-- ui-evidence:v1 -->` marker in the body or a comment.
