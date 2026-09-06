# The daemon — invariants and failure modes

*Relocated verbatim from the repo root `AGENTS.md` by the progressive-disclosure split (only heading levels and relative link paths were adjusted for the new location). `AGENTS.md` is the index that routes here; this file is the detail.*

## Invariants — do not break these without an explicit decision

These are rules, not a map of live code. Several were once enforced by scripts
that no longer exist; each one says so where it applies.

1. **The ledger is the record; the live system is the screen.** Anything
   *decided and remembered* belongs in the ledger; anything *observed right
   now* is read from the running thing. The test is authorship: did this
   system write the value, or did something else produce it as a byproduct?
   The ledger today is `src/estate/internal/ledger` — append-only JSON lines
   at `$ESTATE_LEDGER`, defaulting to `~/.local/state/estate/ledger.jsonl`,
   not the SQLite `ledger.sqlite3` the old supervisor used. The decision
   record this invariant used to cite (`docs/decisions/0001-sqlite-ledger.md`)
   is **not in this tree**.

2. **Write the durable fact before the pretty label.** When one operation
   writes both a durable record and a lighter label pointing at it, write the
   record first. The reverse order strands the work permanently on a crash;
   this order leaves only a stale label. The script that demonstrated it
   (`lane-done.sh`) is gone and **no equivalent exists in the Go tree** — the
   rule binds anything new that writes two places.

3. **Restore refuses rather than invents.** Work that cannot be brought back
   with its own context is reported unrecoverable and left alone — a fresh
   agent wearing a recovered identity's name looks fully healthy and has none
   of the context. `restore.sh` is gone. The live expression of the same
   disposition is `ledger.State`: a turn that could not be observed is
   recorded `unknown`, `unknown` is deliberately **not** terminal, and
   `estate reclaim` frees a slot only for a turn positively observed dead.

4. **Never address the default tmux socket in a test.** `kill-server`,
   `kill-session`, `kill-window` and `respawn-*` must be scoped with
   `TMUX_TMPDIR` and gated on an isolation assertion — a bare `tmux
   kill-server` from a lane destroyed the entire live estate three times in
   one day. The guard that enforced this (`tmux-isolation.sh`) is gone, and
   **no general guard replaced it** — this still binds any script or test you
   write outside the app. What does now exist is scoped to the one package
   that calls tmux: `internal/mirror`'s `tmuxCmd` routes every invocation
   through a six-verb allowlist (`allowedVerbs`), so `kill-server`,
   `kill-session` and `respawn-*` cannot run from it at all, and
   `Config.TmuxTmpdir`, **when set**, scopes each call to a private socket
   directory with `$TMUX` unset — that is the isolation idiom the tests use,
   and `ESTATE_MIRROR_TMUX_TMPDIR` is what sets it; production leaves it empty
   and addresses the operator's own server on purpose, since watching that
   server is the feature. Read that as covering `internal/mirror`, not the tree.
   `internal/isolate` is about git worktrees, not tmux — do not read it as
   this guard.

5. **Address windows by `window_id` (`@7`), never by index.** Killing window
   4 renumbers 5 into 4. A loop killing indices hits shifting targets; that
   destroyed the Telegram poller. `internal/mirror` is the live caller and it
   honours this: `kill-window` is its only destructive verb, and every id it
   passes came out of a `list-windows` call in the same pair, never an index
   and never a name. Its package comment also reconciles this against the
   operator parameter `lane_addressing=session_index_not_raw_window_id` —
   that rule governs *delivering* to a lane across a server restart, which
   this package never does.

6. **`unknown` means "not offered", not "broken".** A classifier over live
   state should be a whitelist: only a recognised shape is offered as free.
   Handing work to something you cannot read is worse than leaving it idle —
   do not "improve" this into a guess. The classifier it was written about
   (`lanes.sh`) is gone; the same disposition is live in `ledger.State` (see
   invariant 3) — unobserved is not finished.

7. **Harness-specific strings live in one place.** In the Go tree that place
   is `src/estate/internal/harness`, which `estate dispatch` asks by name and
   which refuses an unknown or uninstalled harness rather than defaulting.
   Widening a general classifier's pattern to cover another harness is the
   wrong fix — it lets one harness's shapes falsely match another's.

8. **A service is not a lane.** The Telegram poller was the instance: never
   dispatch to it, never "restart" it as a lane, and remember it consumed its
   own inbound queue by acking the offset, so running the inbox by hand
   returned nothing — which is not evidence nobody wrote. **There is no poller
   implementation under `src/`** (`git grep -in poller -- src` finds exactly one
   line: a comment in `internal/mirror` citing this incident, not a process);
   the rule is kept for the class, not for a live process.

9. **Identity is what the estate minted, never a name someone chose.** For
   the old supervisor that string was `<session>:<index>`. Today it is the
   dispatch id, carried as the `dispatch/<id>` head ref and cross-checked
   against a head SHA the estate itself recorded when that turn exited
   (`internal/gate`, `internal/dispatchid`). Compare those, never issue
   numbers, task titles or branch names, when deciding whether two pieces of
   work were done by the same agent. This one **is** enforced: `estate merge`
   refuses a PR whose head ref it cannot join to a dispatch record.

10. **A dispatched turn does not derive its own identity — the estate states
    it.** `estate dispatch` appends the turn's own branch to an author's
    brief and its own dispatch id to a reviewer's, so neither has to work out
    or invent one (`roleGrounding` in `src/estate/main.go`). Do not restate
    those values in a hand-written brief, and do not let a turn re-derive
    them from what it sees around it. `lane-whoami.sh`, the old fallback, is
    gone, and the decision record this invariant used to cite
    (`docs/decisions/0012-invariant-evidence.md`) is **not in this tree**.

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

**A tool that fails closed and that nothing calls is a documentation rule
with a binary attached.** After building anything protective, ask both
*what calls it?* and *is that caller something that survives the failure it
guards against?* `count-agents.sh` was the instance: it existed, it was
tested, and for a time nothing called it, until `host-pressure.sh` was wired
to call it directly. Both scripts are now in `reference/`; the lesson is not.

**An abstraction can be present and correctly avoided.** Routing around a
seam looks identical to nobody having wired it up. Check whether the
avoidance is documented before "fixing" it — the reason belongs next to the
seam.

**A merged fix that never reaches the process running it looks identical to
an unfixed defect.** `agent-supervisor#308` was diagnosed as a live code bug
three times before anyone checked which checkout was actually running —
before re-diagnosing a "still broken" report, check what checkout it was
measured against. (The runbook this used to cite,
`docs/runbooks/stale-checkout-diagnosis.md`, is **not in this tree**.)
