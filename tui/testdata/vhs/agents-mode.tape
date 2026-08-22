# Visual QA for internal/agents' MODE column (SPEC-shell.md S12 depth) --
# modeFor reading real per-row evidence instead of a hardcoded
# session.ExecutionLocal. Run: vhs testdata/vhs/agents-mode.tape
Output testdata/vhs/out/agents-mode.gif
Set FontSize 13
Set Width 1400
Set Height 500
Set Shell "bash"

Hide
Type "go build -o /tmp/agentsdemo ./cmd/agentsdemo && clear" Enter
Sleep 8s
Show

Type "/tmp/agentsdemo" Enter
Sleep 3s
Screenshot testdata/vhs/out/agents-mode-01-list.png

Type "q"
Sleep 1s
