# Visual QA harness

`vhs shell.tape` drives the real binary in a real pty and writes one PNG per
view to `out/`. It is how a change to the shell gets looked at before anyone
is asked to QA it.

## Why VHS and not something we wrote

Decided by an adopt-or-build pass, 2026-08-22. Checked with `gh api`, not
recalled:

| candidate | stars | licence | last push | verdict |
|---|---|---|---|---|
| charmbracelet/vhs | 20,684 | MIT | 2026-08-12 | **adopted** — scriptable tape, PNG + GIF + `.ascii`, already installed with ttyd/ffmpeg |
| charmbracelet/x/exp/teatest | 310 (monorepo) | MIT | 2026-08-16 | **adopt for unit tests** — in-process Bubble Tea, golden files, no binary needed |
| microsoft/tui-test | 233 | MIT | 2026-08-22 | **watch** — Playwright-shaped, drives real TUIs, explicitly built for agent access to terminal state. Thin at 233 stars; use the CLI, do not vendor |
| charmbracelet/freeze | 4,796 | MIT | 2026-08-13 | not needed — static output only, VHS covers it |
| asciinema | 17,706 | **GPL-3.0** | 2026-08-14 | rejected — copyleft, and it records rather than asserts |
| homeport/termshot | 966 | MIT | 2026-08-17 | not needed — command output, not an interactive TUI |

**Blast radius: replaceable leaf.** None of these ship in the binary; they are
dev-time only. Being wrong costs one tape file, not a system. That is why
adopting beats building here — writing a pty driver, an ANSI renderer and a
PNG encoder to replace a 20k-star MIT tool is the definition of the wrong
build.

**Verified, not assumed:** this tape was run against the real binary on
2026-08-22 and produced a correct 1400x800 capture of the rail plus the
function-key nav bar.

**Rewritten 2026-08-22 (BUILD-2, "wire the sidebar routes"):** the tape
above typed bare digit keys ("2"/"3"/...), which drove `internal/rail`'s
own pre-S3 glyph-set picker -- against today's shell (the nav sidebar is
the fixed left column, SPEC-shell.md S3) those keys hit nothing bound, so
every "different" screenshot it produced was the same home frame five
times over. `shell.tape` now drives the real sidebar keys (↓/→/enter) to
five distinct routes -- Home, Agents, Skills, MCP Servers, Admin -- run
against a real `agent-supervisor` checkout and this box's own
`~/.claude/skills`, `~/.claude.json` and docker daemon, confirming each
one renders its real pane (`internal/agents`/`skills`/`mcpservers`/
`admin`), not the S5 stub. Also fixed the build step: `go build ./cmd/...`
stopped resolving to a single `-o` path once `cmd/demo` (agent-tui#83)
added a second main package alongside `cmd/keelson` -- narrowed to
`./cmd/keelson`, the one binary this tape has ever actually driven.
