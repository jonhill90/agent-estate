# Visual QA for internal/knowledge -- Jon's real memory vault, rendered in
# the TUI -- driven through the real shell binary (cmd/keelson) via the
# Knowledge nav route, not a standalone demo binary.
#
# agent-tui#132: this tape's own build target, cmd/knowledgedemo, never
# existed in this repository's history (`git log --all -- cmd/knowledgedemo`
# returns zero commits across every ref) -- unlike agents-mode.tape/#130,
# this was never a restore; the tape was unrunnable from the day it landed
# in #87. Repointed at cmd/keelson with the Knowledge route selected, the
# same target testdata/vhs/knowledge-route.tape (#95) already proved out --
# that tape is deleted by the same change that adds this fix, folded in
# here rather than kept separately (see below).
#
# Why folding knowledge-route.tape in here, not just fixing it in place:
# its own stated purpose was proving the ROUTE reaches internal/knowledge
# rather than internal/stub's placeholder. That is now precisely and
# continuously superseded by the recurring full-nav-walk harness --
# testdata/vhs/full-nav-walk-report.md's own destination 05 (Knowledge)
# already records RENDERS with a real, current vault fact count, re-driven
# the same day this fix was written. Read that report before treating
# either knowledge tape as the route's own coverage; it is not -- the
# route itself is already evidenced there, from a broader, actively
# re-run sweep, not from either tape.
#
# What THIS tape uniquely covers, that the nav-walk sweep does not: opening
# one fact and reading its body -- internal/knowledge.Model's own
# modeList -> modeReading interaction (model.go). Nav-walk takes one
# screenshot per destination and moves on; it proves the list renders,
# never that a fact opens. That drill-down is this tape's only remaining
# job, and it is the one thing about internal/knowledge this repo would
# otherwise have zero visual coverage of.
#
# Reads the REAL $AGENT_MEMORY_VAULT on this machine -- no fixture, no
# fake data -- so the screenshots show real vault fact slugs and a real
# fact's own frontmatter + body.
#
# Uses Wait/Wait+Screen (content-polling), not a fixed sleep, from the
# start -- see agents-mode.tape's own header comment (agent-tui#130,
# 6/20 real captures on re-verification, approved on that honest number
# rather than a claim of determinism) for why a fixed sleep before a vhs
# Screenshot is the wrong idiom, and for the same still-open vhs-internal
# capture flake this tape inherits rather than closes: measured directly
# here too, 4 runs, `knowledge-01-list.png` (the first screenshot) real
# content 4/4, `knowledge-02-fact-opened.png` (the second, after the
# Tab+drill-down below) real content 2/4 -- Wait+Screen confirmed the
# reading pane's own content was on screen both times it still captured
# stale, the same disconnect #130's own investigation found between vhs's
# Wait and its Screenshot pipeline.
Output testdata/vhs/out/knowledge.gif
Set FontSize 13
Set Width 1600
Set Height 850
Set Shell "bash"

Hide
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
Type "go build -o /tmp/atui-knowledge ./cmd/keelson && clear" Enter
Wait
Show

Type "/tmp/atui-knowledge" Enter
Wait+Screen /Home/

# Home -> Dashboard -> Agents -> Chat -> Tasks -> Knowledge (5 downs),
# Enter -- the same route knowledge-route.tape (#95) already proved
# reaches internal/knowledge, not internal/stub's placeholder.
Down
Down
Down
Down
Down
Enter
# "[enter] open" is the list pane's own static legend text (view.go's
# viewList), present regardless of how many facts the vault currently
# holds -- unlike waiting for a specific fact slug, this does not rot as
# the vault's own content changes.
Wait+Screen /\[enter\] open/
Screenshot testdata/vhs/out/knowledge-01-list.png

# [tab] moves keyboard focus from the sidebar to the content pane --
# without it, Down/Enter below drive the SIDEBAR's own nav tree, not the
# knowledge list's row selection (confirmed empirically: this is the
# first tape to drill into a fact via cmd/keelson's real nav rather than
# a standalone binary, and pressing Enter with focus still on the rail
# never opened a fact -- Wait+Screen for the reading pane's footer just
# timed out instead, at this exact point, before this fix).
Tab
Down
Down
Down
Enter
# "[r] reload" is the reading pane's own static legend text, present only
# once a fact has actually finished loading (view.go's viewReading) -- the
# loading/error paths render a shorter "[esc] back  [q] quit" footer
# without it, so this waits specifically for the success path, not just
# any post-Enter render.
Wait+Screen /\[r\] reload/
Screenshot testdata/vhs/out/knowledge-02-fact-opened.png

Type "q"
Wait
