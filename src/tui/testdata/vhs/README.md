# Visual QA harness

`vhs shell.tape` drives the real binary in a real pty and writes one PNG per
view to `out/`. It is how a change to the shell gets looked at before anyone
is asked to QA it.

## Capturing evidence for a PR: use `vhscapture`, not a bare `vhs` run

agent-estate#947: `vhs`'s own `Screenshot` command silently writes a stale,
blank, or transitional frame some fraction of the time -- independent of
which tape it is, what the tape waits on before screenshotting, and how
much application content is on screen. Measured directly, twice:

- `agents-mode.tape`'s own header comment (agent-tui#130): an isolated tape
  with nothing but a `printf` marker + `Wait+Screen` + `Screenshot` (no Go
  program at all) still wrote a blank/missing frame 5/8 runs.
- Re-measured for agent-estate#947 (2026-09-03), same box, same `vhs` v0.11.0: the
  identical marker tape went 8/20 (40%) missing/blank, and `agents-mode.tape`
  itself, run bare 10 consecutive times with zero code changes, produced a
  correct 4393-colour frame 5 times and a blank single-colour (0,0,0)-shaped
  frame the other 5 -- a coin flip.

A previous attempt changed `knowledge-graph.tape`'s wait condition to
`Wait+Screen /legend:.*project/` plus a trailing `Sleep 500ms`, reasoning
that waiting on the LAST thing `View()` composes plus a settle sleep would
be enough. The failure rate was unchanged, because the race is inside
`vhs`'s own tty-to-PNG rasterization, not in how long or what the tape
waits on before asking for a screenshot. **No tape-level wait fixes this** --
this is true of all 26 tapes in this directory, not just the one agent-estate#947 was
filed against, because the reproduction above involves zero tape-specific
content.

`internal/vhscapture` (and its CLI, `cmd/vhscapture`) is the fix: it runs
the whole tape, verifies every `Screenshot` target actually landed and
clears a distinct-colour floor, and retries the ENTIRE run -- never a
longer sleep -- when it didn't. Re-run against `agents-mode.tape`, 11
consecutive times, `-min-colors 100`: 11/11 eventually settled (1-6 vhs
attempts each, well inside the default `-max-attempts 8`), and PASS/FAIL
per attempt is printed for every run, not just the last one.

```
go build -o /tmp/vhscapture ./cmd/vhscapture
/tmp/vhscapture -tape testdata/vhs/agents-mode.tape
```

`-min-colors` defaults to **1000**, not 2. agent-estate#956's review found the old
default of 2 accepted a synthetic 259-colour PNG -- the exact
partial/transitional shape both agent-estate#947 and this PR's own body name as
not-a-real-capture -- as "settled" on the first attempt, at the tool's own
bare invocation with no flags. 1000 is picked from the measured
distribution, not taste: blank frames are 1 colour, the partial shape that
slipped through is 259, and the two settled frames measured directly
(`agents-mode.tape`) were 4393 and 5674 colours. 1000 sits roughly 4x above
the observed partial shape and well under half the lowest observed settled
shape, so the tool now rejects both failure classes agent-estate#947 named at its own
default -- no flag required to get a default that enforces the floor it
claims to. This is still only two tapes' worth of measured settled counts;
a tape whose own real settled frame happens to fall under 1000 needs an
explicit `-min-colors` override once you've measured it bare a few times,
the same as before.

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
added a second main package alongside `cmd/estate` -- narrowed to
`./cmd/estate`, the one binary this tape has ever actually driven.
