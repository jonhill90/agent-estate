# Visual QA for w5f.md: Build -> Workflows, the second of two stubs
# replaced with a real pane (internal/workflows) -- a task's own path
# through the estate (dispatched/delivered/accepted/completed), read from
# the SAME ledger.TaskRow rows internal/board/internal/rail already read
# (board.ReadTaskRows), never a second reader of the ledger.
# Run: vhs testdata/vhs/workflows.tape
#
# Drives the REAL binary against a COPY of this box's own live
# ledger.sqlite3 (auto-discovered the same way cmd/keelson/main.go's
# resolveLedgerSource does for any bare launch, same as
# testdata/vhs/library.tape) -- every row on screen is a real dispatch from
# the real ledger, not a fixture.
Output testdata/vhs/out/workflows.gif
Set FontSize 14
Set Width 1600
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-workflows ./cmd/keelson && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-workflows" Enter
Sleep 3s

# Home(0) -> Lanes(7): 7 downs. -> Build header: 1 down (8 total).
Down
Down
Down
Down
Down
Down
Down
Down
Sleep 300ms

# Right expands Build (cursor stays on the header) -- Skills(1 down),
# Workflows(1 down), Enter selects it.
Right
Sleep 300ms
Down
Down
Enter
Sleep 3s
Screenshot testdata/vhs/out/01-workflows-initial.png

# [tab] moves focus into the content pane (selecting a route leaves focus
# on the sidebar, confirmed building testdata/vhs/library.tape) -- [j]/[k]
# then move the row selection within the dispatch history.
Tab
Sleep 300ms
Type "j"
Sleep 300ms
Type "j"
Sleep 500ms
Screenshot testdata/vhs/out/02-workflows-row-selected.png
