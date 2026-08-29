---
type: Reference
description: Full detail behind AGENTS.md's "Known defects" clean bill of health for agent-tui#49 -- what each of the three original defects was and what fixed it, with the confirming grep/manual-run evidence.
generated:
  at: 2026-08-29T00:00:00-04:00
---

# agent-tui#49 — closed, three defects, all fixed

`AGENTS.md`'s "Known defects" section states the current status in one line
per item; this doc is the original defect and the fix evidence for each.

agent-tui#49 is **closed** (2026-08-16). All three defects it originally
recorded are fixed — re-confirmed by running the actual binary and by grep,
not by memory of the issue text:

1. **Bare launch exits 1.** Fixed. `./estate` with no flags and no
   `$AGENT_SUPERVISOR_REPO` now opens in a degraded state on the Home pane
   instead of exiting (`cmd/estate/main.go`'s `supervisorRepoResolved`
   handling, commented "agent-tui#49 item 1: a bare `estate` must open,
   never exit 1"). Confirmed by running the built binary under a real TTY
   (`script -q ... ./estate`): it renders the sidebar and Home pane rather
   than printing the old `no supervisor to connect to` message and exiting.
2. **The board pane reports itself unavailable with no `-ledger`.** Fixed.
   `resolveLedgerSource` (`cmd/estate/board.go`) now auto-discovers and
   stages a copy of the live ledger when `-ledger`/`$AGENT_TUI_LEDGER` is
   unset (`defaultLedgerLivePath` + `newLedgerCopier`); the old hard
   `boardOK == false` refusal only fires now when discovery genuinely finds
   nothing, not merely because the flag was omitted.
3. **The cost panel's quota line is unwired from the current quota source.**
   Fixed. `internal/cost/quota.go` now shells `quota.sh` out via
   `QuotaRunner`/`ExecQuotaRunner`, wired from `cmd/estate/main.go`'s
   `resolvedQuotaBin` (`<supervisor-repo>/scripts/supervisor/quota.sh`).
   `renderQuota`'s `unknown (no quota source)` string (`internal/cost/
   view.go`) is now the honest fallback for a genuinely missing/failing
   `quota.sh`, not a structurally unwired source — confirmed by `grep -rn
   "quota.sh" --include='*.go' .`, which returns matches throughout
   `internal/cost` and `cmd/estate`.

This is a clean bill of health, not an open punch list — if a regression
reopens any of the three, restore the numbered form above with fresh
confirmation evidence rather than editing this prose in place.
