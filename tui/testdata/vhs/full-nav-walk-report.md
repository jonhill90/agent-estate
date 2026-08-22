# Full nav-tree walk — honest render report

Produced by `testdata/vhs/full-nav-walk.tape` against a binary built from this
branch (`go build -o /tmp/atui-navwalk ./cmd/keelson`), run with
`$AGENT_SUPERVISOR_REPO` set to a real local `agent-supervisor` checkout and
no other flags (no `-ledger`, no `-mcp-cmd` override). One screenshot per
destination in `testdata/vhs/out/`, walking `internal/nav`'s own `Flatten()`
order exactly (`internal/nav/tree.go`) — see the tape's own header comment for
the walk mechanics (sidebar keeps focus after `Enter`; `Right` expands a
group header without moving the cursor).

Two destinations (Dashboard, Tasks) looked ambiguous at the walk's normal 2s
settle time — possibly still loading, possibly stuck. Both were re-checked
with a dedicated 8s-settle tape (`testdata/vhs/out/01b-dashboard-recheck.png`,
`testdata/vhs/out/04d-tasks-recheck.png`) before being called EMPTY below;
8s did not change either screen, so this is not "still loading," it's stuck.

Legend: **RENDERS** real content, looks right · **EMPTY** renders but shows
nothing (correct or bug, stated) · **STUB** still the placeholder ·
**BROKEN** error/panic/garbage · **could not measure** unreachable.

| # | Destination | Result | Notes |
|---|---|---|---|
| 00 | Home | RENDERS | |
| 01 | Dashboard | **EMPTY — likely a bug** | All five stat rows (AGENTS/OPEN PRS/MERGED TODAY/SPEND TODAY/VAULT FACTS) show `unknown`, footer says "not fetched yet." Re-checked at 8s settle (`01b-dashboard-recheck.png`): unchanged. No error, no timeout message, no visible fetch-in-progress indicator — it just never fetches. |
| 02 | Agents | RENDERS | Real STATE/MODE data for live lanes (e.g. `estate:build-2..5 busy`, `Hill90:supervisor`), MODE column showing `local` for real running lanes — the agent-tui#86 column fill is live. |
| 03 | Chat | RENDERS | Real UI, but content is `chat.FixtureSource` (documented — no ACP/pi-rpc transport exists yet per `internal/chat/fixture.go`). Expected non-live, not a bug. |
| 04 | Tasks | **EMPTY — likely a bug** | Header says "(loading)", all five kanban columns show `(0)`/`(empty)`. Re-checked at 8s settle (`04d-tasks-recheck.png`): unchanged, still "(loading)" forever. This run had no `-ledger` flag — AGENTS.md's known defect #2 documents the board pane as "unavailable" with no `-ledger`, but this run shows a permanently stuck "(loading)" state instead of the documented `! unavailable / no -ledger... configured` message. Either the documented message path isn't being hit, or there's a second, undocumented stuck-loading path when reached via the nav sidebar rather than `-board`. |
| 05 | Knowledge | STUB | "not built yet -- no description recorded for this route." |
| 06 | Library | RENDERS | Real `live_parameters` (200 rows), `possibility_count: 931`. |
| 07 | Lanes | RENDERS | Real rail data, real cost figures ($525.34 claude, $0.13 codex). |
| 08 | Build → Skills | RENDERS | Real list, ~35 skills, honestly labelled "unknown/unevaluated/unknown" where data isn't known. |
| 09 | Build → Workflows | STUB | |
| 10 | Build → MCP Servers | RENDERS | Real 3 servers (deepwiki, context7, microsoft-learn). |
| 11 | Connect → Connections | RENDERS | Real connector/model data — claude/codex/pi harnesses, codex model list. |
| 12 | Connect → Models | STUB | Same model data is already visible under Connections (11) but this dedicated route is unwired. |
| 13 | Connect → Storage | STUB | |
| 14 | Connect → Discord | STUB | |
| 15 | Connect → Secrets | STUB | |
| 16 | Observe → Usage | RENDERS | Real cost/quota data: claude $525.34 today, codex $0.13, session/weekly quota percentages, "fetched 20s ago." Note: this appears to contradict `AGENTS.md`'s known defect #3 ("quota line unwired... renders unknown (no quota source)") — quota IS rendering real percentages here. Worth a follow-up doc check; not fixed in this pass. |
| 17 | Observe → Monitoring | STUB | |
| 18 | Docs → API Docs | STUB | |
| 19 | Docs → Platform Docs | STUB | (`KindExternal` in the nav tree, but the route itself still renders the stub pane rather than an external-link affordance.) |
| 20 | Admin → Services | RENDERS | Real docker container list (bold_diffie, agentbox, basic-memory-mcp) and real dependency check (gh/sqlite3/npx/tmux/docker/python3 all `yes`). |
| 21 | Admin → Profiles | RENDERS | Same shared admin page as #20 (all 5 Admin children route to one `PaneAdmin`, confirmed via `routeToPane` in `internal/shell/model.go`) — correctly renders "no per-user profiles in this estate -- one operator, no accounts" rather than fabricating fake profiles. Correct by design. |
| 22 | Admin → Users | RENDERS | Same shared page; correctly renders "no user/role accounts in this estate -- one operator, no multi-tenant concept." Correct by design. |
| 23 | Admin → Dependencies | RENDERS | Same shared page, dependency table visible. |
| 24 | Admin → Settings | RENDERS | Same shared page, `theme: Signal (default)` visible. |

## Summary

- **RENDERS (real content): 14** — Home, Agents, Chat, Library, Lanes,
  Build/Skills, Build/MCP Servers, Connect/Connections, Observe/Usage,
  Admin×5.
- **STUB (honest placeholder): 9** — Knowledge, Build/Workflows,
  Connect/Models, Connect/Storage, Connect/Discord, Connect/Secrets,
  Observe/Monitoring, Docs/API Docs, Docs/Platform Docs.
- **EMPTY, likely bugs: 2** — Dashboard, Tasks. Both show a permanent
  "not fetched" / "(loading)" state with zero progress at 8s, not a slow
  fetch. Neither errors, panics, or renders garbage — they just never
  resolve. Worth its own targeted fix pass (out of scope here per this
  task's brief).
- **BROKEN: 0.**
- **Could not measure: 0** — every one of the 25 destinations in the
  Flatten() walk was reachable and screenshotted.

## Not fixed here, flagged for the next pass

1. Dashboard and Tasks both hang in a perpetual loading state rather than
   erroring or completing — needs its own investigation (likely the
   `board.Fetcher`/dashboard aggregator seam not being invoked, or invoked
   but never returning, when reached via nav routing rather than the
   `-board` launch flag).
2. `AGENTS.md`'s documented known defect #3 (quota line unwired, renders
   "unknown (no quota source)") does not match what `16-observe-usage.png`
   shows — real session/weekly quota percentages render. This doc claim may
   be stale; worth a re-verify and, if stale, an update to `AGENTS.md`'s
   "Verified" stamp and defect list (not done here — this pass is
   observation-only per the brief).
3. `Connect → Models` is a STUB even though the same model data already
   renders under `Connect → Connections` (11) — the dedicated route just
   isn't wired to reuse it.
