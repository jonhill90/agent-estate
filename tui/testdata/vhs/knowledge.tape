# Visual QA for internal/knowledge -- Jon's real memory vault, rendered in
# the TUI. Run: vhs testdata/vhs/knowledge.tape
#
# Reads the REAL $AGENT_MEMORY_VAULT on this machine -- no fixture, no
# fake data. The list screenshot shows real fact slugs from the vault;
# the second screenshot shows one real fact actually opened (its own
# frontmatter + body, read only at that point -- see internal/knowledge's
# own package doc comment on progressive disclosure).
Output testdata/vhs/out/knowledge.gif
Set FontSize 13
Set Width 1600
Set Height 850
Set Shell "bash"

Hide
Type "go build -o /tmp/knowledgedemo ./cmd/knowledgedemo && clear" Enter
Sleep 8s
Show

Type "/tmp/knowledgedemo" Enter
Sleep 3s
Screenshot testdata/vhs/out/knowledge-01-list.png

Down
Sleep 500ms
Down
Sleep 500ms
Down
Sleep 500ms
Enter
Sleep 2s
Screenshot testdata/vhs/out/knowledge-02-fact-opened.png

Type "q"
Sleep 1s
