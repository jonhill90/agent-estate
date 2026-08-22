# Visual QA for w5c.md: the Library view -- the shared corpus
# (live_parameters/open_questions/unacknowledged/possibility_count) on
# screen. Run: vhs testdata/vhs/library.tape
#
# Drives the REAL binary against a COPY of this box's own live
# ledger.sqlite3 (auto-discovered the same way cmd/keelson/main.go's
# resolveLedgerSource does for any bare launch -- agent-tui#49 item 2). No
# fixture, no fake rows -- every row on screen is a real judged item from
# the real corpus.
Output testdata/vhs/out/library.gif
Set FontSize 14
Set Width 1500
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-library ./cmd/keelson && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-library" Enter
Sleep 3s

# Home -> ... -> Library: internal/nav's own tree order is Home, Dashboard,
# Agents, Chat, Tasks, Knowledge, Library, Lanes (internal/nav/tree.go) --
# 6 downs from Home. Enter: the real internal/library pane.
Down
Down
Down
Down
Down
Down
Enter
Sleep 3s
Screenshot testdata/vhs/out/01-library-live-parameters.png

# [tab] moves keyboard focus from the sidebar into the content pane --
# without it, the [f]/[v]/[enter] keys below would hit internal/nav's own
# handler instead of internal/library's (confirmed live: the first version
# of this tape sent them straight after Enter and every screenshot came
# back identical to the one above).
Tab
Sleep 300ms

# [f] narrows to weight=hard, [x] leaves status unfiltered -- the "what is
# actually binding" view.
Type "f"
Sleep 500ms
Screenshot testdata/vhs/out/02-library-weight-hard.png

# [enter] opens the selected row -- real full body + the originating
# prompt's own context, not just the truncated list snippet.
Enter
Sleep 500ms
Screenshot testdata/vhs/out/03-library-detail-open.png

Escape
Sleep 300ms

# [v] cycles to open_questions.
Type "v"
Sleep 500ms
Screenshot testdata/vhs/out/04-library-open-questions.png

# [v] again cycles to unacknowledged.
Type "v"
Sleep 500ms
Screenshot testdata/vhs/out/05-library-unacknowledged.png
