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
