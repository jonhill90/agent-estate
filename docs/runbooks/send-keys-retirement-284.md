---
type: Measurement
description: Measured enumeration of the 29 `send-keys` ledger rows retired under agent-supervisor#284, with method and before/after evidence.
generated:
  at: 2026-08-16T19:40:11-04:00
---

# Retiring the `send-keys` lanes (agent-supervisor#284)

This is the enumeration #284 asked for -- the 29 `send-keys` rows measured
from a ledger copy, classified, and what happened to each. Re-run this
method for the next attrition pass; do not skip straight to a count.

## Method

1. Copy the ledger, verify the source's md5 is unchanged on both sides.
   Never open the live ledger for write.

   ```sh
   SRC=/Users/jon/.local/state/agent-dotfiles-supervisor/ledger.sqlite3
   md5_before=$(md5 -q "$SRC"); cp "$SRC" /tmp/ledger-copy.sqlite3
   md5_after=$(md5 -q "$SRC"); [ "$md5_before" = "$md5_after" ] || echo "AVOID -- source changed"
   sqlite3 /tmp/ledger-copy.sqlite3 \
     "SELECT lane, harness, transport, server_id, session_id, pane_id FROM lanes WHERE transport='send-keys';"
   ```

2. For each row, get the CURRENT live server incarnation per session
   (`tmux list-sessions -F '#{session_name} #{session_id} #{socket_path}
   #{session_created}'`) and the current pane table (`tmux list-panes -a -F
   '#{session_name} #{window_index} #{window_id} #{pane_id} #{window_name}'`).
   A row is **stale** (as#235) if either:
   - its `server_id` (`<socket>:<session_created>`) does not match the
     session's current incarnation -- the tmux server behind it is gone, or
   - its `pane_id` does not appear in the current pane table for that
     session -- `renumber-windows on` closed/renumbered past it.

   A stale row costs nothing to "retire": the pane it names is already gone.

3. For every row that IS live, resolve its worktree (workers are named
   `ad-<slug>-<pid>` under `$TMPDIR`, matching the window name) and check:

   ```sh
   git status --porcelain          # dirty working tree?
   git rev-list --count '@{u}..HEAD'   # unpushed commits, if a real branch/upstream
   ```

   Anything with unpushed commits gets pushed BEFORE the window is touched
   -- unpushed work is the only thing a boundary destroys (a killed window
   loses the live agent conversation, never the git history once pushed).

4. Retire smallest-risk first: idle (`free`), clean tree, 0 unpushed. Kill
   by **window_id**, never index, never the session or server:

   ```sh
   tmux kill-window -t 'agent-supervisor:@16'
   ```

   Leave anything mid-review, dirty, or genuinely watched. Name those
   lanes as exceptions IN CODE (`scripts/supervisor/live-pane-exceptions.sh`),
   not in this prose.

## Enumeration, measured 2026-08-16

29 `send-keys` rows in the ledger copy. 15 stale (pane/server already
gone -- zero retirement cost), 14 live.

| lane | live? | worktree | git state | action |
|---|---|---|---|---|
| ad241repro-22535:2 | stale (server_id `socket:1` mismatch) | -- | -- | none needed |
| agent-dotfiles:2..10 (9 rows) | stale (session `agent-dotfiles` no longer exists) | -- | -- | none needed |
| agent-supervisor:2 | stale (server_id mismatch) | -- | -- | none needed |
| agent-supervisor:9 | stale (pane %23 gone, session matches) | -- | -- | none needed |
| agent-tui:7 | stale (pane %26 gone, session matches) | -- | -- | none needed |
| skills:5 | stale (pane %20 gone, session matches) | -- | -- | none needed |
| agent-supervisor:3 (`as260-rerev266`) | live | ad-260-rerev266 | detached, reviewing PR266, clean | **left** -- mid-review |
| agent-supervisor:4 (`as260-rev265b`) | live | ad-260-rev265b | lane/260-rev265b, 0 ahead, clean | **retired** (`@5`) |
| agent-supervisor:5 (`as240-fix258`) | live | ad-240-fix258 | lane/240-fix258, 0 ahead, clean | **retired** (`@6`) |
| agent-supervisor:6 (`as260-fix266`) | live | ad-260-fix266 | lane/260-fix266, **2 unpushed** | pushed to `origin/lane/260-fix266`, then **retired** (`@16`) |
| agent-supervisor:7 (`as236-rev257`) | live | ad-236-rev257 | detached, reviewing PR257, clean | **left** -- mid-review |
| agent-supervisor:8 (`as241-fix241`) | live | ad-241-fix241 | lane/241-fix241, 0 ahead, clean | **retired** (`@22`) |
| agent-tui:2 (`as264-quota-timeouts`) | live | ad-264-quota-timeouts | lane already merged (agent-tui#267), 0 ahead | **retired** (`@8`) |
| agent-tui:3 (`at51-rev59`) | live | ad-51-rev59 | detached, reviewing PR59, clean | **left** -- mid-review |
| agent-tui:4 (`as227-rev237`) | live but **broken** | pane cwd (`ad-227-rev237-73810`) no longer exists at all | nothing to lose -- already stranded | **retired** (`@10`) |
| agent-tui:5 (`at38-app-keelson`) | live | ad-38-app-keelson | lane already merged (agent-tui#43), 0 ahead | **retired** (`@24`) |
| agent-tui:6 (`at44-qa43`) | live | ad-44-qa43 | lane/44-qa43, 0 ahead, clean | **retired** (`@25`) |
| skills:2 (`as260-v265`) | live | ad-260-v265 | lane/260-v265, 0 ahead, clean | **retired** (`@17`) |
| skills:3 (`as171-rev255`) | live | ad-171-rev255 | detached, reviewing PR255 for #171 itself, clean | **left** -- mid-review |
| skills:4 (`as260-rev265c`) | live | ad-260-rev265c | lane/260-rev265c, 0 ahead, clean | **retired** (`@19`) |

10 windows retired this pass. 4 left alone (all mid-review, `--reviews-pr`
dispatches that fall through dispatch.sh's #171/#274 default by design --
see `live-pane-exceptions.sh`'s header for why that is a scope boundary,
not a per-lane exception). 15 rows were already stale before this pass.

## Why the SQL count doesn't move today

`SELECT transport, count(*) FROM lanes GROUP BY transport` still reads
the same numbers immediately after a `kill-window` -- nothing deletes a
`lanes` row, by design (the ledger is the record; a hand-run `DELETE`
against the live database is exactly what the acceptance section says not
to do). A retired window's row goes from "live send-keys" to "stale
send-keys," the same bucket the other 15 rows were already in. The count
that actually falls is the one that matters -- **live, dispatchable
`send-keys` panes** -- from 14 to 4. The ratio in the ledger inverts as
attrition keeps adding `claude-print` rows for new work (#274's design);
#284's job was to stop the 10 that were still capable of stranding a pane,
not to hand-edit the ledger to make a SELECT look different.

## Before / after -- live send-keys panes specifically

| | claude-print (ledger) | send-keys (ledger, all rows) | send-keys, **live** panes only |
|---|---|---|---|
| before | 18 | 29 | 14 |
| after | 18 | 29 (10 now stale) | 4 |

Stranded-pane count: this runbook does not re-measure it (that number
comes from watchdog/digest history, not from this sweep) -- the honest
claim here is the live-pane count fell from 14 to 4, which is the
population capable of stranding anything. `claude-print` lanes have no
tmux pane at all, so they are outside this count by construction.

## Second pass, measured 2026-08-16 (attrition2)

The first pass left 4 live rows (all mid-review at the time) and 15 stale
rows, 29 total. Re-running the same method on a fresh ledger copy (md5
unchanged both sides) against the *current* live tmux estate:

- **`agent-dotfiles` and `ad241repro-22535`** (10 rows): session no longer
  exists / server_id mismatch. Still stale, unchanged from pass one.
- **`agent-supervisor:4,5,6,8,9`**: `pane_id` no longer in the live pane
  table -- these are the 5 windows pass one already retired (`@5 @6 @16
  @22` plus the pre-existing `agent-supervisor:9`). Now stale, as
  expected; nothing to do.
- **`agent-tui:4,5,6,7` and `skills:2,4,5`**: same -- pass one's
  retirements, now stale.
- **`agent-tui:3`** (`at51-rev59`, pane `%9`): `pane_id` **is** in the
  live pane table, but the window currently at that pane is a different,
  newer lane (`as251-rev251`, confirmed by `repo`/`harness_session_id`
  mismatch and by reading the pane's own content). This is exactly the
  as#235 hazard the brief calls out: **a stale row can point at another
  lane's live pane.** Killing by this row's `pane_id` would have
  destroyed unrelated, live work. Left alone -- not retirable through
  this row at all.
- **`agent-supervisor:3` (`as260-rerev266`), `agent-supervisor:7`
  (`as236-rev257`), `skills:3` (`as171-rev255`)**: pane_id live, repo and
  harness_session_id match the current window, `lanes.sh` reports `free`
  (idle -- the review each was doing is done), worktree clean, 0 commits
  ahead of upstream (two are detached PR-review checkouts, one tracks its
  own branch with nothing unpushed). **Retired**, smallest-risk first, by
  window_id:
  - `agent-supervisor:@4` (`as260-rerev266`)
  - `agent-supervisor:@21` (`as236-rev257`)
  - `skills:@18` (`as171-rev255`)
- **`agent-tui:2` (`as251-rev251`, pane `%9`)**: pane_id live, repo and
  harness_session_id match, but `lanes.sh` reports `busy` -- mid-turn,
  actively producing output. **Left alone.** This is a transient state,
  not one of the three permanent roles `live-pane-exceptions.sh` lists,
  so it is not added there; it is simply not safe to kill mid-turn (the
  in-flight turn is the only thing that would be lost, since nothing was
  committed yet to lose). Revisit on the next pass once it goes idle.

3 windows retired, 0 needed a push first (all clean, 0 ahead). Per the
amendment, every retirement was paired with a lane added back so the
dispatchable pool does not shrink:

```
bootstrap-session.sh --session agent-supervisor --lanes 4 --add-lanes --cwd .../agent-dotfiles   # 2 created
bootstrap-session.sh --session skills           --lanes 5 --add-lanes --cwd .../Skills            # 1 created
```

(`agent-tui` needed no top-up -- its one live row was left alone, not
retired.) **3 lanes retired, 3 lanes added.** All three new windows came
up `free` per `lanes.sh` within seconds.

### Before / after, this pass

| | send-keys, live panes | send-keys, ledger rows (raw) |
|---|---|---|
| before | 4 | 29 |
| after | 1 (`agent-tui:2`, busy, left) | 29 (unchanged -- killing a window never deletes a row, and the new `free-N` windows have no ledger row until dispatched) |

The raw `SELECT transport, count(*) FROM lanes GROUP BY transport` still
reads `claude-print 23 / send-keys 29` before and after this pass, for
the same reason pass one documented: a killed window's row goes stale,
not deleted, and a freshly bootstrapped `free-N` window has no row at all
until `dispatch.sh` puts work on it (at which point #274's default routes
it to `claude-print`). The ratio inverts through that mechanism over the
next several dispatches, not through this sweep rewriting rows by hand.

Stranded-pane count: not re-measured this pass either, for the same
reason as pass one -- it is watchdog/digest history, not something this
sweep produces. The population that can strand a pane (live send-keys)
fell from 4 to 1, and the 1 remaining is mid-turn, not idle-and-stuck.

One cosmetic side effect worth naming so it isn't mistaken for a bug:
after retiring `skills:@18`, `renumber-windows` shifted the two windows
above it down by one index, and `bootstrap-session.sh --add-lanes`
independently named its new window by target index -- the result is two
windows both currently named `free-5` in the `skills` session. Both
classify correctly as `free` under `lanes.sh` (which reads pane shape,
not name uniqueness) and `dispatch.sh` addresses lanes by `window_id`,
never by name, so this has no dispatch-correctness impact. It is the same
class of drift as#235 already named for ledger rows, now visible in
window names too; not fixed here because naming collisions under
`renumber-windows` are outside this issue's scope (`as#235` territory,
not `as#284`'s).
