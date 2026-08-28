# README screenshots for agent-tui#117 ("go-public prep").
#
# Drives `cmd/demo` -- the shell's own real rail/nav/lipgloss rendering
# code, wired to invented data instead of live sources (see cmd/demo's
# package doc comment). Every frame carries the visible
# "ALL DATA ON THIS SCREEN IS FAKE" footer, so nothing here is mistakable
# for live estate state: no real agent names, PR numbers, paths,
# usernames or hostnames ever reach this binary, because it never reads
# anything -- there is no supervisor connection, no $HOME, no repo path.
#
# This is deliberately a different tape from full-nav-walk.tape, which
# drives the real `estate` binary against a live agent-supervisor
# checkout and DOES surface real state -- correct for QA-ing a change,
# wrong for a public README. Re-run this one to refresh the README images:
#
#   vhs testdata/vhs/readme-demo.tape
#
# 2026-08-23 (estate-loop/b5-readme-screenshots): added Agents and Lanes
# captures -- the brief that follows this one asked specifically for those
# two alongside Dashboard/Chat, on the grounds that Home alone is the
# least informative screen. Both destinations already had real fixture
# data in cmd/demo/data.go; no new fixture content was needed, only two
# more stops on the same walk. Cursor position carries forward between
# screenshots (Down moves relative to wherever Enter last landed), so the
# down-counts below are relative, not absolute indices -- see
# internal/nav/tree.go's Flatten() order (every top-level Item, then every
# Group) for why Lanes is reached from Library with a single Down: Lanes
# is nav.Build()'s last top-level Item, immediately after Library.
Output testdata/vhs/out/readme-demo.gif
Set FontSize 13
Set Width 1400
Set Height 800
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-demo ./cmd/demo && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-demo" Enter
Sleep 1s
Screenshot docs/screenshots/home.png

Down
Enter
Sleep 500ms
Screenshot docs/screenshots/dashboard.png

Down
Enter
Sleep 500ms
Screenshot docs/screenshots/agents.png

Down
Enter
Sleep 500ms
Screenshot docs/screenshots/chat.png

Down
Enter
Sleep 500ms
Screenshot docs/screenshots/tasks.png

Down
Down
Enter
Sleep 500ms
Screenshot docs/screenshots/library.png

Down
Enter
Sleep 500ms
Screenshot docs/screenshots/lanes.png

Type "q"
