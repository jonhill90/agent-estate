# Visual QA for SPEC-shell.md S6/S7 depth: Model/Task/Cost columns.
# Run: vhs testdata/vhs/agents.tape
#
# Drives the REAL binary against the REAL agent-supervisor MCP server and a
# COPY of this box's own live ledger.sqlite3 (auto-discovered the same way
# cmd/estate/main.go's resolveLedgerSource does for any bare launch --
# agent-tui#49 item 2). No fixture, no fake sessions/lanes/ccusage source.
#
# agent-tui#185: the COST column's real `ccusage session --json` subprocess
# takes a measured 16-38s to resolve after this pane's Init() fires it --
# see the Sleep before 01-agents-live.png below for the full writeup. If
# COST reads `unknown` for every row in either screenshot, that is this
# box's ccusage/npx resolution being slower than the margin below, not a
# code regression -- do not re-file this as the same defect #185 already
# settled without re-measuring the bracket first.
Output testdata/vhs/out/agents.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
# agent-tui#168 (the third reference, not named by the issue itself):
# this used to force AGENT_SUPERVISOR_REPO to a hardcoded
# $HOME/source/repos/Personal/agent-supervisor default, which no longer
# exists on this box now that it renamed to agent-estate -- so a bare
# run of this tape drove the built binary's own "supervisor repo not
# found" degraded path and still produced a GIF, silently. Removed
# rather than updated to a second hardcoded name: cmd/estate/
# supervisor.go's discoverSupervisorRepo already tries both names (and
# any git walk-up) with no env var needed, so leaving AGENT_SUPERVISOR_REPO
# unset here exercises that real resolution instead of a tape-local
# fallback list that would drift from it. An explicit override still
# works if $AGENT_SUPERVISOR_REPO is exported before running vhs.
Type "go build -o /tmp/atui-agents ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-agents" Enter
Sleep 5s

# Home -> Dashboard -> Agents (2 downs), Enter: the real internal/agents
# pane, now wired to lane.Lane.Model, the ledger-keyed Task join, and the
# ledger+ccusage Cost join (this change).
Down
Down
Enter
# agent-tui#185: this used to sleep 8s here (16s cumulative from tape
# start including the 3s build + 5s launch above), which screenshots the
# Agents frame BEFORE the real `ccusage session --json` subprocess this
# view's Init() fires can return -- COST then reads `unknown` for every
# row even though nothing is broken. Measured on this box, same binary,
# same SHA: `full-nav-walk.tape` reaches its own Agents frame at 38s
# cumulative and COST is populated there. The fetch resolves somewhere
# between 16s and 38s after launch -- a wall-clock dependency on this
# box's `ccusage`/npx resolution time, not a constant. 45s here (53s
# cumulative) is a margin above the measured 38s, not a value tuned to
# match full-nav-walk.tape's own number -- if `ccusage` is ever slower
# than this margin on some other box, this frame can still legitimately
# show `unknown`; see this tape's own header comment.
Sleep 45s
Screenshot testdata/vhs/out/01-agents-live.png

# [r] forces a second round of all three fetches -- confirms refresh, not
# just the initial Init(), reaches the same columns. Same margin as above:
# the refresh re-issues the same `ccusage session --json` subprocess, so
# it is bound by the same 16-38s bracket measured from ITS OWN trigger,
# not from tape start.
Type "r"
Sleep 45s
Screenshot testdata/vhs/out/02-agents-refreshed.png
