# Visual QA for the TUI shell. Run: vhs testdata/vhs/shell.tape
# Produces one PNG per view so a human can look at all of them without
# launching anything.
Output testdata/vhs/out/shell.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "export AGENT_SUPERVISOR_REPO=${AGENT_SUPERVISOR_REPO:-$HOME/source/repos/Personal/agent-supervisor}" Enter
Type "go build -o /tmp/atui ./cmd/... && clear" Enter
Sleep 3s
Show

Type "/tmp/atui" Enter
Sleep 5s
Screenshot testdata/vhs/out/01-home.png

Type "2"
Sleep 2s
Screenshot testdata/vhs/out/02-board.png

Type "3"
Sleep 2s
Screenshot testdata/vhs/out/03-cost.png

Type "4"
Sleep 2s
Screenshot testdata/vhs/out/04-gallery.png

Type "5"
Sleep 2s
Screenshot testdata/vhs/out/05-flow.png

Type "6"
Sleep 2s
Screenshot testdata/vhs/out/06-chat.png
