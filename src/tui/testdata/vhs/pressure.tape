# Visual QA for agent-estate#987: Home's new Pressure section.
# Two runs of the same binary, driven only by -estate-bin: the first
# points at a fake `estate` that answers `pressure` with a real, healthy
# line (Present/OK); the second points at a binary that does not exist,
# to capture the Unreadable path -- exactly the two outcomes
# estatus.ReadPressure distinguishes, never a fabricated zero.
Output testdata/vhs/out/pressure.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-987 ./cmd/estate && clear" Enter
Sleep 3s
Type "printf '%b' '#!/bin/sh\necho \x22load 0.14/core  free 4396MB  inflight 3  weekly budget 94% left\x22\nexit 0\n' > /tmp/fake-estate-ok && chmod +x /tmp/fake-estate-ok" Enter
Sleep 1s
Show

Type "AGENT_TUI_ESTATE_BIN=/tmp/fake-estate-ok /tmp/atui-987" Enter
Sleep 5s
Screenshot testdata/vhs/out/pressure-present.png

Hide
Type "q" Sleep 500ms
Type "AGENT_TUI_ESTATE_BIN=/tmp/estate-does-not-exist /tmp/atui-987" Enter
Sleep 3s
Show
Sleep 2s
Screenshot testdata/vhs/out/pressure-unreadable.png
