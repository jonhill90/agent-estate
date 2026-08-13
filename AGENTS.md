# agent-supervisor — agent orientation

*(`AGENTS.md` and `CLAUDE.md` are the same file — one is a symlink, so there is
no second copy to drift.)*

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
  laneview/                  two viewers, neither required
  mcp_server.py              read surface over MCP
  watchdog.sh                liveness, from OUTSIDE the loop
  inbox-poll.sh              Telegram poller (a service, never a lane)
tests/supervisor/            41 files; the suite is the contract
```

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
   `TMUX_TMPDIR` and gated by `assert_isolated`. A bare `tmux kill-server` from
   a lane destroyed the entire live estate three times in one day, including
   unrelated sessions belonging to the operator. The rule is not "never call
   it" — one test harness calls it sixteen times safely.

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

## Conventions

- Branch with a type prefix; never commit to `main`.
- One independent review per PR, by someone who did not write it — including
  fixup commits. `dispatch.sh --reviews-pr` asks the ledger, not the branch name.
- One fix pass. If a PR fails a second review, close it and file what remains.
- Cheaper model tiers for workers and reviewers; reserve the expensive tier for
  judgement.
- Anything touching tmux behaviour runs against an isolated socket or on a
  throwaway host — never the machine you are working on.
