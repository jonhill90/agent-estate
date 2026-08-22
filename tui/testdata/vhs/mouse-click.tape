# Visual QA for feat/mouse-nav's rebase onto SPEC-shell.md S3: the sidebar
# (internal/nav), not the footer, is the real clickable nav now. Run:
#   vhs testdata/vhs/mouse-click.tape
#
# WHY THIS TAPE ONLY SCREENSHOTS, NEVER TYPES A CLICK: checked, not assumed
# (see this branch's own PR body for the full trace) -- VHS's `Type` sends
# one pty write() per character, with a real inter-character gap even at
# `Set TypingSpeed 0ms`. A synthetic SGR mouse report (`ESC [ < b ; x ; y M`)
# has to arrive as ONE contiguous read for bubbletea's escape-sequence
# parser to recognise it as a mouse event rather than a run of stray
# keystrokes -- confirmed by instrumenting a throwaway bubbletea program to
# log every message it received: typing the sequence through VHS produced
# nine separate KeyMsg lines ("esc", "[", "<", "0", ...), never one
# MouseMsg. `tmux send-keys -l` writes its argument as a single burst and
# does not have this problem (same probe, same sequence: one clean
# `MOUSE action=... x=... y=...` line) -- that is what this PR's own body
# uses to drive the actual click, pasted there as text captures. VHS is
# used here for what it demonstrably does well: a real screenshot of the
# real render.
Output testdata/vhs/out/mouse-click.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-mouse ./cmd/keelson && clear" Enter
Sleep 5s
Show

Type "/tmp/atui-mouse -mcp-cmd false" Enter
Sleep 3s
Screenshot testdata/vhs/out/mouse-01-sidebar.png

Type "q" Enter
Sleep 1s
