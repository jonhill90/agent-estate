# Visual QA for agent-tui#115's unified-list decide-by-variant pane
# (internal/lanechat/unifiedlist): "lanes and threads as one list of
# 'agents you can talk to', state and conversation on the same row."
# Fully offline, same reason the other two lanechat-*.tape files give --
# no supervisor repo, no ledger, no cmd/fakemcp needed.
#
# Verifies the one interaction that has no equivalent in the other two
# variants: a row starts COLLAPSED (one line, state+preview only) and
# [enter] EXPANDS it in place to show the transcript and composer,
# pushing the rows below it down -- there is no persistent second column
# here at all, which is the structural difference under review, not a
# skin of laneprimary/roomprimary's own two-column shape.
#
#   - collapsed state: every row is one line.
#   - expanded state: the selected row grows an inline transcript+composer
#     panel; the rows after it are still visible, just pushed down.
#   - [i] before expanding does nothing (compose requires an open row) --
#     proven directly by trying it collapsed first.
#   - the same @-mention gate as the other two variants, proven once here.
#   - "FIXTURE DATA" is visible on screen.
#
# Run:
#   go build -o /tmp/atui-lanechat-ul ./cmd/keelson
#   vhs testdata/vhs/lanechat-unified-list.tape
Output testdata/vhs/out/lanechat-unified-list.gif
Set FontSize 13
Set Width 1500
Set Height 850
Set Shell "bash"

Type "/tmp/atui-lanechat-ul -lanechat-unified-list"
Enter
Sleep 2s
Screenshot testdata/vhs/out/lanechat-unified-list-01-collapsed.png

# [tab]: focus starts on the nav sidebar (SPEC-shell.md S3) -- this pane's
# own enter/i keys only take effect once focus moves into the content area.
Tab
Sleep 600ms

# [i] on a collapsed row is a no-op -- composing requires an expanded row.
Type "i"
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-unified-list-02-i-noop-while-collapsed.png

# Expand the first row in place.
Enter
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-unified-list-03-expanded.png

# Compose against the expanded row, refused: @ghost is not a participant.
Type "i"
Sleep 600ms
Type "@ghost hello"
Enter
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-unified-list-04-refused-unknown.png

# Collapse it again with esc -- back to one line per row. Two presses:
# the first closes the composer (leaves the row expanded, same as
# handleComposerKey's own "esc" case), the second collapses the row
# itself -- each needs its own render cycle to land before the next key.
Escape
Sleep 600ms
Escape
Sleep 600ms
Screenshot testdata/vhs/out/lanechat-unified-list-05-collapsed-again.png

Type "q"
Sleep 600ms
