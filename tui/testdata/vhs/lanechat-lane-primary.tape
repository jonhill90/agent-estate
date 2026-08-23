# Visual QA for agent-tui#115's lane-primary decide-by-variant pane
# (internal/lanechat/laneprimary): "the rail stays the spine, a
# conversation opens against the selected lane." Fully offline -- this
# pane takes no live Fetcher/Source/Sender at all (internal/lanechat's
# shared fixture is compiled into the binary, the same posture
# internal/gallery already has), so unlike testdata/vhs/rail-task-join.tape
# or chat-mentions.tape, this tape needs no agent-supervisor repo, no
# ledger and no cmd/fakemcp -- a bare `-lanechat-lane-primary` launch is
# the whole story.
#
# Verifies:
#   - the narrow rail (left) lists every fixture lane with its glyph and
#     state, moving selection with j/k.
#   - the right column shows exactly ONE conversation at a time -- the
#     selected lane's own thread, not a second independent thread list.
#   - the composer's @-mention gate (agent-tui#114's ValidateMentions,
#     reused, not reimplemented) refuses an unknown name and a stopped
#     lane, and accepts a real running one -- the same two distinct
#     refusal reasons chat-mentions.tape already proves for internal/chat
#     itself, proven here a second time against this variant's own
#     composer.
#   - the on-screen text says "FIXTURE DATA" -- agent-tui#115's own hard
#     constraint that fake data must never be mistakable for live state.
#
# Run:
#   go build -o /tmp/atui-lanechat-lp ./cmd/estate
#   vhs testdata/vhs/lanechat-lane-primary.tape
Output testdata/vhs/out/lanechat-lane-primary.gif
Set FontSize 13
Set Width 1500
Set Height 850
Set Shell "bash"

Type "/tmp/atui-lanechat-lp -lanechat-lane-primary"
Enter
Sleep 2s
Screenshot testdata/vhs/out/lanechat-lane-primary-01-initial.png

# [tab]: shell.Model starts every launch with focus on the nav sidebar
# (SPEC-shell.md S3) -- this pane's own j/k/i keys only take effect once
# focus moves into the content area, the same "tab in first" step every
# other pane's own tape (chat-mentions.tape included) already needs.
Tab
Sleep 600ms

# Move selection down onto the hung lane (fixture-borealis) -- proves the
# right column's conversation follows the rail's own selection.
Down
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-lane-primary-02-selected-hung-lane.png

# Compose against the selected lane, refused: @ghost is not a fixture
# participant at all.
Type "i"
Sleep 600ms
Type "@ghost are you there"
Enter
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-lane-primary-03-refused-unknown.png

# Compose again, refused a different way: @fixture-delta is a real fixture
# lane but its state is "dead" -- not running.
Escape
Sleep 600ms
Type "i"
Sleep 600ms
Type "@fixture-delta status?"
Enter
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-lane-primary-04-refused-not-running.png

# Compose a third time, accepted: @fixture-atlas is real and busy (running).
# Never delivered to a live daemon (there is no live lane behind fixture
# data) -- the status line says "queued (fixture -- no live lane behind
# this room)" rather than claiming delivery, per this variant's own honesty
# rule.
Escape
Sleep 600ms
Type "i"
Sleep 600ms
Type "@fixture-atlas ping"
Enter
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-lane-primary-05-accepted-queued.png

Type "q"
Sleep 600ms
