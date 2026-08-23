# Full nav-tree walk — honest render report

**Re-run #2, agent-b5.md.** Original walk: agent-tui#94 (25 destinations,
14 RENDERS/9 STUB/2 EMPTY/0 BROKEN). This re-run drives the SAME tape
pattern (`testdata/vhs/full-nav-walk.tape`, rewritten for the current tree
shape) after merging `origin/main` into `feat/three-stubs` (PR #96) --
main had moved twice underneath that branch (#95's Knowledge wiring, plus
this same merge's own two-file conflict resolution in
`cmd/keelson/main.go`/`internal/shell/model.go`), so this re-run exists to
catch any route the MERGE regressed, not just the two/three this branch's
own change touched. **24 destinations now, not 25** — one fewer: `Connect
-> Models` was removed from `internal/nav`'s tree entirely (not
re-stubbed), see `internal/nav/tree.go`'s own doc comment on that Group's
`Children` for the evidence.

Produced against a binary built from the merged branch
(`go build -o /tmp/atui-navwalk ./cmd/keelson`), run with
`$AGENT_SUPERVISOR_REPO` set to a real local `agent-supervisor` checkout and
no other flags (no `-ledger`, no `-mcp-cmd` override). One screenshot per
destination in `testdata/vhs/out/` (gitignored, not committed), walking the
CURRENT `internal/nav` tree's on-screen order (top-level items, then each
group expanded and every child visited in source order) -- see the tape's
own header comment for the walk mechanics.

