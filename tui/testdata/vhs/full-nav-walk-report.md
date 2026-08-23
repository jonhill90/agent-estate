# Full nav-tree walk — honest render report

**GENERATED. Do not hand-edit this file.** Rebuilt by `go run ./cmd/navwalk` from `testdata/vhs/nav-walk/manifest.json` (the destination list) and `testdata/vhs/nav-walk/observations/*.jsonl` (one append-only file per destination, agent-b3.md's own structural fix for a shared table that every nav-measuring lane used to conflict on). To record a new measurement: append one `navwalk.Observation` to that route's own `.jsonl` file (`navwalk.AppendObservation`), then re-run the generator. A route with no observation file yet renders as `could not measure` below, not a blank row.

Legend: **RENDERS** real content, looks right · **EMPTY** renders but shows nothing (correct or bug, stated in its own notes) · **STUB** still the placeholder · **BROKEN** error/panic/garbage · **REMOVED** no longer a destination at all · **could not measure** never recorded or unreachable.

| # | Destination | Result | Source | Notes |
|---|---|---|---|---|
| 00 | Home | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged. Title 'agent-tui' plus the sidebar-navigation blurb, confirmed against a real live capture -- this is the exact fingerprint the queue's stale-home-text claim would show elsewhere if true, and it does not appear on any other destination. |
| 01 | Dashboard | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged, but the tape's own Sleep before this screenshot (4s) was too short for the real gh-latency-bound fetch this route's own prior observation already documented (17s-25s+) -- caught 'not fetched yet' at 4s, real figures (6 agents, 2 open PRs, 34 merged today, $489.04 spend, 69 vault facts, 'fetched 11s ago') after bumping the tape's Sleep to 30s. Fixed in testdata/vhs/full-nav-walk.tape rather than re-recording a false EMPTY. |
| 02 | Agents | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real live lane rows (estate:director, build-2..5, tui-demo:bash), real STATE/MODE columns. |
| 03 | Chat | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real live session transcripts in the unified feed (259 messages), not fixture data. |
| 04 | Tasks | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged, same 4s-too-short timing issue as Dashboard (shared board.ExecRunner fetch path) -- caught '(loading)' at 4s, a real bounded error ('board: mcp: lanes: supervisor call failed: lanes.sh --json exited 1: lanes: session agent-supervisor does not exist') after bumping to 30s, matching this route's own prior observation's predicted shape exactly. Tape fixed. |
| 05 | Knowledge | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- 69 real vault facts now (was 64), expected drift in a continuously-written vault, not a regression. |
| 06 | Library | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- 931 real hard constraints live. |
| 07 | Lanes | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged in the full-walk capture (real session/lane rows for estate and tui-demo, real per-lane cost). Two earlier standalone attempts (15s and 30s waits) both hit a real, reproducible-in-the-moment 'multi-session unavailable ... sessions call timed out ... mcp: tools/call: no reply' error before this clean capture succeeded -- host load average was 24.59-24.69 (measured via uptime) during those attempts, well above this estate's own host-guard trip threshold (agent-dotfiles' 3.0/core), so treated as host-saturation-induced MCP timeout, not a code regression. Worth watching if it recurs under normal load. |
| 08 | Build → Skills | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real skills list from the live roster. |
| 09 | Build → Workflows | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real dispatch history rows from the ledger. |
| 10 | Build → MCP Servers | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real 3 servers (microsoft-learn, deepwiki, context7). |
| 11 | Connect → Connections | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real connector/model data. |
| 12 | Connect → Storage | STUB | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- honest stub, explicitly names why (no credential-free local seam to hill90-app's MinIO/S3). |
| 13 | Connect → Discord | STUB | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- honest stub, explicitly names why (no Discord integration exists anywhere in this estate). |
| 14 | Connect → Secrets | RENDERS | navcheck.md full-walk re-drive (main, post agent-tui#106) (2026-08-23) | Was STUB in the prior observation (predates #106). #106 merged 2026-08-23T05:36:56Z, implementing internal/secrets (levels 1-4, never 5) as a real PaneSecrets route. Driven live with no -secrets-schema/$HILL90_APP_REPO configured: shows a real, specific 'no schema configured' state naming the exact file it would read (hill90-app's platform/vault/secrets-schema.yaml), not the honest-stub placeholder. Confirmed via internal/stub's own Descriptions map, which no longer carries a 'secrets' entry (same exclusion tasks/usage/lanes/etc. get for being wired to a real pane). |
| 15 | Observe → Usage | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real cost/token data ($470.78 today, fetched 11s ago). |
| 16 | Observe → Monitoring | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real host data (LOAD AVG 24.59, matching an independent 'uptime' check run in the same window). |
| 17 | Docs → API Docs | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real 'no spec configured' state naming the exact file it would read, with no $HILL90_APP_REPO set in this tape. |
| 18 | Docs → Platform Docs | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- names the external destination and the [o] open-in-browser action. |
| 19 | Admin → Services | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- real docker container list, real dependency check. |
| 20 | Admin → Profiles | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- same shared admin page as Services, rail highlight correctly on Profiles. |
| 21 | Admin → Users | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- same shared admin page, rail highlight correctly on Users. |
| 22 | Admin → Dependencies | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | Unchanged -- same shared admin page, rail highlight correctly on Dependencies. |
| 23 | Admin → Settings | RENDERS | navcheck.md full-walk re-drive (main) (2026-08-23) | First real capture ever recorded for this destination -- every prior full-nav-walk.tape run lost it because the tape had no Sleep after its final Screenshot command, so VHS tore the pty down before the last frame flushed to disk (confirmed: 4 separate re-runs before this fix all produced exactly 23 of 24 PNGs, always missing this one). Fixed by adding a trailing Sleep to the tape. Content itself is unchanged -- same shared admin page, theme: Signal (default) visible. |

## Summary

- **RENDERS: 22**
- **STUB: 2**
