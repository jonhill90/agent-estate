# Visual QA for internal/agents' MODE column (SPEC-shell.md S12 depth) --
# modeFor reading real per-row evidence instead of a hardcoded
# session.ExecutionLocal. Run: vhs testdata/vhs/agents-mode.tape
#
# KNOWN HARNESS-LEVEL FLAKE, measured directly, not assumed -- read this
# before treating a blank capture as this program (or this tape) being
# broken (agent-tui#130 review, estate:2, re-verified independently here
# after that review before pushing a fix):
#
#   - The ORIGINAL defect this tape fixed was real and is fixed: a fixed
#     `Sleep 4s` before the launch screenshot raced the program's own
#     render, and `go build`'s own `Sleep 8s` raced cold-cache build time.
#     Both are now `Wait`/`Wait+Screen` (poll for the shell prompt, then
#     poll for `director:w1` -- real rendered content -- rather than a
#     guessed duration for either).
#   - That fix does NOT make the capture reliable, and no tape-level fix
#     found here does. `vhs`'s own `Screenshot` command intermittently
#     writes a stale or empty frame even once `Wait+Screen` has already
#     confirmed the target content is on screen -- reproduced with an
#     isolated tape containing nothing but `printf 'MARKERLINE\n'` +
#     `Wait+Screen /MARKERLINE/` + `Screenshot` (no Go program, no
#     agentsdemo, no fixture at all): 8 runs, 5 silently wrote no file,
#     3 succeeded, and total elapsed time was identical (~2s) whether the
#     run succeeded or not -- ruling out a timeout, a build race, or
#     anything specific to this program. Against the real agentsdemo
#     binary: 10 consecutive `Wait+Screen`-gated runs produced valid
#     content on runs 3 and 8 only (2/10); adding a 2s settle `Sleep`
#     after `Wait+Screen` confirmed the content was present did not
#     improve this (2/10 again, different runs). `vhs` is pinned at
#     v0.11.0, the current latest release (charmbracelet/vhs, checked
#     2026-08-23) -- there is no newer version to try.
#   - This corroborates, independently and on different content, what
#     estate:4 measured on `tight/lanes.png` during the #129 review: the
#     same class of flake, firing on roughly the same order of frequency,
#     self-resolving on retry. Both findings point at `vhs`'s own
#     Screenshot/frame-capture pipeline, not at any one tape's content or
#     timing.
#   - PRACTICAL READING: if you run this tape once and get a small
#     (~3-8KB) blank-looking PNG, that is this known flake, not a
#     regression -- rerun it. A GENUINE regression looks different: every
#     run blank, or the screen showing an error/crash rather than an empty
#     prompt. The `Wait`/`Wait+Screen` gates above are kept because they
#     are still strictly more correct than a tuned sleep (no build-race,
#     no guessed launch duration) even though they do not close the
#     underlying `vhs` gap -- reverting to a bare `Sleep` would reintroduce
#     the original, avoidable race on top of this one.
Output testdata/vhs/out/agents-mode.gif
Set FontSize 13
Set Width 1400
Set Height 500
Set Shell "bash"

Hide
Type "go build -o /tmp/agentsdemo ./cmd/agentsdemo && clear" Enter
Wait
Show

Type "/tmp/agentsdemo" Enter
Wait+Screen /director:w1/
Screenshot testdata/vhs/out/agents-mode-01-list.png

Type "q"
Wait
