# Visual QA for agent-tui#115's room-primary decide-by-variant pane
# (internal/lanechat/roomprimary): "one room per lane, with the lane's live
# state as room metadata." Fully offline, same reason
# lanechat-lane-primary.tape's own header gives -- no supervisor repo, no
# ledger, no cmd/fakemcp needed.
#
# Verifies:
#   - every room ROW itself carries the lane's own glyph, name, state and
#     idle time inline (not just a title, unlike internal/chat's own
#     RenderThreadRow) -- both "busy" and "hung" are visible directly on
#     the room list, read straight off this pane's own text.
#   - the same @-mention gate as lanechat-lane-primary.tape, proven once
#     here (a stopped participant refused) rather than all three directions
#     a second time -- the mechanism is identical (chat.ValidateMentions,
#     reused, not reimplemented); what differs between the three tapes is
#     the LAYOUT around it, which is the actual thing under review.
#   - "FIXTURE DATA" is visible on screen.
#
# Run:
#   go build -o /tmp/atui-lanechat-rp ./cmd/estate
#   vhs testdata/vhs/lanechat-room-primary.tape
Output testdata/vhs/out/lanechat-room-primary.gif
Set FontSize 13
Set Width 1500
Set Height 850
Set Shell "bash"

Type "/tmp/atui-lanechat-rp -lanechat-room-primary"
Enter
Sleep 2s
Screenshot testdata/vhs/out/lanechat-room-primary-01-room-list.png

# [tab]: focus starts on the nav sidebar (SPEC-shell.md S3) -- this pane's
# own j/k/i keys only take effect once focus moves into the content area.
Tab
Sleep 600ms

# Move onto the dead lane's room (fixture-delta is the 4th fixture lane --
# 3 downs from the first row).
Down
Down
Down
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-room-primary-02-selected-dead-room.png

# Compose against it, refused: fixture-delta is dead, not running.
Type "i"
Sleep 600ms
Type "@fixture-delta still there?"
Enter
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-room-primary-03-refused-not-running.png

Type "q"
Sleep 600ms
