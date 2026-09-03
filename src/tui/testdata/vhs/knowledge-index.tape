# agent-estate#946's own "must be reachable in the app" requirement --
# not the vault list (internal/knowledge's default mode), but the NEW [c]
# sub-pane: `estate knowledge`'s own compiled index
# (internal/knowledgeindex), reached from the existing Knowledge route.
#
# Navigation: Home is focused on launch; Down x5 walks Home -> Dashboard
# -> Agents -> Chat -> Tasks -> Knowledge (nav.Build's own top-level
# order, internal/nav/tree.go), Enter opens it. [c] switches the pane
# into modeCompiled; [esc] returns to the vault's own list.
#
# ESTATE_KNOWLEDGE_INDEX points this run at a real, freshly-generated
# index (estate knowledge run against this machine's real sources) --
# not a fixture -- so what this capture shows is the real four-source
# result, honest FAIL lines included if any source could not be read.
Output testdata/vhs/out/knowledge-index.gif
Set FontSize 13
Set Width 1600
Set Height 900
Set Shell "bash"

Hide
Type "go build -o /tmp/atui-knowledge ./cmd/estate && clear" Enter
Sleep 3s
Show

Type "/tmp/atui-knowledge" Enter
Sleep 2s

# --- Knowledge route: the vault's own list (existing behaviour) ---
Down Down Down Down Down
Enter
Sleep 2s
Screenshot testdata/vhs/out/knowledge-01-vault-list.png

# --- [c]: the compiled index's own list, per-source status + items ---
Tab
Type "c"
Sleep 2s
Screenshot testdata/vhs/out/knowledge-02-compiled-list.png

# --- [enter] on one item: its own three tiers ---
Enter
Sleep 1s
Screenshot testdata/vhs/out/knowledge-03-compiled-detail.png

# --- [esc] twice: detail -> compiled list -> back to the vault list ---
Escape
Sleep 500ms
Escape
Sleep 1s
Screenshot testdata/vhs/out/knowledge-04-back-to-vault-list.png

Sleep 1s