**Update (agent-b3.md + agent2.md, PR #97, merged on top of this file):**
rows 01 (Dashboard) and 04 (Tasks) below — carried forward here as "EMPTY,
likely a bug" from #94's original walk, unchanged by PR #96's own
re-walk above since neither Dashboard nor Tasks was in scope for that
branch — are now fixed. Both rows, the Summary counts, and the "Net
change"/"Not fixed here" sections below are updated with the fix and a
fresh re-check; everything else on this page (rows 00, 02-03, 05-24, and
PR #96's own "Route-table completeness" section) is left exactly as that
re-walk recorded it.

Legend: **RENDERS** real content, looks right · **EMPTY** renders but shows
nothing (correct or bug, stated) · **STUB** still the placeholder ·
**BROKEN** error/panic/garbage · **could not measure** unreachable.

| # | Destination | Result | Changed since #94's walk? | Notes |
|---|---|---|---|---|
| 00 | Home | RENDERS | no | Unchanged. |
| 01 | Dashboard | **RENDERS (fixed, PR #97)** | **yes — fixed since this re-walk (not by this branch)** | Was "EMPTY — likely a bug" above. Root cause: `board.ExecRunner`/`cost.ExecRunner` (the seam `buildDashboardFetch` composes `gh`/`ccusage` calls through) had no timeout at all — the only external-process seam in this program without one. A first fix (a bare `execTimeout` context) did not actually work on Linux (CI caught it: `exec.CommandContext`'s default cancellation only signals the direct child; a shell-forked grandchild survives and keeps `Output()`'s pipes open). Real fix: put every subprocess in its own process group and kill the group on cancellation. Re-walked twice after the real fix: real figures both times (`AGENTS 11 total`, `SPEND TODAY $676.97`, `VAULT FACTS 64`, ...), though real elapsed time varies (~17s to >25s, genuinely `gh`-latency-bound under today's heavier concurrent estate load) — never hangs indefinitely, which is the only thing this fix claims. |
| 02 | Agents | RENDERS | no | Unchanged -- real STATE/MODE/data for live lanes. |
| 03 | Chat | RENDERS real data (fixed, agent-b3.md) | yes — fixed since this re-walk (not by this branch) | Was `chat.FixtureSource` only (documented, expected non-live). Now backed by `chat.ClaudeCodeSource`, a real read-only `Source` reading this machine's own Claude Code CLI session transcripts (`~/.claude/projects`), wrapped in `chat.FallbackSource` so it falls back to the fixture -- visibly, with a `! showing fixture data` banner -- only when no project directory is configured at all. Re-walked (`chat-real-01-list.png`, `chat-real-02-second-thread.png`): real thread titles (`Fix failing nightly lat...`, `Execute instructions fr...`, `session f86fe023`, ...), real tool calls with real done/failed status, a real PR link (`github.com/jonhill90/skills/pull/238`) inside a real transcript -- no fixture banner. Separately drove the fallback path (`chat-fallback-01-notice.png`, `-claude-projects-dir` pointed at a directory that does not exist): the `! showing fixture data -- no real chat source is configured` banner renders and the content shown is unmistakably the synthetic fixture threads (`lane/20-chat-threads`, ...), proving both directions rather than assuming the untested one. |
| 04 | Tasks | **RENDERS an honest error (fixed, PR #97)** | **yes — fixed since this re-walk (not by this branch)** | Was "EMPTY — likely a bug" above. Same root cause and fix as row 01 (`board.ExecRunner`, shared by `buildBoardFetch`). Re-walked after the fix: `! unavailable` / `board: mcp: lanes: supervisor call failed: lanes.sh --json exited 1: lanes: session 'agent-supervisor' does not exist` — a real, bounded error resolved within seconds, never "(loading)" forever. (This box's default `-session` had no live tmux session by that literal name at re-check time; `boardOK` was true via the live-ledger auto-discovery path, agent-tui#49 item 2.) |
| 05 | Knowledge | **RENDERS** | **yes — fixed since #94 (not by this branch)** | Was STUB in #94's walk. Fixed by #95 (`fix(shell): wire the "knowledge" nav route to internal/knowledge`, merged into main while this branch's PR #96 was open) -- confirmed still real after this merge: 64 real vault facts listed (`memory-conventions`, `python-package-manager-uv`, ...), `sort: index (64 facts)` footer. |
| 06 | Library | RENDERS | no | Unchanged -- real `live_parameters`, `possibility_count: 931 hard constraints live`. |
| 07 | Lanes | RENDERS | no | Unchanged -- real rail/session data. |
| 08 | Build → Skills | RENDERS | no | Unchanged -- real skills list. |
| 09 | Build → Workflows | **RENDERS** | **yes — this branch's own fix (w5f.md/PR #96)** | Was STUB in #94's walk. Now `internal/workflows` -- real dispatch history from the ledger, ~30 rows, real lanes/statuses/timestamps, confirmed still correct after the merge conflict resolution (`internal/shell/model.go`'s routing arm for `PaneWorkflows` survived the merge, verified both by this screenshot and by `TestWorkflowsRouteShowsRealWorkflowsPane`, added this pass). |
| 10 | Build → MCP Servers | RENDERS | no | Unchanged -- real 3 servers. |
| 11 | Connect → Connections | RENDERS | no | Unchanged -- real connector/model data; still shows its own `-- models --` section (the redundancy evidence for #12's removal, below). |
| 12 | Connect → Models | **REMOVED (not a destination)** | **yes — this branch's own removal (w5f.md/PR #96)** | Was STUB in #94's walk (`"Same model data is already visible under Connections (11) but this dedicated route is unwired"` -- #94's own flagged follow-up, item 3). Removed from `internal/nav`'s tree entirely rather than re-stubbed -- confirmed the sidebar's Connect group now lists exactly four children (Connections, Storage, Discord, Secrets), no Models row, in this walk's own screenshots. |
| 13 | Connect → Storage | STUB | no | Unchanged. Renumbered from #94's item 13 (no content change). |
| 14 | Connect → Discord | STUB | no | Unchanged. Renumbered from #94's item 14. |
| 15 | Connect → Secrets | STUB | no | Unchanged. Renumbered from #94's item 15. |
| 16 | Observe → Usage | RENDERS | no | Unchanged -- real cost/quota data. |
| 17 | Observe → Monitoring | **RENDERS** | **yes — this branch's own fix (w5f.md/PR #96)** | Was STUB in #94's walk. Now `internal/monitor` -- real host data (`CORES 11`, `LOAD AVG 14.41, 10.37, 9.03`, `SWAP USED 0.0%`, `CLAUDE PROCESSES 22`), confirmed still correct after the merge. One partial-data note: this particular capture's `-- agents -- BY STATE` line read `unknown` (Host figures rendered fine) -- Snapshot's own design means Host and Agents fail independently (`internal/monitor/host.go`'s own doc comment), so this is the sessions sub-fetch being slow/unlucky at this exact 2s capture, not a routing regression; the dedicated `testdata/vhs/monitoring.tape` (PR #96, unchanged by this merge) shows real agent counts (`10 total (free:1 busy:3 supervisor:6)`) at its own longer settle time. |
| 18 | Docs → API Docs | STUB | no | Unchanged. Renumbered from #94's item 18. |
| 19 | Docs → Platform Docs | STUB | no | Unchanged. Renumbered from #94's item 19. |
| 20 | Admin → Services | RENDERS | no | Unchanged -- real docker container list, real dependency check. |
| 21 | Admin → Profiles | RENDERS | no | Unchanged -- same shared admin page, correct-by-design honest "no per-user profiles" text. |
| 22 | Admin → Users | RENDERS | no | Unchanged -- same shared page, correct-by-design honest "no user/role accounts" text. |
| 23 | Admin → Dependencies | RENDERS | no | Unchanged -- same shared page. |
| 24 | Admin → Settings | RENDERS | no | Confirmed by a separate targeted tape after the main walk's own last screenshot needed a re-run (`vhs`'s own flakiness on a walk's final `Screenshot` command -- same symptom `testdata/vhs/monitoring.tape` hit and worked around during PR #96, not a shell bug: the same key sequence reproduced cleanly in a live `tmux` check). Same shared page, `theme: Signal (default)` visible. |

## Summary

- **RENDERS (real content): 19** — Home, Dashboard (fixed by #97), Agents,
  Chat, Tasks (real error, fixed by #97), Knowledge (fixed by #95),
  Library, Lanes, Build/Skills, Build/Workflows (fixed by this branch),
  Build/MCP Servers, Connect/Connections, Observe/Usage,
  Observe/Monitoring (fixed by this branch), Admin×5.
- **STUB (honest placeholder): 5** — Connect/Storage, Connect/Discord,
  Connect/Secrets, Docs/API Docs, Docs/Platform Docs.
- **EMPTY, likely bugs: 0.** Dashboard and Tasks (both listed above as
  EMPTY at the time of this re-walk) are fixed by #97 — see rows 01/04
  above.
- **BROKEN: 0.**
- **Could not measure: 0** — every one of the 24 destinations in the
  current tree was reachable and screenshotted (Settings needed a second,
  targeted tape run after the main walk's last screenshot silently
  dropped -- a `vhs` capture flake, not an app failure -- confirmed live
  via `tmux` before treating it as such rather than guessing).
- **Net change vs #94's walk: +2 RENDERS (Workflows, Monitoring, this
  branch's own fixes) +1 RENDERS from an unrelated merge (Knowledge, #95)
  +2 RENDERS from a second unrelated merge (Dashboard, Tasks, #97)
  -1 destination (Models, removed rather than left stub) -3 STUB. Zero
  regressions found** -- every route #94 called RENDERS still RENDERS
  after both merges; the two pre-existing EMPTY bugs (Dashboard, Tasks)
  are now fixed, not merely unchanged.

## Route-table completeness, verified by test not by eye

Per agent-b5.md's own requirement ("confirm against origin/main's own
list that no route present there is missing from your resolved file"):
diffed `origin/main`'s `routeToPane` map (17 keys, including `knowledge`
from #95) against the merged, conflict-resolved map in
`internal/shell/model.go` -- all 17 of main's keys are present, plus this
branch's own two additions (`monitoring`, `workflows`). Zero missing.
This check is now also a permanent Go test
(`TestRouteToPaneNeverLosesAWiredRoute`, `internal/shell/model_test.go`),
mutation-checked by temporarily deleting the `monitoring` entry and
confirming the test fails, then restoring it and confirming green --
so a future merge that drops a route fails `go test`, not just this
manual walk. A second test, `TestEveryNavRouteHasAPaneOrIsAnHonestStub`,
cross-checks the other direction: every `KindRoute` leaf in
`nav.Build()`'s current tree is either wired or explicitly named as an
expected stub, so a route nobody wired AND nobody flagged cannot go
unnoticed either.

## Fixed since this re-walk (PR #97, agent-b3.md + agent2.md)

1. **Dashboard and Tasks both resolve now, bounded, instead of hanging
   indefinitely** (rows 01/04 above). Root cause: `board.ExecRunner`/
   `cost.ExecRunner` — the one seam every `gh`/`sqlite3`/`ccusage` call in
   this program shells through — had no timeout at all, the only
   external-process seam in this program without one (`internal/mcp`'s
   `callTimeout`, the ledger `.backup`'s own `busy_timeout` pragma both
   already had theirs). A first fix (a bare `execTimeout` passed to
   `exec.CommandContext`) did not actually bound anything on Linux — CI
   caught it (`TestExecRunnerSurfacesATimeoutInsteadOfHangingForever`
   failed both packages, ~3.0s instead of a 200ms test bound). Root cause,
   instrumented rather than assumed: `exec.CommandContext`'s default
   cancellation only signals the direct child; a shell-wrapped command
   that forks (rather than exec-replaces) leaves a grandchild alive
   holding `Output()`'s pipes open, so `Output()` blocks until that
   grandchild exits on its own. Real fix: put every subprocess in its own
   process group (`SysProcAttr.Setpgid`) and kill the whole group on
   cancellation — the same fix `agent-supervisor`'s own
   `daemon/internal/agent/procgroup.go` uses for this identical class of
   orphaned-child hang. Reproduced red, then green, in a `golang:1.26`
   Linux container matching CI exactly; mutation-checked in the same
   container by reverting to the exact prior commit (red, byte-for-byte
   matching CI's own failure) and restoring the fix (green).
2. `AGENTS.md`'s documented known defect #3 (quota line unwired) still
   appears stale against `16-observe-usage.png`'s real quota percentages --
   #94's own flagged item 2, not re-verified here (out of scope for both
   this merge-resolution pass and #97).
