# Post-merge QA for w5d.md: confirms the #93/#91/#92/#90 conflict resolution
# didn't silently revert anything -- Dashboard, Agents' MODE column, and
# Library all render for real. Run: vhs testdata/vhs/library-postmerge.tape
#
# Selecting a route leaves keyboard focus on the SIDEBAR (confirmed live
# while building this tape's first version -- there is no auto-switch to
# the content pane), so cursor position is tracked explicitly below rather
# than assumed reset after each Enter.
Output testdata/vhs/out/library-postmerge.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-postmerge ./cmd/keelson && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-postmerge" Enter
Sleep 3s

# Home(0) -> Dashboard(1): 1 down. Enter: #92's real pane, not reverted.
Down
Enter
Sleep 3s
Screenshot testdata/vhs/out/01-postmerge-dashboard.png

# Dashboard(1) -> Agents(2): 1 down. Enter: #91's real MODE column, not
# reverted.
Down
Enter
Sleep 3s
Screenshot testdata/vhs/out/02-postmerge-agents-mode.png

# Agents(2) -> Library(6): 4 downs. Enter: #93's own pane, still real.
Down
Down
Down
Down
Enter
Sleep 3s
Screenshot testdata/vhs/out/03-postmerge-library-live-parameters.png

# Tab moves focus into the content pane; [enter] opens the first real row.
Tab
Sleep 300ms
Enter
Sleep 500ms
Screenshot testdata/vhs/out/04-postmerge-library-detail.png
