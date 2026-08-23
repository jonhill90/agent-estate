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
# drives the real `keelson` binary against a live agent-supervisor
# checkout and DOES surface real state -- correct for QA-ing a change,
# wrong for a public README. Re-run this one to refresh the README images:
#
#   vhs testdata/vhs/readme-demo.tape
#
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

Type "q"
